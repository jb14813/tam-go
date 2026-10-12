use crate::{
    ServerState,
    auth::{Session, validate_password},
    http::{self, header},
};
use axum::{
    extract::{FromRequest, Multipart, Request},
    http::{StatusCode, request::Parts},
    response::{IntoResponse, Redirect, Response},
};
use r_tam_core::{models::BackupFile, wire};
use serde_json::{Value, json};
use std::collections::HashMap;
struct Form {
    fields: HashMap<String, String>,
    file: Option<Vec<u8>>,
}
impl Form {
    fn get(&self, name: &str) -> &str {
        self.fields
            .get(name)
            .map(String::as_str)
            .unwrap_or_default()
    }
}
pub async fn handle(
    state: ServerState,
    request: Request,
    activity: r_tam_core::desktop::Activity,
) -> Response {
    let addr = request
        .extensions()
        .get::<axum::extract::ConnectInfo<std::net::SocketAddr>>()
        .map(|v| v.0.ip().to_string())
        .unwrap_or_else(|| "unknown".into());
    let (parts, body) = request.into_parts();
    let checking = state.clone();
    let (parts, activity, authorization) = match tokio::task::spawn_blocking(move || {
        let authorization = preflight(&checking, &parts);
        (parts, activity, authorization)
    })
    .await
    {
        Ok(v) => v,
        Err(e) => return secure(http::internal(e)),
    };
    let session = match authorization {
        Ok(v) => v,
        Err(response) => return secure(*response),
    };
    let bytes = if parts.method == "POST" {
        match axum::body::to_bytes(body, http::MAX_BODY).await {
            Ok(v) => v,
            Err(_) => {
                return secure(page_error(
                    &state,
                    session.as_ref(),
                    StatusCode::PAYLOAD_TOO_LARGE,
                ));
            }
        }
    } else {
        axum::body::Bytes::new()
    };
    let mut form = Form {
        fields: HashMap::new(),
        file: None,
    };
    if parts.method == "POST" {
        if header(&parts.headers, "content-type").starts_with("multipart/form-data") {
            let mut req = Request::new(axum::body::Body::from(bytes));
            *req.headers_mut() = parts.headers.clone();
            axum::extract::DefaultBodyLimit::max(http::MAX_BODY).apply(&mut req);
            let mut upload = match Multipart::from_request(req, &()).await {
                Ok(v) => v,
                Err(_) => return secure(page_error(&state, None, StatusCode::BAD_REQUEST)),
            };
            loop {
                let field = match upload.next_field().await {
                    Ok(Some(v)) => v,
                    Ok(None) => break,
                    Err(_) => return secure(page_error(&state, None, StatusCode::BAD_REQUEST)),
                };
                let name = field.name().unwrap_or_default().to_owned();
                let is_file = field.file_name().is_some();
                let data = match field.bytes().await {
                    Ok(v) => v,
                    Err(_) => return secure(page_error(&state, None, StatusCode::BAD_REQUEST)),
                };
                if is_file && name == "file" {
                    if form.file.is_none() {
                        form.file = Some(data.to_vec())
                    }
                } else {
                    form.fields
                        .entry(name)
                        .or_insert_with(|| String::from_utf8_lossy(&data).into_owned());
                }
            }
        } else if header(&parts.headers, "content-type")
            .parse::<mime::Mime>()
            .is_ok_and(|v| {
                v.type_() == mime::APPLICATION && v.subtype() == mime::WWW_FORM_URLENCODED
            })
        {
            for (key, value) in form_urlencoded::parse(&bytes) {
                form.fields
                    .entry(key.into_owned())
                    .or_insert(value.into_owned());
            }
        }
    }
    let response = match tokio::task::spawn_blocking(move || {
        let _activity = activity;
        serve(&state, &parts, &form, &addr, session)
    })
    .await
    {
        Ok(v) => v,
        Err(e) => {
            log::error!("admin: {e}");
            http::error(
                StatusCode::INTERNAL_SERVER_ERROR,
                "Internal error; see the server log.",
            )
        }
    };
    secure(response)
}
fn secure(mut response: Response) -> Response {
    let h = response.headers_mut();
    if !h.contains_key("cache-control") {
        h.insert("cache-control", "no-store".parse().unwrap());
    }
    h.insert("x-content-type-options", "nosniff".parse().unwrap());
    h.insert("referrer-policy", "no-referrer".parse().unwrap());
    h.insert("content-security-policy","default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'".parse().unwrap());
    response
}
fn render(
    s: &ServerState,
    session: Option<&Session>,
    code: StatusCode,
    page: &str,
    data: Value,
    message: &str,
) -> Response {
    let mut env = minijinja::Environment::new();
    for (name, source) in [
        ("layout.html", include_str!("../templates/layout.html")),
        ("login.html", include_str!("../templates/login.html")),
        ("setup.html", include_str!("../templates/setup.html")),
        ("status.html", include_str!("../templates/status.html")),
        ("keys.html", include_str!("../templates/keys.html")),
        ("backup.html", include_str!("../templates/backup.html")),
        ("password.html", include_str!("../templates/password.html")),
        ("error.html", include_str!("../templates/error.html")),
    ] {
        if let Err(e) = env.add_template(name, source) {
            return http::internal(e);
        }
    }
    let view = json!({"LoggedIn":session.is_some_and(|v|v.logged_in),"Active":page,"CSRF":session.map(|v|v.csrf.as_str()).unwrap_or_default(),"Message":session.map(|v|s.sessions.take_flash(&v.id)).unwrap_or_default(),"Error":message,"Data":data});
    match env
        .get_template(&format!("{page}.html"))
        .and_then(|t| t.render(view))
    {
        Ok(html) => (code, [("content-type", "text/html; charset=utf-8")], html).into_response(),
        Err(e) => http::internal(e),
    }
}
fn page_error(s: &ServerState, session: Option<&Session>, status: StatusCode) -> Response {
    let text = match status {
        StatusCode::FORBIDDEN => "The form token is missing or has expired. Go back and try again.",
        StatusCode::NOT_FOUND => "There is no such page.",
        StatusCode::METHOD_NOT_ALLOWED => "That method is not allowed here.",
        StatusCode::PAYLOAD_TOO_LARGE => "The upload is too large.",
        StatusCode::INTERNAL_SERVER_ERROR => "Internal error; see the server log.",
        _ => "The form could not be read.",
    };
    render(
        s,
        session,
        status,
        "error",
        json!({"Title":status.canonical_reason().unwrap_or("Error"),"Text":text}),
        "",
    )
}
fn cookie_id(p: &Parts) -> &str {
    header(&p.headers, "cookie")
        .split(';')
        .filter_map(|part| part.trim().split_once('='))
        .find(|(name, _)| *name == "tam_admin")
        .map(|(_, v)| v)
        .unwrap_or_default()
}
fn cookie(s: &ServerState, session: Option<&Session>, response: &mut Response) {
    let value = session.map(|v| v.id.as_str()).unwrap_or_default();
    let age = session.map(|v| v.expires - (s.clock)()).unwrap_or(0);
    let secure = if s.tls { "; Secure" } else { "" };
    response.headers_mut().append(
        "set-cookie",
        format!("tam_admin={value}; Path=/admin; Max-Age={age}; HttpOnly; SameSite=Strict{secure}")
            .parse()
            .unwrap(),
    );
}
fn redirect(path: &str) -> Response {
    Redirect::to(path).into_response()
}
fn logged(s: &ServerState, p: &Parts) -> Result<Session, Box<Response>> {
    match s.sessions.get(cookie_id(p)) {
        Some(v) => Ok(v),
        None if header(&p.headers, "accept").contains("application/json") => Err(Box::new(
            http::error(StatusCode::UNAUTHORIZED, "Not logged in"),
        )),
        None => Err(Box::new(redirect("/admin/"))),
    }
}
fn token(s: &ServerState, p: &Parts, form: &Form) -> Result<Session, Box<Response>> {
    s.sessions
        .visitor(cookie_id(p))
        .filter(|v| v.valid_token(form.get("csrf")))
        .ok_or_else(|| Box::new(page_error(s, None, StatusCode::FORBIDDEN)))
}
fn start(s: &ServerState, old: &Session, path: &str) -> Response {
    s.sessions.delete(&old.id);
    let new = s.sessions.create();
    let mut r = redirect(path);
    cookie(s, Some(&new), &mut r);
    r
}
fn too_many(s: &ServerState, session: &Session, page: &str, data: Value, wait: i64) -> Response {
    let mut r = render(
        s,
        Some(session),
        StatusCode::TOO_MANY_REQUESTS,
        page,
        data,
        &format!("Too many wrong passwords. Wait {wait} seconds and try again."),
    );
    r.headers_mut()
        .insert("retry-after", wait.to_string().parse().unwrap());
    r
}
fn preflight(s: &ServerState, p: &Parts) -> Result<Option<Session>, Box<Response>> {
    let path = p.uri.path();
    let method = p.method.as_str();
    let get = method == "GET" || method == "HEAD";
    let allowed = match path {
        "/admin"
        | "/admin/"
        | "/admin/status"
        | "/admin/backup"
        | "/admin/backup/download"
        | "/admin/logo.svg"
        | "/admin/favicon.ico"
        | "/favicon.ico" => get,
        "/admin/login" | "/admin/setup" | "/admin/keys/delete" | "/admin/backup/restore" => {
            method == "POST"
        }
        "/admin/logout" | "/admin/keys" | "/admin/password" => get || method == "POST",
        _ => {
            return Err(Box::new(page_error(
                s,
                s.sessions.get(cookie_id(p)).as_ref(),
                StatusCode::NOT_FOUND,
            )));
        }
    };
    if !allowed {
        return Err(Box::new(page_error(
            s,
            None,
            StatusCode::METHOD_NOT_ALLOWED,
        )));
    }
    match path {
        "/admin/login" if !s.password.is_set() => Err(Box::new(redirect("/admin/"))),
        "/admin/setup" if s.password.is_set() => Err(Box::new(redirect("/admin/"))),
        "/admin" | "/admin/" | "/admin/login" | "/admin/setup" | "/admin/logout"
        | "/admin/logo.svg" | "/admin/favicon.ico" | "/favicon.ico" => Ok(None),
        _ => logged(s, p).map(Some),
    }
}
fn serve(
    s: &ServerState,
    p: &Parts,
    form: &Form,
    addr: &str,
    session: Option<Session>,
) -> Response {
    let path = p.uri.path();
    let method = p.method.as_str();
    let get = method == "GET" || method == "HEAD";
    if path == "/admin/logo.svg" {
        return (
            [
                ("content-type", "image/svg+xml"),
                ("cache-control", "public, max-age=86400"),
            ],
            include_bytes!("../templates/logo.svg").as_slice(),
        )
            .into_response();
    }
    if path == "/admin/favicon.ico" || path == "/favicon.ico" {
        return (
            [
                ("content-type", "image/x-icon"),
                ("cache-control", "public, max-age=86400"),
            ],
            include_bytes!("../templates/favicon.ico").as_slice(),
        )
            .into_response();
    }
    if path == "/admin" || path == "/admin/" {
        let existing = s.sessions.visitor(cookie_id(p));
        if existing.as_ref().is_some_and(|v| v.logged_in) && s.password.is_set() {
            return redirect("/admin/status");
        };
        let session = existing.clone().unwrap_or_else(|| s.sessions.visit());
        let mut r = render(
            s,
            Some(&session),
            StatusCode::OK,
            if s.password.is_set() {
                "login"
            } else {
                "setup"
            },
            json!({}),
            "",
        );
        if existing.is_none() {
            cookie(s, Some(&session), &mut r)
        }
        return r;
    }
    if path == "/admin/login" {
        let session = match token(s, p, form) {
            Ok(v) => v,
            Err(r) => return *r,
        };
        let (right, wait) = s
            .limiter
            .check(addr, || s.password.check(form.get("password")));
        if wait > 0 {
            return too_many(s, &session, "login", json!({}), wait);
        }
        if !right {
            return render(
                s,
                Some(&session),
                StatusCode::UNAUTHORIZED,
                "login",
                json!({}),
                "Wrong password.",
            );
        }
        return start(s, &session, "/admin/status");
    }
    if path == "/admin/setup" {
        let session = match token(s, p, form) {
            Ok(v) => v,
            Err(r) => return *r,
        };
        if let Err(e) = validate_password(form.get("password")) {
            return render(
                s,
                Some(&session),
                StatusCode::BAD_REQUEST,
                "setup",
                json!({}),
                &e.to_string(),
            );
        }
        if form.get("password") != form.get("confirm") {
            return render(
                s,
                Some(&session),
                StatusCode::BAD_REQUEST,
                "setup",
                json!({}),
                "The passwords do not match.",
            );
        }
        match s.password.setup(form.get("password")) {
            Ok(true) => return start(s, &session, "/admin/status"),
            Ok(false) => return redirect("/admin/"),
            Err(e) => {
                log::error!("admin set password: {e}");
                return render(
                    s,
                    Some(&session),
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "setup",
                    json!({}),
                    "Could not save the password; see the server log.",
                );
            }
        }
    }
    if path == "/admin/logout" {
        let session = if method == "POST" {
            match token(s, p, form) {
                Ok(v) => Some(v),
                Err(r) => return *r,
            }
        } else {
            s.sessions.get(cookie_id(p))
        };
        if let Some(session) = session {
            s.sessions.delete(&session.id)
        }
        let mut r = redirect("/admin/");
        cookie(s, None, &mut r);
        return r;
    }
    let mut session = session.expect("protected admin route passed session preflight");
    if method == "POST" {
        session = match token(s, p, form) {
            Ok(v) if v.logged_in => v,
            Ok(_) => return redirect("/admin/"),
            Err(r) => return *r,
        }
    }
    let result = match path {
        "/admin/status" => status(
            s,
            &session,
            header(&p.headers, "accept").contains("application/json"),
        ),
        "/admin/keys" if get => keys(s, &session, StatusCode::OK, json!({}), ""),
        "/admin/keys" => {
            let description = form.get("description").trim();
            if description.is_empty() {
                return keys(
                    s,
                    &session,
                    StatusCode::BAD_REQUEST,
                    json!({}),
                    "Give the client a name.",
                );
            }
            match s.store.create_key(description) {
                Ok(k) => keys(s, &session, StatusCode::OK, json!({"NewKey":k}), ""),
                Err(e) => {
                    log::error!("admin create key: {e}");
                    page_error(s, Some(&session), StatusCode::INTERNAL_SERVER_ERROR)
                }
            }
        }
        "/admin/keys/delete" => {
            let key = form.get("key");
            if key.is_empty() {
                return keys(
                    s,
                    &session,
                    StatusCode::BAD_REQUEST,
                    json!({}),
                    "Choose a key to delete.",
                );
            }
            if form.get("confirm").is_empty() {
                match s.store.list_keys() {
                    Ok(rows) => {
                        if let Some(k) = rows.iter().find(|k| k.auth_key == key) {
                            return keys(
                                s,
                                &session,
                                StatusCode::OK,
                                json!({"Confirm":key_row(k,(s.clock)())}),
                                "",
                            );
                        }
                    }
                    Err(e) => {
                        log::error!("admin keys: {e}");
                        return page_error(s, Some(&session), StatusCode::INTERNAL_SERVER_ERROR);
                    }
                }
                s.sessions
                    .flash(&session.id, "That key was already gone.".into());
                return redirect("/admin/keys");
            }
            match s.store.delete_key(key) {
                Ok(gone) => {
                    s.presence.forget(key);
                    s.sessions.flash(
                        &session.id,
                        gone.map(|v| format!("Deleted the key for {}.", v.description))
                            .unwrap_or_else(|| "That key was already gone.".into()),
                    );
                    redirect("/admin/keys")
                }
                Err(e) => {
                    log::error!("admin delete key: {e}");
                    page_error(s, Some(&session), StatusCode::INTERNAL_SERVER_ERROR)
                }
            }
        }
        "/admin/backup" => backup(s, &session, StatusCode::OK, ""),
        "/admin/backup/download" => match s.store.export() {
            Ok(bf) => {
                let mut r = http::json(StatusCode::OK, &bf);
                r.headers_mut().insert(
                    "content-disposition",
                    "attachment; filename=\"tam-backup.json\"".parse().unwrap(),
                );
                r
            }
            Err(e) => {
                log::error!("admin export: {e}");
                page_error(s, Some(&session), StatusCode::INTERNAL_SERVER_ERROR)
            }
        },
        "/admin/backup/restore" => {
            if form.get("confirm").is_empty() {
                return backup(
                    s,
                    &session,
                    StatusCode::BAD_REQUEST,
                    "Tick the box to confirm that the file may overwrite this server's data.",
                );
            }
            let Some(file) = form.file.as_ref() else {
                return backup(
                    s,
                    &session,
                    StatusCode::BAD_REQUEST,
                    "Choose a backup file.",
                );
            };
            let mut bf: BackupFile = match wire::decode(file) {
                Ok(v) => v,
                Err(e) => {
                    return backup(
                        s,
                        &session,
                        StatusCode::BAD_REQUEST,
                        &format!("That is not a backup file: {e}"),
                    );
                }
            };
            if let Err(e) = bf.validate() {
                return backup(
                    s,
                    &session,
                    StatusCode::BAD_REQUEST,
                    &format!("That backup cannot be restored: {e}"),
                );
            }
            match s.store.import(&bf) {
                Ok(()) => {
                    s.sessions.flash(
                        &session.id,
                        format!(
                            "Restored {} prefixes, {} baskets and {} tickets.",
                            bf.prefixes.len(),
                            bf.baskets.len(),
                            bf.tickets.len()
                        ),
                    );
                    redirect("/admin/backup")
                }
                Err(e) => {
                    log::error!("admin restore: {e}");
                    page_error(s, Some(&session), StatusCode::INTERNAL_SERVER_ERROR)
                }
            }
        }
        "/admin/password" if get => render(
            s,
            Some(&session),
            StatusCode::OK,
            "password",
            json!({"FromEnv":s.password.from_env()}),
            "",
        ),
        "/admin/password" => {
            let data = json!({"FromEnv":s.password.from_env()});
            let (right, wait) = s
                .limiter
                .check(addr, || s.password.check(form.get("current")));
            if wait > 0 {
                return too_many(s, &session, "password", data, wait);
            }
            let error = if !right {
                Some("The current password is wrong.".to_owned())
            } else if let Err(e) = validate_password(form.get("password")) {
                Some(e.to_string())
            } else if form.get("password") != form.get("confirm") {
                Some("The new passwords do not match.".into())
            } else {
                None
            };
            if let Some(error) = error {
                return render(
                    s,
                    Some(&session),
                    StatusCode::BAD_REQUEST,
                    "password",
                    data,
                    &error,
                );
            }
            match s.password.set(form.get("password")) {
                Ok(()) => {
                    s.sessions.delete_others(&session.id);
                    s.sessions.flash(
                        &session.id,
                        "Password changed. Every other browser logged in here is logged out."
                            .into(),
                    );
                    redirect("/admin/password")
                }
                Err(e) => {
                    log::error!("admin password: {e}");
                    render(
                        s,
                        Some(&session),
                        StatusCode::INTERNAL_SERVER_ERROR,
                        "password",
                        data,
                        "Could not save the password; see the server log.",
                    )
                }
            }
        }
        _ => page_error(s, Some(&session), StatusCode::NOT_FOUND),
    };
    if method == "HEAD" {
        let (parts, _) = result.into_parts();
        Response::from_parts(parts, axum::body::Body::empty())
    } else {
        result
    }
}
fn uptime(seconds: i64) -> String {
    let n = seconds.max(0);
    if n >= 86400 {
        format!(
            "{} d {} h {} min",
            n / 86400,
            n % 86400 / 3600,
            n % 3600 / 60
        )
    } else if n >= 3600 {
        format!("{} h {} min", n / 3600, n % 3600 / 60)
    } else if n >= 60 {
        format!("{} min {} s", n / 60, n % 60)
    } else {
        format!("{n} s")
    }
}
fn persisted(value: &str) -> i64 {
    time::OffsetDateTime::parse(value, &time::format_description::well_known::Rfc3339)
        .map(|v| v.unix_timestamp())
        .unwrap_or(0)
}
fn ago(then: i64, now: i64) -> String {
    if then == 0 {
        return "never".into();
    }
    let diff = now - then;
    let relative = if diff < 60 {
        "just now".into()
    } else if diff < 3600 {
        format!("{} min ago", diff / 60)
    } else if diff < 86400 {
        format!("{} h ago", diff / 3600)
    } else {
        format!("{} d ago", diff / 86400)
    };
    let instant = time::OffsetDateTime::from_unix_timestamp(then).unwrap();
    let time = instant
        .to_offset(time::UtcOffset::local_offset_at(instant).unwrap_or(time::UtcOffset::UTC));
    format!(
        "{:04}-{:02}-{:02} {:02}:{:02} ({relative})",
        time.year(),
        u8::from(time.month()),
        time.day(),
        time.hour(),
        time.minute()
    )
}
fn key_row(k: &r_tam_core::models::AuthKey, now: i64) -> Value {
    let short = if k.auth_key.len() > 4 {
        format!("{}…", &k.auth_key[..4])
    } else {
        k.auth_key.clone()
    };
    let last_seen = if k.last_seen.is_empty() {
        "never".into()
    } else {
        let time = persisted(&k.last_seen);
        if time == 0 {
            k.last_seen.clone()
        } else {
            ago(time, now)
        }
    };
    json!({
        "Description": k.description,
        "Key": k.auth_key,
        "Short": short,
        "LastSeen": last_seen,
    })
}
fn keys(
    s: &ServerState,
    session: &Session,
    code: StatusCode,
    mut data: Value,
    message: &str,
) -> Response {
    match s.store.list_keys() {
        Ok(rows) => {
            data["Keys"] = rows
                .iter()
                .map(|k| key_row(k, (s.clock)()))
                .collect::<Vec<_>>()
                .into();
            render(s, Some(session), code, "keys", data, message)
        }
        Err(e) => {
            log::error!("admin keys: {e}");
            page_error(s, Some(session), StatusCode::INTERNAL_SERVER_ERROR)
        }
    }
}
fn backup(s: &ServerState, session: &Session, code: StatusCode, message: &str) -> Response {
    match s.store.counts() {
        Ok((p, t, b)) => render(
            s,
            Some(session),
            code,
            "backup",
            json!({"Prefixes":p,"Tickets":t,"Baskets":b}),
            message,
        ),
        Err(e) => {
            log::error!("admin counts: {e}");
            page_error(s, Some(session), StatusCode::INTERNAL_SERVER_ERROR)
        }
    }
}
fn status(s: &ServerState, session: &Session, wants_json: bool) -> Response {
    let (p, t, b) = match s.store.counts() {
        Ok(v) => v,
        Err(e) => {
            log::error!("admin counts: {e}");
            return page_error(s, Some(session), StatusCode::INTERNAL_SERVER_ERROR);
        }
    };
    let keys = match s.store.list_keys() {
        Ok(v) => v,
        Err(e) => {
            log::error!("admin keys: {e}");
            return page_error(s, Some(session), StatusCode::INTERNAL_SERVER_ERROR);
        }
    };
    let now = (s.clock)();
    let live = s.presence.snapshot();
    let clients: Vec<_> = keys
        .iter()
        .map(|key| {
            let presence = live.get(&key.auth_key);
            let seen = presence
                .map(|v| v.seen)
                .filter(|v| *v != 0)
                .unwrap_or_else(|| persisted(&key.last_seen));
            let updated = presence
                .map(|v| v.updated)
                .filter(|v| *v != 0)
                .unwrap_or_else(|| persisted(&key.last_update));
            let state = if seen == 0 {
                "never".into()
            } else if now - seen <= 15 {
                "connected".into()
            } else {
                format!("away for {}", uptime(now - seen))
            };
            json!({
                "name": key.description,
                "program": presence.map(|v| v.client.as_str()).unwrap_or_default(),
                "state": state,
                "last_seen": ago(seen, now),
                "last_update": ago(updated, now),
                "queued": presence.and_then(|v| v.pending),
            })
        })
        .collect();
    let mut data = json!({"uptime":uptime(now-s.started),"prefixes":p,"tickets":t,"baskets":b,"clients":clients});
    if wants_json {
        return http::json(StatusCode::OK, &data);
    }
    data["Addr"] = s.addr.clone().into();
    data["Addresses"] = s.addresses.clone().into();
    data["TLS"] = s.tls.into();
    data["DataDir"] = s.data_dir.clone().into();
    data["Version"] = s.version.clone().into();
    render(s, Some(session), StatusCode::OK, "status", data, "")
}

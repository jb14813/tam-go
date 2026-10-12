use crate::{
    ServerState,
    http::{self, error, header, json},
};
use axum::{
    extract::Request,
    http::{StatusCode, request::Parts},
    response::Response,
};
use r_tam_core::{
    Result,
    models::{self, BackupFile, Form, Order, Outcome, SaveBatch},
    store::{Operation, OperationResult, StoredRows},
    wire,
};
pub async fn handle(
    state: ServerState,
    request: Request,
    activity: r_tam_core::desktop::Activity,
) -> Response {
    let addr = request
        .extensions()
        .get::<axum::extract::ConnectInfo<std::net::SocketAddr>>()
        .map(|c| c.0.ip().to_string())
        .unwrap_or_else(|| "unknown".into());
    let (parts, body) = request.into_parts();
    let checking = state.clone();
    let (parts, activity, authorization) = match tokio::task::spawn_blocking(move || {
        let authorization = authorize(&checking, &parts, &addr);
        (parts, activity, authorization)
    })
    .await
    {
        Ok(v) => v,
        Err(e) => return http::internal(e),
    };
    let route = match authorization {
        Ok(v) => v,
        Err(response) => {
            let mut response = *response;
            if parts.method == "HEAD" {
                *response.body_mut() = axum::body::Body::empty()
            }
            return response;
        }
    };
    let bytes = if parts.method == "POST" {
        match axum::body::to_bytes(body, http::MAX_BODY).await {
            Ok(bytes) => bytes,
            Err(_) => {
                return error(
                    StatusCode::PAYLOAD_TOO_LARGE,
                    "http: request body too large",
                );
            }
        }
    } else {
        axum::body::Bytes::new()
    };
    match tokio::task::spawn_blocking(move || {
        let _activity = activity;
        serve(&state, &parts, &bytes, route)
    })
    .await
    {
        Ok(response) => response,
        Err(e) => http::internal(e),
    }
}
fn valid_route(path: &str) -> Option<Vec<String>> {
    let mut segments = path.split('/').skip(1).map(http::decode_path);
    if segments.next().as_deref() != Some("api") {
        return None;
    }
    let seg: Vec<String> = segments.filter(|v| !v.is_empty()).collect();
    if path.ends_with('/') && !seg.is_empty() {
        return None;
    }
    let refs: Vec<&str> = seg.iter().map(String::as_str).collect();
    match refs.as_slice() {
        []
        | ["auth"]
        | ["prefixes"]
        | ["backuprestore"]
        | ["reports", "counts"]
        | ["search", "tickets"] => Some(seg),
        ["tickets" | "baskets" | "drawing", ..] if seg.len() <= 4 => Some(seg),
        ["reports", "byname" | "bybasket", _] => Some(seg),
        _ => None,
    }
}
fn allowed(seg: &[&str], method: &str) -> bool {
    match seg {
        [] => method == "GET" || method == "HEAD",
        ["auth"] | ["prefixes"] => matches!(method, "GET" | "HEAD" | "POST" | "DELETE"),
        ["tickets" | "baskets" | "drawing"] | ["backuprestore"] | ["search", "tickets"] => {
            matches!(method, "GET" | "HEAD" | "POST")
        }
        _ => method == "GET" || method == "HEAD",
    }
}
struct AuthorizedRoute {
    decoded: Vec<String>,
    authed: bool,
}
fn authorize(
    s: &ServerState,
    p: &Parts,
    addr: &str,
) -> std::result::Result<AuthorizedRoute, Box<Response>> {
    let Some(decoded) = valid_route(p.uri.path()) else {
        return Err(Box::new(error(StatusCode::NOT_FOUND, "Not Found")));
    };
    let seg: Vec<&str> = decoded.iter().map(String::as_str).collect();
    if !allowed(&seg, p.method.as_str()) {
        let mut response = error(StatusCode::METHOD_NOT_ALLOWED, "Method Not Allowed");
        let methods = ["DELETE", "GET", "HEAD", "POST"]
            .into_iter()
            .filter(|method| allowed(&seg, method))
            .collect::<Vec<_>>()
            .join(", ");
        response
            .headers_mut()
            .insert("allow", methods.parse().unwrap());
        return Err(Box::new(response));
    }
    if seg == ["auth"] {
        if !s.password.is_set() {
            return Err(Box::new(error(
                StatusCode::SERVICE_UNAVAILABLE,
                "server password not set",
            )));
        }
        let plain = header(&p.headers, "tam-pw");
        if plain.is_empty() {
            return Err(Box::new(error(
                StatusCode::UNAUTHORIZED,
                "Invalid Password",
            )));
        }
        let (right, wait) = s.limiter.check(addr, || s.password.check(plain));
        if wait > 0 {
            return Err(Box::new(limited(wait)));
        }
        if !right {
            return Err(Box::new(error(
                StatusCode::UNAUTHORIZED,
                "Invalid Password",
            )));
        }
        return Ok(AuthorizedRoute {
            decoded,
            authed: false,
        });
    }
    let authed = s
        .store
        .key_exists(header(&p.headers, "tam-key"))
        .map_err(|e| Box::new(http::internal(e)))?;
    if !seg.is_empty() && !authed {
        return Err(Box::new(error(StatusCode::UNAUTHORIZED, "Invalid Key")));
    }
    Ok(AuthorizedRoute { decoded, authed })
}
fn serve(s: &ServerState, p: &Parts, body: &[u8], route: AuthorizedRoute) -> Response {
    let seg: Vec<&str> = route.decoded.iter().map(String::as_str).collect();
    let response = serve_inner(s, p, body, &seg, route.authed);
    if p.method == "HEAD" {
        let (parts, _) = response.into_parts();
        return Response::from_parts(parts, axum::body::Body::empty());
    }
    response
}
fn serve_inner(s: &ServerState, p: &Parts, body: &[u8], seg: &[&str], authed: bool) -> Response {
    let h = &p.headers;
    let method = p.method.as_str();
    let query = p.uri.query().unwrap_or_default();
    let key = header(h, "tam-key");
    if seg == ["auth"] {
        return match method {
            "GET" | "HEAD" => respond(s.store.list_keys()),
            "POST" => {
                if !http::json_type(h) {
                    return error(
                        StatusCode::BAD_REQUEST,
                        "Content-Type must be application/json",
                    );
                }
                match wire::decode::<Description>(body) {
                    Ok(v) => respond(s.store.create_key(&v.description)),
                    Err(e) => error(StatusCode::BAD_REQUEST, e.to_string()),
                }
            }
            _ => {
                let key = http::query(query, "key_to_del");
                if key.is_empty() {
                    return error(StatusCode::BAD_REQUEST, "key_to_del is required");
                }
                match s.store.delete_key(&key) {
                    Ok(Some(gone)) => {
                        s.presence.forget(&key);
                        json(StatusCode::OK, &gone)
                    }
                    Ok(None) => error(StatusCode::NOT_FOUND, "Key not found"),
                    Err(e) => http::internal(e),
                }
            }
        };
    }
    if authed {
        let mut client = header(h, "x-tam-client").trim().to_owned();
        if client.is_empty() {
            client = header(h, "user-agent")
                .split_whitespace()
                .next()
                .unwrap_or("unknown")
                .into()
        }
        client = client.chars().take(80).collect();
        let pending = if seg.is_empty() {
            header(h, "x-tam-pending")
                .parse::<i64>()
                .ok()
                .filter(|n| *n >= 0)
        } else {
            None
        };
        if s.presence.seen(key, &client, pending, (s.clock)())
            && let Err(e) = s.store.touch_key(key)
        {
            log::error!("record last_seen: {e}")
        }
    }
    let response = route(s, p, body, seg, query, authed);
    if authed
        && matches!(method, "POST" | "DELETE")
        && response.status().is_success()
        && !response.headers().contains_key("x-tam-stale")
        && s.presence.updated(key, (s.clock)())
        && let Err(e) = s.store.mark_key_updated(key)
    {
        log::error!("record last_update: {e}")
    }
    response
}
pub(crate) fn limited(wait: i64) -> Response {
    let mut response = error(
        StatusCode::TOO_MANY_REQUESTS,
        format!("Too many wrong passwords. Wait {wait} seconds and try again."),
    );
    response
        .headers_mut()
        .insert("retry-after", wait.to_string().parse().unwrap());
    response
}
fn respond<T: serde::Serialize>(result: Result<T>) -> Response {
    match result {
        Ok(v) => json(StatusCode::OK, &v),
        Err(e) => http::internal(e),
    }
}
fn route(
    s: &ServerState,
    p: &Parts,
    body: &[u8],
    seg: &[&str],
    query: &str,
    authed: bool,
) -> Response {
    let method = p.method.as_str();
    if seg.is_empty() {
        let mut doc = serde_json::json!({"whoami":"TAM Server","authenticated":authed,"healthy":true,"name":s.name,"version":s.version});
        match s.store.event() {
            Ok(Some(ev)) => doc["event"] = ev.event.into(),
            Ok(None) => {}
            Err(e) => return http::internal(e),
        }
        return json(StatusCode::OK, &doc);
    }
    if method == "POST" {
        if !http::json_type(&p.headers) {
            return error(
                StatusCode::BAD_REQUEST,
                "Content-Type must be application/json",
            );
        }
        if seg == ["backuprestore"] {
            let mut bf: BackupFile = match wire::decode(body) {
                Ok(v) => v,
                Err(e) => return error(StatusCode::BAD_REQUEST, e.to_string()),
            };
            if let Err(e) = bf.validate() {
                return error(StatusCode::BAD_REQUEST, e.to_string());
            }
            if header(&p.headers, "x-tam-merge") == "newer" {
                return match s.store.merge_newer(&bf) {
                    Ok(r) => json(
                        StatusCode::OK,
                        &serde_json::json!({"message":format!("{} rows added and {} updated; {} were as new or newer on the server and stayed.",r.added,r.updated,r.kept),"added":r.added,"updated":r.updated,"kept":r.kept}),
                    ),
                    Err(r_tam_core::Error::Validation(_)) => error(
                        StatusCode::CONFLICT,
                        "This client's copy belongs to another event than the one this server holds; nothing was written.",
                    ),
                    Err(e) => http::internal(e),
                };
            }
            return match s.store.import(&bf) {
                Ok(()) => json(
                    StatusCode::OK,
                    &serde_json::json!({"message":"Backup file imported successfully."}),
                ),
                Err(e) => http::internal(e),
            };
        }
        let form = match seg {
            ["prefixes"] => Form::Prefixes,
            ["baskets"] => Form::Baskets,
            ["drawing"] => Form::Drawing,
            _ => Form::Tickets,
        };
        let mut saves = match SaveBatch::decode(form, body) {
            Ok(v) => v,
            Err(e) => return error(StatusCode::BAD_REQUEST, e.to_string()),
        };
        if let Err(e) = saves.validate() {
            return error(StatusCode::BAD_REQUEST, e.to_string());
        }
        let bytes = match wire::go_json(&saves) {
            Ok(v) => v,
            Err(e) => return http::internal(e),
        };
        return ordered(s, p, &bytes, Operation::Save(saves));
    }
    if method == "DELETE" {
        let name = http::query(query, "p");
        if name.is_empty() {
            return error(StatusCode::BAD_REQUEST, "p is required");
        }
        return ordered(s, p, b"null", Operation::DeletePrefix(name));
    }
    let prefix = seg.get(1).copied().unwrap_or_default();
    let limits =
        if matches!(seg.first(), Some(&"tickets" | &"baskets" | &"drawing")) && seg.len() == 3 {
            match http::int(seg[2], "id") {
                Ok(id) => Some((id, id)),
                Err(e) => return error(StatusCode::BAD_REQUEST, e),
            }
        } else if seg.len() == 4 {
            let from = match http::int(seg[2], "from") {
                Ok(v) => v,
                Err(e) => return error(StatusCode::BAD_REQUEST, e),
            };
            let to = match http::int(seg[3], "to") {
                Ok(v) => v,
                Err(e) => return error(StatusCode::BAD_REQUEST, e),
            };
            Some((from.min(to), from.max(to)))
        } else {
            None
        };
    match seg {
        ["prefixes"] => respond(s.store.list_prefixes()),
        ["tickets", ..] => respond(match limits {
            Some((a, b)) => s.store.ticket_range(prefix, a, b),
            None if seg.len() == 2 => s.store.tickets_by_prefix(prefix),
            _ => s.store.all_tickets(),
        }),
        ["baskets", ..] => respond(match limits {
            Some((a, b)) => s.store.basket_range(prefix, a, b),
            None if seg.len() == 2 => s.store.baskets_by_prefix(prefix),
            _ => s.store.all_baskets(),
        }),
        ["drawing", ..] => respond(match limits {
            Some((a, b)) => s.store.drawing_range(prefix, a, b),
            None if seg.len() == 2 => s.store.drawing_by_prefix(prefix),
            _ => s.store.all_drawing(),
        }),
        ["reports", "byname", p] => respond(s.store.report_by_name(p)),
        ["reports", "bybasket", p] => respond(s.store.report_by_basket(p)),
        ["reports", "counts"] => respond(s.store.report_counts()),
        ["search", "tickets"] => respond(s.store.search_tickets(
            &http::query(query, "first_name"),
            &http::query(query, "last_name"),
            &http::query(query, "phone_number"),
        )),
        ["backuprestore"] => respond(s.store.export()),
        _ => error(StatusCode::NOT_FOUND, "Not Found"),
    }
}
fn ordered(s: &ServerState, p: &Parts, body: &[u8], op: Operation) -> Response {
    let client = header(&p.headers, "x-tam-client-name");
    let save = header(&p.headers, "x-tam-save")
        .parse::<i64>()
        .ok()
        .filter(|n| *n > 0);
    if client.is_empty() || client.len() > 64 || save.is_none() {
        return error(
            StatusCode::BAD_REQUEST,
            "X-TAM-Client-Name must name the client, in at most 64 characters, and X-TAM-Save number its save, above 0",
        );
    }
    let save = save.unwrap();
    let order = Order {
        client: client.into(),
        save,
    };
    let digest = wire::save_digest(
        p.method.as_str(),
        &http::decode_path(p.uri.path()),
        p.uri.query().unwrap_or_default(),
        body,
    );
    let result = match s.store.apply_ordered(&order, &digest, &op) {
        Ok(r) => r,
        Err(e) => return http::internal(e),
    };
    if result.outcome == Outcome::Behind {
        let mut r = json(
            StatusCode::CONFLICT,
            &serde_json::json!({"detail":format!("save {save} of this client is older than its save {}, which the server has applied; send it again with a number above {}",result.last,result.last),"last_save":result.last}),
        );
        r.headers_mut()
            .insert("x-tam-last-save", result.last.to_string().parse().unwrap());
        return r;
    }
    let mut response = match result.result {
        Some(OperationResult::Rows(rows)) => rows_response(rows),
        Some(OperationResult::DeletedPrefix(Some(row))) => json(StatusCode::OK, &row),
        Some(OperationResult::DeletedPrefix(None)) => {
            error(StatusCode::NOT_FOUND, "Prefix not found")
        }
        _ => match op {
            Operation::DeletePrefix(prefix) => json(
                StatusCode::OK,
                &models::Prefix {
                    prefix,
                    ..Default::default()
                },
            ),
            Operation::Save(ref saves) => match current(s, saves) {
                Ok(rows) => rows_response(rows),
                Err(r_tam_core::Error::Validation(detail)) => error(StatusCode::CONFLICT, detail),
                Err(e) => http::internal(e),
            },
            _ => http::internal("unexpected ordered result"),
        },
    };
    if result.outcome == Outcome::Repeat {
        response
            .headers_mut()
            .insert("x-tam-stale", "1".parse().unwrap());
    }
    response
}
fn rows_response(rows: StoredRows) -> Response {
    match rows.go_json() {
        Ok(mut data) => {
            data.push(b'\n');
            axum::response::IntoResponse::into_response((
                [("content-type", "application/json")],
                data,
            ))
        }
        Err(e) => http::internal(e),
    }
}
fn current(s: &ServerState, saves: &SaveBatch) -> Result<StoredRows> {
    macro_rules! rows {
        ($items:expr,$method:ident,$field:ident,$id:ident) => {{
            $items
                .iter()
                .map(|row| {
                    s.store
                        .$method(&row.$field.prefix, row.$field.$id)?
                        .ok_or_else(|| {
                            r_tam_core::Error::Validation(
                                "a row of this save is gone from the server".into(),
                            )
                        })
                })
                .collect::<std::result::Result<Vec<_>, _>>()?
        }};
    }
    Ok(match saves {
        SaveBatch::Prefixes(items) => StoredRows::Prefixes(
            items
                .iter()
                .map(|r| {
                    s.store.prefix_by_name(&r.prefix.prefix)?.ok_or_else(|| {
                        r_tam_core::Error::Validation(
                            "a row of this save is gone from the server".into(),
                        )
                    })
                })
                .collect::<std::result::Result<Vec<_>, _>>()?,
        ),
        SaveBatch::Tickets(items) => StoredRows::Tickets(rows!(items, ticket, ticket, t_id)),
        SaveBatch::Baskets(items) => StoredRows::Baskets(rows!(items, basket, basket, b_id)),
        SaveBatch::Drawing(items) => StoredRows::Drawing(rows!(items, basket, basket, b_id)),
    })
}

#[derive(Default)]
struct Description {
    description: String,
}
impl<'de> serde::Deserialize<'de> for Description {
    fn deserialize<D: serde::Deserializer<'de>>(decoder: D) -> std::result::Result<Self, D::Error> {
        struct Visitor;
        impl<'de> serde::de::Visitor<'de> for Visitor {
            type Value = Description;
            fn expecting(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
                f.write_str("a key description object")
            }
            fn visit_unit<E: serde::de::Error>(self) -> std::result::Result<Description, E> {
                Ok(Description::default())
            }
            fn visit_map<M: serde::de::MapAccess<'de>>(
                self,
                mut map: M,
            ) -> std::result::Result<Description, M::Error> {
                let mut value = Description::default();
                while let Some(key) = map.next_key::<String>()? {
                    let raw = map.next_value::<Box<serde_json::value::RawValue>>()?;
                    if key
                        .chars()
                        .map(|c| {
                            if c == 'ſ' {
                                's'
                            } else {
                                c.to_ascii_lowercase()
                            }
                        })
                        .eq("description".chars())
                        && let Some(text) = serde_json::from_str::<Option<String>>(raw.get())
                            .map_err(serde::de::Error::custom)?
                    {
                        value.description = text;
                    }
                }
                Ok(value)
            }
        }
        decoder.deserialize_any(Visitor)
    }
}

use crate::{ClientState, http};
use axum::{
    http::{HeaderMap, StatusCode as Code},
    response::{IntoResponse, Response},
};
use r_tam_core::{Error, Result, models::*, store::StoredRows};
use serde_json::{Value, json};
use std::time::Duration;
pub async fn dispatch(
    s: &ClientState,
    method: &str,
    path: &str,
    q: &str,
    headers: &HeaderMap,
    body: &[u8],
) -> Response {
    let result = execute(s, method, path, q, headers, body).await;
    match result {
        Ok(r) => r,
        Err(e) if e.is_validation() => http::error(Code::BAD_REQUEST, e.to_string()),
        Err(e) => http::internal(e),
    }
}
async fn execute(
    s: &ClientState,
    method: &str,
    path: &str,
    q: &str,
    headers: &HeaderMap,
    body: &[u8],
) -> Result<Response> {
    if method == "GET" {
        match path {
            "/api" | "/api/" => {
                if let Some(rc) = s.remote()? {
                    if s.online() {
                        let r = rc.with_timeout(Duration::from_secs(3)).get("/api").await;
                        if s.observe(&r)
                            && let Ok(r) = r
                            && r.ok()
                            && let Ok(v) = r.json::<Value>()
                            && v["whoami"] == "TAM Server"
                        {
                            return Ok(http::json(Code::OK, &v));
                        }
                    }
                    let healthy = s.connection.lock().unwrap_or_else(|e| e.into_inner()).state
                        == "unauthenticated";
                    return Ok(http::json(
                        Code::OK,
                        &json!({"whoami":"TAM Server","authenticated":false,"healthy":healthy}),
                    ));
                }
                return Ok(http::json(Code::OK, &json!({"whoami":"TAM Client"})));
            }
            "/api/settings" => return Ok(http::json(Code::OK, &s.settings.get())),
            "/api/status" => return Ok(http::json(Code::OK, &s.status().await?)),
            "/api/servers" => return Ok(http::json(Code::OK, &s.cache.servers().await)),
            "/api/outbox/failed" => {
                let failed = s.db(|st| st.failed()).await?;
                let entries:Vec<_>=failed.into_iter().map(|i|json!({"made":i.created_at,"save":format!("{} {}",i.method,i.path),"reason":i.last_error})).collect();
                return Ok(http::json(Code::OK, &entries));
            }
            "/api/backuprestore/local" => {
                return Ok(http::json(Code::OK, &s.db(|st| st.export()).await?));
            }
            "/api/backuprestore/remote" => {
                let Some(rc) = s.remote()? else {
                    return Ok(http::json(Code::OK, &json!({})));
                };
                let r = rc.get("/api/backuprestore").await;
                let r = match r {
                    Ok(r) => r,
                    Err(e) => {
                        s.failure(e);
                        return Ok(http::error(Code::BAD_GATEWAY, "Remote server unreachable"));
                    }
                };
                if !r.ok() {
                    return Ok(http::forward(r));
                }
                let mut backup = match r.json::<BackupFile>() {
                    Ok(v) => v,
                    Err(_) => {
                        return Ok(http::error(
                            Code::BAD_GATEWAY,
                            "Remote server sent an invalid backup",
                        ));
                    }
                };
                if backup.validate().is_err() {
                    return Ok(http::error(
                        Code::BAD_GATEWAY,
                        "Remote server sent an invalid backup",
                    ));
                }
                return Ok(http::json(Code::OK, &backup));
            }
            "/api/auth" => return proxy_auth(s, method, q, headers, body).await,
            _ => {}
        }
        if let Some(route) = ReadRoute::parse(path, q)? {
            return read(s, route).await;
        }
    }
    if method == "POST" {
        match path {
            "/api/settings" => {
                let _numbering = s.numbering.lock().await;
                let _sending = s.sending.lock().await;
                let bytes = body.to_vec();
                let settings = s.settings.clone();
                let activity = s
                    .shutdown
                    .enter()
                    .ok_or_else(|| Error::Internal("Client is shutting down".into()))?;
                let updated = tokio::task::spawn_blocking(move || {
                    let _activity = activity;
                    settings.update(|cur| {
                        let next = cur.merge(&bytes)?.normalize();
                        next.validate()?;
                        Ok(next)
                    })
                })
                .await
                .map_err(|e| Error::Internal(e.to_string()))??;
                s.reset();
                return Ok(http::json(Code::OK, &updated));
            }
            "/api/pair" => return Ok(crate::pair::pair(s, body).await),
            "/api/unpair" => return Ok(crate::pair::unpair(s, body).await),
            "/api/shutdown" => {
                if !s.shutdown_enabled {
                    return Ok(http::error(
                        Code::NOT_IMPLEMENTED,
                        "Shutdown is not available in this setup",
                    ));
                }
                let stop = s.shutdown.clone();
                tokio::spawn(async move {
                    tokio::task::yield_now().await;
                    stop.request()
                });
                return Ok(http::json(
                    Code::OK,
                    &json!({"message":"R-TAM client is shutting down."}),
                ));
            }
            "/api/outbox/retry" => {
                let _numbering = s.numbering.lock().await;
                let host = s.host.clone();
                let n = s.db(move |st| st.retry_failed(&host)).await?;
                let pending = s.db(|st| Ok(st.outbox_counts()?.0)).await?;
                s.kick.notify_one();
                return Ok(http::json(
                    Code::OK,
                    &json!({"message":format!("Retrying {n} save{}",if n==1{""}else{"s"}),"pending":pending}),
                ));
            }
            "/api/outbox/discard" => {
                let n = s.db(|st| st.discard_failed()).await?;
                return Ok(http::json(
                    Code::OK,
                    &json!({"message":format!("Discarded {n} save{}",if n==1{""}else{"s"})}),
                ));
            }
            "/api/auth" => return proxy_auth(s, method, q, headers, body).await,
            _ => {}
        }
        if path.starts_with("/api/backuprestore/") {
            return backup_write(s, path, body).await;
        }
        if let Some(form) = if path == "/api/search/tickets" {
            Some(Form::Tickets)
        } else {
            Form::from_path(path)
        } {
            let batch = SaveBatch::decode(form, body)?;
            return save(s, path, batch).await;
        }
    }
    if method == "DELETE" {
        match path {
            "/api/prefixes" => return delete_prefix(s, q).await,
            "/api/auth" => return proxy_auth(s, method, q, headers, body).await,
            _ => {}
        }
    }
    let known = matches!(
        path,
        "/api"
            | "/api/"
            | "/api/settings"
            | "/api/status"
            | "/api/servers"
            | "/api/pair"
            | "/api/unpair"
            | "/api/outbox/failed"
            | "/api/outbox/retry"
            | "/api/outbox/discard"
            | "/api/shutdown"
            | "/api/backuprestore/local"
            | "/api/backuprestore/remote"
    ) || Form::from_path(path).is_some()
        || ReadRoute::parse(path, q)?.is_some();
    Ok(http::error(
        if known {
            Code::METHOD_NOT_ALLOWED
        } else {
            Code::NOT_FOUND
        },
        if known {
            "Method Not Allowed"
        } else {
            "Not Found"
        },
    ))
}
async fn proxy_auth(
    s: &ClientState,
    method: &str,
    q: &str,
    headers: &HeaderMap,
    body: &[u8],
) -> Result<Response> {
    let Some(rc) = s.remote()? else {
        return Ok(http::error(Code::INTERNAL_SERVER_ERROR, "Not configured."));
    };
    let path = if method == "DELETE" {
        format!(
            "/api/auth?key_to_del={}",
            http::escape(&http::query(q, "key_to_del"))
        )
    } else {
        "/api/auth".into()
    };
    let r = rc
        .request(
            method,
            &path,
            &[("TAM-PW", http::header(headers, "TAM-PWD"))],
            (method == "POST").then_some(body),
        )
        .await;
    match r {
        Err(e) => {
            s.failure(e);
            Ok(http::error(Code::BAD_GATEWAY, "Remote server unreachable"))
        }
        Ok(r) if !r.ok() => Ok(http::forward(r)),
        Ok(r) => Ok((
            Code::from_u16(r.status).unwrap_or(Code::OK),
            [("content-type", "application/json")],
            r.body,
        )
            .into_response()),
    }
}
async fn backup_write(s: &ClientState, path: &str, body: &[u8]) -> Result<Response> {
    let _numbering = s.numbering.lock().await;
    let _sending = s.sending.lock().await;
    if s.db(|st| st.outbox_waiting()).await? {
        return Ok(http::error(
            Code::CONFLICT,
            "This client still has saves waiting for the server; send or set them aside before restoring or pushing data.",
        ));
    }
    if path == "/api/backuprestore/local" {
        let mut backup: r_tam_core::models::BackupFile = r_tam_core::wire::decode(body)?;
        backup.validate()?;
        s.db(move |st| st.import(&backup)).await?;
        return Ok(http::json(
            Code::OK,
            &json!({"message":"Data loaded successfully."}),
        ));
    }
    let target = path.strip_prefix("/api/backuprestore/push/");
    if let Some(target) = target
        && !matches!(target, "prefixes" | "tickets" | "baskets")
    {
        return Ok(http::error(
            Code::BAD_REQUEST,
            "Can only push prefixes, tickets, or baskets.",
        ));
    }
    if target.is_none() && path != "/api/backuprestore/remote" {
        return Ok(http::error(Code::NOT_FOUND, "Not Found"));
    }
    let Some(rc) = s.remote()? else {
        return Ok(http::error(Code::INTERNAL_SERVER_ERROR, "Server not set."));
    };
    let mut backup = if target.is_some() {
        s.db(|st| st.export()).await?
    } else {
        r_tam_core::wire::decode::<BackupFile>(body)?
    };
    backup.validate()?;
    if let Some(target) = target {
        if target != "prefixes" {
            backup.prefixes.clear()
        }
        if target != "tickets" {
            backup.tickets.clear()
        }
        if target != "baskets" {
            backup.baskets.clear()
        }
    }
    let bytes = r_tam_core::wire::go_json(&backup)?;
    let r = rc
        .request(
            "POST",
            "/api/backuprestore",
            if target.is_some() {
                &[("X-TAM-Merge", "newer")]
            } else {
                &[]
            },
            Some(&bytes),
        )
        .await;
    match r {
        Err(e) => {
            s.failure(e);
            Ok(http::error(Code::BAD_GATEWAY, "Remote server unreachable"))
        }
        Ok(r) if !r.ok() => Ok(http::forward(r)),
        Ok(_) => {
            s.connection
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .pull_needed = true;
            s.kick.notify_one();
            Ok(http::json(
                Code::OK,
                &json!({"message":if let Some(t)=target{format!("{}{} pushed.",t[..1].to_uppercase(),&t[1..])}else{"Backup file imported successfully.".into()}}),
            ))
        }
    }
}
async fn save(s: &ClientState, path: &str, batch: SaveBatch) -> Result<Response> {
    let numbering = std::sync::Arc::new(s.numbering.clone().lock_owned().await);
    let Some(rc) = s.remote()? else {
        let rows = s
            .db_guarded(numbering.clone(), move |st| st.save(&batch))
            .await?;
        return Ok(rows_response(rows, false));
    };
    if !s.online() || s.db(|st| st.outbox_waiting()).await? {
        let host = s.host.clone();
        let path = path.to_owned();
        let result = s
            .db_guarded(numbering.clone(), move |st| {
                st.accept_offline(&host, "POST", &path, &batch)
            })
            .await?;
        s.kick.notify_one();
        return Ok(rows_response(result.result.rows, true));
    }
    let host = s.host.clone();
    let request_path = path.to_owned();
    let body = r_tam_core::wire::go_json(&batch)?;
    // Own dispatch before publishing the intent: replay also selects under this
    // lock, so it must not consume a live handler's acknowledgment.
    let sending = std::sync::Arc::new(s.sending.clone().lock_owned().await);
    let live = s.begin_direct();
    let intent = live.intent.clone();
    let mut item = s
        .db_guarded((numbering.clone(), sending.clone()), move |st| {
            let item = st.journal_online(&host, "POST", &request_path, &body)?;
            intent.publish(item.id);
            Ok(item)
        })
        .await?;
    for attempt in 0..3 {
        let r = rc
            .with_timeout(Duration::from_secs(5))
            .ordered_request("POST", path, &item.order, 1, Some(&item.body))
            .await;
        if let Ok(res) = &r
            && let Some(last) = crate::sync::last_save(res)
            && attempt < 2
        {
            let id = item.id;
            let host = s.host.clone();
            item.order = s
                .db_guarded((numbering.clone(), sending.clone()), move |st| {
                    st.renumber_outbox(id, &host, last)
                })
                .await?;
            continue;
        }
        if s.observe(&r) {
            let res = r?;
            if !res.ok() {
                let id = item.id;
                s.db_guarded((numbering.clone(), sending.clone()), move |st| {
                    st.reject_online(id)
                })
                .await?;
                return Ok(http::forward(res));
            }
            let id = item.id;
            let answer = res.body;
            match s
                .db_guarded((numbering.clone(), sending.clone()), move |st| {
                    st.reconcile(id, &answer)
                })
                .await
            {
                Ok(result) => return Ok(rows_response(result.rows, false)),
                Err(e) => s.failure(e),
            }
        }
        // The intent is already durable. Promotion accepts offline values in the
        // same transaction while retaining that exact request and number.
        drop(live);
        let id = item.id;
        let result = s
            .db_guarded((numbering.clone(), sending.clone()), move |st| {
                st.accept_journal_offline(id)
            })
            .await?;
        s.kick.notify_one();
        return Ok(rows_response(result.rows, true));
    }
    Err(Error::Internal("save retries exhausted".into()))
}
fn rows_response(rows: StoredRows, queued: bool) -> Response {
    let mut r = match rows.go_json() {
        Ok(body) => (Code::OK, [("content-type", "application/json")], body).into_response(),
        Err(e) => http::internal(e),
    };
    if queued {
        r.headers_mut()
            .insert("X-TAM-Queued", axum::http::HeaderValue::from_static("1"));
    }
    r
}
async fn delete_prefix(s: &ClientState, q: &str) -> Result<Response> {
    let name = http::query(q, "p");
    if name.is_empty() {
        return Ok(http::error(Code::BAD_REQUEST, "p is required"));
    }
    let numbering = std::sync::Arc::new(s.numbering.clone().lock_owned().await);
    let Some(rc) = s.remote()? else {
        let gone = s
            .db_guarded(numbering.clone(), move |st| st.delete_prefix(&name))
            .await?;
        return Ok(match gone {
            Some(p) => http::json(Code::OK, &p),
            None => http::error(Code::NOT_FOUND, "Prefix not found"),
        });
    };
    let path = format!("/api/prefixes?p={}", http::escape(&name));
    if !s.online() || s.db(|st| st.outbox_waiting()).await? {
        let host = s.host.clone();
        match s
            .db_guarded(numbering.clone(), move |st| {
                st.accept_delete_offline(&host, &path, &name)
            })
            .await
        {
            Ok((_, p)) => {
                let mut r = http::json(Code::OK, &p);
                r.headers_mut()
                    .insert("X-TAM-Queued", axum::http::HeaderValue::from_static("1"));
                s.kick.notify_one();
                return Ok(r);
            }
            Err(Error::Validation(_)) => {
                return Ok(http::error(Code::NOT_FOUND, "Prefix not found"));
            }
            Err(e) => return Err(e),
        }
    }
    let host = s.host.clone();
    let request_path = path.clone();
    let local_name = name.clone();
    let prior = s.db(move |st| st.prefix_by_name(&local_name)).await?;
    let sending = std::sync::Arc::new(s.sending.clone().lock_owned().await);
    let live = s.begin_direct();
    let intent = live.intent.clone();
    let mut item = s
        .db_guarded((numbering.clone(), sending.clone()), move |st| {
            let item = st.journal_online(&host, "DELETE", &request_path, &[])?;
            intent.publish(item.id);
            Ok(item)
        })
        .await?;
    for attempt in 0..3 {
        let r = rc
            .with_timeout(Duration::from_secs(5))
            .ordered_request("DELETE", &path, &item.order, 1, None)
            .await;
        if let Ok(res) = &r
            && let Some(last) = crate::sync::last_save(res)
            && attempt < 2
        {
            let id = item.id;
            let host = s.host.clone();
            item.order = s
                .db_guarded((numbering.clone(), sending.clone()), move |st| {
                    st.renumber_outbox(id, &host, last)
                })
                .await?;
            continue;
        }
        if s.observe(&r) {
            let res = r?;
            if !res.ok() && res.status != 404 {
                let id = item.id;
                s.db_guarded((numbering.clone(), sending.clone()), move |st| {
                    st.reject_online(id)
                })
                .await?;
                return Ok(http::forward(res));
            }
            let id = item.id;
            let p = name.clone();
            let remote_had_it = res.status != 404;
            match s
                .db_guarded((numbering.clone(), sending.clone()), move |st| {
                    st.reconcile_delete(id, &p, (res.status != 404).then_some(res.body.as_slice()))
                })
                .await
            {
                Ok(()) => {
                    if prior.is_none() && !remote_had_it {
                        return Ok(http::error(Code::NOT_FOUND, "Prefix not found"));
                    }
                    return Ok(http::json(
                        Code::OK,
                        &prior.unwrap_or(Prefix {
                            prefix: name,
                            ..Default::default()
                        }),
                    ));
                }
                Err(e) => s.failure(e),
            }
        }
        drop(live);
        let id = item.id;
        let p = s
            .db_guarded((numbering.clone(), sending.clone()), move |st| {
                st.accept_delete_journal_offline(id)
            })
            .await?;
        let mut response = http::json(
            Code::OK,
            &p.unwrap_or(Prefix {
                prefix: name,
                ..Default::default()
            }),
        );
        response
            .headers_mut()
            .insert("X-TAM-Queued", axum::http::HeaderValue::from_static("1"));
        s.kick.notify_one();
        return Ok(response);
    }
    Err(Error::Internal("delete retries exhausted".into()))
}

#[derive(Clone)]
struct ReadRoute {
    kind: String,
    prefix: Option<String>,
    from: Option<i64>,
    to: Option<i64>,
    remote_path: String,
    search: (String, String, String),
}
impl ReadRoute {
    fn parse(path: &str, q: &str) -> Result<Option<Self>> {
        let raw: Vec<_> = path.trim_start_matches('/').split('/').collect();
        if raw.first() != Some(&"api") {
            return Ok(None);
        }
        let p: Vec<String> = raw.into_iter().map(http::decode_path).collect();
        let mut r = Self {
            kind: String::new(),
            prefix: None,
            from: None,
            to: None,
            remote_path: path.to_owned(),
            search: Default::default(),
        };
        if p.len() == 3 && p[1] == "search" && p[2] == "tickets" {
            r.kind = "search".into();
            r.search = (
                http::query(q, "first_name"),
                http::query(q, "last_name"),
                http::query(q, "phone_number"),
            );
            r.remote_path = format!(
                "{path}?first_name={}&last_name={}&phone_number={}",
                http::escape(&r.search.0),
                http::escape(&r.search.1),
                http::escape(&r.search.2)
            );
            return Ok(Some(r));
        }
        if p.len() >= 3
            && p[1] == "reports"
            && ((p[2] == "counts" && p.len() == 3)
                || (matches!(p[2].as_str(), "byname" | "bybasket") && p.len() == 4))
        {
            r.kind = p[2].clone();
            r.prefix = p.get(3).cloned();
            return Ok(Some(r));
        }
        if p.len() < 2
            || !matches!(
                p[1].as_str(),
                "prefixes" | "tickets" | "baskets" | "drawing"
            )
            || p.len() > 5
            || (p[1] == "prefixes" && p.len() != 2)
        {
            return Ok(None);
        }
        r.kind = p[1].clone();
        r.prefix = p.get(2).cloned();
        if let Some(id) = p.get(3) {
            r.from = Some(
                http::int(id, if p.len() == 4 { "id" } else { "from" })
                    .map_err(Error::Validation)?,
            )
        }
        if let Some(id) = p.get(4) {
            let to = http::int(id, "to").map_err(Error::Validation)?;
            let from = r.from.unwrap();
            let (from, to) = if from <= to { (from, to) } else { (to, from) };
            let to = to.min(from.saturating_add(300));
            r.from = Some(from);
            r.to = Some(to);
            r.remote_path = format!(
                "/api/{}/{}/{from}/{to}",
                r.kind,
                http::path_escape(r.prefix.as_ref().unwrap())
            )
        }
        Ok(Some(r))
    }
    fn local(&self, st: r_tam_core::store::Store) -> Result<Vec<Value>> {
        fn values<T: serde::Serialize>(v: Vec<T>) -> Result<Vec<Value>> {
            v.into_iter()
                .map(|v| serde_json::to_value(v).map_err(Error::from))
                .collect()
        }
        let p = self.prefix.as_deref().unwrap_or("");
        let from = self.from;
        let to = self.to;
        match self.kind.as_str() {
            "prefixes" => values(st.list_prefixes()?),
            "tickets" => values(match (from, to, self.prefix.as_ref()) {
                (Some(a), Some(b), _) => st.ticket_range(p, a, b)?,
                (Some(id), None, _) => st.ticket(p, id)?.into_iter().collect(),
                (_, _, Some(_)) => st.tickets_by_prefix(p)?,
                _ => st.all_tickets()?,
            }),
            "baskets" => values(match (from, to, self.prefix.as_ref()) {
                (Some(a), Some(b), _) => st.basket_range(p, a, b)?,
                (Some(id), None, _) => st.basket(p, id)?.into_iter().collect(),
                (_, _, Some(_)) => st.baskets_by_prefix(p)?,
                _ => st.all_baskets()?,
            }),
            "drawing" => values(match (from, to, self.prefix.as_ref()) {
                (Some(a), Some(b), _) => st.drawing_range(p, a, b)?,
                (Some(id), None, _) => st.drawing_line(p, id)?.into_iter().collect(),
                (_, _, Some(_)) => st.drawing_by_prefix(p)?,
                _ => st.all_drawing()?,
            }),
            "counts" => values(st.report_counts()?),
            "byname" => values(st.report_by_name(p)?),
            "bybasket" => values(st.report_by_basket(p)?),
            "search" => {
                values(st.search_tickets(&self.search.0, &self.search.1, &self.search.2)?)
            }
            _ => Ok(vec![]),
        }
    }
    fn decode_and_cache(&self, st: r_tam_core::store::Store, body: &[u8]) -> Result<Vec<Value>> {
        macro_rules! rows {
            ($ty:ty,$cache:expr) => {{
                let v: Vec<$ty> =
                    r_tam_core::wire::decode::<Option<Vec<$ty>>>(body)?.unwrap_or_default();
                $cache(&st, &v)?;
                v.into_iter()
                    .map(|v| serde_json::to_value(v).map_err(Error::from))
                    .collect()
            }};
        }
        macro_rules! report {
            ($ty:ty) => {{
                let v: Vec<$ty> =
                    r_tam_core::wire::decode::<Option<Vec<$ty>>>(body)?.unwrap_or_default();
                v.into_iter()
                    .map(|v| serde_json::to_value(v).map_err(Error::from))
                    .collect()
            }};
        }
        match self.kind.as_str() {
            "prefixes" => rows!(Prefix, r_tam_core::store::Store::upsert_prefixes),
            "tickets" | "search" => rows!(Ticket, r_tam_core::store::Store::upsert_tickets),
            "baskets" => rows!(Basket, r_tam_core::store::Store::upsert_baskets),
            "drawing" => {
                let v: Vec<DrawingLine> =
                    r_tam_core::wire::decode::<Option<Vec<DrawingLine>>>(body)?.unwrap_or_default();
                let baskets: Vec<_> = v
                    .iter()
                    .map(|d| Basket {
                        prefix: d.prefix.clone(),
                        b_id: d.b_id,
                        description: d.description.clone(),
                        winning_ticket: d.winning_ticket,
                        win_rev: d.win_rev,
                        ..Default::default()
                    })
                    .collect();
                st.upsert_winning(&baskets)?;
                v.into_iter()
                    .map(|v| serde_json::to_value(v).map_err(Error::from))
                    .collect()
            }
            "counts" => report!(ReportCountLine),
            "byname" => report!(ReportByNameLine),
            "bybasket" => report!(ReportByBasketLine),
            _ => Ok(vec![]),
        }
    }
    fn placeholder(&self, id: i64, pref: &str) -> Value {
        let p = self.prefix.clone().unwrap_or_default();
        match self.kind.as_str() {
            "tickets" => serde_json::to_value(Ticket {
                prefix: p,
                t_id: id,
                pref: pref.into(),
                ..Default::default()
            })
            .unwrap(),
            "baskets" => serde_json::to_value(Basket {
                prefix: p,
                b_id: id,
                ..Default::default()
            })
            .unwrap(),
            _ => serde_json::to_value(DrawingLine {
                prefix: p,
                b_id: id,
                ..Default::default()
            })
            .unwrap(),
        }
    }
}
async fn read(s: &ClientState, route: ReadRoute) -> Result<Response> {
    let remote = s.remote()?;
    let mut rows = None;
    let mut copied = false;
    if let Some(rc) = &remote
        && s.online()
        && !s.db(|st| st.outbox_waiting()).await?
    {
        // Reports never write mirror rows. An unrelated cache refresh must not
        // invalidate an otherwise valid server report, but switching events or
        // accepting a queued save still requires using the current local copy.
        let report = matches!(route.kind.as_str(), "counts" | "byname" | "bybasket");
        let (generation, event) = s
            .db(move |st| {
                if report {
                    Ok((None, Some(st.mirror_event()?)))
                } else {
                    Ok((Some(st.generation()?), None))
                }
            })
            .await?;
        let configuration = s.settings.get();
        let r = rc
            .with_timeout(Duration::from_secs(4))
            .get(&route.remote_path)
            .await;
        if s.observe(&r)
            && let Ok(r) = r
            && r.ok()
        {
            let _numbering = s.numbering.lock().await;
            let _sending = s.sending.lock().await;
            let route = route.clone();
            if s.settings.get() == configuration {
                rows = s
                    .db(move |st| {
                        let changed = if let Some(generation) = generation {
                            st.generation()? != generation
                        } else {
                            Some(st.mirror_event()?) != event
                        };
                        if changed || st.outbox_waiting()? {
                            return Err(Error::Validation(
                                "The local copy changed during the download".into(),
                            ));
                        }
                        route.decode_and_cache(st, &r.body)
                    })
                    .await
                    .ok()
            }
        }
    }
    let rows = if let Some(v) = rows {
        v
    } else {
        copied = remote.is_some();
        let route = route.clone();
        s.db(move |st| route.local(st)).await?
    };
    let pref = s.settings.get().default_pref;
    if let Some(from) = route.from {
        if let Some(to) = route.to {
            let id = if route.kind == "tickets" {
                "t_id"
            } else {
                "b_id"
            };
            let out: Vec<_> = (from..=to)
                .map(|n| {
                    rows.iter()
                        .find(|v| v[id].as_i64() == Some(n))
                        .cloned()
                        .unwrap_or_else(|| route.placeholder(n, &pref))
                })
                .collect();
            return Ok(http::json(Code::OK, &out));
        }
        return Ok(http::json(
            Code::OK,
            &rows
                .into_iter()
                .next()
                .unwrap_or_else(|| route.placeholder(from, &pref)),
        ));
    }
    let mut r = http::json(Code::OK, &rows);
    if copied {
        r.headers_mut()
            .insert("X-TAM-Copy", axum::http::HeaderValue::from_static("1"));
    }
    Ok(r)
}
#[cfg(all(test, not(windows)))]
mod replay_race_tests {
    use super::*;
    use axum::{body::Body, http::Request};
    use r_tam_core::{schema::Role, store::Store};
    use std::{future::Future, task::Poll};
    use tower::ServiceExt;

    async fn admin_cookie(app: &axum::Router) -> String {
        let visitor = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/admin/")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        let cookie = visitor.headers()["set-cookie"]
            .to_str()
            .unwrap()
            .split(';')
            .next()
            .unwrap()
            .to_owned();
        let html = String::from_utf8(
            axum::body::to_bytes(visitor.into_body(), 100000)
                .await
                .unwrap()
                .to_vec(),
        )
        .unwrap();
        let csrf = html
            .split("name=\"csrf\" value=\"")
            .nth(1)
            .unwrap()
            .split('"')
            .next()
            .unwrap();
        let login = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/admin/login")
                    .header("cookie", cookie)
                    .header("content-type", "application/x-www-form-urlencoded")
                    .body(Body::from(format!("csrf={csrf}&password=test-pass")))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(login.status(), Code::SEE_OTHER);
        login.headers()["set-cookie"]
            .to_str()
            .unwrap()
            .split(';')
            .next()
            .unwrap()
            .to_owned()
    }
    async fn admin_queued(app: &axum::Router, cookie: &str) -> serde_json::Value {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/admin/status")
                    .header("cookie", cookie)
                    .header("accept", "application/json")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), Code::OK);
        let doc: serde_json::Value = serde_json::from_slice(
            &axum::body::to_bytes(response.into_body(), 100000)
                .await
                .unwrap(),
        )
        .unwrap();
        doc["clients"][0]["queued"].clone()
    }

    #[tokio::test]
    async fn waiting_counts_excludes_exact_live_id_without_hiding_older_intents() {
        let dir = tempfile::tempdir().unwrap();
        let client = ClientState::new(
            Store::open(&dir.path().join("client.db"), Role::Client).unwrap(),
            &dir.path().join("settings.json"),
        );
        let older = client
            .store
            .journal_online(
                "host",
                "POST",
                "/api/tickets",
                br#"[{"prefix":"A","t_id":1}]"#,
            )
            .unwrap();
        let current = client
            .store
            .journal_online(
                "host",
                "POST",
                "/api/tickets",
                br#"[{"prefix":"A","t_id":2}]"#,
            )
            .unwrap();
        let live = client.begin_direct();
        live.intent.publish(current.id);
        assert_eq!(client.waiting_counts().await.unwrap(), (1, 0));
        assert_eq!(client.store.outbox_counts().unwrap(), (2, 0));
        live.intent.publish(older.id + current.id + 1);
        assert_eq!(client.waiting_counts().await.unwrap(), (2, 0));
        live.intent.publish(current.id);
        drop(live);
        assert_eq!(client.waiting_counts().await.unwrap(), (2, 0));
    }

    async fn uncertain_direct_is_waiting_after_promotion(method: &str) {
        let server_dir = tempfile::tempdir().unwrap();
        let server_store = Store::open(&server_dir.path().join("server.db"), Role::Server).unwrap();
        let prefix = Prefix {
            prefix: "A".into(),
            color: "blue".into(),
            ..Default::default()
        };
        server_store
            .upsert_prefixes(std::slice::from_ref(&prefix))
            .unwrap();
        let key = server_store.create_key("uncertain regression").unwrap();
        let app = r_tam_server::router(r_tam_server::ServerState::new(
            server_store,
            r_tam_server::auth::Password::load(server_dir.path(), "test-pass").unwrap(),
        ));
        let cookie = admin_cookie(&app).await;
        let corrupt = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(true));
        let served = app.clone().layer(axum::middleware::from_fn(
            move |request: Request<Body>, next: axum::middleware::Next| {
                let corrupt = corrupt.clone();
                async move {
                    let save = request.headers().contains_key("X-TAM-Save");
                    let answer = next.run(request).await;
                    if save && corrupt.swap(false, std::sync::atomic::Ordering::SeqCst) {
                        (
                            Code::OK,
                            [("content-type", "text/html")],
                            "<html>uncertain gateway</html>",
                        )
                            .into_response()
                    } else {
                        answer
                    }
                }
            },
        ));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let peer = tokio::spawn(async move { axum::serve(listener, served).await.unwrap() });
        let client_dir = tempfile::tempdir().unwrap();
        let client = ClientState::new(
            Store::open(&client_dir.path().join("client.db"), Role::Client).unwrap(),
            &client_dir.path().join("settings.json"),
        );
        client
            .settings
            .update(|mut cfg| {
                cfg.remote_server = "127.0.0.1".into();
                cfg.remote_port = port.to_string();
                cfg.remote_key = key.auth_key;
                Ok(cfg)
            })
            .unwrap();
        client.store.upsert_prefixes(&[prefix]).unwrap();
        let deleting = method == "DELETE";
        let response = crate::router(client.clone())
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(if deleting {
                        "/api/prefixes?p=A"
                    } else {
                        "/api/tickets"
                    })
                    .header("content-type", "application/json")
                    .body(Body::from(if deleting {
                        ""
                    } else {
                        r#"[{"prefix":"A","t_id":1,"first_name":"uncertain"}]"#
                    }))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), Code::OK);
        assert_eq!(response.headers()["X-TAM-Queued"], "1");
        assert_eq!(client.status().await.unwrap()["pending"], 1);
        assert_eq!(client.store.outbox_counts().unwrap(), (1, 0));
        client.sync_once(true).await.unwrap();
        assert_eq!(admin_queued(&app, &cookie).await, 1);
        assert_eq!(client.status().await.unwrap()["pending"], 0);
        assert_eq!(client.store.outbox_counts().unwrap(), (0, 0));
        client.sync_once(true).await.unwrap();
        assert_eq!(admin_queued(&app, &cookie).await, 0);
        peer.abort();
    }
    #[tokio::test]
    async fn uncertain_post_promotion_reports_waiting_until_verified_replay() {
        uncertain_direct_is_waiting_after_promotion("POST").await;
    }
    #[tokio::test]
    async fn uncertain_delete_promotion_reports_waiting_until_verified_replay() {
        uncertain_direct_is_waiting_after_promotion("DELETE").await;
    }

    fn cancelled_sqlite_journal_keeps_later_requests_behind_it(first_method: &str) {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .max_blocking_threads(2)
            .build()
            .unwrap()
            .block_on(async {
                let server_dir = tempfile::tempdir().unwrap();
                let server_store =
                    Store::open(&server_dir.path().join("server.db"), Role::Server).unwrap();
                let prefix = Prefix {
                    prefix: "A".into(),
                    color: "blue".into(),
                    ..Default::default()
                };
                server_store
                    .upsert_prefixes(std::slice::from_ref(&prefix))
                    .unwrap();
                let key = server_store.create_key("cancelled SQL regression").unwrap();
                let password =
                    r_tam_server::auth::Password::load(server_dir.path(), "test-pass").unwrap();
                let seen = std::sync::Arc::new(std::sync::Mutex::new(Vec::<i64>::new()));
                let wire = seen.clone();
                let server = r_tam_server::router(r_tam_server::ServerState::new(
                    server_store.clone(),
                    password,
                ))
                .layer(axum::middleware::from_fn(
                    move |request: Request<Body>, next: axum::middleware::Next| {
                        let wire = wire.clone();
                        async move {
                            if let Some(number) = request.headers().get("X-TAM-Save") {
                                wire.lock()
                                    .unwrap()
                                    .push(number.to_str().unwrap().parse().unwrap());
                            }
                            next.run(request).await
                        }
                    },
                ));
                let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
                let port = listener.local_addr().unwrap().port();
                let admin = server.clone();
                let cookie = admin_cookie(&admin).await;
                let peer =
                    tokio::spawn(async move { axum::serve(listener, server).await.unwrap() });
                let client_dir = tempfile::tempdir().unwrap();
                let client = ClientState::new(
                    Store::open(&client_dir.path().join("client.db"), Role::Client).unwrap(),
                    &client_dir.path().join("settings.json"),
                );
                client
                    .settings
                    .update(|mut cfg| {
                        cfg.remote_server = "127.0.0.1".into();
                        cfg.remote_port = port.to_string();
                        cfg.remote_key = key.auth_key;
                        Ok(cfg)
                    })
                    .unwrap();
                client.store.upsert_prefixes(&[prefix]).unwrap();
                let writer = rusqlite::Connection::open(client.store.path()).unwrap();
                writer.execute_batch("BEGIN IMMEDIATE").unwrap();
                let deleting = first_method == "DELETE";
                let first_path = if deleting {
                    "/api/prefixes?p=A"
                } else {
                    "/api/tickets"
                };
                let first_body = if deleting {
                    ""
                } else {
                    r#"[{"prefix":"A","t_id":1,"first_name":"old"}]"#
                };
                let request = Request::builder()
                    .method(first_method)
                    .uri(first_path)
                    .header("content-type", "application/json")
                    .body(Body::from(first_body))
                    .unwrap();
                let mut first = Box::pin(crate::router(client.clone()).oneshot(request));
                // Complete the read, then submit the journal while an external
                // SQLite writer prevents its BEGIN IMMEDIATE from completing.
                while client.sending.try_lock().is_ok() {
                    std::future::poll_fn(|cx| {
                        assert!(first.as_mut().poll(cx).is_pending());
                        Poll::Ready(())
                    })
                    .await;
                    tokio::task::spawn_blocking(|| ()).await.unwrap();
                }
                drop(first);
                assert!(client.store.pending().unwrap().is_empty());
                assert_eq!(client.waiting_counts().await.unwrap(), (0, 0));
                assert!(client.store.ticket("A", 1).unwrap().is_none());
                assert_eq!(
                    client.store.prefix_by_name("A").unwrap().unwrap().color,
                    "blue"
                );
                assert!(seen.lock().unwrap().is_empty());
                let (path, body) = if deleting {
                    ("/api/prefixes", r#"[{"prefix":"A","color":"green"}]"#)
                } else {
                    (
                        "/api/tickets",
                        r#"[{"prefix":"A","t_id":1,"first_name":"new"}]"#,
                    )
                };
                let request = Request::builder()
                    .method("POST")
                    .uri(path)
                    .header("content-type", "application/json")
                    .body(Body::from(body))
                    .unwrap();
                let mut second = Box::pin(crate::router(client.clone()).oneshot(request));
                // WAL readers can observe the empty committed outbox while the
                // first journal is blocked. This sentinel completes on the other
                // blocking worker, so the second caller can reach its next await.
                std::future::poll_fn(|cx| {
                    assert!(second.as_mut().poll(cx).is_pending());
                    Poll::Ready(())
                })
                .await;
                while client.sending.try_lock().is_ok() {
                    std::future::poll_fn(|cx| {
                        assert!(second.as_mut().poll(cx).is_pending());
                        Poll::Ready(())
                    })
                    .await;
                    if client.sending.try_lock().is_err() {
                        break;
                    }
                    tokio::task::spawn_blocking(|| ()).await.unwrap();
                }
                writer.execute_batch("COMMIT").unwrap();
                let response = tokio::time::timeout(Duration::from_secs(3), second)
                    .await
                    .unwrap()
                    .unwrap();
                assert_eq!(response.status(), Code::OK);
                assert_eq!(client.status().await.unwrap()["pending"], 2);
                assert_eq!(client.store.outbox_counts().unwrap(), (2, 0));
                for path in [
                    "/api/backuprestore/local",
                    "/api/backuprestore/push/prefixes",
                ] {
                    let response = crate::router(client.clone())
                        .oneshot(
                            Request::builder()
                                .method("POST")
                                .uri(path)
                                .header("content-type", "application/json")
                                .body(Body::from("{}"))
                                .unwrap(),
                        )
                        .await
                        .unwrap();
                    assert_eq!(response.status(), Code::CONFLICT);
                }
                client.sync_once(true).await.unwrap();
                assert_eq!(admin_queued(&admin, &cookie).await, 2);
                client.sync_once(true).await.unwrap();
                assert_eq!(admin_queued(&admin, &cookie).await, 0);
                assert_eq!(
                    *seen.lock().unwrap(),
                    vec![1, 2],
                    "later handler bypassed the canceled journal"
                );
                assert!(client.store.pending().unwrap().is_empty());
                assert!(client.store.failed().unwrap().is_empty());
                if deleting {
                    assert_eq!(
                        client.store.prefix_by_name("A").unwrap().unwrap().color,
                        "green"
                    );
                    assert_eq!(
                        server_store.prefix_by_name("A").unwrap().unwrap().color,
                        "green"
                    );
                } else {
                    assert_eq!(
                        client.store.ticket("A", 1).unwrap().unwrap().first_name,
                        "new"
                    );
                    assert_eq!(
                        server_store.ticket("A", 1).unwrap().unwrap().first_name,
                        "new"
                    );
                }
                peer.abort();
            });
    }
    #[test]
    fn cancelled_post_journal_cannot_be_overtaken_during_sqlite_contention() {
        cancelled_sqlite_journal_keeps_later_requests_behind_it("POST");
    }
    #[test]
    fn cancelled_delete_journal_cannot_be_overtaken_during_sqlite_contention() {
        cancelled_sqlite_journal_keeps_later_requests_behind_it("DELETE");
    }

    fn replay_cannot_consume_live_direct_intent(method: &str, path: &str, body: &'static str) {
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .max_blocking_threads(1)
            .build()
            .unwrap()
            .block_on(async {
                let server_dir = tempfile::tempdir().unwrap();
                let server_store =
                    Store::open(&server_dir.path().join("server.db"), Role::Server).unwrap();
                let prefix = Prefix {
                    prefix: "A".into(),
                    color: "blue".into(),
                    ..Default::default()
                };
                server_store
                    .upsert_prefixes(std::slice::from_ref(&prefix))
                    .unwrap();
                let key = server_store.create_key("race regression").unwrap();
                let password =
                    r_tam_server::auth::Password::load(server_dir.path(), "test-pass").unwrap();
                let server = r_tam_server::router(r_tam_server::ServerState::new(
                    server_store.clone(),
                    password,
                ));
                let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
                let port = listener.local_addr().unwrap().port();
                let peer =
                    tokio::spawn(async move { axum::serve(listener, server).await.unwrap() });
                let client_dir = tempfile::tempdir().unwrap();
                let client = ClientState::new(
                    Store::open(&client_dir.path().join("client.db"), Role::Client).unwrap(),
                    &client_dir.path().join("settings.json"),
                );
                client
                    .settings
                    .update(|mut cfg| {
                        cfg.remote_server = "127.0.0.1".into();
                        cfg.remote_port = port.to_string();
                        cfg.remote_key = key.auth_key;
                        Ok(cfg)
                    })
                    .unwrap();
                client.store.upsert_prefixes(&[prefix]).unwrap();

                // Pause an existing send, then poll real replay into the first
                // sending-lock wait. Tokio's FIFO lock puts replay ahead of the
                // later direct handler, without a production hook or a sleep.
                let sending = client.sending.lock().await;
                let mut replay = Box::pin(client.sync_once(false));
                std::future::poll_fn(|cx| {
                    assert!(replay.as_mut().poll(cx).is_pending());
                    Poll::Ready(())
                })
                .await;
                let request = Request::builder()
                    .method(method)
                    .uri(path)
                    .header("content-type", "application/json")
                    .body(Body::from(body))
                    .unwrap();
                let mut direct = Box::pin(crate::router(client.clone()).oneshot(request));
                // With one blocking worker, each sentinel is behind every DB
                // operation submitted by the preceding direct-handler poll.
                // Three rounds settle both old and repaired handlers at their
                // send-lock wait, while replay remains first in the lock FIFO.
                for _ in 0..3 {
                    std::future::poll_fn(|cx| {
                        assert!(direct.as_mut().poll(cx).is_pending());
                        Poll::Ready(())
                    })
                    .await;
                    tokio::task::spawn_blocking(|| ()).await.unwrap();
                }
                assert!(client.store.ticket("A", 1).unwrap().is_none());
                assert_eq!(
                    client.store.prefix_by_name("A").unwrap().unwrap().color,
                    "blue"
                );
                drop(sending);
                let (replayed, response) = tokio::time::timeout(Duration::from_secs(3), async {
                    tokio::join!(replay, direct)
                })
                .await
                .unwrap();
                replayed.unwrap();
                let response = response.unwrap();
                assert!(!response.headers().contains_key("X-TAM-Queued"));
                let status = response.status();
                let bytes = axum::body::to_bytes(response.into_body(), 4096)
                    .await
                    .unwrap();
                assert_eq!(
                    status,
                    Code::OK,
                    "direct {method} {path} failed: {}",
                    String::from_utf8_lossy(&bytes)
                );
                assert!(client.store.pending().unwrap().is_empty());
                assert!(client.store.failed().unwrap().is_empty());
                let answer: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
                if method == "POST" {
                    assert_eq!(answer[0]["first_name"], "single owner");
                    assert_eq!(
                        client.store.ticket("A", 1).unwrap().unwrap().first_name,
                        "single owner"
                    );
                    assert_eq!(
                        server_store.ticket("A", 1).unwrap().unwrap().first_name,
                        "single owner"
                    );
                } else {
                    assert_eq!(answer["color"], "blue");
                    assert!(client.store.prefix_by_name("A").unwrap().is_none());
                    assert!(server_store.prefix_by_name("A").unwrap().is_none());
                }
                peer.abort();
            });
    }
    #[test]
    fn replay_and_direct_post_keep_one_owner_of_the_live_intent() {
        replay_cannot_consume_live_direct_intent(
            "POST",
            "/api/tickets",
            r#"[{"prefix":"A","t_id":1,"first_name":"single owner"}]"#,
        );
    }
    #[test]
    fn replay_and_direct_delete_keep_one_owner_of_the_live_intent() {
        replay_cannot_consume_live_direct_intent("DELETE", "/api/prefixes?p=A", "");
    }
}

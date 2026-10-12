use crate::ClientState;
use axum::{
    body::{Body, to_bytes},
    extract::State,
    http::{HeaderMap, Request, StatusCode},
    response::{IntoResponse, Response},
};
use serde::Serialize;
pub const MAX_BODY: usize = 64 << 20;
pub fn json<T: Serialize>(status: StatusCode, value: &T) -> Response {
    match r_tam_core::wire::go_json(value) {
        Ok(mut b) => {
            b.push(b'\n');
            (status, [("content-type", "application/json")], b).into_response()
        }
        Err(e) => internal(e),
    }
}
pub fn error(status: StatusCode, detail: impl AsRef<str>) -> Response {
    json(status, &serde_json::json!({"detail":detail.as_ref()}))
}
pub fn internal(e: impl std::fmt::Display) -> Response {
    log::error!("client: {e}");
    error(
        StatusCode::INTERNAL_SERVER_ERROR,
        "Internal error; see the daemon log",
    )
}
pub fn header<'a>(headers: &'a HeaderMap, key: &str) -> &'a str {
    headers.get(key).and_then(|v| v.to_str().ok()).unwrap_or("")
}
pub fn query(q: &str, key: &str) -> String {
    form_urlencoded::parse(r_tam_core::wire::canonical_query(q).as_bytes())
        .find(|(k, _)| k == key)
        .map(|(_, v)| v.into_owned())
        .unwrap_or_default()
}
pub fn escape(s: &str) -> String {
    form_urlencoded::byte_serialize(s.as_bytes()).collect()
}
pub fn path_escape(s: &str) -> String {
    escape(s).replace('+', "%20")
}
pub fn decode_path(s: &str) -> String {
    form_urlencoded::parse(format!("v={}", s.replace('+', "%2B")).as_bytes())
        .next()
        .map(|(_, v)| v.into_owned())
        .unwrap_or_default()
}
pub fn int(s: &str, name: &str) -> Result<i64, String> {
    let n = s
        .parse::<i64>()
        .map_err(|_| format!("{name} must be an integer"))?;
    if !(0..=r_tam_core::models::MAX_EXACT).contains(&n) {
        return Err(format!("{name} must be between 0 and 9007199254740991"));
    }
    Ok(n)
}
pub fn forward(r: r_tam_core::remote::RemoteResponse) -> Response {
    let status = StatusCode::from_u16(r.status).unwrap_or(StatusCode::BAD_GATEWAY);
    if let Ok(v) = r.json::<serde_json::Value>()
        && v.get("detail").is_some()
    {
        return json(status, &v);
    }
    error(status, status.canonical_reason().unwrap_or("Remote error"))
}
fn route_methods(path: &str) -> &'static [&'static str] {
    match path {
        "/api" | "/api/" | "/api/status" | "/api/servers" | "/api/outbox/failed" => &["GET"],
        "/api/settings" | "/api/backuprestore/local" | "/api/backuprestore/remote" => {
            &["GET", "POST"]
        }
        "/api/shutdown"
        | "/api/pair"
        | "/api/unpair"
        | "/api/outbox/retry"
        | "/api/outbox/discard" => &["POST"],
        "/api/auth" | "/api/prefixes" => &["GET", "POST", "DELETE"],
        "/api/tickets" | "/api/baskets" | "/api/drawing" | "/api/search/tickets" => {
            &["GET", "POST"]
        }
        "/api/reports/counts" => &["GET"],
        _ => {
            let parts: Vec<_> = path.split('/').collect();
            if parts.first() == Some(&"") && parts.get(1) == Some(&"api") {
                if parts.len() == 5
                    && parts[2] == "backuprestore"
                    && parts[3] == "push"
                    && !parts[4].is_empty()
                {
                    return &["POST"];
                }
                if parts.len() == 5
                    && parts[2] == "reports"
                    && matches!(parts[3], "byname" | "bybasket")
                    && !parts[4].is_empty()
                {
                    return &["GET"];
                }
                if (4..=6).contains(&parts.len())
                    && matches!(parts[2], "tickets" | "baskets" | "drawing")
                    && parts[3..].iter().all(|p| !p.is_empty())
                {
                    return &["GET"];
                }
            }
            &[]
        }
    }
}
fn clean_path(path: &str) -> Option<String> {
    let mut parts = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                parts.pop();
            }
            _ => parts.push(part),
        }
    }
    let mut clean = format!("/{}", parts.join("/"));
    if path.ends_with('/') && clean != "/" {
        clean.push('/');
    }
    (clean != path).then_some(clean)
}
pub async fn handle(State(s): State<ClientState>, request: Request<Body>) -> Response {
    let Some(_activity) = s.shutdown.enter() else {
        return error(StatusCode::SERVICE_UNAVAILABLE, "Client is shutting down");
    };
    let (parts, body) = request.into_parts();
    let method = parts.method.as_str();
    let path = parts.uri.path();
    if let Some(mut clean) = clean_path(path) {
        if let Some(query) = parts.uri.query() {
            clean.push('?');
            clean.push_str(query);
        }
        return (StatusCode::MOVED_PERMANENTLY, [("location", clean)]).into_response();
    }
    if !path.starts_with("/api") {
        return crate::assets::serve(method, path);
    }
    let head = method == "HEAD";
    let method = if head { "GET" } else { method };
    let methods = route_methods(path);
    if methods.is_empty() {
        return error(StatusCode::NOT_FOUND, "Not Found");
    }
    if !methods.contains(&method) {
        return error(StatusCode::METHOD_NOT_ALLOWED, "Method Not Allowed");
    }
    if method != "GET" {
        let site = header(&parts.headers, "sec-fetch-site");
        if !matches!(site, "" | "same-origin" | "none") {
            return error(StatusCode::FORBIDDEN, "Cross-site request refused");
        }
        // Go DELETE endpoints have no body; POST must pass the JSON barrier.
        if method == "POST"
            && !header(&parts.headers, "content-type")
                .parse::<mime::Mime>()
                .is_ok_and(|v| v.type_() == mime::APPLICATION && v.subtype() == mime::JSON)
        {
            return error(
                StatusCode::UNSUPPORTED_MEDIA_TYPE,
                "Content-Type must be application/json",
            );
        }
    }
    let bytes = match to_bytes(body, MAX_BODY).await {
        Ok(v) => v,
        Err(_) => return error(StatusCode::PAYLOAD_TOO_LARGE, "Request body too large"),
    };
    if method == "POST"
        && let Err(e) = r_tam_core::wire::decode::<serde_json::Value>(&bytes)
    {
        return error(StatusCode::BAD_REQUEST, e.to_string());
    }
    let mut response = crate::api::dispatch(
        &s,
        method,
        path,
        parts.uri.query().unwrap_or(""),
        &parts.headers,
        &bytes,
    )
    .await;
    if head {
        *response.body_mut() = Body::empty();
    }
    response
}

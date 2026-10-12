use axum::{
    body::Body,
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
};
use serde::Serialize;
pub const MAX_BODY: usize = 64 << 20;
pub fn json<T: Serialize>(status: StatusCode, value: &T) -> Response {
    match r_tam_core::wire::go_json(value) {
        Ok(mut bytes) => {
            bytes.push(b'\n');
            (status, [("content-type", "application/json")], bytes).into_response()
        }
        Err(e) => internal(e),
    }
}
pub fn error(status: StatusCode, detail: impl AsRef<str>) -> Response {
    json(status, &serde_json::json!({"detail":detail.as_ref()}))
}
pub fn internal(error: impl std::fmt::Display) -> Response {
    log::error!("server: {error}");
    error_response()
}
fn error_response() -> Response {
    (
        StatusCode::INTERNAL_SERVER_ERROR,
        [("content-type", "application/json")],
        Body::from("{\"detail\":\"Internal error; see the daemon log\"}\n"),
    )
        .into_response()
}
pub fn header<'a>(headers: &'a HeaderMap, key: &str) -> &'a str {
    headers.get(key).and_then(|v| v.to_str().ok()).unwrap_or("")
}
pub fn json_type(headers: &HeaderMap) -> bool {
    header(headers, "content-type")
        .parse::<mime::Mime>()
        .is_ok_and(|v| v.type_() == mime::APPLICATION && v.subtype() == mime::JSON)
}
pub fn query(query: &str, key: &str) -> String {
    form_urlencoded::parse(r_tam_core::wire::canonical_query(query).as_bytes())
        .find(|(k, _)| k == key)
        .map(|(_, v)| v.into_owned())
        .unwrap_or_default()
}
pub fn int(value: &str, name: &str) -> Result<i64, String> {
    let n = value
        .parse::<i64>()
        .map_err(|_| format!("{name} must be an integer"))?;
    if !(0..=r_tam_core::models::MAX_EXACT).contains(&n) {
        return Err(format!("{name} must be between 0 and 9007199254740991"));
    }
    Ok(n)
}
pub fn decode_path(value: &str) -> String {
    let mut result = Vec::with_capacity(value.len());
    let mut iter = value.as_bytes().iter().copied();
    while let Some(b) = iter.next() {
        if b == b'%' {
            match (iter.next(), iter.next()) {
                (Some(a), Some(b)) => {
                    if let (Some(a), Some(b)) = ((a as char).to_digit(16), (b as char).to_digit(16))
                    {
                        result.push((a * 16 + b) as u8)
                    }
                }
                _ => break,
            }
        } else {
            result.push(b)
        }
    }
    String::from_utf8_lossy(&result).into_owned()
}
pub fn clean_path(path: &str) -> Option<String> {
    let mut pieces = Vec::new();
    for piece in path.split('/') {
        match piece {
            "" | "." => {}
            ".." => {
                pieces.pop();
            }
            _ => pieces.push(piece),
        }
    }
    let mut clean = format!("/{}", pieces.join("/"));
    if path.ends_with('/') && clean != "/" {
        clean.push('/')
    }
    if clean == path { None } else { Some(clean) }
}

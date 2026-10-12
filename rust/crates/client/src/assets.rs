use axum::{
    http::StatusCode,
    response::{IntoResponse, Response},
};
include!(concat!(env!("OUT_DIR"), "/assets.rs"));
fn mime(path: &str) -> &'static str {
    match path.rsplit('.').next().unwrap_or("") {
        "html" => "text/html; charset=utf-8",
        "js" => "text/javascript; charset=utf-8",
        "css" => "text/css; charset=utf-8",
        "json" | "map" => "application/json",
        "svg" => "image/svg+xml",
        "png" => "image/png",
        "ico" => "image/x-icon",
        "woff2" => "font/woff2",
        "txt" => "text/plain; charset=utf-8",
        _ => "application/octet-stream",
    }
}
pub fn serve(method: &str, path: &str) -> Response {
    if !matches!(method, "GET" | "HEAD") {
        return StatusCode::METHOD_NOT_ALLOWED.into_response();
    }
    if path == "/" {
        return (StatusCode::FOUND, [("location", "/web/")]).into_response();
    }
    if path == "/web" {
        return (StatusCode::MOVED_PERMANENTLY, [("location", "/web/")]).into_response();
    }
    let icon = path == "/favicon.ico";
    let Some(rel) = (if icon {
        Some("favicon.ico")
    } else {
        path.strip_prefix("/web/")
    }) else {
        return StatusCode::NOT_FOUND.into_response();
    };
    let decoded = crate::http::decode_path(rel);
    let rel = decoded.as_str();
    if let Some((name, body)) = FILES.iter().find(|(name, _)| *name == rel) {
        return (
            StatusCode::OK,
            [
                ("content-type", mime(name)),
                (
                    "cache-control",
                    if icon {
                        "public, max-age=86400"
                    } else if rel.starts_with("_app/immutable/") {
                        "public, max-age=31536000, immutable"
                    } else {
                        "no-cache"
                    },
                ),
            ],
            if method == "HEAD" { &[][..] } else { *body },
        )
            .into_response();
    }
    if icon || rel.starts_with("_app/") && !rel.ends_with('/') {
        return StatusCode::NOT_FOUND.into_response();
    }
    let body = FILES
        .iter()
        .find(|(name, _)| *name == "index.html")
        .unwrap()
        .1;
    (
        StatusCode::OK,
        [
            ("content-type", "text/html; charset=utf-8"),
            ("cache-control", "no-cache"),
        ],
        if method == "HEAD" { &[][..] } else { body },
    )
        .into_response()
}

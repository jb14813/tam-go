//! Go-compatible R-TAM server API and administration.
mod admin;
mod api;
pub mod auth;
pub mod http;
pub mod presence;
use r_tam_core::{desktop::Shutdown, store::Store};
use std::sync::Arc;
#[derive(Clone)]
pub struct ServerState {
    pub store: Store,
    pub password: Arc<auth::Password>,
    pub limiter: Arc<auth::Limiter>,
    pub presence: Arc<presence::Presence>,
    pub shutdown: Arc<Shutdown>,
    pub name: String,
    pub version: String,
    pub addr: String,
    pub addresses: Vec<String>,
    pub tls: bool,
    pub data_dir: String,
    pub started: i64,
    pub clock: auth::Clock,
    sessions: Arc<auth::Sessions>,
}
impl ServerState {
    pub fn new(store: Store, password: auth::Password) -> Self {
        Self::with_clock(store, password, auth::clock())
    }
    pub fn with_clock(store: Store, password: auth::Password, clock: auth::Clock) -> Self {
        let started = clock();
        Self {
            data_dir: store
                .path()
                .parent()
                .unwrap_or(std::path::Path::new("."))
                .display()
                .to_string(),
            store,
            password: Arc::new(password),
            limiter: Arc::new(auth::Limiter::new(clock.clone())),
            presence: Arc::default(),
            shutdown: Arc::new(Shutdown::new()),
            name: std::env::var("COMPUTERNAME")
                .or_else(|_| std::env::var("HOSTNAME"))
                .unwrap_or_else(|_| "R-TAM Server".into()),
            version: env!("CARGO_PKG_VERSION").into(),
            addr: String::new(),
            addresses: vec![],
            tls: false,
            started,
            sessions: Arc::new(auth::Sessions::new(clock.clone())),
            clock,
        }
    }
}
pub fn router(state: ServerState) -> axum::Router {
    axum::Router::new().fallback(dispatch).with_state(state)
}
async fn dispatch(
    axum::extract::State(state): axum::extract::State<ServerState>,
    request: axum::extract::Request,
) -> axum::response::Response {
    if let Some(path) = http::clean_path(request.uri().path()) {
        let location = match request.uri().query() {
            Some(query) => format!("{path}?{query}"),
            None => path,
        };
        let mut response = http::error(
            axum::http::StatusCode::MOVED_PERMANENTLY,
            "Moved Permanently",
        );
        response
            .headers_mut()
            .insert("location", location.parse().unwrap());
        return response;
    }
    let Some(activity) = state.shutdown.enter() else {
        return http::error(
            axum::http::StatusCode::SERVICE_UNAVAILABLE,
            "server shutting down",
        );
    };
    let is_admin =
        request.uri().path().starts_with("/admin") || request.uri().path() == "/favicon.ico";
    let operation = async move {
        if is_admin {
            admin::handle(state, request, activity).await
        } else {
            api::handle(state, request, activity).await
        }
    };
    tokio::time::timeout(std::time::Duration::from_secs(120), operation)
        .await
        .unwrap_or_else(|_| {
            http::error(axum::http::StatusCode::REQUEST_TIMEOUT, "request timed out")
        })
}

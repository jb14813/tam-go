//! Bounded HTTP transport retaining status, headers and Go-compatible JSON bodies.
use crate::{Error, Result, models::Order};
use serde::de::DeserializeOwned;
use std::time::Duration;
fn internal(e: impl std::fmt::Display) -> Error {
    Error::Internal(e.to_string())
}
#[derive(Clone)]
pub struct Remote {
    base: String,
    key: String,
    http: reqwest::Client,
    timeout: Duration,
    response_limit: usize,
}
#[derive(Debug)]
pub struct RemoteResponse {
    pub status: u16,
    pub headers: reqwest::header::HeaderMap,
    pub body: Vec<u8>,
}
impl RemoteResponse {
    pub fn ok(&self) -> bool {
        (200..300).contains(&self.status)
    }
    pub fn retryable(&self) -> bool {
        self.status >= 500
    }
    pub fn json<T: DeserializeOwned>(&self) -> Result<T> {
        crate::wire::decode(&self.body)
    }
}
#[derive(Debug, Clone, serde::Deserialize, serde::Serialize, Default)]
#[serde(default)]
pub struct ServerInfo {
    pub whoami: String,
    pub name: String,
    pub event: String,
    pub version: String,
}
impl Remote {
    pub fn new(base: &str, key: &str, insecure: bool) -> Result<Self> {
        Self::build(base, key, insecure, None)
    }
    pub fn pinned(base: &str, key: &str, pin: &str) -> Result<Self> {
        Self::build(base, key, true, Some(pin))
    }
    pub fn from_settings(settings: &crate::config::Settings) -> Result<Self> {
        if settings.remote_tls && !settings.remote_fingerprint.is_empty() {
            Self::pinned(
                &settings.remote_url(),
                &settings.remote_key,
                &settings.remote_fingerprint,
            )
        } else {
            Self::new(
                &settings.remote_url(),
                &settings.remote_key,
                settings.remote_tls,
            )
        }
    }
    fn build(base: &str, key: &str, insecure: bool, pin: Option<&str>) -> Result<Self> {
        let url = reqwest::Url::parse(base).map_err(internal)?;
        if !matches!(url.scheme(), "http" | "https")
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.query().is_some()
            || url.fragment().is_some()
        {
            return Err(Error::Validation(
                "remote URL must be an HTTP(S) origin".into(),
            ));
        }
        if pin.is_some() && url.scheme() != "https" {
            return Err(Error::Validation("a certificate pin requires HTTPS".into()));
        }
        let mut builder = reqwest::Client::builder()
            .connect_timeout(Duration::from_secs(5))
            .read_timeout(Duration::from_secs(10))
            .timeout(Duration::from_secs(30))
            .tcp_keepalive(Duration::from_secs(30))
            .retry(reqwest::retry::never())
            .redirect(reqwest::redirect::Policy::custom(|attempt| {
                if attempt.previous().len() >= 10 {
                    attempt.error("too many redirects")
                } else if attempt
                    .previous()
                    .first()
                    .is_some_and(|u| u.origin() != attempt.url().origin())
                {
                    attempt.stop()
                } else {
                    attempt.follow()
                }
            }));
        if insecure {
            builder = builder.tls_backend_preconfigured(crate::tls::client_config(pin)?);
        } else {
            let _ = rustls::crypto::ring::default_provider().install_default();
        }
        Ok(Self {
            base: base.trim_end_matches('/').into(),
            key: key.into(),
            http: builder.build().map_err(internal)?,
            timeout: Duration::from_secs(30),
            response_limit: 64 * 1024 * 1024,
        })
    }
    pub fn with_key(&self, key: &str) -> Self {
        let mut out = self.clone();
        out.key = key.into();
        out
    }
    pub fn with_timeout(&self, timeout: Duration) -> Self {
        let mut out = self.clone();
        out.timeout = timeout;
        out
    }
    pub fn with_response_limit(&self, limit: usize) -> Self {
        let mut out = self.clone();
        out.response_limit = limit;
        out
    }
    pub async fn get(&self, path: &str) -> Result<RemoteResponse> {
        self.request("GET", path, &[], None).await
    }
    pub async fn post<T: serde::Serialize + ?Sized>(
        &self,
        path: &str,
        body: &T,
    ) -> Result<RemoteResponse> {
        let bytes = crate::wire::go_json(body)?;
        self.request("POST", path, &[], Some(&bytes)).await
    }
    pub async fn delete(&self, path: &str) -> Result<RemoteResponse> {
        self.request("DELETE", path, &[], None).await
    }
    pub async fn request(
        &self,
        method: &str,
        path: &str,
        headers: &[(&str, &str)],
        body: Option<&[u8]>,
    ) -> Result<RemoteResponse> {
        if !path.starts_with('/') || path.starts_with("//") {
            return Err(Error::Validation(
                "remote request path must be absolute within the server".into(),
            ));
        }
        let method = reqwest::Method::from_bytes(method.as_bytes()).map_err(internal)?;
        let mut key = reqwest::header::HeaderValue::from_str(&self.key).map_err(internal)?;
        key.set_sensitive(true);
        let mut request = self
            .http
            .request(method, format!("{}{path}", self.base))
            .timeout(self.timeout)
            .header("TAM-KEY", key)
            .header(
                "X-TAM-Client",
                concat!("tam-client/", env!("CARGO_PKG_VERSION")),
            );
        if let Some(body) = body {
            request = request
                .header("Content-Type", "application/json")
                .body(body.to_vec());
        }
        for (k, v) in headers {
            let mut value = reqwest::header::HeaderValue::from_str(v).map_err(internal)?;
            if k.eq_ignore_ascii_case("TAM-PW") || k.eq_ignore_ascii_case("TAM-KEY") {
                value.set_sensitive(true);
            }
            request = request.header(*k, value);
        }
        let task = async {
            let mut response = request
                .send()
                .await
                .map_err(|e| internal(format!("remote request: {e:?}")))?;
            let status = response.status().as_u16();
            let headers = response.headers().clone();
            let mut body = Vec::new();
            while let Some(bytes) = response.chunk().await.map_err(internal)? {
                if body.len().saturating_add(bytes.len()) > self.response_limit {
                    return Err(internal(format!(
                        "remote response exceeds {} bytes",
                        self.response_limit
                    )));
                }
                body.extend_from_slice(&bytes);
            }
            if (200..300).contains(&status)
                && (headers
                    .get("content-type")
                    .is_some_and(|v| v.as_bytes().starts_with(b"text/html"))
                    || body.iter().find(|b| !b.is_ascii_whitespace()) == Some(&b'<'))
            {
                return Err(internal(
                    "remote server returned HTML instead of a TAM response",
                ));
            }
            Ok(RemoteResponse {
                status,
                headers,
                body,
            })
        };
        tokio::time::timeout(self.timeout, task)
            .await
            .map_err(internal)?
    }
    pub async fn ordered_request(
        &self,
        method: &str,
        path: &str,
        order: &Order,
        pending: i64,
        body: Option<&[u8]>,
    ) -> Result<RemoteResponse> {
        let save = order.save.to_string();
        let pending = pending.to_string();
        self.request(
            method,
            path,
            &[
                ("X-TAM-Client-Name", &order.client),
                ("X-TAM-Save", &save),
                ("X-TAM-Pending", &pending),
            ],
            body,
        )
        .await
    }
    pub async fn probe(&self) -> Result<ServerInfo> {
        let response = self.get("/api").await?;
        let info: ServerInfo = response.json()?;
        if !response.ok() || info.whoami != "TAM Server" {
            return Err(internal("remote did not answer as a TAM server"));
        }
        Ok(info)
    }
    pub async fn create_key(&self, password: &str, description: &str) -> Result<RemoteResponse> {
        let bytes = crate::wire::go_json(&serde_json::json!({"description":description}))?;
        self.request("POST", "/api/auth", &[("TAM-PW", password)], Some(&bytes))
            .await
    }
}

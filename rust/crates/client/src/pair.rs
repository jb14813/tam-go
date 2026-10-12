use crate::{ClientState, http};
use r_tam_core::{Error, Result};
impl ClientState {
    pub(crate) async fn change_event_locked(&self, event: &str, label: &str) -> Result<String> {
        let (snapshot, message) = self.preserve_snapshot(label).await?;
        let new = event.to_owned();
        self.db(move |st| {
            st.transition_copy(
                &snapshot,
                &new,
                "Saved for an earlier event; retry to send it to this event",
            )
        })
        .await?;
        Ok(message)
    }
    async fn preserve_snapshot(
        &self,
        label: &str,
    ) -> Result<(r_tam_core::store::Snapshot, String)> {
        let snapshot = self.db(|st| st.snapshot()).await?;
        let dir = self.data_dir.clone();
        let backup = snapshot.backup.clone();
        let label = label.to_owned();
        let activity = self
            .shutdown
            .enter()
            .ok_or_else(|| Error::Internal("Client is shutting down".into()))?;
        let path = tokio::task::spawn_blocking(move || {
            let _activity = activity;
            r_tam_core::files::preserve_event(&dir, &label, &backup)
        })
        .await
        .map_err(|e| Error::Internal(e.to_string()))??;
        Ok((
            snapshot,
            format!(
                " This client's data was saved to {} in its data folder first.",
                path.file_name().unwrap_or_default().to_string_lossy()
            ),
        ))
    }
}
pub async fn pair(s: &ClientState, body: &[u8]) -> axum::response::Response {
    use axum::http::StatusCode as Code;
    #[derive(serde::Deserialize, Default)]
    #[serde(default)]
    struct Request {
        host: String,
        port: String,
        tls: bool,
        password: String,
    }
    let req: Request = match r_tam_core::wire::decode(body) {
        Ok(v) => v,
        Err(e) => return http::error(Code::BAD_REQUEST, e.to_string()),
    };
    let mut cfg = r_tam_core::config::Settings {
        remote_server: req.host.trim().into(),
        remote_port: if req.port.trim().is_empty() {
            "8000".into()
        } else {
            req.port.trim().into()
        },
        remote_tls: req.tls,
        ..Default::default()
    };
    if cfg.remote_server.is_empty() {
        return http::error(Code::BAD_REQUEST, "host is required");
    }
    if req.password.is_empty() {
        return http::error(Code::BAD_REQUEST, "password is required");
    }
    if let Err(e) = cfg.validate() {
        return http::error(Code::BAD_REQUEST, e.to_string());
    }
    if cfg.remote_tls {
        match r_tam_core::tls::fingerprint(&r_tam_core::config::host_port(
            &cfg.remote_server,
            &cfg.remote_port,
        ))
        .await
        {
            Ok(fp) => cfg.remote_fingerprint = fp,
            Err(e) => {
                s.failure(e);
                return http::error(Code::BAD_GATEWAY, "Remote server unreachable");
            }
        }
    }
    let rc = match r_tam_core::remote::Remote::from_settings(&cfg) {
        Ok(r) => r.with_timeout(std::time::Duration::from_secs(10)),
        Err(e) => return http::internal(e),
    };
    let info = match rc.probe().await {
        Ok(v) => v,
        Err(e) => {
            s.failure(e);
            return http::error(Code::BAD_GATEWAY, "Remote did not answer as a TAM server");
        }
    };
    let key = match rc.create_key(&req.password, &s.host).await {
        Ok(r) if matches!(r.status, 401 | 403) => {
            return http::error(Code::UNAUTHORIZED, "The server rejected the password");
        }
        Ok(r) if r.status == 503 => {
            return http::error(
                Code::SERVICE_UNAVAILABLE,
                "The server has no password yet; open its admin page first",
            );
        }
        Ok(r) if !r.ok() => return http::forward(r),
        Ok(r) => r
            .json::<serde_json::Value>()
            .ok()
            .and_then(|v| v["auth_key"].as_str().map(str::to_owned))
            .unwrap_or_default(),
        Err(e) => {
            s.failure(e);
            return http::error(Code::BAD_GATEWAY, "Remote server unreachable");
        }
    };
    if key.is_empty() {
        return http::error(Code::BAD_GATEWAY, "The server did not return an access key");
    }
    cfg.remote_key = key.clone();
    cfg.remote_name = if info.name.is_empty() {
        cfg.remote_server.clone()
    } else {
        info.name
    };
    let _numbering = s.numbering.lock().await;
    let _sending = s.sending.lock().await;
    let prev = s.settings.get();
    let mine = match s.db(|st| st.mirror_event()).await {
        Ok(v) => v,
        Err(e) => return http::internal(e),
    };
    let same = !prev.remote_url().is_empty()
        && ((prev.remote_server == cfg.remote_server && prev.remote_port == cfg.remote_port)
            || (!prev.remote_name.is_empty() && prev.remote_name == cfg.remote_name))
        && (info.event.is_empty() || mine.is_empty() || info.event == mine);
    let preserved = if !same {
        match s.preserve_snapshot("before-pairing-").await {
            Ok(v) => Some(v),
            Err(e) => {
                let _ = rc
                    .request(
                        "DELETE",
                        &format!("/api/auth?key_to_del={}", http::escape(&key)),
                        &[("TAM-PW", &req.password)],
                        None,
                    )
                    .await;
                return http::error(
                    Code::INTERNAL_SERVER_ERROR,
                    format!(
                        "This client's data could not be saved to a file in its data folder ({e}), so it did not pair. Make room or fix the folder, then pair again."
                    ),
                );
            }
        }
    } else {
        None
    };
    let server = r_tam_core::config::host_port(&cfg.remote_server, &cfg.remote_port);
    let name = cfg.remote_name.clone();
    if let Err(e) = s.settings.update(|cur| {
        Ok(r_tam_core::config::Settings {
            remote_server: cfg.remote_server,
            remote_port: cfg.remote_port,
            remote_tls: cfg.remote_tls,
            remote_key: cfg.remote_key,
            remote_name: cfg.remote_name,
            remote_fingerprint: cfg.remote_fingerprint,
            ..cur
        })
    }) {
        return http::internal(e);
    }
    let kept = if let Some((snapshot, message)) = preserved {
        let new = info.event;
        if let Err(e) = s
            .db(move |st| {
                st.transition_copy(
                    &snapshot,
                    &new,
                    "Saved for an earlier event; retry to send it to this event",
                )
            })
            .await
        {
            let _ = s.settings.update(|_| Ok(prev));
            s.reset();
            return http::internal(e);
        }
        message
    } else {
        String::new()
    };
    s.reset();
    s.kick.notify_one();
    http::json(
        Code::OK,
        &serde_json::json!({"message":format!("Paired with {name}.{kept}"),"server":server}),
    )
}
pub async fn unpair(s: &ClientState, body: &[u8]) -> axum::response::Response {
    use axum::http::StatusCode as Code;
    let req: serde_json::Value = match r_tam_core::wire::decode(body) {
        Ok(v) => v,
        Err(e) => return http::error(Code::BAD_REQUEST, e.to_string()),
    };
    if !req.is_null() && !req.is_object() {
        return http::error(Code::BAD_REQUEST, "unpair request must be an object");
    }
    if req
        .get("password")
        .is_some_and(|v| !v.is_null() && !v.is_string())
    {
        return http::error(Code::BAD_REQUEST, "password must be a string");
    }
    let _numbering = s.numbering.lock().await;
    let _sending = s.sending.lock().await;
    let cfg = s.settings.get();
    if cfg.remote_url().is_empty() {
        return http::error(Code::BAD_REQUEST, "This client is not paired with a server");
    }
    let password = req["password"].as_str().unwrap_or("");
    if !password.is_empty()
        && !cfg.remote_key.is_empty()
        && let Ok(Some(rc)) = s.remote()
    {
        let _ = rc
            .with_timeout(std::time::Duration::from_secs(5))
            .request(
                "DELETE",
                &format!("/api/auth?key_to_del={}", http::escape(&cfg.remote_key)),
                &[("TAM-PW", password)],
                None,
            )
            .await;
    }
    if let Err(e) = s.settings.update(|mut cur| {
        cur.remote_server.clear();
        cur.remote_key.clear();
        cur.remote_name.clear();
        cur.remote_fingerprint.clear();
        Ok(cur)
    }) {
        return http::internal(e);
    }
    if let Err(e) = s
        .db(|st| st.fail_all_outbox("not sent before unpairing"))
        .await
    {
        if let Err(restore) = s.settings.update(|_| Ok(cfg)) {
            log::error!("unpair could not restore settings after queue failure: {restore}");
        }
        s.reset();
        return http::internal(e);
    }
    s.reset();
    http::json(
        Code::OK,
        &serde_json::json!({"message":"Standalone again."}),
    )
}

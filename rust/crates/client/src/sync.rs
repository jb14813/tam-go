use crate::{ClientState, http};
use r_tam_core::{Error, Result, models::BackupFile, remote::RemoteResponse};
use std::time::{Duration, Instant};
#[derive(Clone)]
pub struct Timings {
    pub heartbeat: Duration,
    pub ping_timeout: Duration,
    pub offline_after: Duration,
    pub backoff: Vec<Duration>,
}
impl Default for Timings {
    fn default() -> Self {
        Self {
            heartbeat: Duration::from_secs(5),
            ping_timeout: Duration::from_secs(2),
            offline_after: Duration::from_secs(30),
            backoff: [1, 2, 5, 10, 30]
                .into_iter()
                .map(Duration::from_secs)
                .collect(),
        }
    }
}
#[derive(Default)]
pub(crate) struct Connection {
    pub state: String,
    pub since: Option<Instant>,
    pub last_ok: String,
    pub event: String,
    pub pull_needed: bool,
    pub retry_at: Option<Instant>,
    pub retries: usize,
    pub settings: Option<r_tam_core::config::Settings>,
}
impl ClientState {
    pub fn online(&self) -> bool {
        let c = self.connection.lock().unwrap_or_else(|e| e.into_inner());
        c.state.is_empty() || c.state == "connected"
    }
    pub fn reset(&self) {
        *self.connection.lock().unwrap_or_else(|e| e.into_inner()) = Connection::default();
        self.kick.notify_one();
    }
    pub fn success(&self) {
        let mut c = self.connection.lock().unwrap_or_else(|e| e.into_inner());
        if c.state != "connected" {
            c.pull_needed = true
        }
        c.state = "connected".into();
        c.since = None;
        c.last_ok = time::OffsetDateTime::now_utc()
            .replace_nanosecond(0)
            .expect("zero nanoseconds are valid")
            .format(&time::format_description::well_known::Rfc3339)
            .unwrap_or_default();
    }
    pub fn failure(&self, e: impl std::fmt::Display) {
        log::warn!("remote server: {e}");
        let mut c = self.connection.lock().unwrap_or_else(|e| e.into_inner());
        let since = *c.since.get_or_insert_with(Instant::now);
        c.state = if e.to_string().contains("certificate") || e.to_string().contains("fingerprint")
        {
            "certificate"
        } else if since.elapsed() >= self.timings.offline_after {
            "offline"
        } else {
            "reconnecting"
        }
        .into();
    }
    pub fn observe(&self, r: &Result<RemoteResponse>) -> bool {
        match r {
            Err(e) => {
                self.failure(e);
                false
            }
            Ok(r) if matches!(r.status, 401 | 403) => {
                self.connection
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .state = "unauthenticated".into();
                false
            }
            Ok(r) if r.retryable() => {
                self.failure(format!("server answered {}", r.status));
                false
            }
            Ok(_) => {
                self.success();
                true
            }
        }
    }
    pub async fn status(&self) -> Result<serde_json::Value> {
        let cfg = self.settings.get();
        let problem = self.settings.problem();
        if cfg.remote_url().is_empty() {
            let mut v = serde_json::json!({"mode":"standalone"});
            if !problem.is_empty() {
                v["settings_error"] = problem.into()
            }
            return Ok(v);
        }
        let (pending, failed) = self.waiting_counts().await?;
        let c = self.connection.lock().unwrap_or_else(|e| e.into_inner());
        let state = if c.state.is_empty() {
            "reconnecting"
        } else {
            &c.state
        };
        let name = if cfg.remote_name.is_empty() {
            &cfg.remote_server
        } else {
            &cfg.remote_name
        };
        let mut v = serde_json::json!({"mode":"remote","state":state,"server":r_tam_core::config::host_port(&cfg.remote_server,&cfg.remote_port),"server_name":name,"pending":pending,"failed":failed,"last_ok":c.last_ok});
        if !problem.is_empty() {
            v["settings_error"] = problem.into()
        }
        Ok(v)
    }
    pub fn start_sync(&self) -> tokio::task::JoinHandle<()> {
        let s = self.clone();
        tokio::spawn(async move { s.run_sync().await })
    }
    async fn run_sync(&self) {
        let mut next_ping = Instant::now();
        loop {
            if self.shutdown.is_requested() {
                break;
            }
            let Some(_activity) = self.shutdown.enter() else {
                break;
            };
            tokio::select! {
                biased;
                _ = self.shutdown.requested() => break,
                result = self.sync_once(Instant::now() >= next_ping) => {
                    if let Err(e) = result { self.failure(e) }
                }
            }
            if Instant::now() >= next_ping {
                next_ping = Instant::now() + self.timings.heartbeat
            }
            drop(_activity);
            tokio::select! {_=self.shutdown.requested()=>break,_=self.kick.notified()=>{next_ping=Instant::now()},_=tokio::time::sleep(Duration::from_millis(200))=>{}}
        }
    }
    pub async fn sync_once(&self, ping: bool) -> Result<()> {
        let cfg = self.settings.get();
        if cfg.remote_url().is_empty() {
            return Ok(());
        }
        {
            let mut c = self.connection.lock().unwrap_or_else(|e| e.into_inner());
            if c.settings.as_ref().is_some_and(|old| {
                old.remote_url() != cfg.remote_url()
                    || old.remote_key != cfg.remote_key
                    || old.remote_fingerprint != cfg.remote_fingerprint
            }) {
                *c = Connection::default()
            }
            c.settings = Some(cfg.clone());
        }
        let rc = self
            .remote()?
            .ok_or_else(|| Error::Internal("remote unavailable".into()))?;
        if ping {
            let pending = self.waiting_counts().await?.0.to_string();
            let r = rc
                .with_timeout(self.timings.ping_timeout)
                .request("GET", "/api", &[("X-TAM-Pending", &pending)], None)
                .await;
            if !self.observe(&r) {
                return Ok(());
            }
            let r = r?;
            let info: r_tam_core::remote::ServerInfo = r.json()?;
            if !r.ok() || info.whoami != "TAM Server" {
                return Err(Error::Internal(
                    "Remote did not answer as a TAM server".into(),
                ));
            }
            if !cfg.remote_key.is_empty()
                && !r.json::<serde_json::Value>()?["authenticated"]
                    .as_bool()
                    .unwrap_or(false)
            {
                self.connection
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .state = "unauthenticated".into();
                return Ok(());
            }
            let _numbering = self.numbering.lock().await;
            let _sending = self.sending.lock().await;
            if self.settings.get() != cfg {
                return Ok(());
            }
            let mine = self.db(|st| st.mirror_event()).await?;
            if !info.event.is_empty() && !mine.is_empty() && info.event != mine {
                self.change_event_locked(&info.event, "before-event-")
                    .await?;
                self.connection
                    .lock()
                    .unwrap_or_else(|e| e.into_inner())
                    .pull_needed = true;
            }
            self.connection
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .event = info.event;
        }
        if !self.online() {
            return Ok(());
        }
        let retry = self
            .connection
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .retry_at;
        if retry.is_some_and(|at| Instant::now() < at) {
            return Ok(());
        }
        while !self.shutdown.is_requested() {
            let _sending = self.sending.lock().await;
            if self.settings.get() != cfg {
                return Ok(());
            }
            let item = self.db(|st| st.next_outbox()).await?;
            let Some(item) = item else { break };
            let pending = self.db(|st| Ok(st.outbox_counts()?.0)).await?;
            let r = rc
                .with_timeout(Duration::from_secs(5))
                .ordered_request(
                    &item.method,
                    &item.path,
                    &item.order,
                    pending,
                    (item.method == "POST").then_some(item.body.as_slice()),
                )
                .await;
            if let Ok(res) = &r
                && let Some(last) = last_save(res)
            {
                let id = item.id;
                self.db(move |st| {
                    st.skip_saves_to(last)?;
                    st.fail_outbox(
                        id,
                        "This save is older than the server's last save; retry to send it again",
                    )
                })
                .await?;
                continue;
            }
            if !self.observe(&r) {
                let detail = r
                    .as_ref()
                    .err()
                    .map(ToString::to_string)
                    .unwrap_or_else(|| format!("server answered {}", r.as_ref().unwrap().status));
                self.retry_later(item.id, &detail).await?;
                return Ok(());
            }
            let res = r?;
            if !res.ok() && !(item.method == "DELETE" && res.status == 404) {
                let id = item.id;
                let detail = detail(&res);
                self.db(move |st| st.fail_outbox(id, &detail)).await?;
                continue;
            }
            let id = item.id;
            let reconciled = if item.method == "DELETE" {
                let prefix = http::query(item.path.split_once('?').map_or("", |(_, q)| q), "p");
                self.db(move |st| {
                    st.reconcile_delete(
                        id,
                        &prefix,
                        (res.status != 404).then_some(res.body.as_slice()),
                    )
                })
                .await
                .map(|_| ())
            } else {
                self.db(move |st| st.reconcile(id, &res.body))
                    .await
                    .map(|_| ())
            };
            if let Err(e) = reconciled {
                self.failure(&e);
                self.retry_later(id, &e.to_string()).await?;
                return Ok(());
            }
            {
                let mut c = self.connection.lock().unwrap_or_else(|e| e.into_inner());
                c.retries = 0;
                c.retry_at = None;
                c.pull_needed = true;
            }
        }
        let needed = self
            .connection
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .pull_needed;
        if needed && !self.shutdown.is_requested() {
            let generation = self.db(|st| st.generation()).await?;
            let r = rc.get("/api/backuprestore").await;
            if !self.observe(&r) {
                return Ok(());
            }
            let r = r?;
            if !r.ok() {
                return Err(Error::Internal(detail(&r)));
            }
            let mut backup: BackupFile = r.json()?;
            backup.validate()?;
            let _numbering = self.numbering.lock().await;
            if self.settings.get() != cfg || self.db(|st| st.outbox_waiting()).await? {
                return Ok(());
            }
            let mine = self.db(|st| st.mirror_event()).await?;
            if !mine.is_empty() && !backup.event.is_empty() && mine != backup.event {
                self.change_event_locked(&backup.event, "before-event-")
                    .await?;
                return Ok(());
            }
            self.db(move |st| st.mirror_apply(generation, &backup))
                .await?;
            self.connection
                .lock()
                .unwrap_or_else(|e| e.into_inner())
                .pull_needed = false;
        }
        Ok(())
    }
    async fn retry_later(&self, id: i64, reason: &str) -> Result<()> {
        let text = reason.to_owned();
        self.db(move |st| st.note_outbox_attempt(id, &text)).await?;
        let mut c = self.connection.lock().unwrap_or_else(|e| e.into_inner());
        let backoff = self
            .timings
            .backoff
            .get(c.retries)
            .or_else(|| self.timings.backoff.last())
            .copied()
            .unwrap_or(Duration::from_secs(30));
        c.retry_at = Some(Instant::now() + backoff);
        c.retries = c.retries.saturating_add(1);
        Ok(())
    }
}
pub fn last_save(r: &RemoteResponse) -> Option<i64> {
    if r.status != 409 {
        return None;
    }
    r.json::<serde_json::Value>()
        .ok()?
        .get("last_save")?
        .as_i64()
        .filter(|n| *n > 0 && *n <= r_tam_core::models::MAX_EXACT)
}
pub fn detail(r: &RemoteResponse) -> String {
    r.json::<serde_json::Value>()
        .ok()
        .and_then(|v| v["detail"].as_str().map(str::to_owned))
        .unwrap_or_else(|| format!("server answered {}", r.status))
}

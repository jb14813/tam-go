pub mod api;
pub mod assets;
pub mod http;
pub mod pair;
pub mod sync;
use r_tam_core::{
    config::{Settings, SettingsFile},
    desktop::Shutdown,
    discovery::SweepCache,
    remote::Remote,
    store::Store,
};
use std::{
    path::{Path, PathBuf},
    sync::{
        Arc, Mutex, Weak,
        atomic::{AtomicBool, AtomicI64, Ordering},
    },
};
pub(crate) struct DirectIntent {
    id: AtomicI64,
    active: AtomicBool,
}
impl DirectIntent {
    pub(crate) fn publish(&self, id: i64) {
        self.id.store(id, Ordering::Release);
    }
    fn active_id(&self) -> Option<i64> {
        let id = self.id.load(Ordering::Acquire);
        (id != 0 && self.active.load(Ordering::Acquire)).then_some(id)
    }
}
pub(crate) struct DirectSend {
    pub(crate) intent: Arc<DirectIntent>,
    kick: Arc<tokio::sync::Notify>,
}
impl Drop for DirectSend {
    fn drop(&mut self) {
        self.intent.active.store(false, Ordering::Release);
        self.kick.notify_one();
    }
}
#[derive(Clone)]
pub struct ClientState {
    pub store: Store,
    pub settings: Arc<SettingsFile>,
    pub shutdown: Arc<Shutdown>,
    pub host: String,
    pub data_dir: PathBuf,
    pub shutdown_enabled: bool,
    pub timings: sync::Timings,
    pub(crate) connection: Arc<Mutex<sync::Connection>>,
    pub(crate) numbering: Arc<tokio::sync::Mutex<()>>,
    pub(crate) sending: Arc<tokio::sync::Mutex<()>>,
    pub(crate) kick: Arc<tokio::sync::Notify>,
    pub(crate) cache: Arc<SweepCache>,
    remote_cache: Arc<Mutex<Option<(Settings, Remote)>>>,
    live_direct: Arc<Mutex<Weak<DirectIntent>>>,
}
impl ClientState {
    pub fn new(store: Store, path: &Path) -> Self {
        Self {
            store,
            settings: Arc::new(SettingsFile::open(path)),
            shutdown: Arc::new(Shutdown::new()),
            host: r_tam_core::discovery::machine_hostname(),
            data_dir: path.parent().unwrap_or(Path::new(".")).into(),
            shutdown_enabled: false,
            timings: sync::Timings::default(),
            connection: Arc::new(Mutex::new(sync::Connection::default())),
            numbering: Arc::new(tokio::sync::Mutex::new(())),
            sending: Arc::new(tokio::sync::Mutex::new(())),
            kick: Arc::new(tokio::sync::Notify::new()),
            cache: Arc::new(SweepCache::new()),
            remote_cache: Arc::new(Mutex::new(None)),
            live_direct: Arc::default(),
        }
    }
    pub fn remote(&self) -> r_tam_core::Result<Option<Remote>> {
        let settings = self.settings.get();
        if settings.remote_url().is_empty() {
            return Ok(None);
        }
        let mut cache = self.remote_cache.lock().unwrap_or_else(|e| e.into_inner());
        if cache.as_ref().is_none_or(|(old, _)| {
            old.remote_url() != settings.remote_url()
                || old.remote_tls != settings.remote_tls
                || old.remote_fingerprint != settings.remote_fingerprint
        }) {
            *cache = Some((settings.clone(), Remote::from_settings(&settings)?));
        }
        Ok(cache
            .as_ref()
            .map(|(_, r)| r.with_key(&settings.remote_key)))
    }
    pub async fn db<T: Send + 'static>(
        &self,
        operation: impl FnOnce(Store) -> r_tam_core::Result<T> + Send + 'static,
    ) -> r_tam_core::Result<T> {
        let activity = self
            .shutdown
            .enter()
            .ok_or_else(|| r_tam_core::Error::Internal("client is shutting down".into()))?;
        let store = self.store.clone();
        tokio::task::spawn_blocking(move || {
            let _activity = activity;
            operation(store)
        })
        .await
        .map_err(|e| r_tam_core::Error::Internal(e.to_string()))?
    }
    pub(crate) async fn db_guarded<T: Send + 'static>(
        &self,
        ownership: impl Send + 'static,
        operation: impl FnOnce(Store) -> r_tam_core::Result<T> + Send + 'static,
    ) -> r_tam_core::Result<T> {
        self.db(move |store| {
            // A canceled caller cannot release serialization before this
            // blocking write finishes publishing its durable state.
            let _ownership = ownership;
            operation(store)
        })
        .await
    }
    pub(crate) fn begin_direct(&self) -> DirectSend {
        let intent = Arc::new(DirectIntent {
            id: AtomicI64::new(0),
            active: AtomicBool::new(true),
        });
        *self.live_direct.lock().unwrap_or_else(|e| e.into_inner()) = Arc::downgrade(&intent);
        DirectSend {
            intent,
            kick: self.kick.clone(),
        }
    }
    pub(crate) async fn waiting_counts(&self) -> r_tam_core::Result<(i64, i64)> {
        let live = self.live_direct.clone();
        self.db(move |store| {
            let (mut pending, failed) = store.outbox_counts()?;
            let active = live.lock().unwrap_or_else(|e| e.into_inner()).upgrade();
            if let Some(id) = active.as_ref().and_then(|intent| intent.active_id())
                && store.pending()?.iter().any(|item| item.id == id)
            {
                // Only this exact live intent is excluded. Durable orphans and
                // accepted offline work keep their ordinary waiting count.
                pending = pending.saturating_sub(1);
            }
            Ok((pending, failed))
        })
        .await
    }
    pub async fn cache_shutdown(&self) {
        self.cache.shutdown().await;
    }
    pub async fn cleanup(&self) {
        self.shutdown.request();
        self.kick.notify_waiters();
        self.cache_shutdown().await;
        self.shutdown.drain().await;
    }
}
pub fn router(state: ClientState) -> axum::Router {
    axum::Router::new().fallback(http::handle).with_state(state)
}

use r_tam_client::{ClientState, router};
use r_tam_core::{
    cli::{Options, Role, listen_address},
    desktop, schema,
    store::Store,
};
use std::{
    io::Write,
    sync::{Mutex, OnceLock},
    time::Duration,
};
struct Logger;
static LOGGER: Logger = Logger;
static LOG_FILE: OnceLock<Mutex<std::fs::File>> = OnceLock::new();
impl log::Log for Logger {
    fn enabled(&self, m: &log::Metadata<'_>) -> bool {
        m.level() <= log::Level::Info
    }
    fn log(&self, r: &log::Record<'_>) {
        if !self.enabled(r.metadata()) {
            return;
        }
        let line = format!(
            "{} {} {}\n",
            time::OffsetDateTime::now_utc(),
            r.level(),
            r.args()
        );
        eprint!("{line}");
        if let Some(f) = LOG_FILE.get()
            && let Ok(mut f) = f.lock()
        {
            let _ = f.write_all(line.as_bytes());
            let _ = f.flush();
        }
    }
    fn flush(&self) {
        if let Some(f) = LOG_FILE.get()
            && let Ok(mut f) = f.lock()
        {
            let _ = f.flush();
        }
    }
}
#[tokio::main]
async fn main() {
    if let Err(e) = run().await {
        eprintln!("R-TAM Client: {e}");
        log::error!("R-TAM Client: {e}");
        std::process::exit(1)
    }
}
async fn run() -> Result<(), Box<dyn std::error::Error>> {
    let options = Options::parse(Role::Client, std::env::args().skip(1))?;
    if options.help {
        print!("{}", Role::Client.usage());
        return Ok(());
    }
    let dir = desktop::data_dir()?;
    let _lock = desktop::InstanceGuard::acquire(&dir)?;
    if let Ok(f) = desktop::open_log(&dir, "tam-client.log") {
        let _ = LOG_FILE.set(Mutex::new(f));
    }
    log::set_logger(&LOGGER)?;
    log::set_max_level(log::LevelFilter::Info);
    desktop::set_console_title("R-TAM - client");
    let mut state = ClientState::new(
        Store::open(&dir.join("tam-local.db"), schema::Role::Client)?,
        &dir.join("settings.json"),
    );
    state.shutdown_enabled = true;
    let listener = std::net::TcpListener::bind(listen_address(&options.addr))?;
    listener.set_nonblocking(true)?;
    let bound = listener.local_addr()?;
    let url = desktop::browser_url(bound, false);
    let app = router(state.clone());
    println!("R-TAM Client\n{url}");
    log::info!(
        "R-TAM Client listening at {url} (data in {})",
        dir.display()
    );
    let tray = if options.tray {
        Some(desktop::tray(
            desktop::TrayOptions {
                tooltip: format!("R-TAM Client on {bound}"),
                icon: desktop::CLIENT_ICON,
                open_url: url.clone(),
                quit_label: "Shut Down R-TAM".into(),
            },
            (*state.shutdown).clone(),
        )?)
    } else {
        None
    };
    if options.open
        && let Err(e) = desktop::open_browser(&url)
    {
        log::warn!("could not open browser: {e}; open {url}")
    }
    let sync = state.start_sync();
    let signal = tokio::spawn(desktop::stop_on_signal((*state.shutdown).clone()));
    let handle = axum_server::Handle::new();
    let stop = state.shutdown.clone();
    let stopping = handle.clone();
    let shutdown = tokio::spawn(async move {
        stop.requested().await;
        stopping.graceful_shutdown(Some(Duration::from_secs(3)));
    });
    let mut server = axum_server::from_tcp(listener)?.handle(handle);
    server
        .http_builder()
        .http1()
        .timer(hyper_util::rt::TokioTimer::new())
        .header_read_timeout(Duration::from_secs(10));
    let result = server.serve(app.into_make_service()).await;
    state.shutdown.request();
    state.cache_shutdown().await;
    sync.await?;
    state.shutdown.drain().await;
    shutdown.await?;
    signal.await??;
    if let Some(tray) = tray {
        tray.join()?
    }
    result?;
    log::info!("R-TAM Client stopped");
    log::logger().flush();
    Ok(())
}

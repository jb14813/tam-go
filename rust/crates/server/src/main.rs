use r_tam_core::{
    cli::{Options, Role, listen_address},
    desktop, discovery, schema,
    store::Store,
    tls,
};
use r_tam_server::{ServerState, auth::Password, router};
use std::{
    io::Write,
    path::Path,
    sync::{Arc, Mutex, OnceLock},
    time::Duration,
};
struct Logger;
static LOGGER: Logger = Logger;
static LOG_FILE: OnceLock<Mutex<std::fs::File>> = OnceLock::new();
impl log::Log for Logger {
    fn enabled(&self, metadata: &log::Metadata<'_>) -> bool {
        metadata.level() <= log::Level::Info
    }
    fn log(&self, record: &log::Record<'_>) {
        if !self.enabled(record.metadata()) {
            return;
        }
        let line = format!(
            "{} {} {}\n",
            time::OffsetDateTime::now_utc(),
            record.level(),
            record.args()
        );
        eprint!("{line}");
        if let Some(file) = LOG_FILE.get()
            && let Ok(mut file) = file.lock()
        {
            let _ = file.write_all(line.as_bytes());
            let _ = file.flush();
        }
    }
    fn flush(&self) {
        if let Some(file) = LOG_FILE.get()
            && let Ok(mut file) = file.lock()
        {
            let _ = file.flush();
        }
    }
}
#[tokio::main]
async fn main() {
    if let Err(error) = run().await {
        log::error!("R-TAM Server: {error}");
        eprintln!("R-TAM Server: {error}");
        std::process::exit(1)
    }
}
async fn run() -> Result<(), Box<dyn std::error::Error>> {
    let opt = Options::parse(Role::Server, std::env::args().skip(1))?;
    if opt.help {
        print!("{}", Role::Server.usage());
        return Ok(());
    }
    let data_dir = desktop::data_dir()?;
    let _lock = desktop::InstanceGuard::acquire(&data_dir)?;
    if let Ok(file) = desktop::open_log(&data_dir, "tam-server.log") {
        let _ = LOG_FILE.set(Mutex::new(file));
    }
    log::set_logger(&LOGGER)?;
    log::set_max_level(log::LevelFilter::Info);
    desktop::set_console_title("R-TAM - server");
    let store = Store::open(&data_dir.join("tam-remote.db"), schema::Role::Server)?;
    let password = Password::load(&data_dir, &std::env::var("TAM_PWD").unwrap_or_default())?;
    let cert = if opt.tls {
        Some(tls::certificate_files(
            &data_dir,
            (!opt.cert.is_empty()).then(|| Path::new(&opt.cert)),
            (!opt.key.is_empty()).then(|| Path::new(&opt.key)),
            &[
                "localhost",
                &discovery::machine_hostname(),
                "127.0.0.1",
                "::1",
            ],
        )?)
    } else {
        None
    };
    let listener = std::net::TcpListener::bind(listen_address(&opt.addr))?;
    listener.set_nonblocking(true)?;
    let bound = listener.local_addr()?;
    let url = desktop::browser_url(bound, opt.tls);
    let mut state = ServerState::new(store, password);
    state.addr = bound.to_string();
    state.name = discovery::machine_hostname();
    state.tls = opt.tls;
    state.addresses = if bound.ip().is_unspecified() {
        discovery::local_addresses()
            .iter()
            .map(|ip| {
                format!(
                    "{}://{ip}:{}/",
                    if opt.tls { "https" } else { "http" },
                    bound.port()
                )
            })
            .collect()
    } else {
        vec![url.clone()]
    };
    let stop = state.shutdown.clone();
    let app = router(state.clone());
    println!("R-TAM Server\n{url}");
    for address in &state.addresses {
        println!("{address}");
    }
    log::info!(
        "R-TAM Server listening at {url} (data in {})",
        data_dir.display()
    );
    let _announcement = if opt.announce {
        match discovery::announce(&state.name, bound.port(), opt.tls, &state.version) {
            Ok(a) => Some(a),
            Err(error) => {
                log::warn!("not announcing on the network: {error}");
                None
            }
        }
    } else {
        None
    };
    let tray = if opt.tray {
        Some(desktop::tray(
            desktop::TrayOptions {
                tooltip: format!("R-TAM Server on {bound}"),
                icon: desktop::SERVER_ICON,
                open_url: format!("{url}admin/"),
                quit_label: "Shut Down R-TAM Server".into(),
            },
            (*stop).clone(),
        )?)
    } else {
        None
    };
    if opt.open {
        desktop::open_browser(&format!("{url}admin/"))?;
    }
    let signal = tokio::spawn(desktop::stop_on_signal((*stop).clone()));
    let handle = axum_server::Handle::new();
    let stopping = stop.clone();
    let handle_stop = handle.clone();
    let shutdown_task = tokio::spawn(async move {
        stopping.requested().await;
        handle_stop.graceful_shutdown(Some(Duration::from_secs(3)));
    });
    let result = if let Some(cert) = cert {
        let mut tls_config = cert.server_config()?;
        tls_config.alpn_protocols = vec![b"http/1.1".to_vec()];
        let config = axum_server::tls_rustls::RustlsConfig::from_config(Arc::new(tls_config));
        let mut server = axum_server::from_tcp_rustls(listener, config)?.handle(handle);
        server
            .http_builder()
            .http1()
            .timer(hyper_util::rt::TokioTimer::new())
            .header_read_timeout(Duration::from_secs(10));
        server
            .serve(app.into_make_service_with_connect_info::<std::net::SocketAddr>())
            .await
    } else {
        let mut server = axum_server::from_tcp(listener)?.handle(handle);
        server
            .http_builder()
            .http1()
            .timer(hyper_util::rt::TokioTimer::new())
            .header_read_timeout(Duration::from_secs(10));
        server
            .serve(app.into_make_service_with_connect_info::<std::net::SocketAddr>())
            .await
    };
    stop.request();
    stop.drain().await;
    shutdown_task.await?;
    signal.await??;
    if let Some(tray) = tray {
        tray.join()?;
    }
    result?;
    log::info!("R-TAM Server stopped");
    log::logger().flush();
    Ok(())
}

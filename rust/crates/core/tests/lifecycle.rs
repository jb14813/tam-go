use r_tam_core::desktop::{InstanceGuard, Shutdown, browser_command, browser_url, open_log};
use std::io::Write;
#[test]
fn instance_guard_releases_and_log_appends() {
    let d = tempfile::tempdir().unwrap();
    let g = InstanceGuard::acquire(d.path()).unwrap();
    assert!(InstanceGuard::acquire(d.path()).is_err());
    drop(g);
    let _g = InstanceGuard::acquire(d.path()).unwrap();
    open_log(d.path(), "tam-client.log")
        .unwrap()
        .write_all(b"first\n")
        .unwrap();
    open_log(d.path(), "tam-client.log")
        .unwrap()
        .write_all(b"second\n")
        .unwrap();
    assert_eq!(
        std::fs::read(d.path().join("tam-client.log")).unwrap(),
        b"first\nsecond\n"
    );
    assert!(open_log(d.path(), "../escape").is_err());
}
#[test]
fn folder_lock_applies_across_processes() {
    let d = tempfile::tempdir().unwrap();
    let guard = InstanceGuard::acquire(d.path()).unwrap();
    let run = |expected| {
        std::process::Command::new(std::env::current_exe().unwrap())
            .args(["--exact", "folder_lock_child", "--nocapture"])
            .env("RTAM_LOCK_TEST_PATH", d.path())
            .env("RTAM_LOCK_TEST_EXPECT", expected)
            .status()
            .unwrap()
    };
    assert!(run("blocked").success());
    drop(guard);
    assert!(run("acquired").success());
}
#[test]
fn folder_lock_child() {
    if let Some(path) = std::env::var_os("RTAM_LOCK_TEST_PATH") {
        let result = InstanceGuard::acquire(std::path::Path::new(&path));
        if std::env::var("RTAM_LOCK_TEST_EXPECT").unwrap() == "blocked" {
            assert!(result.is_err());
        } else {
            assert!(result.is_ok());
        }
    }
}
#[test]
fn browser_addresses_match_bound_ipv4_ipv6_and_os_command() {
    assert_eq!(
        browser_url("0.0.0.0:3080".parse().unwrap(), false),
        "http://localhost:3080/"
    );
    assert_eq!(
        browser_url("[::1]:8443".parse().unwrap(), true),
        "https://[::1]:8443/"
    );
    let (program, args) = browser_command("http://localhost:3080/");
    if cfg!(windows) {
        assert_eq!(program, "rundll32");
        assert_eq!(
            args,
            ["url.dll,FileProtocolHandler", "http://localhost:3080/"]
        );
    }
}
#[tokio::test]
async fn shutdown_refuses_new_work_and_waits_for_requests_and_sync() {
    let stop = Shutdown::new();
    let request = stop.enter().unwrap();
    let sync = stop.enter().unwrap();
    stop.request();
    assert!(stop.enter().is_none());
    assert!(stop.is_requested());
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(20), stop.drain())
            .await
            .is_err()
    );
    drop(request);
    drop(sync);
    tokio::time::timeout(std::time::Duration::from_secs(1), stop.drain())
        .await
        .unwrap();
}
#[cfg(windows)]
#[test]
fn native_tray_quit_callback_and_console_close_drain_without_listeners() {
    use r_tam_core::desktop::{CLIENT_ICON, ConsoleCloseGuard, TrayAction, TrayOptions, tray};
    let stop = Shutdown::new();
    let handle = tray(
        TrayOptions {
            tooltip: "R-TAM lifecycle validation".into(),
            icon: CLIENT_ICON,
            open_url: String::new(),
            quit_label: "Shut Down R-TAM Validation".into(),
        },
        stop.clone(),
    )
    .unwrap();
    handle.dispatch(TrayAction::Quit).unwrap();
    handle.join().unwrap();
    assert!(stop.is_requested());
    let stop = Shutdown::new();
    let activity = stop.enter().unwrap();
    let guard = ConsoleCloseGuard::install(stop.clone()).unwrap();
    let (tx, rx) = std::sync::mpsc::channel();
    let thread = std::thread::spawn(move || {
        tx.send(guard.handle_event(2)).unwrap();
    });
    std::thread::sleep(std::time::Duration::from_millis(30));
    assert!(stop.is_requested());
    assert!(
        rx.try_recv().is_err(),
        "console close must wait for active work to drain"
    );
    drop(activity);
    assert!(rx.recv_timeout(std::time::Duration::from_secs(1)).unwrap());
    thread.join().unwrap();
}
#[test]
fn discovery_txt_network_filters_targets_and_duplicate_collapse() {
    use r_tam_core::discovery::{
        Network, Server, announcement_info, collapse, skip_interface, sweep_targets,
    };
    let info = announcement_info(
        "Hall",
        "hall.local.",
        8000,
        true,
        "1.2.3",
        &["192.168.1.10".parse().unwrap()],
    )
    .unwrap();
    assert!(info.get_fullname().ends_with("._tam._tcp.local."));
    for (k, v) in [
        ("port", "8000"),
        ("tls", "true"),
        ("name", "Hall"),
        ("v", "1.2.3"),
    ] {
        assert_eq!(info.get_property_val_str(k), Some(v));
    }
    for n in [
        "docker0",
        "br-abc",
        "virbr0",
        "vethABC",
        "lxc0",
        "cni0",
        "flannel.1",
        "podman0",
        "vEthernet (WSL)",
        "vEthernet (Default Switch)",
    ] {
        assert!(skip_interface(n), "{n}");
    }
    for n in ["br0", "eth0", "vEthernet (External)"] {
        assert!(!skip_interface(n), "{n}");
    }
    let nets = vec![Network {
        address: "192.168.1.20".parse().unwrap(),
        netmask: "255.255.255.0".parse().unwrap(),
    }];
    let targets = sweep_targets(&nets);
    assert_eq!(targets.len(), 254);
    assert_eq!(targets[0], "192.168.1.1");
    assert_eq!(targets[253], "192.168.1.254");
    let a = Server {
        name: "Hall".into(),
        host: "10.0.0.2".into(),
        port: "8000".into(),
        tls: false,
        version: "1".into(),
    };
    let mut b = a.clone();
    b.host = "192.168.1.10".into();
    let out = collapse(vec![a, b], &nets);
    assert_eq!(out.len(), 1);
    assert_eq!(out[0].host, "192.168.1.10");
}
#[test]
fn browse_candidates_keep_local_address_until_server_composition() {
    use r_tam_core::discovery::{Network, announcement_info, collapse, discovered_candidates};
    let remote = announcement_info(
        "Hall",
        "hall-remote.local.",
        8000,
        false,
        "go",
        &["10.0.0.10".parse().unwrap()],
    )
    .unwrap();
    let local = announcement_info(
        "Hall",
        "hall-local.local.",
        8000,
        false,
        "go",
        &["192.168.50.10".parse().unwrap()],
    )
    .unwrap();
    let candidates =
        discovered_candidates([remote.as_resolved_service(), local.as_resolved_service()]);
    let nets = [Network {
        address: "192.168.50.20".parse().unwrap(),
        netmask: "255.255.255.0".parse().unwrap(),
    }];
    let servers = collapse(candidates, &nets);
    assert_eq!(servers.len(), 1);
    assert_eq!(servers[0].host, "192.168.50.10");
    let combined = announcement_info(
        "Hall",
        "hall.local.",
        8000,
        false,
        "go",
        &[
            "10.0.0.10".parse().unwrap(),
            "192.168.50.10".parse().unwrap(),
        ],
    )
    .unwrap();
    let servers = collapse(
        discovered_candidates([combined.as_resolved_service()]),
        &nets,
    );
    assert_eq!(servers[0].host, "192.168.50.10");
}
#[cfg(not(windows))]
#[tokio::test]
async fn discovery_sweep_probes_tam_only_and_cache_does_not_repeat() {
    use r_tam_core::discovery::{PortSpec, SweepCache, sweep_hosts};
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let ln = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = ln.local_addr().unwrap().port();
    let count = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let calls = count.clone();
    let task = tokio::spawn(async move {
        loop {
            let (mut socket, _) = ln.accept().await.unwrap();
            let calls = calls.clone();
            tokio::spawn(async move {
                let mut b = [0; 1024];
                assert!(socket.read(&mut b).await.unwrap() > 0);
                calls.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
                let body = br#"{"whoami":"TAM Server","name":"Hall","version":"go"}"#;
                socket
                    .write_all(
                        format!(
                            "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                            body.len()
                        )
                        .as_bytes(),
                    )
                    .await
                    .unwrap();
                socket.write_all(body).await.unwrap();
            });
        }
    });
    let hosts = vec!["127.0.0.1".to_owned()];
    let ports = vec![PortSpec { port, tls: false }];
    let found = sweep_hosts(hosts.clone(), ports.clone()).await;
    assert_eq!(found.len(), 1);
    assert_eq!(found[0].name, "Hall");
    let cache = SweepCache::new();
    cache.get_or_start(hosts.clone(), ports.clone());
    let found = tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            let out = cache.get_or_start(hosts.clone(), ports.clone());
            if !out.is_empty() {
                break out;
            }
            tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(found.len(), 1);
    for _ in 0..5 {
        assert_eq!(cache.get_or_start(hosts.clone(), ports.clone()).len(), 1);
    }
    tokio::time::sleep(std::time::Duration::from_millis(30)).await;
    assert_eq!(count.load(std::sync::atomic::Ordering::SeqCst), 2);
    task.abort();
}
#[cfg(not(windows))]
#[tokio::test]
async fn mdns_announcement_is_browsable_and_stops_on_drop() {
    use r_tam_core::discovery::{announce, browse};
    let name = format!("R-TAM-validation-{}", std::process::id());
    let announcement = announce(&name, 8123, true, "validation").unwrap();
    let found = browse(std::time::Duration::from_secs(4)).await.unwrap();
    let server = found
        .iter()
        .find(|s| s.name == name)
        .expect("local mDNS announcement was not browsed");
    assert_eq!(server.port, "8123");
    assert!(server.tls);
    assert_eq!(server.version, "validation");
    assert!(!server.host.is_empty());
    drop(announcement);
    let found = browse(std::time::Duration::from_millis(300)).await.unwrap();
    assert!(!found.iter().any(|s| s.name == name));
}
#[cfg(not(windows))]
#[tokio::test]
async fn discovery_shutdown_cancels_running_probe_connections() {
    use r_tam_core::discovery::{PortSpec, SweepCache};
    use tokio::io::AsyncReadExt;
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let cache = SweepCache::new();
    cache.get_or_start(
        vec!["127.0.0.1".into()],
        vec![PortSpec { port, tls: false }],
    );
    let (mut socket, _) = listener.accept().await.unwrap();
    let mut buffer = [0; 1024];
    assert!(socket.read(&mut buffer).await.unwrap() > 0);
    cache.shutdown().await;
    assert_eq!(
        tokio::time::timeout(
            std::time::Duration::from_millis(50),
            socket.read(&mut buffer)
        )
        .await
        .unwrap()
        .unwrap(),
        0
    );
    assert!(
        cache
            .get_or_start(
                vec!["127.0.0.1".into()],
                vec![PortSpec { port, tls: false }]
            )
            .is_empty()
    );
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(30), listener.accept())
            .await
            .is_err()
    );
}
#[cfg(target_os = "linux")]
#[test]
fn mdns_cancel_releases_daemon_without_waiting() {
    run_mdns_cleanup_child("cancel");
}
#[cfg(target_os = "linux")]
#[test]
fn cache_shutdown_releases_active_browse_daemon() {
    run_mdns_cleanup_child("shutdown");
}
#[cfg(target_os = "linux")]
fn run_mdns_cleanup_child(mode: &str) {
    assert!(
        std::process::Command::new(std::env::current_exe().unwrap())
            .args(["--exact", "mdns_cleanup_child", "--nocapture"])
            .env("RTAM_MDNS_CLEANUP_TEST", mode)
            .status()
            .unwrap()
            .success()
    );
}
#[cfg(target_os = "linux")]
fn mdns_resources() -> (std::collections::HashSet<String>, usize) {
    let sockets = std::fs::read_dir("/proc/self/fd")
        .unwrap()
        .flatten()
        .filter_map(|entry| std::fs::read_link(entry.path()).ok())
        .map(|link| link.to_string_lossy().into_owned())
        .filter(|link| link.starts_with("socket:["))
        .collect();
    let daemons = std::fs::read_dir("/proc/self/task")
        .unwrap()
        .flatten()
        .filter(|entry| {
            std::fs::read_to_string(entry.path().join("comm"))
                .is_ok_and(|name| name.trim() == "mDNS_daemon")
        })
        .count();
    (sockets, daemons)
}
#[cfg(target_os = "linux")]
#[test]
fn mdns_cleanup_child() {
    use r_tam_core::discovery::{SweepCache, browse};
    use std::time::Duration;
    let Ok(mode) = std::env::var("RTAM_MDNS_CLEANUP_TEST") else {
        return;
    };
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .unwrap();
    let result = runtime.block_on(async {
        let cache = SweepCache::new();
        // Seed an empty, fresh sweep so servers() starts only mDNS in this test.
        cache.get_or_start(vec![], vec![]);
        tokio::task::yield_now().await;
        let baseline = mdns_resources();
        let browsing_cache = cache.clone();
        let shutdown = mode == "shutdown";
        let task = tokio::spawn(async move {
            if shutdown {
                browsing_cache.servers().await;
            } else {
                browse(Duration::from_secs(60)).await.unwrap();
            }
        });
        tokio::time::timeout(Duration::from_secs(2), async {
            while mdns_resources().1 <= baseline.1 {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .map_err(|_| "browse did not create an mDNS daemon")?;
        if shutdown {
            cache.shutdown().await;
        } else {
            task.abort();
        }
        tokio::time::timeout(Duration::from_millis(500), async {
            let _ = task.await;
            while mdns_resources() != baseline {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .map_err(|_| "mDNS sockets or daemon survived cancellation/shutdown")
    });
    // A failing implementation's blocking worker must not delay the RED test by 60s.
    runtime.shutdown_timeout(Duration::from_millis(100));
    assert!(result.is_ok(), "{result:?}");
}

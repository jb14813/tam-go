#![cfg(not(windows))]
use axum::{
    body::Body,
    http::{Request, StatusCode},
    routing::{get, post},
};
use http_body_util::BodyExt;
use r_tam_client::{ClientState, router};
use r_tam_core::{schema::Role, store::Store};
use serde_json::{Value, json};
use std::sync::{
    Arc,
    atomic::{AtomicUsize, Ordering},
};
use tower::ServiceExt;
async fn send(
    s: &ClientState,
    path: &str,
    value: Value,
) -> (StatusCode, Value, axum::http::HeaderMap) {
    let r = router(s.clone())
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(path)
                .header("content-type", "application/json")
                .body(Body::from(value.to_string()))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = r.status();
    let h = r.headers().clone();
    let b = r.into_body().collect().await.unwrap().to_bytes();
    (status, serde_json::from_slice(&b).unwrap(), h)
}
fn state(dir: &std::path::Path, port: u16) -> ClientState {
    let s = ClientState::new(
        Store::open(&dir.join("tam-local.db"), Role::Client).unwrap(),
        &dir.join("settings.json"),
    );
    s.settings
        .update(|mut c| {
            c.remote_server = "127.0.0.1".into();
            c.remote_port = port.to_string();
            c.remote_key = "key".into();
            Ok(c)
        })
        .unwrap();
    s
}
async fn live_direct_is_not_waiting_but_cancellation_is(method: &'static str) {
    let server_dir = tempfile::tempdir().unwrap();
    let server_store = Store::open(&server_dir.path().join("server.db"), Role::Server).unwrap();
    let key = server_store.create_key("pending regression").unwrap();
    let app = r_tam_server::router(r_tam_server::ServerState::new(
        server_store.clone(),
        r_tam_server::auth::Password::load(server_dir.path(), "secret").unwrap(),
    ));
    let visitor = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/admin/")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    let cookie = visitor.headers()["set-cookie"]
        .to_str()
        .unwrap()
        .split(';')
        .next()
        .unwrap()
        .to_owned();
    let html = String::from_utf8(
        visitor
            .into_body()
            .collect()
            .await
            .unwrap()
            .to_bytes()
            .to_vec(),
    )
    .unwrap();
    let csrf = html
        .split("name=\"csrf\" value=\"")
        .nth(1)
        .unwrap()
        .split('"')
        .next()
        .unwrap();
    let login = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/admin/login")
                .header("cookie", cookie)
                .header("content-type", "application/x-www-form-urlencoded")
                .body(Body::from(format!("csrf={csrf}&password=secret")))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(login.status(), StatusCode::SEE_OTHER);
    let cookie = login.headers()["set-cookie"]
        .to_str()
        .unwrap()
        .split(';')
        .next()
        .unwrap()
        .to_owned();
    async fn admin_pending(app: &axum::Router, cookie: &str) -> Value {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/admin/status")
                    .header("cookie", cookie)
                    .header("accept", "application/json")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let doc: Value =
            serde_json::from_slice(&response.into_body().collect().await.unwrap().to_bytes())
                .unwrap();
        doc["clients"][0]["queued"].clone()
    }
    let ack_started = Arc::new(tokio::sync::Notify::new());
    let release_ack = Arc::new(tokio::sync::Notify::new());
    let ping_seen = Arc::new(tokio::sync::Notify::new());
    let release_ping = Arc::new(tokio::sync::Notify::new());
    let reported = Arc::new(AtomicUsize::new(999));
    let a = ack_started.clone();
    let r = release_ack.clone();
    let p = ping_seen.clone();
    let q = release_ping.clone();
    let count = reported.clone();
    let served = app.clone().layer(axum::middleware::from_fn(
        move |request: Request<Body>, next: axum::middleware::Next| {
            let a = a.clone();
            let r = r.clone();
            let p = p.clone();
            let q = q.clone();
            let count = count.clone();
            async move {
                let save = request.headers().contains_key("X-TAM-Save");
                let ping = request.uri().path() == "/api";
                if ping {
                    count.store(
                        request.headers()["X-TAM-Pending"]
                            .to_str()
                            .unwrap()
                            .parse()
                            .unwrap(),
                        Ordering::SeqCst,
                    );
                }
                let response = next.run(request).await;
                if save {
                    a.notify_one();
                    r.notified().await;
                }
                if ping {
                    p.notify_one();
                    q.notified().await;
                }
                response
            }
        },
    ));
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, served).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path(), port);
    s.settings
        .update(|mut cfg| {
            cfg.remote_key = key.auth_key;
            Ok(cfg)
        })
        .unwrap();
    let prefix = r_tam_core::models::Prefix {
        prefix: "A".into(),
        color: "blue".into(),
        ..Default::default()
    };
    for cancel in [false, true] {
        s.store
            .upsert_prefixes(std::slice::from_ref(&prefix))
            .unwrap();
        server_store
            .upsert_prefixes(std::slice::from_ref(&prefix))
            .unwrap();
        let run = s.clone();
        let mut direct = tokio::spawn(async move {
            router(run)
                .oneshot(
                    Request::builder()
                        .method(method)
                        .uri(if method == "DELETE" {
                            "/api/prefixes?p=A"
                        } else {
                            "/api/tickets"
                        })
                        .header("content-type", "application/json")
                        .body(Body::from(if method == "DELETE" {
                            ""
                        } else if cancel {
                            r#"[{"prefix":"A","t_id":2,"first_name":"cancelled"}]"#
                        } else {
                            r#"[{"prefix":"A","t_id":1,"first_name":"live"}]"#
                        }))
                        .unwrap(),
                )
                .await
                .unwrap()
        });
        tokio::time::timeout(std::time::Duration::from_secs(1), ack_started.notified())
            .await
            .unwrap();
        assert_eq!(s.store.pending().unwrap().len(), 1);
        assert!(s.store.outbox_waiting().unwrap());
        if cancel {
            direct.abort();
            assert!((&mut direct).await.unwrap_err().is_cancelled());
        }
        let ping_state = s.clone();
        let ping = tokio::spawn(async move { ping_state.sync_once(true).await });
        tokio::time::timeout(std::time::Duration::from_secs(1), ping_seen.notified())
            .await
            .unwrap();
        let expected = usize::from(cancel);
        assert_eq!(
            (
                reported.load(Ordering::SeqCst),
                admin_pending(&app, &cookie).await,
                s.status().await.unwrap()["pending"].clone()
            ),
            (expected, json!(expected), json!(expected)),
            "heartbeat/admin/status must count waiting work, excluding a live direct handler"
        );
        release_ping.notify_one();
        release_ack.notify_one();
        if !cancel {
            assert_eq!(direct.await.unwrap().status(), StatusCode::OK);
        }
        if cancel {
            // The original canceled HTTP response and its recovery replay each
            // consume one acknowledgment permit.
            ack_started.notified().await;
            release_ack.notify_one();
        }
        tokio::time::timeout(std::time::Duration::from_secs(2), ping)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        assert!(s.store.pending().unwrap().is_empty());
        assert!(s.store.failed().unwrap().is_empty());
        assert_eq!(s.status().await.unwrap()["pending"], 0);
        let after = s.clone();
        let ping = tokio::spawn(async move { after.sync_once(true).await });
        ping_seen.notified().await;
        assert_eq!(reported.load(Ordering::SeqCst), 0);
        assert_eq!(admin_pending(&app, &cookie).await, 0);
        release_ping.notify_one();
        ping.await.unwrap().unwrap();
    }
    peer.abort();
}
#[tokio::test]
async fn live_post_heartbeat_admin_and_status_exclude_direct_but_count_cancelled_intent() {
    live_direct_is_not_waiting_but_cancellation_is("POST").await;
}
#[tokio::test]
async fn live_delete_heartbeat_admin_and_status_exclude_direct_but_count_cancelled_intent() {
    live_direct_is_not_waiting_but_cancellation_is("DELETE").await;
}
#[tokio::test]
async fn uncertain_success_auth_and_server_failure_retain_intent_until_verified_repeat() {
    let mode = Arc::new(AtomicUsize::new(0));
    let m = mode.clone();
    let app = axum::Router::new()
        .route(
            "/api",
            get(|| async {
                axum::Json(json!({"whoami":"TAM Server","authenticated":true,"event":"e"}))
            }),
        )
        .route(
            "/api/backuprestore",
            get(|| async {
                axum::Json(json!({"event":"e","prefixes":[],"tickets":[],"baskets":[]}))
            }),
        )
        .route(
            "/api/tickets",
            post(move |axum::Json(mut rows): axum::Json<Value>| {
                let m = m.clone();
                async move {
                    match m.load(Ordering::SeqCst) {
                        1 => (
                            StatusCode::OK,
                            [("content-type", "text/html")],
                            "<html>login</html>".to_owned(),
                        ),
                        2 => {
                            rows[0]["t_id"] = json!(999);
                            (
                                StatusCode::OK,
                                [("content-type", "application/json")],
                                rows.to_string(),
                            )
                        }
                        3 => (
                            StatusCode::UNAUTHORIZED,
                            [("content-type", "application/json")],
                            json!({"detail":"key refused"}).to_string(),
                        ),
                        4 => (
                            StatusCode::INTERNAL_SERVER_ERROR,
                            [("content-type", "application/json")],
                            json!({"detail":"failed"}).to_string(),
                        ),
                        5 => (
                            StatusCode::BAD_REQUEST,
                            [("content-type", "application/json")],
                            json!({"detail":"definitive rejection"}).to_string(),
                        ),
                        6 => (
                            StatusCode::OK,
                            [("content-type", "application/json")],
                            "{}".to_owned(),
                        ),
                        _ => {
                            rows[0]["rev"] = json!(7);
                            (
                                StatusCode::OK,
                                [("content-type", "application/json")],
                                rows.to_string(),
                            )
                        }
                    }
                }
            }),
        );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    for failure in [1, 2, 3, 4, 6] {
        let dir = tempfile::tempdir().unwrap();
        let s = state(dir.path(), port);
        mode.store(failure, Ordering::SeqCst);
        let offered = json!([{"prefix":"A","t_id":1,"first_name":"Durable","pref":"TEXT"}]);
        let response = send(&s, "/api/tickets", offered).await;
        assert_eq!(response.0, StatusCode::OK);
        assert_eq!(response.2["X-TAM-Queued"], "1");
        let pending = s.store.pending().unwrap();
        assert_eq!(pending.len(), 1);
        assert_eq!(s.status().await.unwrap()["pending"], 1);
        assert_eq!(
            s.store.ticket("A", 1).unwrap().unwrap().first_name,
            "Durable"
        );
        mode.store(0, Ordering::SeqCst);
        s.sync_once(true).await.unwrap();
        assert!(s.store.pending().unwrap().is_empty());
        assert_eq!(s.store.ticket("A", 1).unwrap().unwrap().rev, 7);
        assert!(s.store.failed().unwrap().is_empty());
    }
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path(), port);
    mode.store(5, Ordering::SeqCst);
    assert_eq!(
        send(
            &s,
            "/api/tickets",
            json!([{"prefix":"A","t_id":1,"first_name":"Rejected"}])
        )
        .await
        .0,
        StatusCode::BAD_REQUEST
    );
    assert!(s.store.pending().unwrap().is_empty());
    assert!(s.store.ticket("A", 1).unwrap().is_none());
    peer.abort();
}
#[tokio::test]
async fn intent_is_durable_before_ack_and_restart_replays_same_number() {
    let accepted = Arc::new(tokio::sync::Notify::new());
    let release = Arc::new(tokio::sync::Notify::new());
    let calls = Arc::new(std::sync::Mutex::new(Vec::<(String, String)>::new()));
    let a = accepted.clone();
    let r = release.clone();
    let c = calls.clone();
    let app = axum::Router::new()
        .route(
            "/api",
            get(|| async {
                axum::Json(json!({"whoami":"TAM Server","authenticated":true,"event":"e"}))
            }),
        )
        .route(
            "/api/backuprestore",
            get(|| async {
                axum::Json(json!({"event":"e","prefixes":[],"tickets":[],"baskets":[]}))
            }),
        )
        .route(
            "/api/tickets",
            post(
                move |headers: axum::http::HeaderMap, axum::Json(mut rows): axum::Json<Value>| {
                    let a = a.clone();
                    let r = r.clone();
                    let c = c.clone();
                    async move {
                        let first = {
                            let mut c = c.lock().unwrap();
                            c.push((
                                headers["X-TAM-Client-Name"].to_str().unwrap().into(),
                                headers["X-TAM-Save"].to_str().unwrap().into(),
                            ));
                            c.len() == 1
                        };
                        if first {
                            a.notify_one();
                            r.notified().await
                        }
                        rows[0]["rev"] = json!(8);
                        axum::Json(rows)
                    }
                },
            ),
        );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path(), port);
    let run = s.clone();
    let job = tokio::spawn(async move {
        send(
            &run,
            "/api/tickets",
            json!([{"prefix":"A","t_id":1,"first_name":"before ack"}]),
        )
        .await
    });
    accepted.notified().await;
    assert!(s.store.ticket("A", 1).unwrap().is_none());
    assert_eq!(s.store.pending().unwrap().len(), 1);
    job.abort();
    let _ = job.await;
    release.notify_one();
    drop(s);
    let fresh = state(dir.path(), port);
    fresh.sync_once(true).await.unwrap();
    assert!(fresh.store.pending().unwrap().is_empty());
    assert_eq!(
        fresh.store.ticket("A", 1).unwrap().unwrap().first_name,
        "before ack"
    );
    let c = calls.lock().unwrap();
    assert_eq!(c.len(), 2);
    assert_eq!(c[0], c[1]);
    peer.abort();
}
#[tokio::test]
async fn cancelled_direct_delete_releases_dispatch_and_replays_same_intent() {
    let started = Arc::new(tokio::sync::Notify::new());
    let release = Arc::new(tokio::sync::Notify::new());
    let calls = Arc::new(std::sync::Mutex::new(Vec::<(String, String)>::new()));
    let a = started.clone();
    let r = release.clone();
    let c = calls.clone();
    let prefix = json!({"prefix":"A","color":"blue","weight":2,"rev":3});
    let returned_prefix = prefix.clone();
    let app = axum::Router::new()
        .route(
            "/api/prefixes",
            axum::routing::delete(move |headers: axum::http::HeaderMap| {
                let a = a.clone();
                let r = r.clone();
                let c = c.clone();
                let prefix = returned_prefix.clone();
                async move {
                    let first = {
                        let mut c = c.lock().unwrap();
                        c.push((
                            headers["X-TAM-Client-Name"].to_str().unwrap().into(),
                            headers["X-TAM-Save"].to_str().unwrap().into(),
                        ));
                        c.len() == 1
                    };
                    if first {
                        a.notify_one();
                        r.notified().await;
                    }
                    axum::Json(prefix)
                }
            }),
        )
        .route(
            "/api/backuprestore",
            get(|| async {
                axum::Json(json!({"event":"","prefixes":[],"tickets":[],"baskets":[]}))
            }),
        );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path(), port);
    s.store
        .upsert_prefixes(&[serde_json::from_value(prefix).unwrap()])
        .unwrap();
    let direct_state = s.clone();
    let direct = tokio::spawn(async move {
        router(direct_state)
            .oneshot(
                Request::builder()
                    .method("DELETE")
                    .uri("/api/prefixes?p=A")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
    });
    started.notified().await;
    assert_eq!(s.store.prefix_by_name("A").unwrap().unwrap().color, "blue");
    assert_eq!(s.store.pending().unwrap().len(), 1);
    direct.abort();
    assert!(direct.await.unwrap_err().is_cancelled());
    release.notify_one();
    tokio::time::timeout(std::time::Duration::from_secs(1), s.sync_once(false))
        .await
        .unwrap()
        .unwrap();
    assert!(s.store.prefix_by_name("A").unwrap().is_none());
    assert!(s.store.pending().unwrap().is_empty());
    assert!(s.store.failed().unwrap().is_empty());
    let c = calls.lock().unwrap();
    assert_eq!(c.len(), 2);
    assert_eq!(c[0], c[1]);
    peer.abort();
}
#[tokio::test]
async fn delayed_read_does_not_replace_an_offline_edit_accepted_during_download() {
    let started = Arc::new(tokio::sync::Notify::new());
    let release = Arc::new(tokio::sync::Notify::new());
    let a = started.clone();
    let b = release.clone();
    let app = axum::Router::new().route(
        "/api/tickets",
        get(move || {
            let a = a.clone();
            let b = b.clone();
            async move {
                a.notify_one();
                b.notified().await;
                axum::Json(json!([{"prefix":"A","t_id":1,"first_name":"old remote"}]))
            }
        }),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path(), port);
    let read = s.clone();
    let job = tokio::spawn(async move {
        router(read)
            .oneshot(
                Request::builder()
                    .uri("/api/tickets")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap()
    });
    started.notified().await;
    s.failure("WiFi dropped");
    let answer = send(
        &s,
        "/api/tickets",
        json!([{"prefix":"A","t_id":1,"first_name":"new local"}]),
    )
    .await;
    assert_eq!(answer.0, StatusCode::OK);
    release.notify_one();
    let _ = job.await.unwrap();
    assert_eq!(
        s.store.ticket("A", 1).unwrap().unwrap().first_name,
        "new local"
    );
    assert_eq!(s.store.pending().unwrap().len(), 1);
    peer.abort();
}
#[tokio::test]
async fn shutdown_cancels_stalled_sync_and_retains_pending_save() {
    let started = Arc::new(tokio::sync::Notify::new());
    let a = started.clone();
    let app = axum::Router::new()
        .route(
            "/api",
            get(|| async {
                axum::Json(json!({"whoami":"TAM Server","authenticated":true,"event":"e"}))
            }),
        )
        .route(
            "/api/tickets",
            post(move || {
                let a = a.clone();
                async move {
                    a.notify_one();
                    std::future::pending::<()>().await;
                    axum::Json(json!([]))
                }
            }),
        );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path(), port);
    s.store
        .journal_online(
            "host",
            "POST",
            "/api/tickets",
            br#"[{"prefix":"A","t_id":1}]"#,
        )
        .unwrap();
    let sync = s.start_sync();
    started.notified().await;
    s.shutdown.request();
    tokio::time::timeout(std::time::Duration::from_millis(500), sync)
        .await
        .unwrap()
        .unwrap();
    tokio::time::timeout(std::time::Duration::from_millis(500), s.cleanup())
        .await
        .unwrap();
    assert_eq!(s.store.pending().unwrap().len(), 1);
    assert!(s.store.ticket("A", 1).unwrap().is_none());
    peer.abort();
}
#[tokio::test]
async fn online_prefix_delete_returns_local_metadata_and_404_when_neither_side_has_it() {
    let app = axum::Router::new().route(
        "/api/prefixes",
        axum::routing::delete(|| async {
            (
                StatusCode::NOT_FOUND,
                axum::Json(json!({"detail":"Prefix not found"})),
            )
        }),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path(), port);
    s.store
        .upsert_prefixes(&[r_tam_core::models::Prefix {
            prefix: "A".into(),
            color: "blue".into(),
            weight: 2,
            rev: 3,
        }])
        .unwrap();
    async fn remove(s: &ClientState, p: &str) -> (StatusCode, Value) {
        let r = router(s.clone())
            .oneshot(
                Request::builder()
                    .method("DELETE")
                    .uri(format!("/api/prefixes?p={p}"))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        let status = r.status();
        let b = r.into_body().collect().await.unwrap().to_bytes();
        (status, serde_json::from_slice(&b).unwrap())
    }
    let response = remove(&s, "A").await;
    assert_eq!(response.0, StatusCode::OK);
    assert_eq!(response.1["color"], "blue");
    assert_eq!(response.1["weight"], 2);
    assert_eq!(remove(&s, "B").await.0, StatusCode::NOT_FOUND);
    assert!(s.store.pending().unwrap().is_empty());
    peer.abort();
}
#[tokio::test]
async fn heartbeat_reports_pending_and_treats_public_root_key_refusal_as_unauthenticated() {
    let reported = Arc::new(AtomicUsize::new(0));
    let count = reported.clone();
    let app = axum::Router::new().route(
        "/api",
        get(move |h: axum::http::HeaderMap| {
            count.store(
                h.get("X-TAM-Pending")
                    .and_then(|v| v.to_str().ok())
                    .and_then(|v| v.parse::<usize>().ok())
                    .unwrap_or(999),
                Ordering::SeqCst,
            );
            async {
                axum::Json(
                    json!({"whoami":"TAM Server","authenticated":false,"healthy":true,"event":"e"}),
                )
            }
        }),
    );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path(), port);
    s.store
        .journal_online(
            "host",
            "POST",
            "/api/tickets",
            br#"[{"prefix":"A","t_id":1}]"#,
        )
        .unwrap();
    let result = s.sync_once(true).await;
    assert_eq!(reported.load(Ordering::SeqCst), 1);
    result.unwrap();
    assert_eq!(s.status().await.unwrap()["state"], "unauthenticated");
    assert_eq!(s.store.pending().unwrap().len(), 1);
    peer.abort();
}

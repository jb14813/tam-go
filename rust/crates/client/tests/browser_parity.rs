#![cfg(not(windows))]
use axum::{body::Body, http::Request, middleware};
use r_tam_client::{ClientState, router};
use r_tam_core::{schema::Role, store::Store};
use serde_json::{Value, json};
use std::sync::Arc;
use tokio::sync::Notify;
use tower::ServiceExt;

async fn call(
    client: &ClientState,
    method: &str,
    path: &str,
    value: Value,
) -> (Value, axum::http::HeaderMap) {
    let response = router(client.clone())
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header("content-type", "application/json")
                .body(if method == "GET" {
                    Body::empty()
                } else {
                    Body::from(value.to_string())
                })
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), 200);
    let headers = response.headers().clone();
    let body = axum::body::to_bytes(response.into_body(), 100000)
        .await
        .unwrap();
    (serde_json::from_slice(&body).unwrap(), headers)
}

struct Event {
    _server_dir: tempfile::TempDir,
    _client_dir: tempfile::TempDir,
    store: Store,
    client: ClientState,
    peer: tokio::task::JoinHandle<()>,
    reached: Arc<Notify>,
    release: Arc<Notify>,
}
impl Drop for Event {
    fn drop(&mut self) {
        self.peer.abort();
    }
}
impl Event {
    async fn new() -> Self {
        let server_dir = tempfile::tempdir().unwrap();
        let client_dir = tempfile::tempdir().unwrap();
        let store = Store::open(&server_dir.path().join("server.db"), Role::Server).unwrap();
        let key = store.create_key("browser parity").unwrap();
        let reached = Arc::new(Notify::new());
        let release = Arc::new(Notify::new());
        let (entered, resume) = (reached.clone(), release.clone());
        let app = r_tam_server::router(r_tam_server::ServerState::new(
            store.clone(),
            r_tam_server::auth::Password::load(server_dir.path(), "secret").unwrap(),
        ))
        .layer(middleware::from_fn(
            move |request: Request<Body>, next: middleware::Next| {
                let (entered, resume) = (entered.clone(), resume.clone());
                async move {
                    let report = request.uri().path().starts_with("/api/reports/");
                    let response = next.run(request).await;
                    if report {
                        entered.notify_one();
                        resume.notified().await;
                    }
                    response
                }
            },
        ));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
        let client = ClientState::new(
            Store::open(&client_dir.path().join("client.db"), Role::Client).unwrap(),
            &client_dir.path().join("settings.json"),
        );
        client
            .settings
            .update(|mut c| {
                c.remote_server = "127.0.0.1".into();
                c.remote_port = port.to_string();
                c.remote_key = key.auth_key;
                Ok(c)
            })
            .unwrap();
        for (path, rows) in [
            (
                "/api/prefixes",
                json!([{"prefix":"A","color":"green","weight":1}]),
            ),
            (
                "/api/tickets",
                json!([{"prefix":"A","t_id":1,"first_name":"Ann","last_name":"Lee","pref":"CALL"},{"prefix":"A","t_id":2,"first_name":"Bob","last_name":"Ray","pref":"CALL"}]),
            ),
            (
                "/api/baskets",
                json!([{"prefix":"A","b_id":1,"description":"Wine","donors":"Smiths"}]),
            ),
        ] {
            assert!(
                !call(&client, "POST", path, rows)
                    .await
                    .1
                    .contains_key("X-TAM-Queued")
            );
        }
        Self {
            _server_dir: server_dir,
            _client_dir: client_dir,
            store,
            client,
            peer,
            reached,
            release,
        }
    }
}

#[tokio::test]
async fn concurrent_prefix_mirror_does_not_mark_online_reports_as_copies() {
    let event = Event::new().await;
    call(
        &event.client,
        "POST",
        "/api/drawing",
        json!([{"prefix":"A","b_id":1,"winning_ticket":1,"base":{"winning_ticket":0}}]),
    )
    .await;
    for path in [
        "/api/reports/byname/A",
        "/api/reports/bybasket/A",
        "/api/reports/counts",
    ] {
        let client = event.client.clone();
        let report = tokio::spawn(async move { call(&client, "GET", path, Value::Null).await });
        event.reached.notified().await;
        let before = event.client.store.generation().unwrap();
        call(&event.client, "GET", "/api/prefixes", Value::Null).await;
        assert!(event.client.store.generation().unwrap() > before);
        event.release.notify_one();
        let (rows, headers) = report.await.unwrap();
        assert!(!rows.as_array().unwrap().is_empty());
        assert!(
            !headers.contains_key("X-TAM-Copy"),
            "online {path} incorrectly marked as local: {headers:?}"
        );
    }
}

#[tokio::test]
async fn winner_loaded_before_another_save_is_refused_and_returns_current_winner() {
    let event = Event::new().await;
    let loaded = call(&event.client, "GET", "/api/drawing/A/1/1", Value::Null)
        .await
        .0;
    assert_eq!(loaded[0]["winning_ticket"], 0);
    let other_dir = tempfile::tempdir().unwrap();
    let mut other = ClientState::new(
        Store::open(&other_dir.path().join("client.db"), Role::Client).unwrap(),
        &other_dir.path().join("settings.json"),
    );
    other.host = "another-computer".into();
    other
        .settings
        .update(|_| Ok(event.client.settings.get()))
        .unwrap();
    call(
        &other,
        "POST",
        "/api/drawing",
        json!([{"prefix":"A","b_id":1,"winning_ticket":2,"base":{"winning_ticket":0}}]),
    )
    .await;
    let (answer, headers) = call(&event.client, "POST", "/api/drawing", json!([{"prefix":"A","b_id":1,"winning_ticket":1,"base":{"winning_ticket":loaded[0]["winning_ticket"]}}])).await;
    assert!(!headers.contains_key("X-TAM-Queued"));
    assert_eq!(answer[0]["winning_ticket"], 2);
    for store in [&event.store, &event.client.store] {
        let row = store.basket("A", 1).unwrap().unwrap();
        assert_eq!(row.winning_ticket, 2);
        assert_eq!(row.description, "Wine");
        assert_eq!(row.donors, "Smiths");
    }
    assert!(event.client.store.pending().unwrap().is_empty());
}

#[tokio::test]
async fn report_download_preserves_queued_local_winner_and_offline_copy_notice() {
    let event = Event::new().await;
    call(
        &event.client,
        "POST",
        "/api/drawing",
        json!([{"prefix":"A","b_id":1,"winning_ticket":1}]),
    )
    .await;
    let client = event.client.clone();
    let report =
        tokio::spawn(
            async move { call(&client, "GET", "/api/reports/byname/A", Value::Null).await },
        );
    event.reached.notified().await;
    event.client.failure("offline");
    let (_, headers) = call(
        &event.client,
        "POST",
        "/api/drawing",
        json!([{"prefix":"A","b_id":1,"winning_ticket":2,"base":{"winning_ticket":1}}]),
    )
    .await;
    assert!(headers.contains_key("X-TAM-Queued"));
    event.release.notify_one();
    let (rows, headers) = report.await.unwrap();
    assert_eq!(headers["X-TAM-Copy"], "1");
    assert_eq!(rows[0]["first_name"], "Bob");
    assert_eq!(
        event.store.basket("A", 1).unwrap().unwrap().winning_ticket,
        1
    );
    assert_eq!(
        event
            .client
            .store
            .basket("A", 1)
            .unwrap()
            .unwrap()
            .winning_ticket,
        2
    );
    assert_eq!(event.client.store.outbox_counts().unwrap(), (1, 0));
    event.client.failure("offline");
    for path in [
        "/api/reports/byname/A",
        "/api/reports/bybasket/A",
        "/api/reports/counts",
    ] {
        assert_eq!(
            call(&event.client, "GET", path, Value::Null).await.1["X-TAM-Copy"],
            "1"
        );
    }
}

#[tokio::test]
async fn report_from_previous_event_is_discarded_after_actual_sync_transition() {
    let event = Event::new().await;
    call(
        &event.client,
        "POST",
        "/api/drawing",
        json!([{"prefix":"A","b_id":1,"winning_ticket":1}]),
    )
    .await;
    event.client.sync_once(true).await.unwrap();
    let old_event = event.client.store.mirror_event().unwrap();
    let client = event.client.clone();
    let report =
        tokio::spawn(
            async move { call(&client, "GET", "/api/reports/byname/A", Value::Null).await },
        );
    event.reached.notified().await;
    let mut backup = event.store.export().unwrap();
    backup.event = "next-event".into();
    backup.tickets[0].first_name = "New event buyer".into();
    event.store.import(&backup).unwrap();
    event.client.sync_once(true).await.unwrap();
    assert_ne!(event.client.store.mirror_event().unwrap(), old_event);
    event.release.notify_one();
    let (rows, headers) = report.await.unwrap();
    assert_eq!(headers["X-TAM-Copy"], "1");
    assert_eq!(rows[0]["first_name"], "New event buyer");
}

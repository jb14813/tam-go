#![cfg(not(windows))]
use axum::{
    body::Body,
    http::{Request, StatusCode},
    routing::get,
};
use http_body_util::BodyExt;
use r_tam_client::{ClientState, router};
use r_tam_core::{
    models::{Form, SaveBatch, Ticket},
    schema::Role,
    store::Store,
};
use serde_json::{Value, json};
use tower::ServiceExt;
async fn peer(event: &str) -> (String, tokio::task::JoinHandle<()>) {
    let root = json!({"whoami":"TAM Server","name":"auction","event":event});
    let app = axum::Router::new()
        .route("/api", get(move || async move { axum::Json(root) }))
        .route(
            "/api/auth",
            axum::routing::post(|| async { axum::Json(json!({"auth_key":"PAIRKEY"})) }),
        );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port().to_string();
    (
        port,
        tokio::spawn(async move { axum::serve(listener, app).await.unwrap() }),
    )
}
async fn pair(s: &ClientState, port: &str) -> (StatusCode, Value) {
    let r = router(s.clone())
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/pair")
                .header("content-type", "application/json")
                .body(Body::from(
                    json!({"host":"127.0.0.1","port":port,"password":"pass"}).to_string(),
                ))
                .unwrap(),
        )
        .await
        .unwrap();
    let code = r.status();
    let body = r.into_body().collect().await.unwrap().to_bytes();
    (code, serde_json::from_slice(&body).unwrap())
}
fn state(dir: &std::path::Path) -> ClientState {
    ClientState::new(
        Store::open(&dir.join("tam-local.db"), Role::Client).unwrap(),
        &dir.join("settings.json"),
    )
}
fn seed(s: &ClientState) {
    s.store
        .upsert_tickets(&[Ticket {
            prefix: "A".into(),
            t_id: 1,
            first_name: "old event".into(),
            ..Default::default()
        }])
        .unwrap();
    s.store.set_mirror_event("old").unwrap();
    let batch = SaveBatch::decode(
        Form::Tickets,
        br#"[{"prefix":"A","t_id":2,"first_name":"waiting"}]"#,
    )
    .unwrap();
    s.store
        .accept_offline("host", "POST", "/api/tickets", &batch)
        .unwrap();
}
#[tokio::test]
async fn new_event_archives_before_clearing_and_same_event_repair_retains_intents() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    seed(&s);
    let (port, peer) = peer("new").await;
    assert_eq!(pair(&s, &port).await.0, StatusCode::OK);
    assert!(s.store.all_tickets().unwrap().is_empty());
    assert!(s.store.pending().unwrap().is_empty());
    assert_eq!(s.store.failed().unwrap().len(), 1);
    assert_eq!(s.store.mirror_event().unwrap(), "new");
    let archive = std::fs::read_dir(dir.path())
        .unwrap()
        .filter_map(Result::ok)
        .find(|p| {
            p.file_name()
                .to_string_lossy()
                .starts_with("before-pairing-")
        })
        .unwrap();
    let backup: Value = serde_json::from_slice(&std::fs::read(archive.path()).unwrap()).unwrap();
    assert_eq!(backup["event"], "old");
    assert_eq!(backup["tickets"].as_array().unwrap().len(), 2);
    s.store
        .journal_online(
            "host",
            "POST",
            "/api/tickets",
            br#"[{"prefix":"A","t_id":3}]"#,
        )
        .unwrap();
    let waiting = s.store.pending().unwrap();
    assert_eq!(pair(&s, &port).await.0, StatusCode::OK);
    assert_eq!(s.store.pending().unwrap()[0].order, waiting[0].order);
    peer.abort();
}
#[tokio::test]
async fn archive_failure_leaves_copy_event_pending_and_settings_intact() {
    let dir = tempfile::tempdir().unwrap();
    let mut s = state(dir.path());
    seed(&s);
    let before = s.store.export().unwrap();
    let cfg = s.settings.get();
    let bad = dir.path().join("not-a-folder");
    std::fs::write(&bad, b"file").unwrap();
    s.data_dir = bad;
    let (port, peer) = peer("new").await;
    let answer = pair(&s, &port).await;
    assert_eq!(answer.0, StatusCode::INTERNAL_SERVER_ERROR);
    assert_eq!(s.store.export().unwrap(), before);
    assert_eq!(s.store.pending().unwrap().len(), 1);
    assert_eq!(s.settings.get(), cfg);
    peer.abort();
}
#[tokio::test]
async fn settings_write_failure_does_not_clear_old_event_or_move_pending() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    seed(&s);
    let before = s.store.export().unwrap();
    std::fs::remove_file(dir.path().join("settings.json")).unwrap();
    std::fs::create_dir(dir.path().join("settings.json")).unwrap();
    let (port, peer) = peer("new").await;
    assert_eq!(pair(&s, &port).await.0, StatusCode::INTERNAL_SERVER_ERROR);
    assert_eq!(s.store.export().unwrap(), before);
    assert_eq!(s.store.pending().unwrap().len(), 1);
    peer.abort();
}
#[tokio::test]
async fn heartbeat_changes_event_before_replay_and_pulls_new_copy() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    seed(&s);
    let sent = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let seen = sent.clone();
    let app=axum::Router::new().route("/api",get(||async{axum::Json(json!({"whoami":"TAM Server","name":"auction","event":"new"}))})).route("/api/tickets",axum::routing::post(move||{seen.fetch_add(1,std::sync::atomic::Ordering::SeqCst);async{axum::Json(json!([]))}})).route("/api/backuprestore",get(||async{axum::Json(json!({"event":"new","prefixes":[],"baskets":[],"tickets":[{"prefix":"B","t_id":4,"first_name":"new event"}]}))}));
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    s.settings
        .update(|mut c| {
            c.remote_server = "127.0.0.1".into();
            c.remote_port = port.to_string();
            Ok(c)
        })
        .unwrap();
    s.sync_once(true).await.unwrap();
    assert_eq!(sent.load(std::sync::atomic::Ordering::SeqCst), 0);
    assert_eq!(s.store.failed().unwrap().len(), 1);
    assert!(s.store.pending().unwrap().is_empty());
    assert_eq!(s.store.mirror_event().unwrap(), "new");
    assert!(s.store.ticket("A", 1).unwrap().is_none());
    assert_eq!(
        s.store.ticket("B", 4).unwrap().unwrap().first_name,
        "new event"
    );
    assert!(
        std::fs::read_dir(dir.path())
            .unwrap()
            .filter_map(Result::ok)
            .any(|p| p.file_name().to_string_lossy().starts_with("before-event-"))
    );
    peer.abort();
}

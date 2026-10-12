use axum::{body::Body, http::Request};
use r_tam_core::{schema::Role, store::Store};
use r_tam_server::{ServerState, auth::Password, router};
use tower::ServiceExt;
#[tokio::test]
async fn shutdown_refuses_new_work_and_drains_active() {
    let dir = tempfile::tempdir().unwrap();
    let state = ServerState::new(
        Store::open(&dir.path().join("db"), Role::Server).unwrap(),
        Password::load(dir.path(), "").unwrap(),
    );
    let stop = state.shutdown.clone();
    let activity = stop.enter().unwrap();
    stop.request();
    assert!(stop.enter().is_none());
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(10), stop.drain())
            .await
            .is_err()
    );
    let r = router(state)
        .oneshot(Request::builder().uri("/api").body(Body::empty()).unwrap())
        .await
        .unwrap();
    assert_eq!(r.status(), 503);
    drop(activity);
    tokio::time::timeout(std::time::Duration::from_secs(1), stop.drain())
        .await
        .unwrap();
}
#[tokio::test]
async fn canceled_request_keeps_blocking_auth_work_in_shutdown_drain() {
    use std::sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
        mpsc,
    };
    let dir = tempfile::tempdir().unwrap();
    let clock_calls = Arc::new(AtomicUsize::new(0));
    let calls = clock_calls.clone();
    let state = ServerState::with_clock(
        Store::open(&dir.path().join("db"), Role::Server).unwrap(),
        Password::load(dir.path(), "secret").unwrap(),
        Arc::new(move || {
            calls.fetch_add(1, Ordering::SeqCst);
            1000
        }),
    );
    let limiter = state.limiter.clone();
    let (tx_started, rx_started) = mpsc::channel();
    let (tx_release, rx_release) = mpsc::channel();
    let checking = std::thread::spawn(move || {
        limiter.check("unknown", || {
            tx_started.send(()).unwrap();
            rx_release.recv().unwrap();
            true
        })
    });
    rx_started.recv().unwrap();
    let before = clock_calls.load(Ordering::SeqCst);
    let stop = state.shutdown.clone();
    let app = router(state);
    let request = tokio::spawn(async move {
        app.oneshot(
            Request::builder()
                .uri("/api/auth")
                .header("tam-pw", "secret")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap()
    });
    tokio::time::timeout(std::time::Duration::from_secs(1), async {
        while clock_calls.load(Ordering::SeqCst) == before {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    request.abort();
    let _ = request.await;
    stop.request();
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(10), stop.drain())
            .await
            .is_err()
    );
    tx_release.send(()).unwrap();
    checking.join().unwrap();
    tokio::time::timeout(std::time::Duration::from_secs(1), stop.drain())
        .await
        .unwrap();
}

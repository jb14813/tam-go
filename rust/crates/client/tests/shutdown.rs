use r_tam_client::ClientState;
use r_tam_core::{schema::Role, store::Store};
use std::sync::{Arc, Barrier};
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn cancelled_request_keeps_database_activity_until_worker_finishes() {
    let dir = tempfile::tempdir().unwrap();
    let state = ClientState::new(
        Store::open(&dir.path().join("tam-local.db"), Role::Client).unwrap(),
        &dir.path().join("settings.json"),
    );
    let started = Arc::new(Barrier::new(2));
    let release = Arc::new(Barrier::new(2));
    let run = state.clone();
    let a = started.clone();
    let b = release.clone();
    let job = tokio::spawn(async move {
        run.db(move |_| {
            a.wait();
            b.wait();
            Ok(())
        })
        .await
    });
    let a = started.clone();
    tokio::task::spawn_blocking(move || a.wait()).await.unwrap();
    job.abort();
    let _ = job.await;
    state.shutdown.request();
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(30), state.shutdown.drain())
            .await
            .is_err()
    );
    tokio::task::spawn_blocking(move || release.wait())
        .await
        .unwrap();
    tokio::time::timeout(std::time::Duration::from_secs(1), state.cleanup())
        .await
        .unwrap();
    assert!(state.shutdown.enter().is_none());
}

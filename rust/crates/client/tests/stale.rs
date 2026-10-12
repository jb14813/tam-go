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
use std::sync::{Arc, Mutex};
use tower::ServiceExt;
#[tokio::test]
async fn direct_behind_renumbers_but_queued_behind_fails_then_retries_at_tail() {
    let seen = Arc::new(Mutex::new(Vec::new()));
    let saved = seen.clone();
    let app = axum::Router::new()
        .route(
            "/api",
            get(|| async { axum::Json(json!({"whoami":"TAM Server","event":"e"})) }),
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
                move |headers: axum::http::HeaderMap, axum::Json(rows): axum::Json<Value>| {
                    let saved = saved.clone();
                    async move {
                        let n = headers["X-TAM-Save"]
                            .to_str()
                            .unwrap()
                            .parse::<i64>()
                            .unwrap();
                        saved.lock().unwrap().push(n);
                        if n <= 20 {
                            (
                                StatusCode::CONFLICT,
                                axum::Json(json!({"detail":"older save","last_save":20})),
                            )
                        } else {
                            (StatusCode::OK, axum::Json(rows))
                        }
                    }
                },
            ),
        );
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = listener.local_addr().unwrap().port();
    let peer = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let dir = tempfile::tempdir().unwrap();
    let s = ClientState::new(
        Store::open(&dir.path().join("tam-local.db"), Role::Client).unwrap(),
        &dir.path().join("settings.json"),
    );
    s.settings
        .update(|mut c| {
            c.remote_server = "127.0.0.1".into();
            c.remote_port = port.to_string();
            Ok(c)
        })
        .unwrap();
    async fn save(s: &ClientState, id: i64) -> StatusCode {
        router(s.clone())
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/tickets")
                    .header("content-type", "application/json")
                    .body(Body::from(
                        json!([{"prefix":"A","t_id":id,"first_name":"save"}]).to_string(),
                    ))
                    .unwrap(),
            )
            .await
            .unwrap()
            .status()
    }
    assert_eq!(save(&s, 1).await, StatusCode::OK);
    assert_eq!(*seen.lock().unwrap(), vec![1, 21]);
    assert!(s.store.pending().unwrap().is_empty());
    let queued_dir = tempfile::tempdir().unwrap();
    let q = ClientState::new(
        Store::open(&queued_dir.path().join("tam-local.db"), Role::Client).unwrap(),
        &queued_dir.path().join("settings.json"),
    );
    q.settings
        .update(|mut c| {
            c.remote_server = "127.0.0.1".into();
            c.remote_port = port.to_string();
            Ok(c)
        })
        .unwrap();
    q.failure("offline");
    assert_eq!(save(&q, 1).await, StatusCode::OK);
    assert_eq!(save(&q, 2).await, StatusCode::OK);
    q.sync_once(true).await.unwrap();
    assert_eq!(q.store.failed().unwrap().len(), 2);
    assert!(q.store.pending().unwrap().is_empty());
    q.failure("offline");
    assert_eq!(save(&q, 3).await, StatusCode::OK);
    let first = q.store.pending().unwrap()[0].order.save;
    assert_eq!(first, 21);
    let r = router(q.clone())
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/outbox/retry")
                .header("content-type", "application/json")
                .body(Body::from("{}"))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), StatusCode::OK);
    let bytes = r.into_body().collect().await.unwrap().to_bytes();
    let v: Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(v["pending"], 3);
    assert_eq!(
        q.store
            .pending()
            .unwrap()
            .iter()
            .map(|i| i.order.save)
            .collect::<Vec<_>>(),
        vec![21, 22, 23]
    );
    q.sync_once(true).await.unwrap();
    assert!(q.store.pending().unwrap().is_empty());
    assert!(q.store.failed().unwrap().is_empty());
    peer.abort();
}

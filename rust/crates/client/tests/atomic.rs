use axum::{
    body::Body,
    http::{Request, StatusCode},
};
use r_tam_client::{ClientState, router};
use r_tam_core::{
    models::{Basket, Prefix, Ticket},
    schema::Role,
    store::Store,
};
use tower::ServiceExt;
#[tokio::test]
async fn offline_queue_insert_failure_rolls_back_rows_and_save_allocation() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::open(&dir.path().join("tam-local.db"), Role::Client).unwrap();
    let s = ClientState::new(store.clone(), &dir.path().join("settings.json"));
    s.settings
        .update(|mut c| {
            c.remote_server = "localhost".into();
            Ok(c)
        })
        .unwrap();
    s.failure("offline test");
    let db = rusqlite::Connection::open(store.path()).unwrap();
    db.execute_batch("CREATE TRIGGER deny_queue BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT,'disk test'); END;").unwrap();
    let r = router(s.clone())
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/tickets")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"[{"prefix":"A","t_id":1,"first_name":"unsaved"}]"#,
                ))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), StatusCode::INTERNAL_SERVER_ERROR);
    assert!(store.all_tickets().unwrap().is_empty());
    assert!(store.pending().unwrap().is_empty());
    db.execute_batch("DROP TRIGGER deny_queue").unwrap();
    assert_eq!(store.next_save("host", None).unwrap().save, 1);
}
#[tokio::test]
async fn prefix_delete_preserves_tickets_baskets_and_failed_queue_controls() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("tam-local.db"), Role::Client).unwrap();
    let s = ClientState::new(st.clone(), &dir.path().join("settings.json"));
    st.upsert_prefixes(&[Prefix {
        prefix: "A".into(),
        color: "blue".into(),
        ..Default::default()
    }])
    .unwrap();
    st.upsert_tickets(&[Ticket {
        prefix: "A".into(),
        t_id: 1,
        ..Default::default()
    }])
    .unwrap();
    st.upsert_baskets(&[Basket {
        prefix: "A".into(),
        b_id: 1,
        ..Default::default()
    }])
    .unwrap();
    s.settings
        .update(|mut c| {
            c.remote_server = "localhost".into();
            Ok(c)
        })
        .unwrap();
    s.failure("offline test");
    let r = router(s.clone())
        .oneshot(
            Request::builder()
                .method("DELETE")
                .uri("/api/prefixes?p=A")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), StatusCode::OK);
    assert_eq!(r.headers()["X-TAM-Queued"], "1");
    assert_eq!(st.counts().unwrap(), (0, 1, 1));
    st.fail_all_outbox("test refusal").unwrap();
    let r = router(s)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/outbox/discard")
                .header("content-type", "application/json")
                .body(Body::from("{}"))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), StatusCode::OK);
    assert!(st.failed().unwrap().is_empty());
}
#[tokio::test]
async fn unpair_rejects_invalid_password_and_restores_configuration_when_quarantine_fails() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("tam-local.db"), Role::Client).unwrap();
    let s = ClientState::new(st.clone(), &dir.path().join("settings.json"));
    s.settings
        .update(|mut c| {
            c.remote_server = "localhost".into();
            c.remote_key = "retain".into();
            Ok(c)
        })
        .unwrap();
    let original = s.settings.get();
    async fn unpair(s: &ClientState, body: &str) -> StatusCode {
        router(s.clone())
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/unpair")
                    .header("content-type", "application/json")
                    .body(Body::from(body.to_owned()))
                    .unwrap(),
            )
            .await
            .unwrap()
            .status()
    }
    assert_eq!(
        unpair(&s, r#"{"password":123}"#).await,
        StatusCode::BAD_REQUEST
    );
    assert_eq!(s.settings.get(), original);
    st.journal_online(
        "host",
        "POST",
        "/api/tickets",
        br#"[{"prefix":"A","t_id":1}]"#,
    )
    .unwrap();
    let db = rusqlite::Connection::open(st.path()).unwrap();
    db.execute_batch("CREATE TRIGGER deny_quarantine BEFORE DELETE ON outbox BEGIN SELECT RAISE(ABORT,'test'); END;").unwrap();
    assert_eq!(unpair(&s, "{}").await, StatusCode::INTERNAL_SERVER_ERROR);
    assert_eq!(s.settings.get(), original);
    assert_eq!(st.pending().unwrap().len(), 1);
    assert!(st.failed().unwrap().is_empty());
    db.execute_batch("DROP TRIGGER deny_quarantine").unwrap();
    assert_eq!(unpair(&s, "{}").await, StatusCode::OK);
    assert!(s.settings.get().remote_server.is_empty());
    assert_eq!(st.failed().unwrap().len(), 1);
}

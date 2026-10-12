use axum::{
    body::{Body, to_bytes},
    http::Request,
};
use r_tam_core::{schema::Role, store::Store};
use r_tam_server::{ServerState, auth::Password, router};
use tower::ServiceExt;
async fn send(
    app: &axum::Router,
    key: &str,
    method: &str,
    path: &str,
    body: &str,
    save: i64,
) -> (
    axum::http::StatusCode,
    axum::http::HeaderMap,
    serde_json::Value,
) {
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header("tam-key", key)
                .header("content-type", "application/json")
                .header("x-tam-client-name", "ordered")
                .header("x-tam-save", save.to_string())
                .body(Body::from(body.to_owned()))
                .unwrap(),
        )
        .await
        .unwrap();
    let code = r.status();
    let h = r.headers().clone();
    let doc = serde_json::from_slice(&to_bytes(r.into_body(), 100000).await.unwrap()).unwrap();
    (code, h, doc)
}
#[tokio::test]
async fn normalized_repeat_behind_changed_digest_and_deleted_rows() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("test").unwrap().auth_key;
    let app = router(ServerState::new(
        st.clone(),
        Password::load(dir.path(), "secret").unwrap(),
    ));
    let first =
        r#"[{"t_id":1,"prefix":" p ","first_name":"<Alice>","pref":" phone ","unused":"ignored"}]"#;
    let normalized = r#"[{"prefix":"p","t_id":1,"first_name":"<Alice>","pref":"phone"}]"#;
    assert_eq!(
        send(&app, &key, "POST", "/api/tickets?z=2&a=1", first, 3)
            .await
            .0,
        200
    );
    let rev = st.ticket("p", 1).unwrap().unwrap().rev;
    let (code, h, doc) = send(&app, &key, "POST", "/api/tickets?a=1&z=2", normalized, 3).await;
    assert_eq!(code, 200);
    assert_eq!(h["x-tam-stale"], "1");
    assert_eq!(doc[0]["rev"], rev);
    let (code, h, doc) = send(&app, &key, "POST", "/api/tickets?z=2&a=1", "[]", 3).await;
    assert_eq!(code, 409);
    assert_eq!(h["x-tam-last-save"], "3");
    assert_eq!(doc["last_save"], 3);
    assert_eq!(
        send(&app, &key, "POST", "/api/tickets", normalized, 2)
            .await
            .0,
        409
    );
    let prefix = r#"[{"prefix":"p","color":"white","weight":0}]"#;
    assert_eq!(
        send(&app, &key, "POST", "/api/prefixes", prefix, 4).await.0,
        200
    );
    st.delete_prefix("p").unwrap();
    let (code, h, doc) = send(&app, &key, "POST", "/api/prefixes", prefix, 4).await;
    assert_eq!(code, 409);
    assert_eq!(h["x-tam-stale"], "1");
    assert_eq!(doc["detail"], "a row of this save is gone from the server");
    assert!(st.list_prefixes().unwrap().is_empty());
}
#[tokio::test]
async fn delete_repeat_is_done_even_when_original_missing() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("test").unwrap().auth_key;
    let app = router(ServerState::new(
        st.clone(),
        Password::load(dir.path(), "secret").unwrap(),
    ));
    assert_eq!(
        send(&app, &key, "DELETE", "/api/prefixes?p=absent", "", 1)
            .await
            .0,
        404
    );
    let (code, h, doc) = send(&app, &key, "DELETE", "/api/prefixes?p=absent", "", 1).await;
    assert_eq!(code, 200);
    assert_eq!(h["x-tam-stale"], "1");
    assert_eq!(doc["prefix"], "absent");
}

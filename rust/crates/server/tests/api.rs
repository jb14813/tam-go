use axum::{
    body::{Body, to_bytes},
    http::Request,
};
use r_tam_core::{schema::Role, store::Store};
use r_tam_server::{ServerState, auth::Password, router};
use tower::ServiceExt;
#[tokio::test]
async fn roots_auth_and_route_errors() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("server.db"), Role::Server).unwrap();
    let state = ServerState::new(st, Password::load(dir.path(), "secret").unwrap());
    let app = router(state);
    for path in ["/api", "/api/"] {
        let response = app
            .clone()
            .oneshot(Request::builder().uri(path).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(response.status(), 200);
        let doc: serde_json::Value =
            serde_json::from_slice(&to_bytes(response.into_body(), 10000).await.unwrap()).unwrap();
        assert_eq!(doc["whoami"], "TAM Server");
        assert_eq!(doc["authenticated"], false);
    }
    for (method, path, status) in [
        ("GET", "/api/tickets", 401),
        ("GET", "/api/missing", 404),
        ("PUT", "/api/tickets", 405),
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(path)
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), status);
        let doc: serde_json::Value =
            serde_json::from_slice(&to_bytes(response.into_body(), 10000).await.unwrap()).unwrap();
        assert!(doc["detail"].is_string());
    }
}

async fn request(
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
                .header("tam-pw", "secret")
                .header("content-type", "application/json")
                .header("x-tam-client-name", "test-client")
                .header("x-tam-save", save.to_string())
                .body(Body::from(body.to_owned()))
                .unwrap(),
        )
        .await
        .unwrap();
    let code = r.status();
    let headers = r.headers().clone();
    let data = to_bytes(r.into_body(), 1000000).await.unwrap();
    (code, headers, serde_json::from_slice(&data).unwrap())
}
#[tokio::test]
async fn every_data_route_and_field_ownership() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("buyer").unwrap().auth_key;
    let app = router(ServerState::new(
        st.clone(),
        Password::load(dir.path(), "secret").unwrap(),
    ));
    for path in [
        "/api/prefixes",
        "/api/tickets",
        "/api/tickets/Red",
        "/api/tickets/Red/1",
        "/api/tickets/Red/3/0",
        "/api/baskets",
        "/api/baskets/Red",
        "/api/baskets/Red/1",
        "/api/baskets/Red/3/0",
        "/api/drawing",
        "/api/drawing/Red",
        "/api/drawing/Red/1",
        "/api/drawing/Red/3/0",
        "/api/reports/byname/Red",
        "/api/reports/bybasket/Red",
        "/api/search/tickets",
    ] {
        let (code, _, doc) = request(&app, &key, "GET", path, "", 1).await;
        assert_eq!(code, 200, "{path}: {doc}");
        assert_eq!(doc, serde_json::json!([]), "{path}");
    }
    let prefix = r#"[{"prefix":"Red","color":"red","weight":1}]"#;
    assert_eq!(
        request(&app, &key, "POST", "/api/prefixes", prefix, 1)
            .await
            .0,
        200
    );
    let ticket = r#"[{"prefix":"Red","t_id":1,"first_name":"Alice","last_name":"Buyer","phone_number":"%_","pref":"phone"}]"#;
    assert_eq!(
        request(&app, &key, "POST", "/api/tickets", ticket, 2)
            .await
            .0,
        200
    );
    let basket =
        r#"[{"prefix":"Red","b_id":1,"description":"Prize","donors":"Donor","winning_ticket":99}]"#;
    let (_, _, doc) = request(&app, &key, "POST", "/api/baskets", basket, 3).await;
    assert_eq!(doc[0]["winning_ticket"], 99);
    let draw = r#"[{"prefix":"Red","b_id":1,"description":"ignored","donors":"ignored","winning_ticket":1}]"#;
    let (_, _, doc) = request(&app, &key, "POST", "/api/drawing", draw, 4).await;
    assert_eq!(doc[0]["description"], "Prize");
    assert_eq!(doc[0]["winning_ticket"], 1);
    for path in [
        "/api/tickets/Red/1",
        "/api/tickets/Red/3/0",
        "/api/baskets/Red/1",
        "/api/drawing/Red/1",
        "/api/reports/byname/Red",
        "/api/reports/bybasket/Red",
    ] {
        let (code, _, doc) = request(&app, &key, "GET", path, "", 1).await;
        assert_eq!(code, 200);
        assert_eq!(doc.as_array().unwrap().len(), 1, "{path}");
    }
    let (_, _, doc) = request(
        &app,
        &key,
        "GET",
        "/api/search/tickets?phone_number=%25_",
        "",
        1,
    )
    .await;
    assert_eq!(doc.as_array().unwrap().len(), 1);
    let (_, _, doc) = request(
        &app,
        &key,
        "GET",
        "/api/search/tickets?phone_number=absent",
        "",
        1,
    )
    .await;
    assert_eq!(doc, serde_json::json!([]));
    assert_eq!(
        request(&app, &key, "GET", "/api/reports/counts", "", 1)
            .await
            .0,
        200
    );
    assert_eq!(
        request(&app, &key, "POST", "/api/search/tickets", ticket, 5)
            .await
            .0,
        200
    );
    let (_, _, bf) = request(&app, &key, "GET", "/api/backuprestore", "", 1).await;
    assert!(bf["event"].is_string());
    assert_eq!(bf["tickets"].as_array().unwrap().len(), 1);
    assert_eq!(
        request(&app, &key, "POST", "/api/backuprestore", &bf.to_string(), 0)
            .await
            .0,
        200
    );
    let (code, _, doc) = request(
        &app,
        &key,
        "POST",
        "/api/auth",
        r#"{"description":"second"}"#,
        0,
    )
    .await;
    assert_eq!(code, 200);
    let second = doc["auth_key"].as_str().unwrap();
    assert_eq!(
        request(&app, &key, "GET", "/api/auth", "", 0)
            .await
            .2
            .as_array()
            .unwrap()
            .len(),
        2
    );
    assert_eq!(
        request(
            &app,
            &key,
            "DELETE",
            &format!("/api/auth?key_to_del={second}"),
            "",
            0
        )
        .await
        .0,
        200
    );
    assert_eq!(
        request(&app, &key, "DELETE", "/api/prefixes?p=Red", "", 6)
            .await
            .0,
        200
    );
    assert_eq!(st.counts().unwrap(), (0, 1, 1));
}
#[tokio::test]
async fn invalid_json_numbers_order_and_atomic_restore() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("test").unwrap().auth_key;
    let app = router(ServerState::new(
        st.clone(),
        Password::load(dir.path(), "secret").unwrap(),
    ));
    for body in [
        "[] {}",
        r#"[{"prefix":"p","t_id":"1"}]"#,
        r#"[{"prefix":"p","t_id":1.1}]"#,
        r#"[{"prefix":"p","t_id":null}]"#,
        r#"[{"prefix":"p"}]"#,
        r#"[{"prefix":"p","t_id":9007199254740992}]"#,
        r#"[{"prefix":"p","t_id":1,"rev":-1}]"#,
    ] {
        assert_eq!(
            request(&app, &key, "POST", "/api/tickets", body, 1).await.0,
            400,
            "{body}"
        );
    }
    assert_eq!(st.counts().unwrap(), (0, 0, 0));
    assert_eq!(
        request(&app, &key, "POST", "/api/tickets", "[]", 0).await.0,
        400
    );
    assert_eq!(
        request(&app, &key, "POST", "/api/tickets", "null", 1)
            .await
            .2,
        serde_json::json!([])
    );
    let bad = r#"{"prefixes":[{"prefix":"p","color":"white","weight":0}],"tickets":[{"prefix":"p","t_id":-1}],"baskets":[]}"#;
    assert_eq!(
        request(&app, &key, "POST", "/api/backuprestore", bad, 0)
            .await
            .0,
        400
    );
    assert!(st.list_prefixes().unwrap().is_empty());
    for path in [
        "/api/tickets/p/no",
        "/api/tickets/p/-1",
        "/api/baskets/p/9007199254740992",
        "/api/drawing/p/0/no",
    ] {
        assert_eq!(request(&app, &key, "GET", path, "", 1).await.0, 400);
    }
    let r = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/tickets")
                .header("tam-key", key)
                .header("content-type", "text/plain")
                .body(Body::from("[]"))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 400);
}
#[tokio::test]
async fn newer_merge_rejects_different_event_and_retains_tombstone() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("test").unwrap().auth_key;
    let app = router(ServerState::new(
        st.clone(),
        Password::load(dir.path(), "secret").unwrap(),
    ));
    request(
        &app,
        &key,
        "POST",
        "/api/prefixes",
        r#"[{"prefix":"p","color":"white","weight":0}]"#,
        1,
    )
    .await;
    let original = st.export().unwrap();
    let mut other = original.clone();
    other.event = "different".into();
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/backuprestore")
                .header("tam-key", &key)
                .header("content-type", "application/json")
                .header("x-tam-merge", "newer")
                .body(Body::from(serde_json::to_vec(&other).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 409);
    assert_eq!(st.export().unwrap(), original);
    request(&app, &key, "DELETE", "/api/prefixes?p=p", "", 2).await;
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/backuprestore")
                .header("tam-key", &key)
                .header("content-type", "application/json")
                .header("x-tam-merge", "newer")
                .body(Body::from(serde_json::to_vec(&original).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 200);
    assert!(st.list_prefixes().unwrap().is_empty());
    assert_eq!(
        request(
            &app,
            &key,
            "POST",
            "/api/backuprestore",
            &serde_json::to_string(&original).unwrap(),
            0
        )
        .await
        .0,
        200
    );
    assert_eq!(st.list_prefixes().unwrap().len(), 1);
}

#[tokio::test]
async fn url_encoded_prefix_paths_are_decoded() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("test").unwrap().auth_key;
    let app = router(ServerState::new(
        st,
        Password::load(dir.path(), "secret").unwrap(),
    ));
    assert_eq!(
        request(
            &app,
            &key,
            "POST",
            "/api/tickets",
            r#"[{"prefix":"Red + blue","t_id":1}]"#,
            1
        )
        .await
        .0,
        200
    );
    let (code, _, doc) = request(&app, &key, "GET", "/api/tickets/Red%20%2B%20blue/1", "", 0).await;
    assert_eq!(code, 200);
    assert_eq!(doc.as_array().unwrap().len(), 1);
}
#[tokio::test]
async fn method_allow_header_and_clean_path_redirect() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let app = router(ServerState::new(
        st,
        Password::load(dir.path(), "secret").unwrap(),
    ));
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .method("PUT")
                .uri("/api/tickets")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 405);
    assert_eq!(r.headers().get("allow").unwrap(), "GET, HEAD, POST");
    let r = app
        .oneshot(
            Request::builder()
                .uri("/api//tickets?q=1")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 301);
    assert_eq!(r.headers()["location"], "/api/tickets?q=1");
}
#[tokio::test]
async fn key_description_go_fields_ignore_unknown_and_null() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let app = router(ServerState::new(
        st,
        Password::load(dir.path(), "secret").unwrap(),
    ));
    let (code, _, doc) = request(
        &app,
        "",
        "POST",
        "/api/auth",
        r#"{"DESCRIPTION":"First","description":null,"auth_key":123}"#,
        0,
    )
    .await;
    assert_eq!(code, 200);
    assert_eq!(doc["description"], "First");
}
#[tokio::test]
async fn content_type_parameters_and_body_cap() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("test").unwrap().auth_key;
    let app = router(ServerState::new(
        st.clone(),
        Password::load(dir.path(), "secret").unwrap(),
    ));
    for content_type in [
        "application/json; broken",
        "application/json; charset",
        "text/json",
        "text/plain",
    ] {
        let r = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/tickets")
                    .header("tam-key", &key)
                    .header("content-type", content_type)
                    .body(Body::from("[]"))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r.status(), 400, "{content_type}");
    }
    let r = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/tickets")
                .header("tam-key", key)
                .header("content-type", "application/json")
                .body(Body::from(vec![b' '; 64 * 1024 * 1024 + 1]))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 413);
    assert!(st.all_tickets().unwrap().is_empty());
}
#[tokio::test]
async fn route_and_auth_rejections_precede_json_collection() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("test").unwrap().auth_key;
    let app = router(ServerState::new(
        st,
        Password::load(dir.path(), "secret").unwrap(),
    ));
    let huge = axum::body::Bytes::from(vec![b'x'; 64 * 1024 * 1024 + 1]);
    for (method, path, key_value, status) in [
        ("POST", "/api/tickets", "bad", 401),
        ("POST", "/api/unknown", &key, 404),
        ("PUT", "/api/tickets", &key, 405),
        ("GET", "/api/tickets", &key, 200),
        ("DELETE", "/api/auth", &key, 401),
    ] {
        let r = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(path)
                    .header("tam-key", key_value)
                    .header("content-type", "application/json")
                    .body(Body::from(huge.clone()))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r.status(), status, "{method} {path}");
    }
}

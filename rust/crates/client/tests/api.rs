use axum::{
    body::Body,
    http::{Request, StatusCode},
};
use http_body_util::BodyExt;
use r_tam_client::{ClientState, router};
use r_tam_core::{schema::Role, store::Store};
use serde_json::{Value, json};
use tower::ServiceExt;
fn state(dir: &std::path::Path) -> ClientState {
    ClientState::new(
        Store::open(&dir.join("tam-local.db"), Role::Client).unwrap(),
        &dir.join("settings.json"),
    )
}
async fn call(
    s: &ClientState,
    method: &str,
    path: &str,
    body: Value,
) -> (StatusCode, Value, axum::http::HeaderMap) {
    let r = router(s.clone())
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header("content-type", "application/json")
                .body(if method == "GET" {
                    Body::empty()
                } else {
                    Body::from(body.to_string())
                })
                .unwrap(),
        )
        .await
        .unwrap();
    let status = r.status();
    let headers = r.headers().clone();
    let bytes = r.into_body().collect().await.unwrap().to_bytes();
    (
        status,
        serde_json::from_slice(&bytes).unwrap_or(Value::Null),
        headers,
    )
}
#[tokio::test]
async fn settings_reject_invalid_patches_without_changing_live_primary_or_recovery() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    assert_eq!(
        call(
            &s,
            "POST",
            "/api/settings",
            json!({"venue_name":"Saved venue"})
        )
        .await
        .0,
        StatusCode::OK
    );
    let before = s.settings.get();
    let primary = std::fs::read(dir.path().join("settings.json")).unwrap();
    let recovery = std::fs::read(dir.path().join("settings.json.bak")).unwrap();
    for (patch, detail) in [
        (
            json!({"remote_port":"0"}),
            "remote_port must be a number between 1 and 65535",
        ),
        (
            json!({"default_pref":"PHONE"}),
            "default_pref must be CALL or TEXT",
        ),
        (
            json!({"remote_fingerprint":"bad-pin"}),
            "remote_fingerprint must be a SHA-256 fingerprint in hex, or empty",
        ),
    ] {
        let (status, body, _) = call(&s, "POST", "/api/settings", patch.clone()).await;
        assert_eq!(status, StatusCode::BAD_REQUEST, "patch: {patch}");
        assert_eq!(body, json!({"detail":detail}));
        assert_eq!(s.settings.get(), before);
        assert_eq!(
            call(&s, "GET", "/api/settings", Value::Null).await.1,
            serde_json::to_value(&before).unwrap()
        );
        assert_eq!(
            std::fs::read(dir.path().join("settings.json")).unwrap(),
            primary
        );
        assert_eq!(
            std::fs::read(dir.path().join("settings.json.bak")).unwrap(),
            recovery
        );
    }
}
#[tokio::test]
async fn settings_normalize_merged_values_in_response_and_persistence() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    let (status, body, _) = call(
        &s,
        "POST",
        "/api/settings",
        json!({
            "remote_server":"  example.test  ", "remote_key":"  key-value\t",
            "remote_port":" 8443 ", "remote_tls":true, "default_pref":" TEXT ",
            "venue_name":"  Example Venue  ", "remote_name":"  Paired Server  ",
            "remote_fingerprint":format!(" {} ", ["AB";32].join(":"))
        }),
    )
    .await;
    let expected = json!({
        "remote_server":"example.test", "remote_key":"key-value",
        "remote_port":"8443", "remote_tls":true, "default_pref":"TEXT",
        "venue_name":"Example Venue", "remote_name":"Paired Server",
        "remote_fingerprint":"abababababababababababababababababababababababababababababababab",
        "disable_attrib":false
    });
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body, expected);
    assert_eq!(
        call(&s, "GET", "/api/settings", Value::Null).await.1,
        expected
    );
    assert_eq!(
        serde_json::from_slice::<Value>(&std::fs::read(dir.path().join("settings.json")).unwrap())
            .unwrap(),
        expected
    );
    assert_eq!(
        serde_json::from_slice::<Value>(
            &std::fs::read(dir.path().join("settings.json.bak")).unwrap()
        )
        .unwrap(),
        expected
    );
    // A later partial patch retains the normalized pairing fields.
    assert_eq!(
        call(
            &s,
            "POST",
            "/api/settings",
            json!({"venue_name":" Updated "})
        )
        .await
        .1["remote_key"],
        "key-value"
    );
    assert_eq!(s.settings.get().venue_name, "Updated");
}
#[tokio::test]
async fn standalone_forms_placeholders_ranges_reports_and_backups() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    assert_eq!(
        call(&s, "GET", "/api", Value::Null).await.1,
        json!({"whoami":"TAM Client"})
    );
    assert_eq!(
        call(&s, "POST", "/api/settings", json!({"default_pref":"TEXT"}))
            .await
            .0,
        StatusCode::OK
    );
    let t = call(&s, "GET", "/api/tickets/A/1", Value::Null).await.1;
    assert_eq!(
        t,
        json!({"prefix":"A","t_id":1,"first_name":"","last_name":"","phone_number":"","pref":"TEXT"})
    );
    assert_eq!(
        call(
            &s,
            "POST",
            "/api/prefixes",
            json!([{"prefix":"A","color":"blue","weight":0}])
        )
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(call(&s,"POST","/api/tickets",json!([{"prefix":"A","t_id":2,"first_name":"Ada","last_name":"Lovelace","phone_number":"123","pref":"CALL"}])).await.0,StatusCode::OK);
    assert_eq!(
        call(
            &s,
            "POST",
            "/api/baskets",
            json!([{"prefix":"A","b_id":1,"description":"Prize","donors":"Jane"}])
        )
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(
        call(
            &s,
            "POST",
            "/api/drawing",
            json!([{"prefix":"A","b_id":1,"winning_ticket":2}])
        )
        .await
        .0,
        StatusCode::OK
    );
    let r = call(&s, "GET", "/api/tickets/A/3/1", Value::Null).await.1;
    assert_eq!(r.as_array().unwrap().len(), 3);
    assert_eq!(r[1]["first_name"], "Ada");
    assert_eq!(
        call(&s, "GET", "/api/tickets/A/0/500", Value::Null)
            .await
            .1
            .as_array()
            .unwrap()
            .len(),
        301
    );
    assert_eq!(
        call(&s, "GET", "/api/drawing/A/1", Value::Null).await.1["last_name"],
        "Lovelace"
    );
    assert_eq!(
        call(&s, "GET", "/api/search/tickets?first_name=ad", Value::Null)
            .await
            .1
            .as_array()
            .unwrap()
            .len(),
        1
    );
    assert_eq!(
        call(
            &s,
            "GET",
            "/api/search/tickets?first_name=missing",
            Value::Null
        )
        .await
        .1
        .as_array()
        .unwrap()
        .len(),
        0
    );
    assert_eq!(
        call(
            &s,
            "POST",
            "/api/search/tickets",
            json!([{"prefix":"A","t_id":2,"first_name":"Augusta"}])
        )
        .await
        .0,
        StatusCode::OK
    );
    assert_eq!(
        call(&s, "GET", "/api/reports/byname/A", Value::Null)
            .await
            .1[0]["description"],
        "Prize"
    );
    assert_eq!(
        call(&s, "GET", "/api/reports/bybasket/A", Value::Null)
            .await
            .1[0]["first_name"],
        "Augusta"
    );
    assert_eq!(
        call(&s, "GET", "/api/reports/counts", Value::Null).await.1[0]["total_buys"],
        1
    );
    let backup = call(&s, "GET", "/api/backuprestore/local", Value::Null)
        .await
        .1;
    assert_eq!(backup["tickets"][0]["t_id"], 2);
    assert_eq!(
        call(&s, "POST", "/api/backuprestore/local", backup).await.0,
        StatusCode::OK
    );
    assert_eq!(
        call(&s, "GET", "/api/status", Value::Null).await.1,
        json!({"mode":"standalone"})
    );
}
#[tokio::test]
async fn restore_with_pending_intent_refuses_without_mutation() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    s.store
        .journal_online(
            "test",
            "POST",
            "/api/tickets",
            br#"[{"prefix":"A","t_id":1,"first_name":"waiting"}]"#,
        )
        .unwrap();
    for path in [
        "/api/backuprestore/local",
        "/api/backuprestore/remote",
        "/api/backuprestore/push/tickets",
    ] {
        let r = call(
            &s,
            "POST",
            path,
            json!({"prefixes":[],"tickets":[],"baskets":[]}),
        )
        .await;
        assert_eq!(r.0, StatusCode::CONFLICT);
        assert!(r.1["detail"].as_str().unwrap().contains("waiting"));
    }
    assert!(s.store.all_tickets().unwrap().is_empty());
    assert_eq!(s.store.pending().unwrap().len(), 1);
}
#[tokio::test]
async fn same_site_rejection_precedes_body_collection_and_preserves_rows() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    for site in ["cross-site", "same-site", "unknown"] {
        let response = router(s.clone())
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/api/tickets")
                    .header("sec-fetch-site", site)
                    .header("content-type", "application/json")
                    .body(Body::from("not json"))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::FORBIDDEN, "{site}");
    }
    assert!(s.store.all_tickets().unwrap().is_empty());
}
#[tokio::test]
async fn embedded_spa_routes_dotted_prefixes_missing_assets_and_web_redirect() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    for path in ["/web/", "/web/tickets/5.00"] {
        let r = router(s.clone())
            .oneshot(Request::builder().uri(path).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(r.status(), StatusCode::OK);
        assert_eq!(r.headers()["content-type"], "text/html; charset=utf-8");
    }
    let r = router(s.clone())
        .oneshot(
            Request::builder()
                .uri("/web/_app/immutable/missing.js")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), StatusCode::NOT_FOUND);
    let r = router(s)
        .oneshot(Request::builder().uri("/web").body(Body::empty()).unwrap())
        .await
        .unwrap();
    assert_eq!(r.status(), StatusCode::MOVED_PERMANENTLY);
    assert_eq!(r.headers()["location"], "/web/");
}
#[tokio::test]
async fn remote_status_defaults_name_state_and_second_precision_last_ok() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    s.settings
        .update(|mut c| {
            c.remote_server = "localhost".into();
            Ok(c)
        })
        .unwrap();
    let status = call(&s, "GET", "/api/status", Value::Null).await.1;
    assert_eq!(status["state"], "reconnecting");
    assert_eq!(status["server_name"], "localhost");
    s.success();
    let status = call(&s, "GET", "/api/status", Value::Null).await.1;
    assert_eq!(status["last_ok"].as_str().unwrap().len(), 20);
    s.failure("offline");
    let report = call(&s, "GET", "/api/reports/counts", Value::Null).await;
    assert_eq!(report.2["X-TAM-Copy"], "1");
}
#[tokio::test]
async fn route_method_and_clean_path_selection_precede_body_validation() {
    let dir = tempfile::tempdir().unwrap();
    let s = state(dir.path());
    for (path, expected) in [
        ("/api/status", StatusCode::METHOD_NOT_ALLOWED),
        ("/api/unknown", StatusCode::NOT_FOUND),
    ] {
        let r = router(s.clone())
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri(path)
                    .body(Body::from("not JSON"))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r.status(), expected);
    }
    let r = router(s.clone())
        .oneshot(
            Request::builder()
                .method("HEAD")
                .uri("/api/settings")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), StatusCode::OK);
    assert!(r.into_body().collect().await.unwrap().to_bytes().is_empty());
    let r = router(s)
        .oneshot(
            Request::builder()
                .uri("/api//tickets?first_name=A")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), StatusCode::MOVED_PERMANENTLY);
    assert_eq!(r.headers()["location"], "/api/tickets?first_name=A");
}

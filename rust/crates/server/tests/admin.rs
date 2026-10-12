use axum::{
    body::{Body, to_bytes},
    http::Request,
};
use r_tam_core::{schema::Role, store::Store};
use r_tam_server::{ServerState, auth::Password, router};
use tower::ServiceExt;
#[tokio::test]
async fn setup_token_cookie_and_security() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let app = router(ServerState::new(
        st,
        Password::load(dir.path(), "").unwrap(),
    ));
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/admin/")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 200);
    assert_eq!(r.headers()["cache-control"], "no-store");
    let cookie = r.headers()["set-cookie"].to_str().unwrap().to_string();
    assert!(
        cookie.contains("HttpOnly")
            && cookie.contains("SameSite=Strict")
            && cookie.contains("Path=/admin")
    );
    let html = String::from_utf8(to_bytes(r.into_body(), 100000).await.unwrap().to_vec()).unwrap();
    assert!(html.contains("Welcome to R-TAM Server"));
    let token = html
        .split("name=\"csrf\" value=\"")
        .nth(1)
        .unwrap()
        .split('"')
        .next()
        .unwrap();
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/admin/setup")
                .header("cookie", cookie.split(';').next().unwrap())
                .header("content-type", "application/x-www-form-urlencoded")
                .body(Body::from(format!(
                    "csrf={token}&password=secret&confirm=secret"
                )))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 303);
    assert_eq!(r.headers()["location"], "/admin/status");
    assert!(dir.path().join("server.json").exists());
}

#[test]
fn limiter_caps_concurrent_attempts_and_recovers() {
    use std::sync::{
        Arc,
        atomic::{AtomicI64, AtomicUsize, Ordering},
    };
    let now = Arc::new(AtomicI64::new(100));
    let time = now.clone();
    let limiter = Arc::new(r_tam_server::auth::Limiter::new(Arc::new(move || {
        time.load(Ordering::SeqCst)
    })));
    let calls = Arc::new(AtomicUsize::new(0));
    std::thread::scope(|scope| {
        for _ in 0..20 {
            let l = limiter.clone();
            let c = calls.clone();
            scope.spawn(move || {
                l.check("same", || {
                    c.fetch_add(1, Ordering::SeqCst);
                    false
                });
            });
        }
    });
    assert_eq!(calls.load(Ordering::SeqCst), 5);
    assert_eq!(limiter.check("same", || true), (false, 30));
    assert_eq!(limiter.check("other", || true), (true, 0));
    now.store(130, Ordering::SeqCst);
    assert_eq!(limiter.check("same", || true), (true, 0));
}
#[test]
fn password_hash_and_limits() {
    let dir = tempfile::tempdir().unwrap();
    let pw = Password::load(dir.path(), "environment").unwrap();
    assert!(pw.from_env());
    assert!(pw.check("environment"));
    assert!(pw.set("").is_err());
    assert!(pw.set(&"é".repeat(37)).is_err());
    pw.set(&"x".repeat(72)).unwrap();
    let doc: serde_json::Value =
        serde_json::from_slice(&std::fs::read(dir.path().join("server.json")).unwrap()).unwrap();
    assert!(
        doc["password_hash"]
            .as_str()
            .unwrap()
            .starts_with("$2a$10$")
    );
    let reload = Password::load(dir.path(), "environment").unwrap();
    assert!(reload.check(&"x".repeat(72)));
    assert!(!reload.check("environment"));
    assert!(!reload.from_env());
    std::fs::write(dir.path().join("server.json"), r#"{"password_hash":123}"#).unwrap();
    assert!(Password::load(dir.path(), "fallback").is_err());
}
async fn admin_call(
    app: &axum::Router,
    path: &str,
    cookie: &str,
    form: Option<String>,
    accept: &str,
) -> (axum::http::StatusCode, axum::http::HeaderMap, String) {
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .method(if form.is_some() { "POST" } else { "GET" })
                .uri(path)
                .header("cookie", cookie)
                .header("content-type", "application/x-www-form-urlencoded")
                .header("accept", accept)
                .body(form.map_or(Body::empty(), Body::from))
                .unwrap(),
        )
        .await
        .unwrap();
    let code = r.status();
    let h = r.headers().clone();
    let text =
        String::from_utf8(to_bytes(r.into_body(), 10000000).await.unwrap().to_vec()).unwrap();
    (code, h, text)
}
fn csrf(html: &str) -> String {
    html.split("name=\"csrf\" value=\"")
        .nth(1)
        .unwrap()
        .split('"')
        .next()
        .unwrap()
        .into()
}
async fn login(app: &axum::Router) -> (String, String) {
    let (_, h, html) = admin_call(app, "/admin/", "", None, "").await;
    let visitor = h["set-cookie"].to_str().unwrap().split(';').next().unwrap();
    let (code, h, _) = admin_call(
        app,
        "/admin/login",
        visitor,
        Some(format!("csrf={}&password=secret", csrf(&html))),
        "",
    )
    .await;
    assert_eq!(code, 303);
    let cookie = h["set-cookie"]
        .to_str()
        .unwrap()
        .split(';')
        .next()
        .unwrap()
        .to_owned();
    let (_, _, html) = admin_call(app, "/admin/keys", &cookie, None, "").await;
    (cookie, csrf(&html))
}
#[tokio::test]
async fn admin_forms_sessions_status_and_shared_limiter() {
    use std::sync::{
        Arc,
        atomic::{AtomicI64, Ordering},
    };
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("<client>").unwrap().auth_key;
    let now = Arc::new(AtomicI64::new(1000));
    let time = now.clone();
    let mut state = ServerState::with_clock(
        st.clone(),
        Password::load(dir.path(), "secret").unwrap(),
        Arc::new(move || time.load(Ordering::SeqCst)),
    );
    state.tls = true;
    let app = router(state);
    assert_eq!(
        admin_call(&app, "/admin/status", "", None, "application/json")
            .await
            .0,
        401
    );
    assert_eq!(admin_call(&app, "/admin/status", "", None, "").await.0, 303);
    let (cookie, token) = login(&app).await;
    let (other, _) = login(&app).await;
    assert_eq!(
        admin_call(
            &app,
            "/admin/keys",
            &cookie,
            Some("description=bad".into()),
            ""
        )
        .await
        .0,
        403
    );
    let (code, _, html) = admin_call(
        &app,
        "/admin/keys",
        &cookie,
        Some(format!("csrf={token}&description=%3Cscript%3E")),
        "",
    )
    .await;
    assert_eq!(code, 200);
    assert!(html.contains("&lt;script&gt;"));
    assert!(!html.contains("<script>"));
    assert_eq!(st.list_keys().unwrap().len(), 2);
    let (code, _, html) = admin_call(
        &app,
        "/admin/keys/delete",
        &cookie,
        Some(format!("csrf={token}&key={key}")),
        "",
    )
    .await;
    assert_eq!(code, 200);
    assert!(html.contains("Yes, delete it"));
    assert!(st.key_exists(&key).unwrap());
    assert_eq!(
        admin_call(
            &app,
            "/admin/keys/delete",
            &cookie,
            Some(format!("csrf={token}&key={key}&confirm=yes")),
            ""
        )
        .await
        .0,
        303
    );
    assert!(!st.key_exists(&key).unwrap());
    let key = st.create_key("live").unwrap().auth_key;
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api")
                .header("tam-key", &key)
                .header("x-tam-client", "TAM Client/Go")
                .header("x-tam-pending", "0")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 200);
    let (code, _, doc) = admin_call(&app, "/admin/status", &cookie, None, "application/json").await;
    assert_eq!(code, 200);
    let doc: serde_json::Value = serde_json::from_str(&doc).unwrap();
    let live = doc["clients"]
        .as_array()
        .unwrap()
        .iter()
        .find(|v| v["name"] == "live")
        .unwrap();
    assert_eq!(live["queued"], 0);
    assert_eq!(live["state"], "connected");
    assert_eq!(live["program"], "TAM Client/Go");
    assert!(
        st.list_keys()
            .unwrap()
            .iter()
            .find(|k| k.auth_key == key)
            .unwrap()
            .last_seen
            .ends_with('Z')
    );
    assert_eq!(
        admin_call(&app, "/admin/backup/download", &cookie, None, "")
            .await
            .1["content-disposition"],
        "attachment; filename=\"tam-backup.json\""
    );
    let (_, h, _) = admin_call(&app, "/admin/", "", None, "").await;
    let visitor = h["set-cookie"]
        .to_str()
        .unwrap()
        .split(';')
        .next()
        .unwrap()
        .to_owned();
    assert!(h["set-cookie"].to_str().unwrap().contains("Secure"));
    let (_, _, html) = admin_call(&app, "/admin/", &visitor, None, "").await;
    let anon = csrf(&html);
    for _ in 0..3 {
        assert_eq!(
            admin_call(
                &app,
                "/admin/login",
                &visitor,
                Some(format!("csrf={anon}&password=wrong")),
                ""
            )
            .await
            .0,
            401
        )
    }
    for _ in 0..2 {
        let r = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api/auth")
                    .header("tam-pw", "wrong")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r.status(), 401)
    }
    let r = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/api/auth")
                .header("tam-pw", "secret")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 429);
    assert_eq!(r.headers()["retry-after"], "30");
    now.store(1030, Ordering::SeqCst);
    assert_eq!(
        admin_call(
            &app,
            "/admin/password",
            &cookie,
            Some(format!(
                "csrf={token}&current=secret&password=new&confirm=new"
            )),
            ""
        )
        .await
        .0,
        303
    );
    assert_eq!(
        admin_call(&app, "/admin/status", &other, None, "application/json")
            .await
            .0,
        401
    );
    assert_eq!(
        admin_call(&app, "/admin/status", &cookie, None, "application/json")
            .await
            .0,
        200
    );
    now.store(1000 + 43200, Ordering::SeqCst);
    assert_eq!(
        admin_call(&app, "/admin/status", &cookie, None, "application/json")
            .await
            .0,
        401
    );
    assert_eq!(
        admin_call(
            &app,
            "/admin/login",
            &visitor,
            Some(format!("csrf={anon}&password=new")),
            ""
        )
        .await
        .0,
        403
    );
}
#[tokio::test]
async fn multipart_restore_confirmation_and_large_valid_upload() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let app = router(ServerState::new(
        st.clone(),
        Password::load(dir.path(), "secret").unwrap(),
    ));
    let (cookie, token) = login(&app).await;
    assert_eq!(
        admin_call(
            &app,
            "/admin/backup/restore",
            &cookie,
            Some(format!("csrf={token}")),
            ""
        )
        .await
        .0,
        400
    );
    assert!(st.list_prefixes().unwrap().is_empty());
    let bf = format!(
        r#"{{"prefixes":[],"baskets":[{{"prefix":"p","b_id":1,"description":"{}"}}],"tickets":[]}}"#,
        "x".repeat(2_100_000)
    );
    let multipart = format!(
        "--boundary\r\nContent-Disposition: form-data; name=\"csrf\"\r\n\r\n{token}\r\n--boundary\r\nContent-Disposition: form-data; name=\"confirm\"\r\n\r\nyes\r\n--boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"backup.json\"\r\nContent-Type: application/json\r\n\r\n{bf}\r\n--boundary--\r\n"
    );
    let r = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/admin/backup/restore")
                .header("cookie", cookie)
                .header("content-type", "multipart/form-data; boundary=boundary")
                .body(Body::from(multipart))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 303);
    assert_eq!(
        st.basket("p", 1).unwrap().unwrap().description.len(),
        2_100_000
    );
}
#[tokio::test]
async fn admin_preflight_rejects_before_collecting_oversized_body() {
    let dir = tempfile::tempdir().unwrap();
    let app = router(ServerState::new(
        Store::open(&dir.path().join("db"), Role::Server).unwrap(),
        Password::load(dir.path(), "secret").unwrap(),
    ));
    for (path, method, status) in [
        ("/admin/backup/restore", "POST", 401),
        ("/admin/missing", "POST", 404),
        ("/admin/status", "POST", 405),
        ("/admin/setup", "POST", 303),
    ] {
        let r = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(path)
                    .method(method)
                    .header("accept", "application/json")
                    .body(Body::from(vec![0; 64 * 1024 * 1024 + 1]))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r.status().as_u16(), status, "{method} {path}");
        assert_eq!(r.headers()["x-content-type-options"], "nosniff");
    }
}
struct PendingBody(std::sync::Arc<std::sync::atomic::AtomicUsize>);
impl http_body::Body for PendingBody {
    type Data = axum::body::Bytes;
    type Error = std::convert::Infallible;
    fn poll_frame(
        self: std::pin::Pin<&mut Self>,
        _: &mut std::task::Context<'_>,
    ) -> std::task::Poll<Option<Result<http_body::Frame<Self::Data>, Self::Error>>> {
        self.0.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
        std::task::Poll::Pending
    }
}
#[tokio::test]
async fn admin_preflight_leaves_rejected_and_get_bodies_unread() {
    use std::{
        sync::{
            Arc,
            atomic::{AtomicUsize, Ordering},
        },
        time::Duration,
    };
    let dir = tempfile::tempdir().unwrap();
    let state = ServerState::new(
        Store::open(&dir.path().join("db"), Role::Server).unwrap(),
        Password::load(dir.path(), "secret").unwrap(),
    );
    let stop = state.shutdown.clone();
    let app = router(state);
    let (cookie, _) = login(&app).await;
    for (path, method, authenticated, accept, status) in [
        (
            "/admin/backup/restore",
            "POST",
            false,
            "application/json",
            401,
        ),
        ("/admin/missing", "POST", false, "", 404),
        ("/admin/status", "POST", false, "", 405),
        ("/admin/status", "GET", false, "application/json", 401),
        ("/admin/status", "GET", false, "", 303),
        ("/admin/status", "GET", true, "application/json", 200),
        ("/admin/status", "HEAD", true, "application/json", 200),
        ("/admin/setup", "POST", false, "", 303),
    ] {
        let polls = Arc::new(AtomicUsize::new(0));
        let r = tokio::time::timeout(
            Duration::from_secs(1),
            app.clone().oneshot(
                Request::builder()
                    .uri(path)
                    .method(method)
                    .header("accept", accept)
                    .header("cookie", if authenticated { &cookie } else { "" })
                    .body(Body::new(PendingBody(polls.clone())))
                    .unwrap(),
            ),
        )
        .await
        .expect("admin response waited for an unread body")
        .unwrap();
        assert_eq!(r.status().as_u16(), status, "{method} {path}");
        assert_eq!(polls.load(Ordering::SeqCst), 0, "{method} {path}");
        if status == 303 {
            assert_eq!(r.headers()["location"], "/admin/");
        }
    }
    stop.request();
    tokio::time::timeout(Duration::from_secs(1), stop.drain())
        .await
        .unwrap();
}
#[tokio::test]
async fn canceled_admin_form_body_releases_shutdown_activity() {
    use std::{
        sync::{
            Arc,
            atomic::{AtomicUsize, Ordering},
        },
        time::Duration,
    };
    let dir = tempfile::tempdir().unwrap();
    let state = ServerState::new(
        Store::open(&dir.path().join("db"), Role::Server).unwrap(),
        Password::load(dir.path(), "secret").unwrap(),
    );
    let stop = state.shutdown.clone();
    let app = router(state);
    let (cookie, _) = login(&app).await;
    let polls = Arc::new(AtomicUsize::new(0));
    let body = Body::new(PendingBody(polls.clone()));
    let request = tokio::spawn(
        app.oneshot(
            Request::builder()
                .uri("/admin/backup/restore")
                .method("POST")
                .header("cookie", cookie)
                .body(body)
                .unwrap(),
        ),
    );
    tokio::time::timeout(Duration::from_secs(1), async {
        while polls.load(Ordering::SeqCst) == 0 {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
    stop.request();
    assert!(
        tokio::time::timeout(Duration::from_millis(10), stop.drain())
            .await
            .is_err()
    );
    request.abort();
    assert!(request.await.unwrap_err().is_cancelled());
    tokio::time::timeout(Duration::from_secs(1), stop.drain())
        .await
        .unwrap();
}
#[test]
fn limiter_different_addresses_can_check_concurrently() {
    use std::sync::{Arc, mpsc};
    let limiter = Arc::new(r_tam_server::auth::Limiter::new(Arc::new(|| 100)));
    let (tx, rx) = mpsc::channel();
    let (tx_release, rx_release) = mpsc::channel();
    let l = limiter.clone();
    let first = std::thread::spawn(move || {
        l.check("slow", || {
            tx.send(()).unwrap();
            rx_release.recv().unwrap();
            false
        })
    });
    rx.recv().unwrap();
    let (tx, rx) = mpsc::channel();
    let l = limiter.clone();
    let other = std::thread::spawn(move || {
        tx.send(l.check("other", || true)).unwrap();
    });
    let result = rx.recv_timeout(std::time::Duration::from_millis(200));
    tx_release.send(()).unwrap();
    first.join().unwrap();
    other.join().unwrap();
    assert_eq!(result.unwrap(), (true, 0));
}

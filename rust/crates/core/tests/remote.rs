#![cfg(not(windows))]
use r_tam_core::{
    remote::Remote,
    tls::{certificate_files, fingerprint},
};
use std::time::Duration;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
async fn server(answer: &'static [u8]) -> (String, tokio::task::JoinHandle<String>) {
    let ln = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let base = format!("http://{}", ln.local_addr().unwrap());
    let task = tokio::spawn(async move {
        tokio::time::timeout(Duration::from_secs(2), async move {
            let (mut s, _) = ln.accept().await.unwrap();
            let mut all = Vec::new();
            loop {
                let mut b = [0; 1024];
                let n = s.read(&mut b).await.unwrap();
                assert!(n > 0, "peer closed before request headers");
                all.extend_from_slice(&b[..n]);
                if all.windows(4).any(|w| w == b"\r\n\r\n") {
                    break;
                }
            }
            s.write_all(answer).await.unwrap();
            String::from_utf8(all).unwrap()
        })
        .await
        .expect("loopback test peer did not connect within bound")
    });
    (base, task)
}
#[tokio::test]
async fn preserves_status_headers_and_order_identity() {
    let (base,task)=server(b"HTTP/1.1 409 Conflict\r\nContent-Length: 2\r\nX-TAM-Last-Save: 7\r\nConnection: close\r\n\r\n[]").await;
    let r = Remote::new(&base, "secret", false)
        .unwrap()
        .ordered_request(
            "POST",
            "/api/tickets",
            &r_tam_core::models::Order {
                client: "station".into(),
                save: 7,
            },
            2,
            Some(b"[]"),
        )
        .await
        .unwrap();
    assert_eq!(r.status, 409);
    assert_eq!(r.headers["x-tam-last-save"], "7");
    assert!(!r.ok());
    assert!(!r.retryable());
    let received = task.await.unwrap().to_lowercase();
    for h in [
        "tam-key: secret",
        "x-tam-client-name: station",
        "x-tam-save: 7",
        "x-tam-pending: 2",
        "x-tam-client: tam-client/",
    ] {
        assert!(received.contains(h), "{received}");
    }
}
#[tokio::test]
async fn pairing_key_creation_and_probe_reject_other_services() {
    let(base,task)=server(b"HTTP/1.1 200 OK\r\nContent-Length: 22\r\nConnection: close\r\n\r\n{\"whoami\":\"Other App\"}").await;
    assert!(
        Remote::new(&base, "", false)
            .unwrap()
            .probe()
            .await
            .is_err()
    );
    task.await.unwrap();
    let (base, task) = server(
        b"HTTP/1.1 200 OK\r\nContent-Length: 16\r\nConnection: close\r\n\r\n{\"auth_key\":\"K\"}",
    )
    .await;
    let response = Remote::new(&base, "", false)
        .unwrap()
        .create_key("password", "Station")
        .await
        .unwrap();
    assert_eq!(
        response.json::<serde_json::Value>().unwrap()["auth_key"],
        "K"
    );
    assert!(
        task.await
            .unwrap()
            .to_lowercase()
            .contains("tam-pw: password")
    );
}
#[tokio::test]
async fn bounded_discovery_response_is_rejected_before_decoding() {
    let (base, task) =
        server(b"HTTP/1.1 200 OK\r\nContent-Length: 9\r\nConnection: close\r\n\r\n123456789").await;
    assert!(
        Remote::new(&base, "", false)
            .unwrap()
            .with_response_limit(8)
            .get("/api")
            .await
            .is_err()
    );
    task.await.unwrap();
}
#[tokio::test]
async fn rejects_html_success_and_bounds_timeout() {
    let (base, task) =
        server(b"HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: 6\r\n\r\n<html>")
            .await;
    assert!(
        Remote::new(&base, "", false)
            .unwrap()
            .get("/api")
            .await
            .is_err()
    );
    task.await.unwrap();
    let ln = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let remote = Remote::new(&format!("http://{}", ln.local_addr().unwrap()), "", false)
        .unwrap()
        .with_timeout(Duration::from_millis(30));
    let task = tokio::spawn(async move {
        let _s = ln.accept().await.unwrap();
        tokio::time::sleep(Duration::from_secs(1)).await;
    });
    assert!(remote.get("/api").await.is_err());
    task.abort();
}
#[tokio::test]
async fn does_not_redirect_credentials_to_another_origin() {
    let target = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let ln = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let base = format!("http://{}", ln.local_addr().unwrap());
    let answer = format!(
        "HTTP/1.1 302 Found\r\nLocation: http://{}/api\r\nContent-Length: 0\r\n\r\n",
        target.local_addr().unwrap()
    );
    tokio::spawn(async move {
        let (mut s, _) = ln.accept().await.unwrap();
        let mut b = [0; 4096];
        assert!(s.read(&mut b).await.unwrap() > 0);
        s.write_all(answer.as_bytes()).await.unwrap();
    });
    let r = Remote::new(&base, "secret", false)
        .unwrap()
        .get("/api")
        .await
        .unwrap();
    assert_eq!(r.status, 302);
    assert!(
        tokio::time::timeout(Duration::from_millis(50), target.accept())
            .await
            .is_err()
    );
}
#[tokio::test]
async fn unpinned_pairing_pin_and_changed_certificate() {
    let d = tempfile::tempdir().unwrap();
    let pair = certificate_files(d.path(), None, None, &["localhost", "127.0.0.1"]).unwrap();
    let accept =
        tokio_rustls::TlsAcceptor::from(std::sync::Arc::new(pair.server_config().unwrap()));
    let ln = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = ln.local_addr().unwrap();
    let task = tokio::spawn(async move {
        loop {
            let (s, _) = ln.accept().await.unwrap();
            let accept = accept.clone();
            tokio::spawn(async move {
                if let Ok(mut s) = accept.accept(s).await {
                    let mut b = [0; 4096];
                    if s.read(&mut b).await.unwrap_or(0) > 0 {
                        let _=s.write_all(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{}").await;
                    }
                }
            });
        }
    });
    let pin = fingerprint(&address.to_string()).await.unwrap();
    let base = format!("https://{address}");
    assert!(
        Remote::new(&base, "", true)
            .unwrap()
            .get("/api")
            .await
            .unwrap()
            .ok()
    );
    assert!(
        Remote::pinned(&base, "secret", &pin)
            .unwrap()
            .get("/api")
            .await
            .unwrap()
            .ok()
    );
    let err = Remote::pinned(&base, "secret", &"0".repeat(64))
        .unwrap()
        .get("/api")
        .await
        .unwrap_err();
    assert!(err.to_string().contains("certificate changed"), "{err}");
    task.abort();
    let _ = task.await;
    let replacement = tempfile::tempdir().unwrap();
    let pair =
        certificate_files(replacement.path(), None, None, &["localhost", "127.0.0.1"]).unwrap();
    let accept =
        tokio_rustls::TlsAcceptor::from(std::sync::Arc::new(pair.server_config().unwrap()));
    let listener = tokio::net::TcpListener::bind(address).await.unwrap();
    let peer = tokio::spawn(async move {
        let (socket, _) = listener.accept().await.unwrap();
        match accept.accept(socket).await {
            Ok(mut tls) => {
                let mut b = [0; 4096];
                tls.read(&mut b).await.unwrap_or(0)
            }
            Err(_) => 0,
        }
    });
    let error = Remote::pinned(&base, "secret", &pin)
        .unwrap()
        .get("/api")
        .await
        .unwrap_err();
    assert!(error.to_string().contains("certificate changed"));
    assert_eq!(
        peer.await.unwrap(),
        0,
        "credentials must not reach changed certificate"
    );
}

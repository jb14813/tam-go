use std::process::Command;

#[test]
fn help_exits_successfully_without_initializing_the_client() {
    for flag in ["-h", "--help"] {
        let dir = tempfile::tempdir().unwrap();
        let data = dir.path().join("must-not-be-created");
        let output = Command::new(env!("CARGO_BIN_EXE_r-tam-client"))
            .args(["-addr", "invalid-listen-address", flag, "-unknown"])
            .env("TAM_DATA_DIR", &data)
            .current_dir(dir.path())
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let text = format!(
            "{}{}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(
            text.contains("R-TAM Client")
                && text.contains("-addr")
                && text.contains("-open")
                && text.contains("-tray"),
            "{text}"
        );
        assert!(!text.contains("-announce"));
        assert!(!data.exists());
        assert_eq!(std::fs::read_dir(dir.path()).unwrap().count(), 0);
    }
}

#[test]
fn unknown_client_option_fails_before_initialization() {
    let dir = tempfile::tempdir().unwrap();
    let data = dir.path().join("must-not-be-created");
    let output = Command::new(env!("CARGO_BIN_EXE_r-tam-client"))
        .args(["-unknown", "--help"])
        .env("TAM_DATA_DIR", &data)
        .current_dir(dir.path())
        .output()
        .unwrap();
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("flag provided but not defined"));
    assert!(!data.exists());
}

#[cfg(target_os = "linux")]
#[test]
fn client_binds_go_port_only_ephemeral_address() {
    use std::{
        io::{BufRead, BufReader},
        process::Stdio,
        sync::mpsc,
        time::Duration,
    };
    struct Child(std::process::Child);
    impl Drop for Child {
        fn drop(&mut self) {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }
    let dir = tempfile::tempdir().unwrap();
    let mut child = Child(
        Command::new(env!("CARGO_BIN_EXE_r-tam-client"))
            .args(["-addr", ":0", "-open=false", "-tray=false"])
            .env("TAM_DATA_DIR", dir.path())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .unwrap(),
    );
    let stdout = child.0.stdout.take().unwrap();
    let (tx, rx) = mpsc::channel();
    let reader = std::thread::spawn(move || {
        for line in BufReader::new(stdout).lines().map_while(Result::ok) {
            if line.starts_with("http://") {
                let _ = tx.send(line);
                break;
            }
        }
    });
    let url = rx.recv_timeout(Duration::from_secs(10));
    drop(child);
    reader.join().unwrap();
    let url = url.expect("client should bind :0 and print its bound address");
    let port: u16 = url
        .strip_prefix("http://localhost:")
        .unwrap()
        .trim_end_matches('/')
        .parse()
        .unwrap();
    assert_ne!(port, 0);
}

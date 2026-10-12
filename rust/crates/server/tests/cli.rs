use std::process::Command;

#[test]
fn help_exits_successfully_without_initializing_the_server() {
    for flag in ["-h", "--help"] {
        let dir = tempfile::tempdir().unwrap();
        let data = dir.path().join("must-not-be-created");
        let output = Command::new(env!("CARGO_BIN_EXE_r-tam-server"))
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
        for expected in [
            "R-TAM Server",
            "-addr",
            "-tray",
            "-tls",
            "-cert",
            "-key",
            "-announce",
        ] {
            assert!(text.contains(expected), "missing {expected}: {text}");
        }
        assert!(!text.contains("-open"));
        assert!(!data.exists());
        assert_eq!(std::fs::read_dir(dir.path()).unwrap().count(), 0);
    }
}

#[test]
fn unknown_server_option_fails_before_initialization() {
    let dir = tempfile::tempdir().unwrap();
    let data = dir.path().join("must-not-be-created");
    let output = Command::new(env!("CARGO_BIN_EXE_r-tam-server"))
        .args(["-unknown", "--help"])
        .env("TAM_DATA_DIR", &data)
        .current_dir(dir.path())
        .output()
        .unwrap();
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("flag provided but not defined"));
    assert!(!data.exists());
}

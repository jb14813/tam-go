use r_tam_core::config::{Settings, SettingsFile};
#[test]
fn defaults_keys_partial_merge_and_wrong_types() {
    let s = Settings::default();
    assert_eq!(
        serde_json::to_value(&s).unwrap(),
        serde_json::json!({"remote_server":"","remote_key":"","remote_port":"8000","remote_tls":false,"default_pref":"CALL","venue_name":"Test Venue","disable_attrib":false,"remote_name":"","remote_fingerprint":""})
    );
    assert_eq!(
        s.merge(br#"{"venue_name":"Hall"}"#).unwrap().venue_name,
        "Hall"
    );
    for p in [
        br#"{"unknown":true}"#.as_slice(),
        br#"{"remote_tls":"true"}"#,
        br#"{"remote_port":8000}"#,
    ] {
        assert!(s.merge(p).is_err());
    }
}
#[test]
fn reload_recovers_backup_and_retains_last_good() {
    let d = tempfile::tempdir().unwrap();
    let p = d.path().join("settings.json");
    std::fs::write(p.with_extension("json.bak"), br#"{"venue_name":"Backup"}"#).unwrap();
    let f = SettingsFile::open(&p);
    assert_eq!(f.get().venue_name, "Backup");
    assert!(!f.problem().is_empty());
    f.update(|mut s| {
        s.venue_name = "Live".into();
        Ok(s)
    })
    .unwrap();
    std::fs::write(&p, b"broken").unwrap();
    assert_eq!(f.get().venue_name, "Live");
    assert!(!f.problem().is_empty());
    std::fs::write(&p, br#"{"venue_name":"Fixed"}"#).unwrap();
    assert_eq!(f.get().venue_name, "Fixed");
    assert!(f.problem().is_empty());
}
#[test]
fn failed_replace_does_not_change_memory_or_good_backup() {
    let d = tempfile::tempdir().unwrap();
    let p = d.path().join("settings.json");
    let f = SettingsFile::open(&p);
    f.update(|mut s| {
        s.venue_name = "Good".into();
        Ok(s)
    })
    .unwrap();
    std::fs::remove_file(&p).unwrap();
    std::fs::create_dir(&p).unwrap();
    assert!(
        f.update(|mut s| {
            s.venue_name = "Lost".into();
            Ok(s)
        })
        .is_err()
    );
    assert_eq!(f.get().venue_name, "Good");
    let backup: Settings =
        serde_json::from_slice(&std::fs::read(p.with_extension("json.bak")).unwrap()).unwrap();
    assert_eq!(backup.venue_name, "Good");
}
#[test]
fn partial_disk_nulls_case_fields_normalization_validation_and_ipv6() {
    let d = tempfile::tempdir().unwrap();
    let p = d.path().join("settings.json");
    std::fs::write(&p,br#"{"remote_port":null,"VENUE_NAME":"Hall","remote_tls":true,"remote_tls":null,"ignored":12}"#).unwrap();
    let f = SettingsFile::open(&p);
    assert_eq!(f.get().remote_port, "8000");
    assert!(f.get().remote_tls);
    assert_eq!(f.get().venue_name, "Hall");
    let s=Settings::default().merge(br#"{"remote_server":" ::1 ","remote_port":" 8443 ","remote_tls":true,"remote_fingerprint":"AA:AA"}"#).unwrap().normalize();
    assert_eq!(s.remote_url(), "https://[::1]:8443");
    assert_eq!(s.remote_fingerprint, "aaaa");
    assert!(s.validate().is_err());
    let s = s.merge(br#"{"remote_fingerprint":""}"#).unwrap();
    assert!(s.validate().is_ok());
    assert_eq!(s.merge(br#"{"remote_port":null}"#).unwrap().remote_port, "");
    for patch in [
        br#"{"default_pref":"PHONE"}"#.as_slice(),
        br#"{"remote_server":"https://tam"}"#,
        br#"{"remote_port":"0"}"#,
    ] {
        assert!(s.merge(patch).unwrap().validate().is_err());
    }
}
#[test]
fn serialized_updates_keep_fields_and_corrupt_primary_recovers_without_overwriting() {
    let d = tempfile::tempdir().unwrap();
    let p = d.path().join("settings.json");
    let f = std::sync::Arc::new(SettingsFile::open(&p));
    let a = f.clone();
    let t = std::thread::spawn(move || {
        a.update(|mut s| {
            s.venue_name = "Concurrent".into();
            Ok(s)
        })
        .unwrap()
    });
    f.update(|mut s| {
        s.remote_name = "Server".into();
        Ok(s)
    })
    .unwrap();
    t.join().unwrap();
    assert_eq!(f.get().venue_name, "Concurrent");
    assert_eq!(f.get().remote_name, "Server");
    std::fs::write(&p, b"broken").unwrap();
    let before = std::fs::read(p.with_extension("json.bak")).unwrap();
    let recovered = SettingsFile::open(&p);
    assert_eq!(recovered.get().venue_name, "Concurrent");
    assert_eq!(std::fs::read(&p).unwrap(), b"broken");
    assert_eq!(std::fs::read(p.with_extension("json.bak")).unwrap(), before);
}

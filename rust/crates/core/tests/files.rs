use r_tam_core::{
    files::{atomic_write, preserve_event},
    models::BackupFile,
};
#[test]
fn archive_is_synced_immutable_and_unique() {
    let d = tempfile::tempdir().unwrap();
    let b = BackupFile::default();
    let a = preserve_event(d.path(), "before-pair-", &b).unwrap();
    let bytes = std::fs::read(&a).unwrap();
    let c = preserve_event(d.path(), "before-pair-", &b).unwrap();
    assert_ne!(a, c);
    assert_eq!(bytes, std::fs::read(a).unwrap());
    assert_eq!(serde_json::from_slice::<BackupFile>(&bytes).unwrap(), b);
    assert!(preserve_event(d.path(), "../outside", &b).is_err());
}
#[test]
fn atomic_replacement_is_confined_and_failure_preserves_old() {
    let d = tempfile::tempdir().unwrap();
    let p = d.path().join("settings");
    atomic_write(&p, b"old").unwrap();
    atomic_write(&p, b"new").unwrap();
    assert_eq!(std::fs::read(&p).unwrap(), b"new");
    assert!(atomic_write(&d.path().join("missing/file"), b"bad").is_err());
    assert_eq!(std::fs::read_dir(d.path()).unwrap().count(), 1);
}

use r_tam_core::tls::{certificate_files, fingerprint_of};
#[test]
fn retains_pairs_refuses_incomplete_explicit_and_bad_pairs() {
    let d = tempfile::tempdir().unwrap();
    assert!(
        certificate_files(d.path(), Some("absent.pem".as_ref()), None, &["localhost"]).is_err()
    );
    let p = certificate_files(d.path(), None, None, &["localhost", "127.0.0.1", "::1"]).unwrap();
    assert!(p.created);
    let first = std::fs::read(&p.cert).unwrap();
    assert!(
        !certificate_files(d.path(), None, None, &["localhost"])
            .unwrap()
            .created
    );
    assert_eq!(std::fs::read(&p.cert).unwrap(), first);
    assert!(p.server_config().is_ok());
    std::fs::remove_file(&p.key).unwrap();
    assert!(certificate_files(d.path(), None, None, &["localhost"]).is_err());
    std::fs::write(&p.key, b"wrong").unwrap();
    assert!(certificate_files(d.path(), None, None, &["localhost"]).is_err());
}
#[test]
fn pin_is_go_leaf_der_sha256() {
    assert_eq!(
        fingerprint_of(b"abc"),
        "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
    );
}

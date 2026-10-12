use super::*;
use crate::models::*;
use std::sync::{Barrier, mpsc};

fn ticket(id: i64, name: &str) -> Ticket {
    Ticket {
        prefix: "A".into(),
        t_id: id,
        first_name: name.into(),
        pref: "CALL".into(),
        ..Default::default()
    }
}

#[test]
fn warmed_reads_observe_writer_external_commits_and_key_revocation() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = store.create_key("volunteer").unwrap();
    store.upsert_tickets(&[ticket(1, "first")]).unwrap();
    assert!(store.key_exists(&key.auth_key).unwrap());
    assert_eq!(store.ticket("A", 1).unwrap().unwrap().first_name, "first");
    assert_eq!(store.counts().unwrap(), (0, 1, 0));
    store
        .upsert_tickets(&[ticket(1, "writer"), ticket(2, "second")])
        .unwrap();
    assert_eq!(store.ticket("A", 1).unwrap().unwrap().first_name, "writer");
    assert_eq!(store.counts().unwrap(), (0, 2, 0));
    store.delete_key(&key.auth_key).unwrap();
    assert!(!store.key_exists(&key.auth_key).unwrap());
    let external = Connection::open(store.path()).unwrap();
    external.execute_batch("BEGIN IMMEDIATE; UPDATE tickets SET first_name='external' WHERE t_id=1; DELETE FROM tickets WHERE t_id=2; COMMIT;").unwrap();
    assert_eq!(
        store.ticket("A", 1).unwrap().unwrap().first_name,
        "external"
    );
    assert_eq!(store.counts().unwrap(), (0, 1, 0));
    external
        .execute(
            "INSERT INTO auth_keys(auth_key,description) VALUES(?1,'external')",
            [&key.auth_key],
        )
        .unwrap();
    assert!(store.key_exists(&key.auth_key).unwrap());
    external
        .execute("DELETE FROM auth_keys WHERE auth_key=?1", [&key.auth_key])
        .unwrap();
    assert!(!store.key_exists(&key.auth_key).unwrap());
}

#[test]
fn returning_an_active_read_transaction_does_not_retain_snapshot_or_wal_lock() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    store.upsert_tickets(&[ticket(1, "first")]).unwrap();
    let reader = store.read().unwrap();
    reader.execute_batch("BEGIN").unwrap();
    assert_eq!(
        reader
            .query_row("SELECT count(*) FROM tickets", [], |r| r.get::<_, i64>(0))
            .unwrap(),
        1
    );
    assert!(!reader.is_autocommit());
    drop(reader);
    store.upsert_tickets(&[ticket(2, "second")]).unwrap();
    assert_eq!(store.counts().unwrap(), (0, 2, 0));
    let external = Connection::open(store.path()).unwrap();
    external.busy_timeout(Duration::from_millis(50)).unwrap();
    let checkpoint: (i64, i64, i64) = external
        .query_row("PRAGMA wal_checkpoint(TRUNCATE)", [], |r| {
            Ok((r.get(0)?, r.get(1)?, r.get(2)?))
        })
        .unwrap();
    assert_eq!(checkpoint.0, 0, "returned reader must not pin the WAL");
}

#[test]
fn snapshot_decode_error_releases_transaction_and_later_snapshot_is_fresh() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    store.upsert_tickets(&[ticket(1, "first")]).unwrap();
    assert_eq!(
        store.snapshot().unwrap().backup.tickets[0].first_name,
        "first"
    );
    let external = Connection::open(store.path()).unwrap();
    external
        .execute_batch("UPDATE tickets SET rev='invalid' WHERE t_id=1")
        .unwrap();
    assert!(store.snapshot().is_err());
    external
        .execute_batch("UPDATE tickets SET rev=10,first_name='repaired' WHERE t_id=1")
        .unwrap();
    let snapshot = store.snapshot().unwrap();
    assert_eq!(snapshot.backup.tickets[0].first_name, "repaired");
    assert_eq!(snapshot.backup.tickets[0].rev, 10);
    assert_eq!(snapshot.generation, store.generation().unwrap());
    external.busy_timeout(Duration::from_millis(50)).unwrap();
    let checkpoint: (i64, i64, i64) = external
        .query_row("PRAGMA wal_checkpoint(TRUNCATE)", [], |r| {
            Ok((r.get(0)?, r.get(1)?, r.get(2)?))
        })
        .unwrap();
    assert_eq!(checkpoint.0, 0);
}

#[test]
fn held_snapshot_and_blocked_writer_do_not_serialize_parallel_readers() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    store.upsert_tickets(&[ticket(1, "first")]).unwrap();
    let key = store.create_key("volunteer").unwrap();
    let external = Connection::open(store.path()).unwrap();
    external.execute_batch("BEGIN IMMEDIATE").unwrap();
    let mut held_reader = store.read().unwrap();
    let snapshot = held_reader.transaction().unwrap();
    assert_eq!(
        snapshot
            .query_row("SELECT count(*) FROM tickets", [], |r| r.get::<_, i64>(0))
            .unwrap(),
        1
    );
    let (written, write_result) = mpsc::channel();
    let writer_store = store.clone();
    let writer = std::thread::spawn(move || {
        written
            .send(writer_store.upsert_tickets(&[ticket(2, "second")]))
            .unwrap();
    });
    assert!(
        write_result
            .recv_timeout(Duration::from_millis(100))
            .is_err()
    );
    let barrier = Arc::new(Barrier::new(9));
    let (read_done, read_results) = mpsc::channel();
    let readers = (0..8)
        .map(|_| {
            let store = store.clone();
            let barrier = barrier.clone();
            let key = key.auth_key.clone();
            let done = read_done.clone();
            std::thread::spawn(move || {
                barrier.wait();
                for _ in 0..5 {
                    assert!(store.key_exists(&key).unwrap());
                    assert_eq!(store.counts().unwrap(), (0, 1, 0));
                    assert_eq!(store.ticket("A", 1).unwrap().unwrap().first_name, "first");
                }
                done.send(()).unwrap();
            })
        })
        .collect::<Vec<_>>();
    barrier.wait();
    let all_read = (0..8).all(|_| read_results.recv_timeout(Duration::from_secs(2)).is_ok());
    external.execute_batch("COMMIT").unwrap();
    write_result
        .recv_timeout(Duration::from_secs(6))
        .unwrap()
        .unwrap();
    writer.join().unwrap();
    assert_eq!(
        snapshot
            .query_row("SELECT count(*) FROM tickets", [], |r| r.get::<_, i64>(0))
            .unwrap(),
        1
    );
    snapshot.commit().unwrap();
    drop(held_reader);
    for reader in readers {
        reader.join().unwrap();
    }
    assert!(
        all_read,
        "other readers must finish while the first checkout is held"
    );
    assert_eq!(store.counts().unwrap(), (0, 2, 0));
}

#[test]
fn warmed_reader_statements_observe_schema_changes_and_recover_after_decode_errors() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    store.upsert_tickets(&[ticket(1, "first")]).unwrap();
    assert_eq!(store.ticket("A", 1).unwrap().unwrap().first_name, "first");
    let external = Connection::open(store.path()).unwrap();
    external.execute_batch("ALTER TABLE tickets ADD COLUMN read_schema_test TEXT; UPDATE tickets SET first_name='after-schema-change' WHERE t_id=1").unwrap();
    assert_eq!(
        store.ticket("A", 1).unwrap().unwrap().first_name,
        "after-schema-change"
    );
    external
        .execute_batch("UPDATE tickets SET rev='invalid' WHERE t_id=1")
        .unwrap();
    assert!(store.ticket("A", 1).is_err());
    external
        .execute_batch("UPDATE tickets SET rev=20 WHERE t_id=1")
        .unwrap();
    assert_eq!(store.ticket("A", 1).unwrap().unwrap().rev, 20);
}

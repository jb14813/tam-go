use r_tam_core::{models::*, schema::Role, store::Store};
use std::{sync::mpsc, time::Duration};

#[test]
fn wal_reads_continue_while_an_external_writer_blocks_an_ordered_save() {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("contention.db");
    let store = Store::open(&path, Role::Server).unwrap();
    let external = rusqlite::Connection::open(&path).unwrap();
    external.execute_batch("BEGIN IMMEDIATE").unwrap();
    let (send, receive) = mpsc::channel();
    let writer = store.clone();
    let thread = std::thread::spawn(move || {
        let operation = r_tam_core::store::Operation::Save(SaveBatch::Tickets(vec![TicketSave {
            ticket: Ticket {
                prefix: "LOCK".into(),
                t_id: 1,
                first_name: "Durable".into(),
                pref: "CALL".into(),
                ..Default::default()
            },
            base: None,
        }]));
        let result = writer.apply_ordered(
            &Order {
                client: "contention-client".into(),
                save: 1,
            },
            "contention-hash",
            &operation,
        );
        send.send(result).unwrap();
    });
    assert!(receive.recv_timeout(Duration::from_millis(100)).is_err());
    assert!(store.ticket("LOCK", 1).unwrap().is_none());
    assert_eq!(store.counts().unwrap(), (0, 0, 0));
    external.execute_batch("COMMIT").unwrap();
    let result = receive
        .recv_timeout(Duration::from_secs(6))
        .unwrap()
        .unwrap();
    assert_eq!(result.outcome, Outcome::Applied);
    thread.join().unwrap();
    assert_eq!(
        store.ticket("LOCK", 1).unwrap().unwrap().first_name,
        "Durable"
    );
    assert_eq!(
        external
            .query_row(
                "SELECT last_save FROM client_saves WHERE client='contention-client'",
                [],
                |row| row.get::<_, i64>(0)
            )
            .unwrap(),
        1
    );
    assert_eq!(
        external
            .query_row("PRAGMA integrity_check", [], |row| row.get::<_, String>(0))
            .unwrap(),
        "ok"
    );
}

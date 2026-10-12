use r_tam_core::{
    models::*,
    schema::Role,
    store::{Operation, Store},
};
#[test]
fn duplicate_and_behind_saves_never_mutate() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let order = Order {
        client: "client".into(),
        save: 2,
    };
    let op = Operation::Save(SaveBatch::Tickets(vec![TicketSave {
        ticket: Ticket {
            prefix: "A".into(),
            t_id: 1,
            first_name: "first".into(),
            pref: "CALL".into(),
            ..Default::default()
        },
        base: None,
    }]));
    assert_eq!(st.apply_ordered(&order, "hash", &op).unwrap().last, 2);
    let before = st.ticket("A", 1).unwrap().unwrap();
    assert_eq!(
        st.apply_ordered(&order, "hash", &op).unwrap().outcome,
        Outcome::Repeat
    );
    assert_eq!(
        st.apply_ordered(&order, "different", &op).unwrap().outcome,
        Outcome::Behind
    );
    assert_eq!(
        st.apply_ordered(&Order { save: 1, ..order }, "hash", &op)
            .unwrap()
            .outcome,
        Outcome::Behind
    );
    assert_eq!(st.ticket("A", 1).unwrap().unwrap(), before);
}
#[test]
fn restored_counter_host_identity_and_exhaustion() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("db");
    let st = Store::open(&path, Role::Client).unwrap();
    let first = st.next_save("host", None).unwrap();
    assert_eq!(first.save, 1);
    let next = st.next_save("host", Some(99)).unwrap();
    assert_eq!(next.client, first.client);
    assert_eq!(next.save, 100);
    let other = st.next_save("other", None).unwrap();
    assert_ne!(other.client, first.client);
    assert_eq!(other.save, 101);
    rusqlite::Connection::open(path)
        .unwrap()
        .execute("UPDATE save_order SET last_save=?1", [i64::MAX])
        .unwrap();
    assert!(st.next_save("other", None).is_err());
}
#[test]
fn ordered_write_error_rolls_back_save_and_revision_counters() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let before = st.event().unwrap();
    let db = rusqlite::Connection::open(st.path()).unwrap();
    db.execute_batch("CREATE TRIGGER refuse_order BEFORE INSERT ON client_saves BEGIN SELECT RAISE(ABORT,'injected order record failure'); END;").unwrap();
    let op = Operation::Save(SaveBatch::Tickets(vec![TicketSave {
        ticket: Ticket {
            prefix: "A".into(),
            t_id: 1,
            pref: "CALL".into(),
            ..Default::default()
        },
        base: None,
    }]));
    let order = Order {
        client: "client".into(),
        save: 1,
    };
    assert!(st.apply_ordered(&order, "hash", &op).is_err());
    assert!(st.ticket("A", 1).unwrap().is_none());
    assert_eq!(st.event().unwrap(), before);
    db.execute_batch("DROP TRIGGER refuse_order").unwrap();
    assert_eq!(
        st.apply_ordered(&order, "hash", &op).unwrap().outcome,
        Outcome::Applied
    );
}
#[test]
fn revision_overflow_is_rejected_without_changing_data() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    rusqlite::Connection::open(st.path())
        .unwrap()
        .execute("UPDATE event SET last_rev=?1", [i64::MAX])
        .unwrap();
    assert!(
        st.save_tickets(&[TicketSave {
            ticket: Ticket {
                prefix: "A".into(),
                t_id: 1,
                pref: "CALL".into(),
                ..Default::default()
            },
            base: None
        }])
        .is_err()
    );
    assert!(st.ticket("A", 1).unwrap().is_none());
    assert_eq!(st.event().unwrap().unwrap().last_rev, i64::MAX);
}

#[test]
fn wire_revision_limit_rejects_ordered_mutation_without_changing_rows_or_counters() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let original = Ticket {
        prefix: "A".into(),
        t_id: 1,
        first_name: "before".into(),
        pref: "CALL".into(),
        rev: MAX_EXACT,
        ..Default::default()
    };
    st.import(&BackupFile {
        tickets: vec![original.clone()],
        ..Default::default()
    })
    .unwrap();
    let unchanged = Operation::Save(SaveBatch::Tickets(vec![TicketSave {
        ticket: original.clone(),
        base: None,
    }]));
    st.apply_ordered(
        &Order {
            client: "client".into(),
            save: 1,
        },
        "unchanged",
        &unchanged,
    )
    .unwrap();
    let before = st.export().unwrap();
    let event = st.event().unwrap();
    let marker = st.generation().unwrap();
    let changed = Operation::Save(SaveBatch::Tickets(vec![TicketSave {
        ticket: Ticket {
            first_name: "changed".into(),
            ..original
        },
        base: None,
    }]));
    assert!(
        st.apply_ordered(
            &Order {
                client: "client".into(),
                save: 2
            },
            "changed",
            &changed
        )
        .is_err()
    );
    assert_eq!(st.export().unwrap(), before);
    assert_eq!(st.event().unwrap(), event);
    assert_eq!(st.generation().unwrap(), marker);
    let record: (i64, String) = rusqlite::Connection::open(st.path())
        .unwrap()
        .query_row(
            "SELECT last_save,last_hash FROM client_saves WHERE client='client'",
            [],
            |row| Ok((row.get(0)?, row.get(1)?)),
        )
        .unwrap();
    assert_eq!(record, (1, "unchanged".into()));
    let exported = r_tam_core::wire::go_json(&st.export().unwrap()).unwrap();
    assert_eq!(
        r_tam_core::wire::decode::<BackupFile>(&exported).unwrap(),
        before
    );
}

#[test]
fn wire_revision_limit_rolls_back_partial_batch_and_allows_last_valid_revision() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let first = Ticket {
        prefix: "A".into(),
        t_id: 1,
        first_name: "before".into(),
        pref: "CALL".into(),
        rev: MAX_EXACT - 1,
        ..Default::default()
    };
    let second = Ticket {
        t_id: 2,
        ..first.clone()
    };
    st.import(&BackupFile {
        tickets: vec![first.clone(), second.clone()],
        ..Default::default()
    })
    .unwrap();
    let before = st.export().unwrap();
    let event = st.event().unwrap();
    let marker = st.generation().unwrap();
    let changed_first = TicketSave {
        ticket: Ticket {
            first_name: "changed".into(),
            ..first
        },
        base: None,
    };
    let batch = Operation::Save(SaveBatch::Tickets(vec![
        changed_first.clone(),
        TicketSave {
            ticket: Ticket {
                first_name: "changed".into(),
                ..second
            },
            base: None,
        },
    ]));
    let order = Order {
        client: "boundary-client".into(),
        save: 1,
    };
    assert!(st.apply_ordered(&order, "two-rows", &batch).is_err());
    assert_eq!(st.export().unwrap(), before);
    assert_eq!(st.event().unwrap(), event);
    assert_eq!(st.generation().unwrap(), marker);
    let records: i64 = rusqlite::Connection::open(st.path())
        .unwrap()
        .query_row("SELECT count(*) FROM client_saves", [], |r| r.get(0))
        .unwrap();
    assert_eq!(records, 0);
    assert_eq!(
        st.apply_ordered(
            &order,
            "one-row",
            &Operation::Save(SaveBatch::Tickets(vec![changed_first]))
        )
        .unwrap()
        .outcome,
        Outcome::Applied
    );
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().rev, MAX_EXACT);
    let export = st.export().unwrap();
    assert_eq!(
        r_tam_core::wire::decode::<BackupFile>(&r_tam_core::wire::go_json(&export).unwrap())
            .unwrap(),
        export
    );
}

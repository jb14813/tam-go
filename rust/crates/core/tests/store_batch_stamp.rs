use r_tam_core::{
    models::*,
    schema::Role,
    store::{Operation, OperationResult, Store, StoredRows},
};

fn ticket(id: i64, name: &str) -> TicketSave {
    TicketSave {
        ticket: Ticket {
            prefix: "A".into(),
            t_id: id,
            first_name: name.into(),
            pref: "CALL".into(),
            ..Default::default()
        },
        base: None,
    }
}

fn apply(st: &Store, rows: Vec<TicketSave>) -> Vec<Ticket> {
    let answer = st
        .apply_ordered(
            &Order {
                client: "batch-client".into(),
                save: 1,
            },
            "batch",
            &Operation::Save(SaveBatch::Tickets(rows)),
        )
        .unwrap();
    assert_eq!(answer.outcome, Outcome::Applied);
    let Some(OperationResult::Rows(StoredRows::Tickets(rows))) = answer.result else {
        panic!("ticket rows must be returned in request order")
    };
    rows
}

#[test]
fn duplicate_rows_keep_per_row_order_and_noops_do_not_take_revisions() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    st.save_tickets(&[ticket(1, "seed")]).unwrap();
    let rows = apply(
        &st,
        vec![
            ticket(1, "first"),
            ticket(1, "second"),
            ticket(1, "second"),
            ticket(2, "last"),
        ],
    );
    assert_eq!(rows.iter().map(|r| r.rev).collect::<Vec<_>>(), [2, 3, 3, 4]);
    assert_eq!(
        rows.iter()
            .map(|r| r.first_name.as_str())
            .collect::<Vec<_>>(),
        ["first", "second", "second", "last"]
    );
    assert_eq!(st.ticket("A", 1).unwrap().unwrap(), rows[2]);
    assert_eq!(st.ticket("A", 2).unwrap().unwrap(), rows[3]);
    assert_eq!(st.event().unwrap().unwrap().last_rev, 4);
    assert_eq!(st.generation().unwrap(), 4);
}

#[test]
fn every_changed_row_reads_the_database_counter_after_previous_row_triggers() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let db = rusqlite::Connection::open(st.path()).unwrap();
    db.execute_batch("CREATE TRIGGER advance_counter AFTER INSERT ON tickets WHEN NEW.t_id=1 BEGIN UPDATE event SET last_rev=last_rev+100 WHERE id=1; END;").unwrap();
    let rows = apply(&st, vec![ticket(1, "first"), ticket(2, "second")]);
    assert_eq!(rows[0].rev, 1);
    assert_eq!(rows[1].rev, 102);
    assert_eq!(st.event().unwrap().unwrap().last_rev, 102);
    assert_eq!(st.generation().unwrap(), 2);
}

#[test]
fn invalid_counter_introduced_by_row_trigger_rolls_back_the_entire_ordered_batch() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let before = st.event().unwrap();
    let generation = st.generation().unwrap();
    let db = rusqlite::Connection::open(st.path()).unwrap();
    db.execute_batch("CREATE TRIGGER corrupt_counter AFTER INSERT ON tickets WHEN NEW.t_id=1 BEGIN UPDATE event SET last_rev='invalid' WHERE id=1; END;").unwrap();
    assert!(
        st.apply_ordered(
            &Order {
                client: "batch-client".into(),
                save: 1
            },
            "batch",
            &Operation::Save(SaveBatch::Tickets(vec![
                ticket(1, "first"),
                ticket(2, "second")
            ])),
        )
        .is_err()
    );
    assert!(st.export().unwrap().tickets.is_empty());
    assert_eq!(st.event().unwrap(), before);
    assert_eq!(st.generation().unwrap(), generation);
    let receipts: i64 = db
        .query_row("SELECT count(*) FROM client_saves", [], |r| r.get(0))
        .unwrap();
    assert_eq!(receipts, 0);
}

#[test]
fn empty_and_unchanged_saves_do_not_inspect_invalid_event_state() {
    for corruption in [
        "DELETE FROM event",
        "UPDATE event SET last_rev='invalid'",
        "UPDATE event SET event=x'FF'",
    ] {
        let dir = tempfile::tempdir().unwrap();
        let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
        let initial = st.save_tickets(&[ticket(1, "seed")]).unwrap();
        let generation = st.generation().unwrap();
        let db = rusqlite::Connection::open(st.path()).unwrap();
        db.execute_batch(corruption).unwrap();
        assert!(st.save_tickets(&[]).unwrap().is_empty());
        assert_eq!(st.save_tickets(&[ticket(1, "seed")]).unwrap(), initial);
        assert!(st.save_tickets(&[ticket(1, "changed")]).is_err());
        assert_eq!(st.ticket("A", 1).unwrap().unwrap(), initial[0]);
        assert_eq!(st.generation().unwrap(), generation);
    }
}

#[test]
fn client_batches_retain_each_existing_revision_and_do_not_accept_new_row_revisions() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    st.upsert_tickets(&[Ticket {
        rev: 57,
        ..ticket(1, "seed").ticket
    }])
    .unwrap();
    let mut existing = ticket(1, "changed");
    existing.ticket.rev = 999;
    let mut new = ticket(2, "new");
    new.ticket.rev = 999;
    let tickets = st.save_tickets(&[existing, new]).unwrap();
    assert_eq!(tickets.iter().map(|r| r.rev).collect::<Vec<_>>(), [57, 0]);
    st.upsert_baskets(&[Basket {
        prefix: "A".into(),
        b_id: 1,
        description: "seed".into(),
        winning_ticket: 3,
        rev: 50,
        win_rev: 60,
        ..Default::default()
    }])
    .unwrap();
    let descriptions = st
        .save_baskets(&[
            BasketSave {
                basket: Basket {
                    prefix: "A".into(),
                    b_id: 1,
                    description: "changed".into(),
                    winning_ticket: 99,
                    rev: 999,
                    win_rev: 999,
                    ..Default::default()
                },
                base: None,
            },
            BasketSave {
                basket: Basket {
                    prefix: "A".into(),
                    b_id: 2,
                    description: "new".into(),
                    rev: 999,
                    win_rev: 999,
                    ..Default::default()
                },
                base: None,
            },
        ])
        .unwrap();
    assert_eq!(
        (
            descriptions[0].rev,
            descriptions[0].win_rev,
            descriptions[0].winning_ticket
        ),
        (50, 60, 3)
    );
    assert_eq!((descriptions[1].rev, descriptions[1].win_rev), (0, 0));
    let drawing = st
        .save_winning(&[
            DrawingSave {
                basket: Basket {
                    winning_ticket: 4,
                    description: "unowned".into(),
                    rev: 999,
                    win_rev: 999,
                    ..descriptions[0].clone()
                },
                base: None,
            },
            DrawingSave {
                basket: Basket {
                    prefix: "A".into(),
                    b_id: 3,
                    winning_ticket: 5,
                    description: "unowned".into(),
                    rev: 999,
                    win_rev: 999,
                    ..Default::default()
                },
                base: None,
            },
        ])
        .unwrap();
    assert_eq!((drawing[0].rev, drawing[0].win_rev), (50, 60));
    assert_eq!(drawing[0].description, "changed");
    assert_eq!((drawing[1].rev, drawing[1].win_rev), (0, 0));
    assert!(drawing[1].description.is_empty());
    assert!(st.event().unwrap().is_none());
}

#[test]
fn prefix_batches_clear_server_tombstones_and_keep_client_revisions() {
    let dir = tempfile::tempdir().unwrap();
    let server = Store::open(&dir.path().join("server"), Role::Server).unwrap();
    let batch = SaveBatch::Prefixes(vec![
        PrefixSave {
            prefix: Prefix {
                prefix: "A".into(),
                color: "white".into(),
                weight: 1,
                rev: 999,
            },
            base: None,
        },
        PrefixSave {
            prefix: Prefix {
                prefix: "B".into(),
                color: "red".into(),
                weight: 2,
                rev: 999,
            },
            base: None,
        },
    ]);
    server.save(&batch).unwrap();
    server
        .apply_operation(&Operation::DeletePrefix("A".into()))
        .unwrap();
    let db = rusqlite::Connection::open(server.path()).unwrap();
    let tombstones: i64 = db
        .query_row("SELECT count(*) FROM deleted_prefixes", [], |r| r.get(0))
        .unwrap();
    assert_eq!(tombstones, 1);
    let StoredRows::Prefixes(rows) = server.save(&batch).unwrap() else {
        unreachable!()
    };
    assert_eq!(rows.iter().map(|r| r.rev).collect::<Vec<_>>(), [4, 2]);
    assert_eq!(server.event().unwrap().unwrap().last_rev, 4);
    let tombstones: i64 = db
        .query_row("SELECT count(*) FROM deleted_prefixes", [], |r| r.get(0))
        .unwrap();
    assert_eq!(tombstones, 0);
    let client = Store::open(&dir.path().join("client"), Role::Client).unwrap();
    client.upsert_prefixes(&rows[..1]).unwrap();
    let SaveBatch::Prefixes(mut changed) = batch else {
        unreachable!()
    };
    changed[0].prefix.weight = 3;
    let StoredRows::Prefixes(rows) = client.save(&SaveBatch::Prefixes(changed)).unwrap() else {
        unreachable!()
    };
    assert_eq!(rows.iter().map(|r| r.rev).collect::<Vec<_>>(), [4, 0]);
}

#[test]
fn reused_ticket_statements_observe_added_and_removed_schema_triggers() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let initial = st.save_tickets(&[ticket(1, "seed")]).unwrap();
    let event = st.event().unwrap();
    let generation = st.generation().unwrap();
    let db = rusqlite::Connection::open(st.path()).unwrap();
    db.execute_batch("ALTER TABLE tickets ADD COLUMN schema_test TEXT; CREATE TRIGGER reject_ticket_update BEFORE UPDATE ON tickets BEGIN SELECT RAISE(ABORT,'schema-trigger rejection'); END;").unwrap();
    let order = Order {
        client: "schema-client".into(),
        save: 1,
    };
    let op = Operation::Save(SaveBatch::Tickets(vec![ticket(1, "changed")]));
    let error = st.apply_ordered(&order, "schema-change", &op).unwrap_err();
    assert!(error.to_string().contains("schema-trigger rejection"));
    assert_eq!(st.ticket("A", 1).unwrap().unwrap(), initial[0]);
    assert_eq!(st.event().unwrap(), event);
    assert_eq!(st.generation().unwrap(), generation);
    let receipts: i64 = db
        .query_row("SELECT count(*) FROM client_saves", [], |r| r.get(0))
        .unwrap();
    assert_eq!(receipts, 0);
    db.execute_batch("DROP TRIGGER reject_ticket_update")
        .unwrap();
    assert_eq!(
        st.apply_ordered(&order, "schema-change", &op)
            .unwrap()
            .outcome,
        Outcome::Applied
    );
    let stored = st.ticket("A", 1).unwrap().unwrap();
    assert_eq!(stored.first_name, "changed");
    assert_eq!(stored.rev, 2);
    assert_eq!(st.event().unwrap().unwrap().last_rev, 2);
    assert_eq!(st.generation().unwrap(), generation + 1);
    let receipt: (i64, String) = db
        .query_row(
            "SELECT last_save,last_hash FROM client_saves WHERE client='schema-client'",
            [],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .unwrap();
    assert_eq!(receipt, (1, "schema-change".into()));
}

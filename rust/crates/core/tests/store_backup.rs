use r_tam_core::{models::*, schema::Role, store::Store};
fn ticket(name: &str, rev: i64) -> Ticket {
    Ticket {
        prefix: "A".into(),
        t_id: 1,
        first_name: name.into(),
        pref: "CALL".into(),
        rev,
        ..Default::default()
    }
}
#[test]
fn backup_restore_upserts_and_newer_merge_components() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    st.import(&BackupFile {
        event: "event-a".into(),
        tickets: vec![ticket("older", 3)],
        baskets: vec![Basket {
            prefix: "A".into(),
            b_id: 1,
            description: "new description".into(),
            winning_ticket: 1,
            rev: 9,
            win_rev: 4,
            ..Default::default()
        }],
        ..Default::default()
    })
    .unwrap();
    assert_eq!(st.export().unwrap().tickets, vec![ticket("older", 3)]);
    let merged = st
        .merge_newer(&BackupFile {
            event: "event-a".into(),
            tickets: vec![ticket("newer", 6)],
            baskets: vec![Basket {
                prefix: "A".into(),
                b_id: 1,
                description: "old description".into(),
                winning_ticket: 2,
                rev: 5,
                win_rev: 12,
                ..Default::default()
            }],
            ..Default::default()
        })
        .unwrap();
    assert_eq!(merged.updated, 2);
    let basket = st.basket("A", 1).unwrap().unwrap();
    assert_eq!(basket.description, "new description");
    assert_eq!(basket.winning_ticket, 2);
    let before = st.export().unwrap();
    assert!(
        st.merge_newer(&BackupFile {
            event: "other".into(),
            tickets: vec![ticket("wrong", 30)],
            ..Default::default()
        })
        .is_err()
    );
    assert_eq!(st.export().unwrap(), before);
}
#[test]
fn invalid_backup_is_atomic_and_absent_rows_stay() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    st.import(&BackupFile {
        tickets: vec![ticket("kept", 1)],
        ..Default::default()
    })
    .unwrap();
    let before = st.export().unwrap();
    assert_eq!(before.tickets.len(), 1);
    assert!(
        st.import(&BackupFile {
            prefixes: vec![Prefix {
                prefix: "history-long".into(),
                color: "chartreuse".into(),
                weight: -3,
                rev: 2
            }],
            tickets: vec![Ticket {
                t_id: -1,
                ..ticket("invalid", 1)
            }],
            ..Default::default()
        })
        .is_err()
    );
    assert_eq!(st.export().unwrap(), before);
    st.import(&BackupFile {
        prefixes: vec![Prefix {
            prefix: "history-long".into(),
            color: "unknown".into(),
            weight: -3,
            rev: 2,
        }],
        ..Default::default()
    })
    .unwrap();
    let after = st.export().unwrap();
    assert_eq!(after.tickets, before.tickets);
    assert_eq!(after.prefixes[0].color, "white");
}
#[test]
fn prefix_tombstone_blocks_newer_merge_but_restore_revives_it() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    st.save_prefixes(&[PrefixSave {
        prefix: Prefix {
            prefix: "A".into(),
            color: "white".into(),
            weight: 1,
            ..Default::default()
        },
        base: None,
    }])
    .unwrap();
    let copy = st.export().unwrap();
    st.delete_prefix("A").unwrap();
    assert_eq!(st.merge_newer(&copy).unwrap().kept, 1);
    assert!(st.prefix_by_name("A").unwrap().is_none());
    st.import(&copy).unwrap();
    assert_eq!(st.prefix_by_name("A").unwrap().unwrap(), copy.prefixes[0]);
    let next = st
        .save_prefixes(&[PrefixSave {
            prefix: Prefix {
                color: "red".into(),
                ..copy.prefixes[0].clone()
            },
            base: None,
        }])
        .unwrap();
    assert!(next[0].rev > copy.prefixes[0].rev + 1);
}
#[test]
fn snapshots_are_consistent_during_concurrent_atomic_writes() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let writer = st.clone();
    let gate = std::sync::Arc::new(std::sync::Barrier::new(2));
    let worker_gate = gate.clone();
    let thread = std::thread::spawn(move || {
        worker_gate.wait();
        for rev in 1..101 {
            writer
                .import(&BackupFile {
                    tickets: vec![ticket(&rev.to_string(), rev)],
                    prefixes: vec![Prefix {
                        prefix: "A".into(),
                        color: "white".into(),
                        weight: rev,
                        rev,
                    }],
                    ..Default::default()
                })
                .unwrap();
        }
    });
    gate.wait();
    for _ in 0..100 {
        let bf = st.export().unwrap();
        if let Some(t) = bf.tickets.first() {
            assert_eq!(bf.prefixes[0].rev, t.rev);
            assert_eq!(bf.prefixes[0].weight.to_string(), t.first_name);
        }
    }
    thread.join().unwrap();
    assert_eq!(st.export().unwrap().tickets[0].rev, 100);
}
#[test]
fn event_transition_is_atomic_guards_archive_snapshot_and_restore_pending() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    st.set_mirror_event("old-event").unwrap();
    let batch = SaveBatch::Tickets(vec![TicketSave {
        ticket: ticket("queued", 3),
        base: None,
    }]);
    st.accept_offline("host", "POST", "/api/tickets", &batch)
        .unwrap();
    let archived = st.snapshot().unwrap();
    assert!(st.import(&BackupFile::default()).is_err());
    st.save_tickets(&[TicketSave {
        ticket: ticket("newer", 3),
        base: None,
    }])
    .unwrap();
    assert!(
        st.transition_copy(&archived, "new-event", "event changed")
            .is_err()
    );
    let archived = st.snapshot().unwrap();
    let before = st.export().unwrap();
    let db = rusqlite::Connection::open(st.path()).unwrap();
    db.execute_batch("CREATE TRIGGER refuse_clear BEFORE DELETE ON tickets BEGIN SELECT RAISE(ABORT,'injected event-clear error'); END;").unwrap();
    assert!(
        st.transition_copy(&archived, "new-event", "event changed")
            .is_err()
    );
    assert_eq!(st.export().unwrap(), before);
    assert_eq!(st.outbox_counts().unwrap(), (1, 0));
    db.execute_batch("DROP TRIGGER refuse_clear").unwrap();
    assert_eq!(
        st.transition_copy(&archived, "new-event", "event changed")
            .unwrap(),
        1
    );
    assert_eq!(st.outbox_counts().unwrap(), (0, 1));
    assert!(st.all_tickets().unwrap().is_empty());
    assert_eq!(st.mirror_event().unwrap(), "new-event");
}
#[test]
fn mirror_download_cannot_overwrite_concurrent_local_edit_or_delete() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    st.upsert_tickets(&[ticket("before", 3)]).unwrap();
    st.upsert_prefixes(&[Prefix {
        prefix: "A".into(),
        color: "white".into(),
        weight: 1,
        rev: 2,
    }])
    .unwrap();
    let marker = st.generation().unwrap();
    st.save_tickets(&[TicketSave {
        ticket: ticket("local", 3),
        base: None,
    }])
    .unwrap();
    st.delete_prefix("A").unwrap();
    st.mirror_apply(
        marker,
        &BackupFile {
            event: "same-event".into(),
            tickets: vec![ticket("download", 4)],
            prefixes: vec![Prefix {
                prefix: "A".into(),
                color: "red".into(),
                weight: 2,
                rev: 5,
            }],
            ..Default::default()
        },
    )
    .unwrap();
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().first_name, "local");
    assert!(st.prefix_by_name("A").unwrap().is_none());
}
#[test]
fn typed_backup_revisions_must_be_valid_before_any_upsert() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let before = st.export().unwrap();
    assert!(
        st.import(&BackupFile {
            prefixes: vec![Prefix {
                prefix: "A".into(),
                color: "white".into(),
                weight: 1,
                rev: 3
            }],
            tickets: vec![ticket("invalid", -1)],
            ..Default::default()
        })
        .is_err()
    );
    assert_eq!(st.export().unwrap(), before);
}

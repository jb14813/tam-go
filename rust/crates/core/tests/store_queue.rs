use r_tam_core::{models::*, schema::Role, store::Store};
fn save(name: &str) -> SaveBatch {
    SaveBatch::Tickets(vec![TicketSave {
        ticket: Ticket {
            prefix: "A".into(),
            t_id: 1,
            first_name: name.into(),
            pref: "CALL".into(),
            ..Default::default()
        },
        base: None,
    }])
}
#[test]
fn journal_precedes_dispatch_without_accepting_local_data() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let body = br#"[{"prefix":"A","t_id":1,"first_name":"online","pref":"CALL"}]"#;
    let item = st
        .journal_online("host", "POST", "/api/tickets", body)
        .unwrap();
    assert!(item.id > 0);
    assert_eq!(st.pending().unwrap().len(), 1);
    assert!(st.ticket("A", 1).unwrap().is_none());
    let reopened = Store::open(st.path(), Role::Client).unwrap();
    assert_eq!(reopened.pending().unwrap()[0].body, item.body);
}
#[test]
fn offline_queue_insert_failure_rolls_back_row_and_counter() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("db");
    let st = Store::open(&path, Role::Client).unwrap();
    let sql = rusqlite::Connection::open(&path).unwrap();
    sql.execute_batch("CREATE TRIGGER refuse_queue BEFORE INSERT ON outbox BEGIN SELECT RAISE(ABORT,'injected queue failure'); END;").unwrap();
    assert!(
        st.accept_offline("host", "POST", "/api/tickets", &save("offline"))
            .is_err()
    );
    assert!(st.ticket("A", 1).unwrap().is_none());
    assert!(st.pending().unwrap().is_empty());
    let count: i64 = sql
        .query_row("SELECT count(*) FROM save_order", [], |r| r.get(0))
        .unwrap();
    assert_eq!(count, 0);
}
#[test]
fn malformed_answers_stay_pending_and_old_answers_preserve_newer_local_rows() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let first = st
        .accept_offline("host", "POST", "/api/tickets", &save("first"))
        .unwrap();
    assert_eq!(st.pending().unwrap().len(), 1);
    assert!(
        st.reconcile(first.item.id, br#"[{"prefix":"B","t_id":1}]"#)
            .is_err()
    );
    assert_eq!(st.pending().unwrap().len(), 1);
    let second = st
        .accept_offline("host", "POST", "/api/tickets", &save("second"))
        .unwrap();
    st.reconcile(
        first.item.id,
        br#"[{"prefix":"A","t_id":1,"first_name":"first","pref":"CALL","rev":3}]"#,
    )
    .unwrap();
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().first_name, "second");
    assert_eq!(st.pending().unwrap()[0].id, second.item.id);
    st.reconcile(
        second.item.id,
        br#"[{"prefix":"A","t_id":1,"first_name":"second","pref":"CALL","rev":4}]"#,
    )
    .unwrap();
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().rev, 4);
    assert!(st.pending().unwrap().is_empty());
}

#[test]
fn duplicate_answer_rows_copy_final_occurrence_and_basket_parts_reconcile_apart() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let SaveBatch::Tickets(mut saves) = save("first") else {
        unreachable!()
    };
    if let SaveBatch::Tickets(second) = save("last") {
        saves.extend(second);
    }
    let queued = st
        .accept_offline("host", "POST", "/api/tickets", &SaveBatch::Tickets(saves))
        .unwrap();
    st.reconcile(queued.item.id,br#"[{"prefix":"A","t_id":1,"first_name":"first","pref":"CALL","rev":4},{"prefix":"A","t_id":1,"first_name":"last","pref":"CALL","rev":5}]"#).unwrap();
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().first_name, "last");
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().rev, 5);
    let drawing = st
        .accept_offline(
            "host",
            "POST",
            "/api/drawing",
            &SaveBatch::Drawing(vec![DrawingSave {
                basket: Basket {
                    prefix: "A".into(),
                    b_id: 2,
                    winning_ticket: 1,
                    ..Default::default()
                },
                base: None,
            }]),
        )
        .unwrap();
    let desc = st
        .accept_offline(
            "host",
            "POST",
            "/api/baskets",
            &SaveBatch::Baskets(vec![BasketSave {
                basket: Basket {
                    prefix: "A".into(),
                    b_id: 2,
                    description: "new description".into(),
                    ..Default::default()
                },
                base: None,
            }]),
        )
        .unwrap();
    st.reconcile(
        drawing.item.id,
        br#"[{"prefix":"A","b_id":2,"winning_ticket":1,"win_rev":6}]"#,
    )
    .unwrap();
    let row = st.basket("A", 2).unwrap().unwrap();
    assert_eq!(row.description, "new description");
    assert_eq!(row.win_rev, 6);
    assert_eq!(st.pending().unwrap()[0].id, desc.item.id);
}
#[test]
fn partial_refusal_is_failed_with_refreshed_base_and_retries_at_end() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let request = SaveBatch::Tickets(vec![TicketSave {
        ticket: Ticket {
            prefix: "A".into(),
            t_id: 1,
            first_name: "mine".into(),
            phone_number: "333".into(),
            pref: "CALL".into(),
            ..Default::default()
        },
        base: Some(TicketValues {
            first_name: "base".into(),
            phone_number: "111".into(),
            pref: "CALL".into(),
            ..Default::default()
        }),
    }]);
    let item = st
        .accept_offline("host", "POST", "/api/tickets", &request)
        .unwrap()
        .item;
    let result=st.reconcile(item.id,br#"[{"prefix":"A","t_id":1,"first_name":"mine","phone_number":"222","pref":"CALL","rev":9}]"#).unwrap();
    assert_eq!(result.conflicts.len(), 1);
    assert_eq!(result.conflicts[0].field, "phone number");
    let failed = st.failed().unwrap();
    assert_eq!(failed.len(), 1);
    let SaveBatch::Tickets(redo) = SaveBatch::decode(Form::Tickets, &failed[0].body).unwrap()
    else {
        unreachable!()
    };
    assert_eq!(redo[0].ticket.phone_number, "333");
    assert_eq!(redo[0].base.as_ref().unwrap().phone_number, "222");
    assert_eq!(redo[0].ticket.first_name, "mine");
    assert!(failed[0].order.client.is_empty());
    assert_eq!(failed[0].order.save, 0);
    let later = st
        .accept_offline("host", "POST", "/api/tickets", &save("later"))
        .unwrap()
        .item;
    assert_eq!(st.retry_failed("host").unwrap(), 1);
    let pending = st.pending().unwrap();
    assert_eq!(pending[0].id, later.id);
    assert!(pending[1].order.save > pending[0].order.save);
    assert!(st.failed().unwrap().is_empty());
}
#[test]
fn copied_go_outbox_has_no_metadata_and_preserves_newer_owned_fields() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    st.save(&save("mine")).unwrap();
    let db = rusqlite::Connection::open(st.path()).unwrap();
    let body = br#"[{"prefix":"A","t_id":1,"first_name":"mine","pref":"CALL"}]"#;
    db.execute("INSERT INTO outbox(created_at,method,path,body,client,save_number) VALUES('2026-10-11T00:00:00Z','POST','/api/search/tickets',?1,'copied-go',8)",[body.as_slice()]).unwrap();
    let item = st.next_outbox().unwrap().unwrap();
    assert_eq!(item.body, body);
    assert_eq!(item.order.client, "copied-go");
    st.save(&save("newer")).unwrap();
    st.reconcile(
        item.id,
        br#"[{"prefix":"A","t_id":1,"first_name":"mine","pref":"CALL","rev":4}]"#,
    )
    .unwrap();
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().first_name, "newer");
    assert!(st.pending().unwrap().is_empty());
}
#[test]
fn definitive_online_rejection_leaves_data_and_auth_failure_keeps_intent() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    st.save(&save("original")).unwrap();
    let before = st.export().unwrap();
    let item = st
        .journal_online(
            "host",
            "POST",
            "/api/tickets",
            br#"[{"prefix":"A","t_id":1,"first_name":"online","pref":"CALL"}]"#,
        )
        .unwrap();
    st.note_outbox_attempt(item.id, "Unauthorized").unwrap();
    assert_eq!(st.pending().unwrap().len(), 1);
    assert_eq!(st.export().unwrap(), before);
    st.reject_online(item.id).unwrap();
    assert!(st.pending().unwrap().is_empty());
    assert_eq!(st.export().unwrap(), before);
}
#[test]
fn reconcile_failed_insert_error_rolls_back_copied_rows_and_queue() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let request = SaveBatch::Tickets(vec![TicketSave {
        ticket: Ticket {
            prefix: "A".into(),
            t_id: 1,
            first_name: "mine".into(),
            pref: "CALL".into(),
            ..Default::default()
        },
        base: Some(TicketValues {
            first_name: "base".into(),
            pref: "CALL".into(),
            ..Default::default()
        }),
    }]);
    let item = st
        .accept_offline("host", "POST", "/api/tickets", &request)
        .unwrap()
        .item;
    let before = st.export().unwrap();
    rusqlite::Connection::open(st.path()).unwrap().execute_batch("CREATE TRIGGER refuse_failure BEFORE INSERT ON outbox_failed BEGIN SELECT RAISE(ABORT,'injected failed-list error'); END;").unwrap();
    assert!(
        st.reconcile(
            item.id,
            br#"[{"prefix":"A","t_id":1,"first_name":"other","pref":"CALL","rev":7}]"#
        )
        .is_err()
    );
    assert_eq!(st.export().unwrap(), before);
    assert_eq!(st.pending().unwrap().len(), 1);
}
#[test]
fn refused_retry_updates_copy_and_never_overtakes_later_numbers() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let req = SaveBatch::Tickets(vec![TicketSave {
        ticket: Ticket {
            prefix: "A".into(),
            t_id: 1,
            first_name: "mine".into(),
            pref: "CALL".into(),
            ..Default::default()
        },
        base: Some(TicketValues {
            first_name: "base".into(),
            pref: "CALL".into(),
            ..Default::default()
        }),
    }]);
    let item = st
        .accept_offline("host", "POST", "/api/tickets", &req)
        .unwrap()
        .item;
    st.reconcile(
        item.id,
        br#"[{"prefix":"A","t_id":1,"first_name":"other","pref":"CALL","rev":5}]"#,
    )
    .unwrap();
    st.retry_failed("host").unwrap();
    let retry = st.next_outbox().unwrap().unwrap();
    st.reconcile(
        retry.id,
        br#"[{"prefix":"A","t_id":1,"first_name":"mine","pref":"CALL","rev":6}]"#,
    )
    .unwrap();
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().first_name, "mine");
    let first = st
        .accept_offline("host", "POST", "/api/tickets", &save("one"))
        .unwrap()
        .item;
    let second = st
        .accept_offline("host", "POST", "/api/tickets", &save("two"))
        .unwrap()
        .item;
    assert!(st.renumber_outbox(first.id, "host", 100).is_err());
    assert_eq!(st.pending().unwrap()[1].order, second.order);
}
#[test]
fn online_intent_cannot_be_accepted_offline_twice_over_newer_local_data() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let item = st
        .journal_online(
            "host",
            "POST",
            "/api/tickets",
            br#"[{"prefix":"A","t_id":1,"first_name":"online","pref":"CALL"}]"#,
        )
        .unwrap();
    st.accept_journal_offline(item.id).unwrap();
    st.accept_offline("host", "POST", "/api/tickets", &save("newer"))
        .unwrap();
    assert!(st.accept_journal_offline(item.id).is_err());
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().first_name, "newer");
}
#[test]
fn old_answer_cannot_overwrite_a_changed_then_restored_local_field() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let item = st
        .accept_offline("host", "POST", "/api/tickets", &save("same"))
        .unwrap()
        .item;
    st.save(&save("different")).unwrap();
    st.save(&save("same")).unwrap();
    st.reconcile(
        item.id,
        br#"[{"prefix":"A","t_id":1,"first_name":"server answer","pref":"CALL","rev":5}]"#,
    )
    .unwrap();
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().first_name, "same");
    assert_eq!(st.ticket("A", 1).unwrap().unwrap().rev, 0);
}
#[test]
fn journaled_delete_preserves_identity_and_cannot_remove_a_newer_prefix() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    let original = Prefix {
        prefix: "A".into(),
        color: "white".into(),
        weight: 1,
        rev: 4,
    };
    st.upsert_prefixes(std::slice::from_ref(&original)).unwrap();
    let item = st
        .journal_online("host", "DELETE", "/api/prefixes?p=A", b"")
        .unwrap();
    assert!(st.prefix_by_name("A").unwrap().is_some());
    assert!(
        st.reconcile_delete(item.id, "B", Some(br#"{"prefix":"B"}"#))
            .is_err()
    );
    assert_eq!(st.pending().unwrap().len(), 1);
    assert_eq!(
        st.accept_delete_journal_offline(item.id).unwrap().unwrap(),
        original
    );
    assert_eq!(st.pending().unwrap()[0].order, item.order);
    st.upsert_prefixes(&[Prefix {
        color: "red".into(),
        ..original
    }])
    .unwrap();
    st.reconcile_delete(item.id, "A", Some(br#"{"prefix":"A"}"#))
        .unwrap();
    assert_eq!(st.prefix_by_name("A").unwrap().unwrap().color, "red");
    assert!(st.pending().unwrap().is_empty());
}

#[test]
fn invalid_winner_answers_do_not_mutate_or_acknowledge_online_or_offline_intents() {
    for (path, online) in [
        ("/api/drawing", false),
        ("/api/drawing", true),
        ("/api/baskets", false),
        ("/api/baskets", true),
    ] {
        let dir = tempfile::tempdir().unwrap();
        let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
        let row = Basket {
            prefix: "A".into(),
            b_id: 1,
            winning_ticket: 1,
            ..Default::default()
        };
        let batch = if path == "/api/drawing" {
            SaveBatch::Drawing(vec![DrawingSave {
                basket: row,
                base: None,
            }])
        } else {
            SaveBatch::Baskets(vec![BasketSave {
                basket: row,
                base: None,
            }])
        };
        let item = if online {
            st.journal_online(
                "host",
                "POST",
                path,
                &r_tam_core::wire::go_json(&batch).unwrap(),
            )
            .unwrap()
        } else {
            st.accept_offline("host", "POST", path, &batch)
                .unwrap()
                .item
        };
        let before = st.export().unwrap();
        let pending = st.pending().unwrap();
        let marker = st.generation().unwrap();
        let db = rusqlite::Connection::open(st.path()).unwrap();
        let metadata: (bool, i64) = db
            .query_row(
                "SELECT accepted,generation FROM rust_intents WHERE id=?1",
                [item.id],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )
            .unwrap();
        let last: i64 = db
            .query_row("SELECT last_save FROM save_order WHERE id=1", [], |r| {
                r.get(0)
            })
            .unwrap();
        assert!(
            st.reconcile(
                item.id,
                br#"[{"prefix":"A","b_id":1,"winning_ticket":-1,"win_rev":5}]"#
            )
            .is_err(),
            "accepted invalid {path} answer; online={online}"
        );
        assert_eq!(st.export().unwrap(), before);
        assert_eq!(st.pending().unwrap(), pending);
        assert_eq!(st.generation().unwrap(), marker);
        assert!(st.failed().unwrap().is_empty());
        assert_eq!(
            db.query_row(
                "SELECT accepted,generation FROM rust_intents WHERE id=?1",
                [item.id],
                |r| Ok((r.get(0)?, r.get(1)?))
            )
            .unwrap(),
            metadata
        );
        assert_eq!(
            db.query_row("SELECT last_save FROM save_order WHERE id=1", [], |r| r
                .get::<_, i64>(0))
                .unwrap(),
            last
        );
    }
}

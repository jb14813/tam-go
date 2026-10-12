use r_tam_core::{models::*, schema::Role, store::Store};
#[test]
fn stale_field_merge_returns_actual_rows() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("copy.db"), Role::Server).unwrap();
    let original = st
        .save_tickets(&[TicketSave {
            ticket: Ticket {
                prefix: "A".into(),
                t_id: 1,
                first_name: "Original".into(),
                phone_number: "111".into(),
                pref: "CALL".into(),
                ..Default::default()
            },
            base: None,
        }])
        .unwrap();
    assert_eq!(original.len(), 1, "save must return stored ticket");
    let original = original[0].clone();
    st.save_tickets(&[TicketSave {
        ticket: Ticket {
            phone_number: "222".into(),
            ..original.clone()
        },
        base: None,
    }])
    .unwrap();
    let stored = st
        .save_tickets(&[TicketSave {
            ticket: Ticket {
                first_name: "Corrected".into(),
                phone_number: "333".into(),
                ..original.clone()
            },
            base: Some(TicketValues {
                first_name: original.first_name.clone(),
                phone_number: original.phone_number.clone(),
                pref: original.pref.clone(),
                last_name: original.last_name.clone(),
            }),
        }])
        .unwrap()
        .remove(0);
    assert_eq!(stored.first_name, "Corrected");
    assert_eq!(stored.phone_number, "222");
    assert!(stored.rev > original.rev);
}
#[test]
fn basket_and_drawing_forms_own_separate_fields_and_revision_components() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let initial = st
        .save_baskets(&[BasketSave {
            basket: Basket {
                prefix: "A".into(),
                b_id: 1,
                description: "original".into(),
                donors: "donor".into(),
                winning_ticket: 4,
                ..Default::default()
            },
            base: None,
        }])
        .unwrap()
        .remove(0);
    assert_eq!(initial.rev, initial.win_rev);
    let description = st
        .save_baskets(&[BasketSave {
            basket: Basket {
                description: "corrected".into(),
                winning_ticket: 99,
                ..initial.clone()
            },
            base: None,
        }])
        .unwrap()
        .remove(0);
    assert_eq!(description.winning_ticket, 4);
    assert_eq!(description.win_rev, initial.win_rev);
    assert!(description.rev > initial.rev);
    let winning = st
        .save_winning(&[DrawingSave {
            basket: Basket {
                description: "must not write".into(),
                donors: "must not write".into(),
                winning_ticket: 8,
                ..description.clone()
            },
            base: Some(DrawingValues { winning_ticket: 4 }),
        }])
        .unwrap()
        .remove(0);
    assert_eq!(winning.description, "corrected");
    assert_eq!(winning.donors, "donor");
    assert_eq!(winning.rev, description.rev);
    assert!(winning.win_rev > winning.rev);
    let event = st.event().unwrap();
    assert_eq!(
        st.save_winning(&[DrawingSave {
            basket: winning.clone(),
            base: Some(DrawingValues { winning_ticket: 4 })
        }])
        .unwrap()[0],
        winning
    );
    assert_eq!(st.event().unwrap(), event);
    let added = st
        .save_winning(&[DrawingSave {
            basket: Basket {
                prefix: "A".into(),
                b_id: 2,
                description: "not drawing owned".into(),
                winning_ticket: 3,
                ..Default::default()
            },
            base: None,
        }])
        .unwrap()
        .remove(0);
    assert_eq!(added.description, "");
    assert_eq!(added.rev, 0);
    assert!(added.win_rev > 0);
}
#[test]
fn prefixes_merge_per_field_and_clients_retain_server_revisions() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("server"), Role::Server).unwrap();
    let first = st
        .save_prefixes(&[PrefixSave {
            prefix: Prefix {
                prefix: "A".into(),
                color: "white".into(),
                weight: 1,
                ..Default::default()
            },
            base: None,
        }])
        .unwrap()
        .remove(0);
    st.save_prefixes(&[PrefixSave {
        prefix: Prefix {
            color: "red".into(),
            ..first.clone()
        },
        base: None,
    }])
    .unwrap();
    let actual = st
        .save_prefixes(&[PrefixSave {
            prefix: Prefix {
                color: "blue".into(),
                weight: 2,
                ..first.clone()
            },
            base: Some(PrefixValues {
                color: "white".into(),
                weight: 1,
            }),
        }])
        .unwrap()
        .remove(0);
    assert_eq!(actual.color, "red");
    assert_eq!(actual.weight, 2);
    assert!(actual.rev > first.rev);
    let client = Store::open(&dir.path().join("client"), Role::Client).unwrap();
    client
        .upsert_prefixes(std::slice::from_ref(&actual))
        .unwrap();
    let changed = client
        .save_prefixes(&[PrefixSave {
            prefix: Prefix {
                weight: 3,
                ..actual.clone()
            },
            base: None,
        }])
        .unwrap()
        .remove(0);
    assert_eq!(changed.rev, actual.rev);
}
#[test]
fn save_result_validates_all_identities_and_keeps_only_accepted_changes() {
    use r_tam_core::store::{StoredRows, analyze_result, result_of};
    let batch = SaveBatch::Tickets(vec![TicketSave {
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
    let stored = Ticket {
        prefix: "A".into(),
        t_id: 1,
        first_name: "mine".into(),
        phone_number: "222".into(),
        pref: "CALL".into(),
        rev: 8,
        ..Default::default()
    };
    let result = analyze_result(&batch, StoredRows::Tickets(vec![stored.clone()])).unwrap();
    let SaveBatch::Tickets(kept) = result.kept else {
        unreachable!()
    };
    assert_eq!(kept[0].ticket.first_name, "mine");
    assert_eq!(kept[0].ticket.phone_number, "111");
    assert!(analyze_result(&batch, StoredRows::Tickets(vec![])).is_err());
    assert!(
        analyze_result(
            &batch,
            StoredRows::Tickets(vec![Ticket { t_id: 2, ..stored }])
        )
        .is_err()
    );
    assert!(result_of("/api/search/tickets", b"[]", b"null").is_ok());
}
#[test]
fn keys_are_go_formatted_and_activity_deletion_is_atomic() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Server).unwrap();
    let key = st.create_key("volunteer").unwrap();
    assert_eq!(key.auth_key.len(), 32);
    assert!(
        key.auth_key
            .bytes()
            .all(|b| b.is_ascii_uppercase() || b.is_ascii_digit())
    );
    assert!(st.key_exists(&key.auth_key).unwrap());
    assert!(!st.key_exists("").unwrap());
    st.touch_key(&key.auth_key).unwrap();
    st.mark_key_updated(&key.auth_key).unwrap();
    let listed = st.list_keys().unwrap();
    assert!(!listed[0].last_seen.is_empty());
    assert!(!listed[0].last_update.is_empty());
    st.touch_key("missing").unwrap();
    let db = rusqlite::Connection::open(st.path()).unwrap();
    db.execute_batch("CREATE TRIGGER refuse_key_activity_delete BEFORE DELETE ON auth_key_activity BEGIN SELECT RAISE(ABORT,'injected activity delete failure'); END;").unwrap();
    assert!(st.delete_key(&key.auth_key).is_err());
    assert!(st.key_exists(&key.auth_key).unwrap());
    db.execute_batch("DROP TRIGGER refuse_key_activity_delete")
        .unwrap();
    assert_eq!(
        st.delete_key(&key.auth_key).unwrap().unwrap().auth_key,
        key.auth_key
    );
    assert!(st.list_keys().unwrap().is_empty());
    let rows: i64 = db
        .query_row("SELECT count(*) FROM auth_key_activity", [], |r| r.get(0))
        .unwrap();
    assert_eq!(rows, 0);
}

#[test]
fn answer_validation_preserves_historical_prefix_names_and_weights() {
    use r_tam_core::store::{StoredRows, analyze_result};
    let historical = Prefix {
        prefix: "historical/".repeat(20),
        color: "white".into(),
        weight: -7,
        rev: 4,
    };
    let request = SaveBatch::Prefixes(vec![PrefixSave {
        prefix: historical.clone(),
        base: None,
    }]);
    let result = analyze_result(&request, StoredRows::Prefixes(vec![historical.clone()])).unwrap();
    assert_eq!(result.rows, StoredRows::Prefixes(vec![historical]));
    assert!(
        analyze_result(
            &SaveBatch::Tickets(vec![TicketSave {
                ticket: Ticket {
                    prefix: "A".into(),
                    t_id: -1,
                    ..Default::default()
                },
                base: None
            }]),
            StoredRows::Tickets(vec![Ticket {
                prefix: "A".into(),
                t_id: -1,
                ..Default::default()
            }])
        )
        .is_err()
    );
}

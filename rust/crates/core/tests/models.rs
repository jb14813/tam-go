use r_tam_core::{models::Ticket, wire};

#[test]
fn drawing_validation_checks_all_rows_before_base_winners() {
    use r_tam_core::models::{Form, SaveBatch};
    let mut batch = SaveBatch::decode(
        Form::Drawing,
        br#"[
        {"prefix":"A","b_id":1,"base":{"winning_ticket":-1}},
        {"prefix":"A","b_id":-2}
    ]"#,
    )
    .unwrap();
    assert_eq!(
        batch.validate().unwrap_err().to_string(),
        "basket 2 (A/-2): id must be zero or more"
    );
}

#[test]
fn invalid_color_uses_go_control_character_quoting() {
    use r_tam_core::models::{Form, SaveBatch};
    for (color, quoted) in [
        ("\0", r#""\x00""#),
        ("\u{7}", r#""\a""#),
        ("\u{b}", r#""\v""#),
        ("\u{85}", r#""\u0085""#),
        ("\u{200d}", r#""\u200d""#),
    ] {
        let body = serde_json::to_vec(&serde_json::json!([
            {"prefix":"A", "color":color,"weight":0}
        ]))
        .unwrap();
        let mut batch = SaveBatch::decode(Form::Prefixes, &body).unwrap();
        assert_eq!(
            batch.validate().unwrap_err().to_string(),
            format!(
                "prefix 1 (A): color {quoted} is not one of white, blue, yellow, green, orange, purple, red"
            )
        );
    }
}

#[test]
fn required_ticket_id_rejects_null_and_fraction() {
    for input in [
        r#"{"prefix":"A","t_id":null}"#,
        r#"{"prefix":"A","t_id":4.0}"#,
        r#"{"prefix":"A","t_id":"4"}"#,
    ] {
        assert!(wire::decode::<Ticket>(input.as_bytes()).is_err());
    }
    assert_eq!(
        wire::decode::<Ticket>(br#"{"prefix":"A","t_id":0}"#)
            .unwrap()
            .t_id,
        0
    );
}

#[test]
fn required_ticket_id_rejects_missing_and_browser_inexact() {
    for input in [
        r#"{"prefix":"A"}"#,
        r#"{"prefix":"A","t_id":9007199254740992}"#,
    ] {
        assert!(
            wire::decode::<Ticket>(input.as_bytes()).is_err(),
            "accepted {input}"
        );
    }
}

#[test]
fn optional_numbers_default_null_and_revisions_are_nonnegative() {
    use r_tam_core::models::{Basket, Prefix};
    let p: Prefix = wire::decode(br#"{"prefix":null,"weight":null,"rev":null}"#).unwrap();
    assert_eq!((p.prefix.as_str(), p.weight, p.rev), ("", 0, 0));
    let b: Basket =
        wire::decode(br#"{"prefix":"A","b_id":2,"winning_ticket":null,"win_rev":null}"#).unwrap();
    assert_eq!((b.winning_ticket, b.rev, b.win_rev), (0, 0, 0));
    for input in [
        r#"{"prefix":"A","t_id":1,"rev":-1}"#,
        r#"{"prefix":"A","t_id":1e0}"#,
        r#"{"prefix":"A","t_id":1,"rev":2.0}"#,
    ] {
        assert!(wire::decode::<Ticket>(input.as_bytes()).is_err());
    }
}

#[test]
fn duplicate_rows_and_form_base_fields_survive_normalization() {
    use r_tam_core::models::{Form, SaveBatch};
    let mut saves = SaveBatch::decode(Form::Tickets, br#"[{"prefix":" A ","t_id":0,"pref":" CALL ","base":{"pref":" TEXT "}},{"prefix":" A ","t_id":0,"first_name":"Jo","changed":true}]"#).unwrap();
    saves.validate().unwrap();
    let SaveBatch::Tickets(rows) = saves else {
        panic!("wrong form")
    };
    assert_eq!(rows.len(), 2);
    assert_eq!(
        (
            rows[0].ticket.prefix.as_str(),
            rows[0].ticket.pref.as_str(),
            rows[0].base.as_ref().unwrap().pref.as_str()
        ),
        ("A", "CALL", "TEXT")
    );
    assert_eq!(rows[1].ticket.first_name, "Jo");
}

#[test]
fn validation_preserves_public_row_detail_and_backup_historical_names() {
    use r_tam_core::models::{BackupFile, Form, SaveBatch};
    let mut saves = SaveBatch::decode(Form::Tickets, br#"[{"prefix":" A ","t_id":-1}]"#).unwrap();
    assert_eq!(
        saves.validate().unwrap_err().public_detail(),
        "ticket 1 (A/-1): id must be zero or more"
    );
    let mut backup: BackupFile = wire::decode(br#"{"prefixes":[{"prefix":" old/name ","color":"pink","weight":-2}],"baskets":null,"tickets":[]}"#).unwrap();
    backup.validate().unwrap();
    assert_eq!(backup.prefixes[0].prefix, " old/name ");
    assert_eq!(backup.prefixes[0].color, "white");
    assert_eq!(backup.prefixes[0].weight, -2);
    assert!(backup.baskets.is_empty());
}

#[test]
fn base_winner_rejects_negative_and_prefix_rules_match_go() {
    use r_tam_core::models::{Form, SaveBatch, validate_prefix_name};
    for name in ["", ".", "..", "a/b", "a\\b", "a\u{0085}b", "a\u{0000}"] {
        assert!(validate_prefix_name(name).is_err(), "accepted {name:?}");
    }
    assert_eq!(validate_prefix_name("  Blue  ").unwrap(), "Blue");
    assert!(validate_prefix_name(&"é".repeat(101)).is_err());
    let mut saves = SaveBatch::decode(
        Form::Drawing,
        br#"[{"prefix":"A","b_id":2,"base":{"winning_ticket":-1}}]"#,
    )
    .unwrap();
    assert_eq!(
        saves.validate().unwrap_err().public_detail(),
        "basket 1 (A/2): winning ticket must be zero or more"
    );
}

#[test]
fn duplicate_object_keys_and_case_insensitive_base_match_go() {
    use r_tam_core::models::TicketSave;
    let save: TicketSave = wire::decode(br#"{"PREFIX":"A","t_id":4,"first_name":"Jo","first_name":null,"bAsE":{"pref":"CALL","first_name":"Old"},"base":{"first_name":"New"}}"#).unwrap();
    assert_eq!(save.ticket.first_name, "Jo");
    let base = save.base.unwrap();
    assert_eq!(
        (base.first_name.as_str(), base.pref.as_str()),
        ("New", "CALL")
    );
}

#[test]
fn null_save_list_and_empty_base_preserve_go_defaults() {
    use r_tam_core::models::{Form, SaveBatch};
    let list = SaveBatch::decode(Form::Tickets, b"null").unwrap();
    assert_eq!(wire::go_json(&list).unwrap(), b"[]");
    let list = SaveBatch::decode(
        Form::Prefixes,
        br#"[{"prefix":"A","color":"white","base":{}}]"#,
    )
    .unwrap();
    assert_eq!(
        wire::go_json(&list).unwrap(),
        br#"[{"prefix":"A","color":"white","weight":0,"base":{"color":"","weight":0}}]"#
    );
}

#[test]
fn required_id_uses_final_non_null_duplicate_like_go() {
    assert_eq!(
        wire::decode::<Ticket>(br#"{"t_id":null,"t_id":4}"#)
            .unwrap()
            .t_id,
        4
    );
    assert!(wire::decode::<Ticket>(br#"{"t_id":4,"t_id":null}"#).is_err());
}

#[test]
fn go_field_case_folding_accepts_long_s_in_ascii_names() {
    use r_tam_core::models::TicketSave;
    let save: TicketSave = wire::decode(
        "{\"t_id\":4,\"firſt_name\":\"Jo\",\"baſe\":{\"firſt_name\":\"Old\"}}".as_bytes(),
    )
    .unwrap();
    assert_eq!(save.ticket.first_name, "Jo");
    assert_eq!(save.base.unwrap().first_name, "Old");
}

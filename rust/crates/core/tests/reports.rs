use r_tam_core::{models::*, schema::Role, store::Store};
#[test]
fn report_total_prefix_is_distinct_from_total_and_zero_ticket_is_undrawn() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    st.upsert_tickets(&[
        Ticket {
            prefix: "Total".into(),
            t_id: 0,
            first_name: "zero".into(),
            last_name: "Buyer".into(),
            phone_number: "555".into(),
            ..Default::default()
        },
        Ticket {
            prefix: "A".into(),
            t_id: 1,
            first_name: "zero".into(),
            last_name: "Buyer".into(),
            phone_number: "555".into(),
            ..Default::default()
        },
    ])
    .unwrap();
    st.upsert_baskets(&[Basket {
        prefix: "Total".into(),
        b_id: 1,
        winning_ticket: 0,
        ..Default::default()
    }])
    .unwrap();
    let counts = st.report_counts().unwrap();
    assert_eq!(counts.len(), 3);
    assert_eq!(counts[1].prefix, "Total");
    assert!(!counts[1].is_total);
    assert!(counts[2].is_total);
    assert_eq!(counts[2].unique_buyers, 1);
    assert_eq!(counts[2].total_buys, 2);
    assert_eq!(st.all_drawing().unwrap()[0].first_name, "");
    assert_eq!(st.report_by_name("Total").unwrap()[0].first_name, "");
    assert_eq!(st.report_by_basket("Total").unwrap()[0].first_name, "");
}
#[test]
fn search_is_literal_ascii_case_insensitive_and_supports_nul() {
    let dir = tempfile::tempdir().unwrap();
    let st = Store::open(&dir.path().join("db"), Role::Client).unwrap();
    st.upsert_tickets(&[
        Ticket {
            prefix: "A".into(),
            t_id: 1,
            first_name: "Jo_%\\\0TAIL".into(),
            last_name: "Buyer".into(),
            phone_number: "555".into(),
            ..Default::default()
        },
        Ticket {
            prefix: "A".into(),
            t_id: 2,
            first_name: "Jo".into(),
            last_name: "Buyer".into(),
            phone_number: "555".into(),
            ..Default::default()
        },
    ])
    .unwrap();
    assert_eq!(
        st.search_tickets("_%\\\0tail", "buy", "55").unwrap().len(),
        1
    );
    assert_eq!(st.search_tickets("jo", "", "").unwrap().len(), 2);
}

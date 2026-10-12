use r_tam_core::{
    Result,
    schema::{self, Role},
};
use rusqlite::Connection;
use std::path::Path;

fn columns(db: &Connection, table: &str) -> Vec<String> {
    db.prepare(&format!("PRAGMA table_info({table})"))
        .unwrap()
        .query_map([], |r| r.get(1))
        .unwrap()
        .map(|r| r.unwrap())
        .collect()
}
fn assert_shared(db: &Connection) {
    for (table, expected) in [
        ("prefixes", vec!["prefix", "color", "weight", "rev"]),
        (
            "tickets",
            vec![
                "prefix",
                "t_id",
                "first_name",
                "last_name",
                "phone_number",
                "pref",
                "rev",
            ],
        ),
        (
            "baskets",
            vec![
                "prefix",
                "b_id",
                "description",
                "donors",
                "winning_ticket",
                "rev",
                "win_rev",
            ],
        ),
        ("auth_keys", vec!["auth_key", "description"]),
    ] {
        assert_eq!(columns(db, table), expected);
    }
    assert_eq!(
        db.query_row("PRAGMA integrity_check", [], |r| r.get::<_, String>(0))
            .unwrap(),
        "ok"
    );
    assert_eq!(
        db.query_row("PRAGMA busy_timeout", [], |r| r.get::<_, i64>(0))
            .unwrap(),
        5000
    );
    assert_eq!(
        db.query_row("PRAGMA synchronous", [], |r| r.get::<_, i64>(0))
            .unwrap(),
        2
    );
    assert_eq!(
        db.query_row("PRAGMA journal_mode", [], |r| r.get::<_, String>(0))
            .unwrap(),
        "wal"
    );
}

#[test]
fn fresh_server_exact_schema_event_and_views() -> Result<()> {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("server.db");
    let db = schema::open(&path, Role::Server)?;
    assert_shared(&db);
    for (table, expected) in [
        ("client_saves", vec!["client", "last_save", "last_hash"]),
        ("event", vec!["id", "event", "started", "last_rev"]),
        ("deleted_prefixes", vec!["prefix", "rev"]),
        (
            "auth_key_activity",
            vec!["auth_key", "last_seen", "last_update"],
        ),
    ] {
        assert_eq!(columns(&db, table), expected);
    }
    let event: String = db.query_row("SELECT event FROM event", [], |r| r.get(0))?;
    assert_eq!(event.len(), 32);
    assert!(event.bytes().all(|b| b.is_ascii_hexdigit()));
    db.execute_batch("INSERT INTO tickets VALUES ('A',0,'Ghost','Zero','123','CALL',0); INSERT INTO baskets VALUES ('A',1,'Tea','Donor',0,0,0);")?;
    for view in ["drawing", "report_by_name", "report_by_basket"] {
        assert_eq!(
            db.query_row(&format!("SELECT last_name FROM {view}"), [], |r| r
                .get::<_, Option<String>>(0))?,
            None
        );
    }
    assert_eq!(
        db.query_row(
            "SELECT total_buys FROM report_counts WHERE is_total=1",
            [],
            |r| r.get::<_, i64>(0)
        )?,
        1
    );
    drop(db);
    let reopened = schema::open(&path, Role::Server)?;
    assert_eq!(
        reopened.query_row("SELECT event FROM event", [], |r| r.get::<_, String>(0))?,
        event
    );
    Ok(())
}

#[test]
fn copied_older_go_layout_preserves_rows_unknown_tables_and_upgrades_columns() -> Result<()> {
    let dir = tempfile::tempdir().unwrap();
    let original = dir.path().join("go.db");
    let db = Connection::open(&original)?;
    db.execute_batch("CREATE TABLE prefixes (prefix TEXT PRIMARY KEY,color TEXT,weight INTEGER); INSERT INTO prefixes VALUES ('A','red',2); CREATE TABLE client_saves(client TEXT PRIMARY KEY,last_save INTEGER NOT NULL); INSERT INTO client_saves VALUES ('laptop',41); CREATE TABLE unknown_legacy(x TEXT); INSERT INTO unknown_legacy VALUES ('keep'); CREATE VIEW report_counts AS SELECT 'Totals' AS prefix;")?;
    drop(db);
    let path = dir.path().join("copy.db");
    std::fs::copy(original, &path).unwrap();
    let db = schema::open(&path, Role::Server)?;
    assert_shared(&db);
    assert_eq!(
        db.query_row("SELECT x FROM unknown_legacy", [], |r| r
            .get::<_, String>(0))?,
        "keep"
    );
    assert_eq!(
        db.query_row("SELECT rev FROM prefixes", [], |r| r.get::<_, i64>(0))?,
        0
    );
    assert_eq!(
        db.query_row("SELECT last_save,last_hash FROM client_saves", [], |r| Ok(
            (r.get::<_, i64>(0)?, r.get::<_, String>(1)?)
        ))?,
        (41, "".to_owned())
    );
    assert_eq!(
        db.query_row("SELECT prefix FROM report_counts", [], |r| r
            .get::<_, String>(0))?,
        "Total"
    );
    Ok(())
}

#[test]
fn client_schema_keeps_go_outbox_payload_and_identity() -> Result<()> {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("client.db");
    let db = Connection::open(&path)?;
    db.execute_batch("CREATE TABLE outbox (id INTEGER PRIMARY KEY AUTOINCREMENT,created_at TEXT NOT NULL,method TEXT NOT NULL,path TEXT NOT NULL,body BLOB,attempts INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT ''); INSERT INTO outbox(created_at,method,path,body) VALUES ('2026-10-11T00:00:00Z','POST','/tickets',X'5b5d');")?;
    drop(db);
    let db = schema::open(&path, Role::Client)?;
    assert_shared(&db);
    assert_eq!(
        columns(&db, "outbox"),
        vec![
            "id",
            "created_at",
            "method",
            "path",
            "body",
            "attempts",
            "last_error",
            "client",
            "save_number"
        ]
    );
    assert_eq!(
        columns(&db, "outbox_failed"),
        vec![
            "id",
            "created_at",
            "method",
            "path",
            "body",
            "attempts",
            "last_error",
            "failed_at",
            "client",
            "save_number"
        ]
    );
    assert_eq!(
        columns(&db, "save_order"),
        vec!["id", "client", "host", "last_save"]
    );
    assert_eq!(columns(&db, "mirror"), vec!["id", "event"]);
    assert_eq!(
        db.query_row("SELECT body,client,save_number FROM outbox", [], |r| Ok((
            r.get::<_, Vec<u8>>(0)?,
            r.get::<_, String>(1)?,
            r.get::<_, i64>(2)?
        )))?,
        (b"[]".to_vec(), "".to_owned(), 0)
    );
    Ok(())
}

#[test]
fn server_event_starts_above_existing_go_revision() -> Result<()> {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("revs.db");
    let db = Connection::open(&path)?;
    db.execute_batch("CREATE TABLE baskets(prefix TEXT,b_id INTEGER,description TEXT,donors TEXT,winning_ticket INTEGER,rev INTEGER NOT NULL DEFAULT 0,win_rev INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(prefix,b_id)); INSERT INTO baskets VALUES ('A',1,'','',0,12,52);")?;
    drop(db);
    assert_eq!(
        schema::open(&path, Role::Server)?
            .query_row("SELECT last_rev FROM event", [], |r| r.get::<_, i64>(0))?,
        52
    );
    Ok(())
}

#[test]
fn initialization_failure_rolls_back_schema_and_preserves_existing_view() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("broken.db");
    let db = Connection::open(&path).unwrap();
    db.execute_batch(
        "CREATE TABLE tickets (wrong TEXT); CREATE VIEW drawing AS SELECT 'old' AS description;",
    )
    .unwrap();
    drop(db);
    assert!(schema::open(Path::new(&path), Role::Server).is_err());
    let db = Connection::open(path).unwrap();
    assert_eq!(
        db.query_row("SELECT description FROM drawing", [], |r| r
            .get::<_, String>(0))
            .unwrap(),
        "old"
    );
    assert!(columns(&db, "baskets").is_empty());
}

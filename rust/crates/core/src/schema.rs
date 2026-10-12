//! Permanent idempotent initialization of the exact Go database layout.
use crate::{Error, Result};
use rusqlite::{Connection, TransactionBehavior};
use std::{path::Path, time::Duration};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Role {
    Server,
    Client,
}
const TABLES: &[&str] = &[
    "CREATE TABLE IF NOT EXISTS prefixes (prefix TEXT PRIMARY KEY,color TEXT,weight INTEGER)",
    "CREATE TABLE IF NOT EXISTS tickets (prefix TEXT,t_id INTEGER,first_name TEXT,last_name TEXT,phone_number TEXT,pref TEXT,PRIMARY KEY (prefix,t_id))",
    "CREATE TABLE IF NOT EXISTS baskets (prefix TEXT,b_id INTEGER,description TEXT,donors TEXT,winning_ticket INTEGER,PRIMARY KEY (prefix,b_id))",
    "CREATE TABLE IF NOT EXISTS auth_keys (auth_key TEXT PRIMARY KEY,description TEXT)",
];
const CLIENT_TABLES: &[&str] = &[
    "CREATE TABLE IF NOT EXISTS outbox (id INTEGER PRIMARY KEY AUTOINCREMENT,created_at TEXT NOT NULL,method TEXT NOT NULL,path TEXT NOT NULL,body BLOB,attempts INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '')",
    "CREATE TABLE IF NOT EXISTS outbox_failed (id INTEGER PRIMARY KEY,created_at TEXT NOT NULL,method TEXT NOT NULL,path TEXT NOT NULL,body BLOB,attempts INTEGER NOT NULL DEFAULT 0,last_error TEXT NOT NULL DEFAULT '',failed_at TEXT NOT NULL)",
    "CREATE TABLE IF NOT EXISTS save_order (id INTEGER PRIMARY KEY CHECK (id=1),client TEXT NOT NULL,host TEXT NOT NULL,last_save INTEGER NOT NULL)",
    "CREATE TABLE IF NOT EXISTS mirror (id INTEGER PRIMARY KEY CHECK (id=1),event TEXT NOT NULL)",
];
const SERVER_TABLES: &[&str] = &[
    "CREATE TABLE IF NOT EXISTS client_saves (client TEXT PRIMARY KEY,last_save INTEGER NOT NULL,last_hash TEXT NOT NULL DEFAULT '')",
    "CREATE TABLE IF NOT EXISTS event (id INTEGER PRIMARY KEY CHECK (id=1),event TEXT NOT NULL,started TEXT NOT NULL,last_rev INTEGER NOT NULL DEFAULT 0)",
    "CREATE TABLE IF NOT EXISTS deleted_prefixes (prefix TEXT PRIMARY KEY,rev INTEGER NOT NULL)",
    "CREATE TABLE IF NOT EXISTS auth_key_activity (auth_key TEXT PRIMARY KEY)",
];
const VIEWS: &[(&str, &str)] = &[
    (
        "drawing",
        "CREATE VIEW drawing AS SELECT b.prefix,b.b_id,b.description,b.winning_ticket,t.last_name,t.first_name,t.phone_number,b.win_rev FROM baskets b LEFT JOIN tickets t ON b.prefix=t.prefix AND b.winning_ticket=t.t_id AND b.winning_ticket>0 ORDER BY b.prefix,b.b_id",
    ),
    (
        "report_by_name",
        "CREATE VIEW report_by_name AS SELECT t.last_name,t.first_name,t.phone_number,t.pref,b.prefix,b.b_id,b.description,b.donors,b.winning_ticket FROM baskets b LEFT JOIN tickets t ON b.prefix=t.prefix AND b.winning_ticket=t.t_id AND b.winning_ticket>0 ORDER BY t.last_name,t.first_name,t.phone_number,b.prefix,b.b_id",
    ),
    (
        "report_by_basket",
        "CREATE VIEW report_by_basket AS SELECT b.prefix,b.b_id,b.description,b.donors,b.winning_ticket,t.last_name,t.first_name,t.phone_number,t.pref FROM baskets b LEFT JOIN tickets t ON b.prefix=t.prefix AND b.winning_ticket=t.t_id AND b.winning_ticket>0 ORDER BY b.prefix,b.b_id",
    ),
    (
        "report_counts",
        "CREATE VIEW report_counts AS SELECT prefix,0 AS is_total,COUNT(*) AS unique_buyers,SUM(purchases) AS total_buys FROM (SELECT prefix,first_name,last_name,phone_number,COUNT(*) AS purchases FROM tickets GROUP BY prefix,first_name,last_name,phone_number) GROUP BY prefix UNION ALL SELECT 'Total',1,COUNT(*),coalesce(SUM(purchases),0) FROM (SELECT first_name,last_name,phone_number,COUNT(*) AS purchases FROM tickets GROUP BY first_name,last_name,phone_number)",
    ),
];
fn ensure_column(db: &Connection, table: &str, column: &str, decl: &str) -> Result<()> {
    // Names are static identifiers from the Go schema, never caller input.
    let mut statement = db.prepare(&format!("PRAGMA table_info({table})"))?;
    let columns = statement
        .query_map([], |r| r.get::<_, String>(1))?
        .collect::<rusqlite::Result<Vec<_>>>()?;
    if !columns.iter().any(|c| c == column) {
        db.execute_batch(&format!("ALTER TABLE {table} ADD COLUMN {column} {decl}"))?;
    }
    Ok(())
}
pub fn open(path: &Path, role: Role) -> Result<Connection> {
    let mut db = Connection::open(path)?;
    db.busy_timeout(Duration::from_millis(5000))?;
    db.pragma_update(None, "journal_mode", "WAL")?;
    db.pragma_update(None, "synchronous", "FULL")?;
    let tx = db.transaction_with_behavior(TransactionBehavior::Immediate)?;
    for table in TABLES {
        tx.execute_batch(table)?;
    }
    for (table, column) in [
        ("prefixes", "rev"),
        ("tickets", "rev"),
        ("baskets", "rev"),
        ("baskets", "win_rev"),
    ] {
        ensure_column(&tx, table, column, "INTEGER NOT NULL DEFAULT 0")?;
    }
    for (name, sql) in VIEWS {
        tx.execute_batch(&format!("DROP VIEW IF EXISTS {name}; {sql}"))?;
    }
    match role {
        Role::Client => {
            for table in CLIENT_TABLES {
                tx.execute_batch(table)?;
            }
            for table in ["outbox", "outbox_failed"] {
                ensure_column(&tx, table, "client", "TEXT NOT NULL DEFAULT ''")?;
                ensure_column(&tx, table, "save_number", "INTEGER NOT NULL DEFAULT 0")?;
            }
        }
        Role::Server => {
            for table in SERVER_TABLES {
                tx.execute_batch(table)?;
            }
            ensure_column(&tx, "client_saves", "last_hash", "TEXT NOT NULL DEFAULT ''")?;
            for col in ["last_seen", "last_update"] {
                ensure_column(&tx, "auth_key_activity", col, "TEXT")?;
            }
            let has: bool = tx.query_row("SELECT EXISTS(SELECT 1 FROM event)", [], |r| r.get(0))?;
            if !has {
                let mut random = [0_u8; 16];
                getrandom::fill(&mut random)
                    .map_err(|e| Error::Internal(format!("name the event: {e}")))?;
                let event = random
                    .iter()
                    .map(|b| format!("{b:02x}"))
                    .collect::<String>();
                let started = time::OffsetDateTime::now_utc()
                    .replace_nanosecond(0)
                    .map_err(|e| Error::Internal(e.to_string()))?
                    .format(&time::format_description::well_known::Rfc3339)
                    .map_err(|e| Error::Internal(e.to_string()))?;
                tx.execute("INSERT INTO event (id,event,started,last_rev) SELECT 1,?1,?2,(SELECT max(0,coalesce(max(rev),0)) FROM (SELECT rev FROM prefixes UNION ALL SELECT rev FROM tickets UNION ALL SELECT rev FROM baskets UNION ALL SELECT win_rev FROM baskets))",rusqlite::params![event,started])?;
            }
        }
    }
    // SQLite validates views lazily; resolve their columns before committing so
    // incompatible existing schemas cannot leave a partially initialized copy.
    for (name, _) in VIEWS {
        tx.prepare(&format!("SELECT * FROM {name} LIMIT 0"))?;
    }
    tx.commit()?;
    Ok(db)
}

//! Transactional Go-compatible event persistence.
mod edits;
mod event;
mod order;
mod queue;
#[cfg(test)]
mod read_reuse_tests;
mod reports;
mod rows;
use crate::{
    Error, Result,
    schema::{self, Role},
};
pub use edits::{SaveResult, StoredRows, analyze_result, result_of};
pub use event::{MergeResult, Snapshot};
pub use order::{Operation, OperationResult, OrderedResult};
pub use queue::{QueueItem, QueuedSave, save_path};
use rusqlite::{Connection, Transaction, TransactionBehavior};
use std::{
    ops::{Deref, DerefMut},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::Duration,
};
#[derive(Clone)]
pub struct Store {
    inner: Arc<Inner>,
}
struct Inner {
    path: PathBuf,
    role: Role,
    writer: Mutex<Connection>,
    readers: Mutex<Vec<Connection>>,
}
const MAX_IDLE_READERS: usize = 4;
struct ReadConnection<'a> {
    db: Option<Connection>,
    idle: &'a Mutex<Vec<Connection>>,
}
impl Deref for ReadConnection<'_> {
    type Target = Connection;
    fn deref(&self) -> &Self::Target {
        self.db.as_ref().expect("checked-out read connection")
    }
}
impl DerefMut for ReadConnection<'_> {
    fn deref_mut(&mut self) -> &mut Self::Target {
        self.db.as_mut().expect("checked-out read connection")
    }
}
impl Drop for ReadConnection<'_> {
    fn drop(&mut self) {
        let Some(db) = self.db.take() else {
            return;
        };
        // A returned reader must not carry a snapshot, active statement or
        // uncertain transaction into another operation. Closing it discards
        // that state instead of reusing it.
        if !db.is_autocommit() || db.is_busy() {
            return;
        }
        if let Ok(mut idle) = self.idle.lock()
            && idle.len() < MAX_IDLE_READERS
        {
            idle.push(db);
        }
    }
}
impl Store {
    pub fn open(path: &Path, role: Role) -> Result<Self> {
        let path = std::path::absolute(path)?;
        let mut db = schema::open(&path, role)?;
        let tx = db.transaction_with_behavior(TransactionBehavior::Immediate)?;
        tx.execute_batch("CREATE TABLE IF NOT EXISTS rust_store_clock(id INTEGER PRIMARY KEY CHECK(id=1),generation INTEGER NOT NULL); INSERT OR IGNORE INTO rust_store_clock VALUES(1,0); CREATE TABLE IF NOT EXISTS rust_row_changes(kind TEXT NOT NULL,prefix TEXT NOT NULL,row_id INTEGER NOT NULL,generation INTEGER NOT NULL,PRIMARY KEY(kind,prefix,row_id));")?;
        for (table, id) in [("prefixes", "0"), ("tickets", "t_id"), ("baskets", "b_id")] {
            for (action, record) in [("INSERT", "NEW"), ("UPDATE", "NEW"), ("DELETE", "OLD")] {
                let rowid = if id == "0" {
                    "0".to_owned()
                } else {
                    format!("{record}.{id}")
                };
                tx.execute_batch(&format!("CREATE TRIGGER IF NOT EXISTS rust_change_{table}_{action} AFTER {action} ON {table} BEGIN SELECT CASE WHEN (SELECT generation FROM rust_store_clock WHERE id=1)=9223372036854775807 THEN RAISE(ABORT,'row generation exhausted') END; UPDATE rust_store_clock SET generation=generation+1 WHERE id=1; INSERT INTO rust_row_changes(kind,prefix,row_id,generation) SELECT '{table}',{record}.prefix,{rowid},generation FROM rust_store_clock WHERE id=1 ON CONFLICT(kind,prefix,row_id) DO UPDATE SET generation=excluded.generation; END;"))?;
            }
        }
        // Basket forms own separate components. These markers let acknowledgments
        // stamp one component even when a later save edits the other.
        for (kind, condition) in [
            (
                "basket_descriptions",
                "OLD.description IS NOT NEW.description OR OLD.donors IS NOT NEW.donors OR OLD.rev IS NOT NEW.rev",
            ),
            (
                "winning",
                "OLD.winning_ticket IS NOT NEW.winning_ticket OR OLD.win_rev IS NOT NEW.win_rev",
            ),
        ] {
            for action in ["INSERT", "UPDATE"] {
                let when = if action == "UPDATE" {
                    format!("WHEN {condition}")
                } else {
                    String::new()
                };
                tx.execute_batch(&format!("CREATE TRIGGER IF NOT EXISTS rust_component_{kind}_{action} AFTER {action} ON baskets {when} BEGIN SELECT CASE WHEN (SELECT generation FROM rust_store_clock WHERE id=1)=9223372036854775807 THEN RAISE(ABORT,'row generation exhausted') END; UPDATE rust_store_clock SET generation=generation+1 WHERE id=1; INSERT INTO rust_row_changes(kind,prefix,row_id,generation) SELECT '{kind}',NEW.prefix,NEW.b_id,generation FROM rust_store_clock WHERE id=1 ON CONFLICT(kind,prefix,row_id) DO UPDATE SET generation=excluded.generation; END;"))?;
            }
        }
        if role == Role::Client {
            tx.execute_batch("CREATE TABLE IF NOT EXISTS rust_intents(id INTEGER PRIMARY KEY,accepted INTEGER NOT NULL,generation INTEGER NOT NULL);")?;
        }
        tx.commit()?;
        Ok(Self {
            inner: Arc::new(Inner {
                path,
                role,
                writer: Mutex::new(db),
                readers: Mutex::new(Vec::new()),
            }),
        })
    }
    pub fn path(&self) -> &Path {
        &self.inner.path
    }
    pub fn role(&self) -> Role {
        self.inner.role
    }
    fn read(&self) -> Result<ReadConnection<'_>> {
        // Hold the cache lock only for checkout. Each active read owns its
        // connection and runs independently of readers and the writer.
        let cached = self
            .inner
            .readers
            .lock()
            .map_err(|_| Error::Internal("store reader cache lock poisoned".into()))?
            .pop();
        let db = match cached {
            Some(db) => db,
            None => {
                let db = Connection::open(&self.inner.path)?;
                db.busy_timeout(Duration::from_secs(5))?;
                db
            }
        };
        Ok(ReadConnection {
            db: Some(db),
            idle: &self.inner.readers,
        })
    }
    fn write<T>(&self, f: impl FnOnce(&Transaction<'_>) -> Result<T>) -> Result<T> {
        let mut db = self
            .inner
            .writer
            .lock()
            .map_err(|_| Error::Internal("store writer lock poisoned".into()))?;
        let tx = db.transaction_with_behavior(TransactionBehavior::Immediate)?;
        let value = f(&tx)?;
        tx.commit()?;
        Ok(value)
    }
    pub fn generation(&self) -> Result<i64> {
        generation(&*self.read()?)
    }
}
fn generation(db: &Connection) -> Result<i64> {
    Ok(db.query_row(
        "SELECT generation FROM rust_store_clock WHERE id=1",
        [],
        |r| r.get(0),
    )?)
}
fn row_generation(db: &Connection, kind: &str, prefix: &str, id: i64) -> Result<i64> {
    use rusqlite::OptionalExtension;
    Ok(db
        .query_row(
            "SELECT generation FROM rust_row_changes WHERE kind=?1 AND prefix=?2 AND row_id=?3",
            rusqlite::params![kind, prefix, id],
            |r| r.get(0),
        )
        .optional()?
        .unwrap_or(0))
}
fn has_table(db: &Connection, name: &str) -> Result<bool> {
    Ok(db.query_row(
        "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?1)",
        [name],
        |r| r.get(0),
    )?)
}
fn now() -> Result<String> {
    time::OffsetDateTime::now_utc()
        .format(&time::format_description::well_known::Rfc3339)
        .map_err(|e| Error::Internal(e.to_string()))
}
fn random_hex(bytes: usize) -> Result<String> {
    let mut value = vec![0; bytes];
    getrandom::fill(&mut value).map_err(|e| Error::Internal(e.to_string()))?;
    Ok(value.iter().map(|b| format!("{b:02x}")).collect())
}
fn exhausted(name: &str) -> Error {
    Error::Validation(format!("{name} counter exhausted"))
}

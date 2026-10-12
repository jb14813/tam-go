use super::*;
use crate::models::*;
use rusqlite::{OptionalExtension, params};
#[derive(Debug, Clone)]
pub enum Operation {
    Save(SaveBatch),
    DeletePrefix(String),
    Import(BackupFile),
    MergeNewer(BackupFile),
}
#[derive(Debug, Clone)]
pub enum OperationResult {
    Rows(StoredRows),
    DeletedPrefix(Option<Prefix>),
    Imported,
    Merged(MergeResult),
}
#[derive(Debug, Clone)]
pub struct OrderedResult {
    pub outcome: Outcome,
    pub last: i64,
    pub result: Option<OperationResult>,
}
pub(super) fn next_save_in(db: &Connection, host: &str, after: Option<i64>) -> Result<Order> {
    let prior: Option<(String, String, i64)> = db
        .query_row(
            "SELECT client,host,last_save FROM save_order WHERE id=1",
            [],
            |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
        )
        .optional()?;
    let (mut client, known, last) = prior.unwrap_or_default();
    let last = last.max(after.unwrap_or(0));
    let save = last.checked_add(1).ok_or_else(|| exhausted("save"))?;
    if client.is_empty() || known != host {
        client = random_hex(12)?;
    }
    db.execute("INSERT INTO save_order(id,client,host,last_save) VALUES(1,?1,?2,?3) ON CONFLICT(id) DO UPDATE SET client=excluded.client,host=excluded.host,last_save=excluded.last_save",params![client,host,save])?;
    Ok(Order { client, save })
}
pub(super) fn apply_in(db: &Connection, op: &Operation) -> Result<OperationResult> {
    Ok(match op {
        Operation::Save(s) => OperationResult::Rows(super::edits::save_in(db, s)?),
        Operation::DeletePrefix(p) => {
            OperationResult::DeletedPrefix(super::rows::delete_prefix_in(db, p)?)
        }
        Operation::Import(bf) => {
            super::event::import_in(db, bf)?;
            OperationResult::Imported
        }
        Operation::MergeNewer(bf) => OperationResult::Merged(super::event::merge_in(db, bf)?),
    })
}
impl Store {
    pub fn next_save(&self, host: &str, after: Option<i64>) -> Result<Order> {
        self.write(|tx| next_save_in(tx, host, after))
    }
    pub fn skip_saves_to(&self, last: i64) -> Result<()> {
        self.write(|tx| {
            tx.execute(
                "UPDATE save_order SET last_save=?1 WHERE id=1 AND last_save<?1",
                [last],
            )?;
            Ok(())
        })
    }
    pub fn apply_operation(&self, op: &Operation) -> Result<OperationResult> {
        self.write(|tx| apply_in(tx, op))
    }
    pub fn apply_ordered(
        &self,
        order: &Order,
        digest: &str,
        op: &Operation,
    ) -> Result<OrderedResult> {
        self.write(|tx| {
            if order.client.is_empty() && order.save == 0 {
                return Ok(OrderedResult {
                    outcome: Outcome::Applied,
                    last: 0,
                    result: Some(apply_in(tx, op)?),
                });
            }
            if order.client.is_empty() || order.save <= 0 {
                return Err(Error::Validation(
                    "a numbered save requires a client and positive save number".into(),
                ));
            }
            let prior: Option<(i64, String)> = tx.query_row(
                "SELECT last_save,last_hash FROM client_saves WHERE client=?1",
                [&order.client],
                |r| Ok((r.get(0)?, r.get(1)?)),
            ).optional()?;
            if let Some((last, hash)) = prior && order.save <= last {
                return Ok(OrderedResult {
                    outcome: if order.save == last && digest == hash {
                        Outcome::Repeat
                    } else {
                        Outcome::Behind
                    },
                    last,
                    result: None,
                });
            }
            let result = apply_in(tx, op)?;
            tx.execute(
                "INSERT INTO client_saves(client,last_save,last_hash) VALUES(?1,?2,?3)
                 ON CONFLICT(client) DO UPDATE SET last_save=excluded.last_save,last_hash=excluded.last_hash",
                params![order.client, order.save, digest],
            )?;
            Ok(OrderedResult {
                outcome: Outcome::Applied,
                last: order.save,
                result: Some(result),
            })
        })
    }
}

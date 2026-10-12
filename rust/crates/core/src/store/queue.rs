use super::edits::{batch_json, copy_unless_newer, save_in};
use super::rows::{int, query, text};
use super::*;
use crate::{models::*, wire};
use rusqlite::{OptionalExtension, Row, params};
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct QueueItem {
    pub id: i64,
    pub created_at: String,
    pub method: String,
    pub path: String,
    pub body: Vec<u8>,
    pub attempts: i64,
    pub last_error: String,
    pub order: Order,
}
#[derive(Debug, Clone)]
pub struct QueuedSave {
    pub item: QueueItem,
    pub result: SaveResult,
}
const COLS: &str = "id,created_at,method,path,body,attempts,last_error,client,save_number";
fn queue_row(r: &Row<'_>) -> rusqlite::Result<QueueItem> {
    Ok(QueueItem {
        id: int(r, 0)?,
        created_at: text(r, 1)?,
        method: text(r, 2)?,
        path: text(r, 3)?,
        body: r.get::<_, Option<Vec<u8>>>(4)?.unwrap_or_default(),
        attempts: int(r, 5)?,
        last_error: text(r, 6)?,
        order: Order {
            client: text(r, 7)?,
            save: int(r, 8)?,
        },
    })
}
fn item_in(db: &Connection, id: i64) -> Result<QueueItem> {
    Ok(db.query_row(
        &format!("SELECT {COLS} FROM outbox WHERE id=?1"),
        [id],
        queue_row,
    )?)
}
fn insert_in(
    db: &Connection,
    method: &str,
    path: &str,
    body: &[u8],
    order: &Order,
    accepted: bool,
) -> Result<QueueItem> {
    db.execute("INSERT INTO outbox(created_at,method,path,body,client,save_number) VALUES(?1,?2,?3,?4,?5,?6)",params![now()?,method,path,body,order.client,order.save])?;
    let id = db.last_insert_rowid();
    db.execute(
        "INSERT INTO rust_intents(id,accepted,generation) VALUES(?1,?2,?3)",
        params![id, accepted, generation(db)?],
    )?;
    item_in(db, id)
}
fn remove_in(db: &Connection, id: i64) -> Result<()> {
    db.execute("DELETE FROM outbox WHERE id=?1", [id])?;
    db.execute("DELETE FROM rust_intents WHERE id=?1", [id])?;
    Ok(())
}
pub(super) fn fail_all_in(db: &Connection, reason: &str) -> Result<i64> {
    let n=db.execute("INSERT INTO outbox_failed(id,created_at,method,path,body,attempts,last_error,failed_at,client,save_number) SELECT id,created_at,method,path,body,attempts,?1,?2,client,save_number FROM outbox ORDER BY id",params![reason,now()?])?;
    db.execute("DELETE FROM outbox", [])?;
    Ok(n as i64)
}
fn path_form(path: &str) -> Option<Form> {
    let path = path.split('?').next().unwrap_or(path);
    if matches!(path, "/api/search/tickets" | "/search/tickets") {
        Some(Form::Tickets)
    } else {
        Form::from_path(path)
    }
}
pub fn save_path(method: &str, path: &str) -> bool {
    method == "POST" && path_form(path).is_some()
}
fn validated_saves(method: &str, path: &str, body: &[u8]) -> Result<Option<SaveBatch>> {
    if !save_path(method, path) {
        return Ok(None);
    }
    let mut batch = SaveBatch::decode(path_form(path).expect("checked form"), body)?;
    batch.validate()?;
    Ok(Some(batch))
}
fn delete_name(path: &str) -> Result<String> {
    let url = reqwest::Url::parse(&format!("http://localhost{path}"))
        .map_err(|_| Error::Validation("invalid prefix delete path".into()))?;
    if !matches!(url.path(), "/api/prefixes" | "/prefixes") {
        return Err(Error::Validation(
            "this request is not a prefix delete".into(),
        ));
    }
    url.query_pairs()
        .find(|(key, _)| key == "p")
        .map(|(_, name)| name.into_owned())
        .filter(|name| !name.is_empty())
        .ok_or_else(|| Error::Validation("p is required".into()))
}
impl Store {
    /// Persist an unaccepted online intent and number before dispatch. The exact
    /// returned body and identity are the bytes to send and repeat.
    pub fn journal_online(
        &self,
        host: &str,
        method: &str,
        path: &str,
        body: &[u8],
    ) -> Result<QueueItem> {
        if method == "DELETE" {
            delete_name(path)?;
        }
        let body = if let Some(s) = validated_saves(method, path, body)? {
            batch_json(&s)?
        } else {
            body.to_vec()
        };
        self.write(|tx| {
            let order = super::order::next_save_in(tx, host, None)?;
            insert_in(tx, method, path, &body, &order, false)
        })
    }
    pub fn pending(&self) -> Result<Vec<QueueItem>> {
        query(
            &*self.read()?,
            &format!("SELECT {COLS} FROM outbox ORDER BY id"),
            [],
            queue_row,
        )
    }
    pub fn failed(&self) -> Result<Vec<QueueItem>> {
        query(
            &*self.read()?,
            &format!("SELECT {COLS} FROM outbox_failed ORDER BY id"),
            [],
            queue_row,
        )
    }
    pub fn next_outbox(&self) -> Result<Option<QueueItem>> {
        Ok(self
            .read()?
            .query_row(
                &format!("SELECT {COLS} FROM outbox ORDER BY id LIMIT 1"),
                [],
                queue_row,
            )
            .optional()?)
    }
    pub fn outbox_counts(&self) -> Result<(i64, i64)> {
        Ok(self.read()?.query_row(
            "SELECT (SELECT count(*) FROM outbox),(SELECT count(*) FROM outbox_failed)",
            [],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )?)
    }
    pub fn outbox_waiting(&self) -> Result<bool> {
        Ok(self.outbox_counts()?.0 > 0)
    }
    pub fn accept_offline(
        &self,
        host: &str,
        method: &str,
        path: &str,
        saves: &SaveBatch,
    ) -> Result<QueuedSave> {
        if method != "POST" || path_form(path) != Some(saves.form()) {
            return Err(Error::Validation(
                "the request does not match its save form".into(),
            ));
        }
        let mut saves = saves.clone();
        saves.validate()?;
        self.write(|tx| {
            let rows = save_in(tx, &saves)?;
            let result = analyze_result(&saves, rows)?;
            let order = super::order::next_save_in(tx, host, None)?;
            let item = insert_in(tx, method, path, &result.kept_body()?, &order, true)?;
            Ok(QueuedSave { item, result })
        })
    }
    /// A transport error can accept the journaled intent offline. Its numbered
    /// request stays byte-identical because the server may already have seen it.
    pub fn accept_journal_offline(&self, id: i64) -> Result<SaveResult> {
        self.write(|tx| {
            let accepted: Option<bool> = tx
                .query_row("SELECT accepted FROM rust_intents WHERE id=?1", [id], |r| {
                    r.get(0)
                })
                .optional()?;
            if accepted != Some(false) {
                return Err(Error::Validation(
                    "this intent was already accepted locally".into(),
                ));
            }
            let item = item_in(tx, id)?;
            let saves = validated_saves(&item.method, &item.path, &item.body)?
                .ok_or_else(|| Error::Validation("this request is not a form save".into()))?;
            let result = analyze_result(&saves, save_in(tx, &saves)?)?;
            tx.execute(
                "UPDATE rust_intents SET accepted=1,generation=?1 WHERE id=?2",
                params![generation(tx)?, id],
            )?;
            Ok(result)
        })
    }
    pub fn accept_delete_offline(
        &self,
        host: &str,
        path: &str,
        prefix: &str,
    ) -> Result<(QueueItem, Prefix)> {
        if delete_name(path)? != prefix {
            return Err(Error::Validation("the request names another prefix".into()));
        }
        self.write(|tx| {
            let gone = super::rows::delete_prefix_in(tx, prefix)?
                .ok_or_else(|| Error::Validation("Prefix not found".into()))?;
            let order = super::order::next_save_in(tx, host, None)?;
            let item = insert_in(tx, "DELETE", path, &[], &order, true)?;
            Ok((item, gone))
        })
    }
    /// Accept an ambiguously delivered DELETE locally without changing the
    /// request body or its save number.
    pub fn accept_delete_journal_offline(&self, id: i64) -> Result<Option<Prefix>> {
        self.write(|tx| {
            let accepted: Option<bool> = tx
                .query_row("SELECT accepted FROM rust_intents WHERE id=?1", [id], |r| {
                    r.get(0)
                })
                .optional()?;
            if accepted != Some(false) {
                return Err(Error::Validation(
                    "this intent was already accepted locally".into(),
                ));
            }
            let item = item_in(tx, id)?;
            if item.method != "DELETE" {
                return Err(Error::Validation("this request is not a delete".into()));
            }
            let prefix = delete_name(&item.path)?;
            let gone = super::rows::delete_prefix_in(tx, &prefix)?;
            tx.execute(
                "UPDATE rust_intents SET accepted=1,generation=?1 WHERE id=?2",
                params![generation(tx)?, id],
            )?;
            Ok(gone)
        })
    }
    /// Validate every identity and its response position before writing rows or
    /// deleting the journal. Malformed success leaves both untouched.
    pub fn reconcile(&self, id: i64, answer: &[u8]) -> Result<SaveResult> {
        self.write(|tx| {
            let item = item_in(tx, id)?;
            let saves = validated_saves(&item.method, &item.path, &item.body)?
                .ok_or_else(|| Error::Validation("this request is not a form save".into()))?;
            let rows = StoredRows::decode(saves.form(), answer)?;
            let result = analyze_result(&saves, rows)?;
            let marker: Option<i64> = tx.query_row(
                "SELECT generation FROM rust_intents WHERE id=?1", [id], |r| r.get(0),
            ).optional()?;
            copy_unless_newer(tx, &saves, &result.rows, marker)?;
            if let Some(again) = result.again_body()? {
                let attempts = item.attempts.checked_add(1).ok_or_else(|| exhausted("attempt"))?;
                tx.execute(
                    "INSERT INTO outbox_failed(id,created_at,method,path,body,attempts,last_error,failed_at,client,save_number)
                     SELECT id,created_at,method,path,?1,?2,?3,?4,'',0 FROM outbox WHERE id=?5",
                    params![again, attempts, result.conflict_text(), now()?, id],
                )?;
                // This refused subset was never accepted in the local copy.
                // Retry can copy its answer unless a subsequent edit intervenes.
                tx.execute(
                    "INSERT INTO rust_intents(id,accepted,generation) VALUES(?1,0,?2)
                     ON CONFLICT(id) DO UPDATE SET accepted=0,generation=excluded.generation",
                    params![id, generation(tx)?],
                )?;
                tx.execute("DELETE FROM outbox WHERE id=?1", [id])?;
            } else {
                remove_in(tx, id)?;
            }
            Ok(result)
        })
    }
    /// Successful DELETE response; caller may pass None for a definitive 404.
    pub fn reconcile_delete(&self, id: i64, prefix: &str, answer: Option<&[u8]>) -> Result<()> {
        let returned = answer.map(wire::decode::<Prefix>).transpose()?;
        if returned.as_ref().is_some_and(|p| p.prefix != prefix) {
            return Err(Error::Validation(
                "the answer does not name the deleted prefix".into(),
            ));
        }
        self.write(|tx| {
            let item = item_in(tx, id)?;
            if item.method != "DELETE" {
                return Err(Error::Validation("this request is not a delete".into()));
            }
            if delete_name(&item.path)? != prefix {
                return Err(Error::Validation("the request names another prefix".into()));
            }
            let marker: Option<i64> = tx
                .query_row(
                    "SELECT generation FROM rust_intents WHERE id=?1",
                    [id],
                    |r| r.get(0),
                )
                .optional()?;
            let unchanged = match marker {
                Some(g) => row_generation(tx, "prefixes", prefix, 0)? <= g,
                None => true,
            };
            if unchanged {
                super::rows::delete_prefix_in(tx, prefix)?;
            }
            remove_in(tx, id)
        })
    }
    /// Only a never-accepted online intent can be discarded after a definitive
    /// rejection. Auth/transport/malformed-success failures must remain pending.
    pub fn reject_online(&self, id: i64) -> Result<()> {
        self.write(|tx| {
            let accepted: Option<bool> = tx
                .query_row("SELECT accepted FROM rust_intents WHERE id=?1", [id], |r| {
                    r.get(0)
                })
                .optional()?;
            if accepted != Some(false) {
                return Err(Error::Validation(
                    "an accepted local save must be kept in the failed list".into(),
                ));
            }
            remove_in(tx, id)
        })
    }
    pub fn note_outbox_attempt(&self, id: i64, reason: &str) -> Result<()> {
        self.write(|tx| {
            let attempts: i64 =
                tx.query_row("SELECT attempts FROM outbox WHERE id=?1", [id], |r| {
                    r.get(0)
                })?;
            let attempts = attempts
                .checked_add(1)
                .ok_or_else(|| exhausted("attempt"))?;
            tx.execute(
                "UPDATE outbox SET attempts=?1,last_error=?2 WHERE id=?3",
                params![attempts, reason, id],
            )?;
            Ok(())
        })
    }
    pub fn fail_outbox(&self, id: i64, reason: &str) -> Result<()> {
        self.write(|tx| {
            let item = item_in(tx, id)?;
            let attempts = item.attempts.checked_add(1).ok_or_else(|| exhausted("attempt"))?;
            tx.execute(
                "INSERT INTO outbox_failed(id,created_at,method,path,body,attempts,last_error,failed_at,client,save_number)
                 SELECT id,created_at,method,path,body,?1,?2,?3,client,save_number FROM outbox WHERE id=?4",
                params![attempts, reason, now()?, id],
            )?;
            tx.execute("DELETE FROM outbox WHERE id=?1", [id])?;
            Ok(())
        })
    }
    pub fn fail_all_outbox(&self, reason: &str) -> Result<i64> {
        self.write(|tx| fail_all_in(tx, reason))
    }
    pub fn renumber_outbox(&self, id: i64, host: &str, last: i64) -> Result<Order> {
        self.write(|tx| {
            item_in(tx, id)?;
            let others: bool = tx.query_row(
                "SELECT EXISTS(SELECT 1 FROM outbox WHERE id<>?1)", [id], |r| r.get(0),
            )?;
            if others {
                return Err(Error::Validation(
                    "quarantine this save and retry it at the end of the queue to preserve save order".into(),
                ));
            }
            let order = super::order::next_save_in(tx, host, Some(last))?;
            tx.execute("UPDATE outbox SET client=?1,save_number=?2 WHERE id=?3", params![order.client,order.save,id])?;
            Ok(order)
        })
    }
    pub fn retry_failed(&self, host: &str) -> Result<i64> {
        self.write(|tx| {
            let failed = query(tx, &format!("SELECT {COLS} FROM outbox_failed ORDER BY id"), [], queue_row)?;
            for item in &failed {
                let meta: Option<(bool,i64)> = tx.query_row(
                    "SELECT accepted,generation FROM rust_intents WHERE id=?1", [item.id],
                    |r| Ok((r.get(0)?,r.get(1)?)),
                ).optional()?;
                let order = super::order::next_save_in(tx, host, None)?;
                tx.execute(
                    "INSERT INTO outbox(created_at,method,path,body,attempts,last_error,client,save_number)
                     VALUES(?1,?2,?3,?4,?5,?6,?7,?8)",
                    params![item.created_at,item.method,item.path,item.body,item.attempts,item.last_error,order.client,order.save],
                )?;
                let new_id = tx.last_insert_rowid();
                tx.execute("DELETE FROM rust_intents WHERE id=?1", [item.id])?;
                if let Some((accepted,g)) = meta {
                    tx.execute("INSERT INTO rust_intents(id,accepted,generation) VALUES(?1,?2,?3)",params![new_id,accepted,g])?;
                }
            }
            tx.execute("DELETE FROM outbox_failed", [])?;
            Ok(failed.len() as i64)
        })
    }
    pub fn discard_failed(&self) -> Result<i64> {
        self.write(|tx| {
            tx.execute(
                "DELETE FROM rust_intents WHERE id IN(SELECT id FROM outbox_failed)",
                [],
            )?;
            Ok(tx.execute("DELETE FROM outbox_failed", [])? as i64)
        })
    }
}

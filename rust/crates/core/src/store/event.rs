use super::rows::*;
use super::*;
use crate::models::*;
use rusqlite::{OptionalExtension, Statement, params};
#[derive(Debug, Clone, Default, serde::Serialize)]
pub struct MergeResult {
    pub added: i64,
    pub updated: i64,
    pub kept: i64,
}
/// Archive backup exclusively and flush it before calling transition_copy.
#[derive(Debug, Clone)]
pub struct Snapshot {
    pub backup: BackupFile,
    pub generation: i64,
}
pub(super) fn event_in(db: &Connection) -> Result<Option<Event>> {
    if !has_table(db, "event")? {
        return Ok(None);
    }
    Ok(Some(db.query_row(
        "SELECT event,started,last_rev FROM event WHERE id=1",
        [],
        |r| {
            Ok(Event {
                event: r.get(0)?,
                started: r.get(1)?,
                last_rev: r.get(2)?,
            })
        },
    )?))
}
pub(super) fn event_name(db: &Connection) -> Result<String> {
    if let Some(ev) = event_in(db)? {
        return Ok(ev.event);
    }
    Ok(db
        .query_row("SELECT event FROM mirror WHERE id=1", [], |r| r.get(0))
        .optional()?
        .unwrap_or_default())
}
pub(super) fn stamp(db: &Connection, old: i64) -> Result<i64> {
    let Some(ev) = event_in(db)? else {
        return Ok(old);
    };
    let rev = next_revision(ev.last_rev)?;
    db.execute("UPDATE event SET last_rev=?1 WHERE id=1", [rev])?;
    Ok(rev)
}
fn next_revision(last_rev: i64) -> Result<i64> {
    // A committed revision must remain readable by both native wire decoders.
    if last_rev >= MAX_EXACT {
        return Err(exhausted("revision"));
    }
    last_rev.checked_add(1).ok_or_else(|| exhausted("revision"))
}
enum RevisionState<'a> {
    Client,
    Server {
        counter: Statement<'a>,
        increment: Statement<'a>,
        first: Option<i64>,
    },
}
/// A save batch resolves its event only when a row actually changes. The
/// prepared statements stay inside that transaction; revisions are still read
/// from the database for each changed row, including changes made by triggers.
pub(super) struct BatchStamper<'a> {
    db: &'a Connection,
    state: Option<RevisionState<'a>>,
}
impl<'a> BatchStamper<'a> {
    pub(super) fn new(db: &'a Connection) -> Self {
        Self { db, state: None }
    }
    pub(super) fn stamp(&mut self, old: i64) -> Result<i64> {
        if self.state.is_none() {
            self.state = Some(match event_in(self.db)? {
                Some(event) => RevisionState::Server {
                    counter: self.db.prepare("SELECT last_rev FROM event WHERE id=1")?,
                    increment: self.db.prepare("UPDATE event SET last_rev=?1 WHERE id=1")?,
                    first: Some(event.last_rev),
                },
                None => RevisionState::Client,
            });
        }
        match self.state.as_mut().expect("batch revision state resolved") {
            RevisionState::Client => Ok(old),
            RevisionState::Server {
                counter,
                increment,
                first,
            } => {
                let last_rev = match first.take() {
                    Some(last_rev) => last_rev,
                    None => counter.query_row([], |r| r.get(0))?,
                };
                let rev = next_revision(last_rev)?;
                increment.execute([rev])?;
                Ok(rev)
            }
        }
    }
    pub(super) fn is_server(&self) -> bool {
        matches!(self.state, Some(RevisionState::Server { .. }))
    }
}
fn max_rev(bf: &BackupFile) -> i64 {
    bf.prefixes
        .iter()
        .map(|p| p.rev)
        .chain(bf.tickets.iter().map(|t| t.rev))
        .chain(bf.baskets.iter().flat_map(|b| [b.rev, b.win_rev]))
        .max()
        .unwrap_or(0)
}
fn raise_rev(db: &Connection, rev: i64) -> Result<()> {
    db.execute(
        "UPDATE event SET last_rev=?1 WHERE id=1 AND last_rev<?1",
        [rev],
    )?;
    Ok(())
}
pub(super) fn export_in(db: &Connection) -> Result<BackupFile> {
    Ok(BackupFile {
        event: event_name(db)?,
        prefixes: query(
            db,
            &format!("SELECT {PREFIX_COLS} FROM prefixes ORDER BY weight,prefix"),
            [],
            prefix_row,
        )?,
        baskets: query(
            db,
            &format!("SELECT {BASKET_COLS} FROM baskets ORDER BY prefix,b_id"),
            [],
            basket_row,
        )?,
        tickets: query(
            db,
            &format!("SELECT {TICKET_COLS} FROM tickets ORDER BY prefix,t_id"),
            [],
            ticket_row,
        )?,
    })
}
pub(super) fn import_in(db: &Connection, bf: &BackupFile) -> Result<()> {
    let mut bf: BackupFile = crate::wire::decode(&crate::wire::go_json(bf)?)?;
    bf.validate()?;
    if has_table(db, "outbox")?
        && db.query_row("SELECT EXISTS(SELECT 1 FROM outbox)", [], |r| {
            r.get::<_, bool>(0)
        })?
    {
        return Err(Error::Validation(
            "finish or quarantine pending saves before restoring a backup".into(),
        ));
    }
    for p in &bf.prefixes {
        put_prefix(db, p)?;
    }
    for b in &bf.baskets {
        put_basket(db, b)?;
    }
    for t in &bf.tickets {
        put_ticket(db, t)?;
    }
    if let Some(ev) = event_in(db)? {
        for p in &bf.prefixes {
            db.execute("DELETE FROM deleted_prefixes WHERE prefix=?1", [&p.prefix])?;
        }
        if !bf.event.is_empty() && bf.event != ev.event {
            db.execute("UPDATE event SET event=?1 WHERE id=1", [&bf.event])?;
        }
        raise_rev(db, max_rev(&bf))?;
    }
    Ok(())
}
pub(super) fn merge_in(db: &Connection, bf: &BackupFile) -> Result<MergeResult> {
    let mut bf: BackupFile = crate::wire::decode(&crate::wire::go_json(bf)?)?;
    bf.validate()?;
    let Some(ev) = event_in(db)? else {
        return Err(Error::Validation("only a server merges copies".into()));
    };
    if !bf.event.is_empty() && bf.event != ev.event {
        let n:i64=db.query_row("SELECT (SELECT count(*) FROM prefixes)+(SELECT count(*) FROM tickets)+(SELECT count(*) FROM baskets)",[],|r|r.get(0))?;
        if n > 0 {
            return Err(Error::Validation(
                "this copy belongs to another event than the one this server holds".into(),
            ));
        }
        db.execute("UPDATE event SET event=?1 WHERE id=1", [&bf.event])?;
    }
    let mut res = MergeResult::default();
    for p in &bf.prefixes {
        if let Some(cur) = prefix_in(db, &p.prefix)? {
            if p.rev > cur.rev {
                put_prefix(db, p)?;
                res.updated += 1;
            } else {
                res.kept += 1;
            }
        } else {
            let deleted: Option<i64> = db
                .query_row(
                    "SELECT rev FROM deleted_prefixes WHERE prefix=?1",
                    [&p.prefix],
                    |r| r.get(0),
                )
                .optional()?;
            if deleted.is_some_and(|rev| p.rev <= rev) {
                res.kept += 1;
                continue;
            }
            put_prefix(db, p)?;
            db.execute("DELETE FROM deleted_prefixes WHERE prefix=?1", [&p.prefix])?;
            res.added += 1;
        }
    }
    for t in &bf.tickets {
        if let Some(cur) = ticket_in(db, &t.prefix, t.t_id)? {
            if t.rev > cur.rev {
                put_ticket(db, t)?;
                res.updated += 1;
            } else {
                res.kept += 1;
            }
        } else {
            put_ticket(db, t)?;
            res.added += 1;
        }
    }
    for b in &bf.baskets {
        if let Some(mut cur) = basket_in(db, &b.prefix, b.b_id)? {
            let mut changed = false;
            if b.rev > cur.rev {
                cur.description = b.description.clone();
                cur.donors = b.donors.clone();
                cur.rev = b.rev;
                changed = true;
            }
            if b.win_rev > cur.win_rev {
                cur.winning_ticket = b.winning_ticket;
                cur.win_rev = b.win_rev;
                changed = true;
            }
            if changed {
                put_basket(db, &cur)?;
                res.updated += 1;
            } else {
                res.kept += 1;
            }
        } else {
            put_basket(db, b)?;
            res.added += 1;
        }
    }
    raise_rev(db, max_rev(&bf))?;
    Ok(res)
}
impl Store {
    pub fn event(&self) -> Result<Option<Event>> {
        event_in(&*self.read()?)
    }
    pub fn snapshot(&self) -> Result<Snapshot> {
        let mut db = self.read()?;
        let tx = db.transaction()?;
        let snapshot = Snapshot {
            backup: export_in(&tx)?,
            generation: generation(&tx)?,
        };
        tx.commit()?;
        Ok(snapshot)
    }
    pub fn export(&self) -> Result<BackupFile> {
        Ok(self.snapshot()?.backup)
    }
    pub fn import(&self, bf: &BackupFile) -> Result<()> {
        self.write(|tx| import_in(tx, bf))
    }
    pub fn merge_newer(&self, bf: &BackupFile) -> Result<MergeResult> {
        self.write(|tx| merge_in(tx, bf))
    }
    pub fn mirror_event(&self) -> Result<String> {
        Ok(self
            .read()?
            .query_row("SELECT event FROM mirror WHERE id=1", [], |r| r.get(0))
            .optional()?
            .unwrap_or_default())
    }
    pub fn set_mirror_event(&self, event: &str) -> Result<()> {
        self.write(|tx| set_mirror(tx, event))
    }
    /// Caller must have flushed an exclusive archive of this snapshot. If data
    /// changed while it was archived, obtain and archive a new snapshot first.
    pub fn transition_copy(
        &self,
        archived: &Snapshot,
        new_event: &str,
        reason: &str,
    ) -> Result<i64> {
        if self.role() != Role::Client {
            return Err(Error::Validation(
                "only a client transitions its copy".into(),
            ));
        }
        self.write(|tx| {
            if generation(tx)? != archived.generation || event_name(tx)? != archived.backup.event {
                return Err(Error::Validation(
                    "the copy changed while it was being preserved".into(),
                ));
            }
            let n = super::queue::fail_all_in(tx, reason)?;
            for table in ["prefixes", "tickets", "baskets", "mirror"] {
                tx.execute(&format!("DELETE FROM {table}"), [])?;
            }
            set_mirror(tx, new_event)?;
            Ok(n)
        })
    }
    /// Merge a download while retaining rows edited after its source marker.
    pub fn mirror_apply(&self, source_generation: i64, bf: &BackupFile) -> Result<()> {
        let mut bf: BackupFile = crate::wire::decode(&crate::wire::go_json(bf)?)?;
        bf.validate()?;
        if self.role() != Role::Client {
            return Err(Error::Validation("only a client mirrors a server".into()));
        }
        self.write(|tx| {
            let current = event_name(tx)?;
            if !current.is_empty() && !bf.event.is_empty() && current != bf.event {
                return Err(Error::Validation(
                    "preserve the existing copy before changing events".into(),
                ));
            }
            for p in &bf.prefixes {
                if row_generation(tx, "prefixes", &p.prefix, 0)? <= source_generation {
                    put_prefix(tx, p)?;
                }
            }
            for t in &bf.tickets {
                if row_generation(tx, "tickets", &t.prefix, t.t_id)? <= source_generation {
                    put_ticket(tx, t)?;
                }
            }
            for b in &bf.baskets {
                if row_generation(tx, "baskets", &b.prefix, b.b_id)? <= source_generation {
                    put_basket(tx, b)?;
                }
            }
            if !bf.event.is_empty() {
                set_mirror(tx, &bf.event)?;
            }
            Ok(())
        })
    }
}
fn set_mirror(db: &Connection, event: &str) -> Result<()> {
    db.execute("INSERT INTO mirror(id,event) VALUES(1,?1) ON CONFLICT(id) DO UPDATE SET event=excluded.event",params![event])?;
    Ok(())
}

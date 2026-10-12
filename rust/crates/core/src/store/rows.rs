use super::*;
use crate::models::*;
use rusqlite::{OptionalExtension, Params, Row, params};
pub(super) const PREFIX_COLS: &str = "prefix,color,weight,rev";
pub(super) const TICKET_COLS: &str = "prefix,t_id,first_name,last_name,phone_number,pref,rev";
pub(super) const BASKET_COLS: &str = "prefix,b_id,description,donors,winning_ticket,rev,win_rev";
pub(super) fn text(r: &Row<'_>, i: usize) -> rusqlite::Result<String> {
    Ok(r.get::<_, Option<String>>(i)?.unwrap_or_default())
}
pub(super) fn int(r: &Row<'_>, i: usize) -> rusqlite::Result<i64> {
    Ok(r.get::<_, Option<i64>>(i)?.unwrap_or_default())
}
pub(super) fn prefix_row(r: &Row<'_>) -> rusqlite::Result<Prefix> {
    Ok(Prefix {
        prefix: text(r, 0)?,
        color: text(r, 1)?,
        weight: int(r, 2)?,
        rev: int(r, 3)?,
    })
}
pub(super) fn ticket_row(r: &Row<'_>) -> rusqlite::Result<Ticket> {
    Ok(Ticket {
        prefix: text(r, 0)?,
        t_id: int(r, 1)?,
        first_name: text(r, 2)?,
        last_name: text(r, 3)?,
        phone_number: text(r, 4)?,
        pref: text(r, 5)?,
        rev: int(r, 6)?,
    })
}
pub(super) fn basket_row(r: &Row<'_>) -> rusqlite::Result<Basket> {
    Ok(Basket {
        prefix: text(r, 0)?,
        b_id: int(r, 1)?,
        description: text(r, 2)?,
        donors: text(r, 3)?,
        winning_ticket: int(r, 4)?,
        rev: int(r, 5)?,
        win_rev: int(r, 6)?,
    })
}
pub(super) fn query<T>(
    db: &Connection,
    sql: &str,
    p: impl Params,
    f: fn(&Row<'_>) -> rusqlite::Result<T>,
) -> Result<Vec<T>> {
    let mut st = db.prepare(sql)?;
    Ok(st.query_map(p, f)?.collect::<rusqlite::Result<Vec<_>>>()?)
}
pub(super) fn prefix_in(db: &Connection, p: &str) -> Result<Option<Prefix>> {
    Ok(db
        .query_row(
            &format!("SELECT {PREFIX_COLS} FROM prefixes WHERE prefix=?1"),
            [p],
            prefix_row,
        )
        .optional()?)
}
pub(super) fn ticket_in(db: &Connection, p: &str, id: i64) -> Result<Option<Ticket>> {
    let mut statement = db.prepare_cached(&format!(
        "SELECT {TICKET_COLS} FROM tickets WHERE prefix=?1 AND t_id=?2"
    ))?;
    Ok(statement.query_row(params![p, id], ticket_row).optional()?)
}
pub(super) fn basket_in(db: &Connection, p: &str, id: i64) -> Result<Option<Basket>> {
    Ok(db
        .query_row(
            &format!("SELECT {BASKET_COLS} FROM baskets WHERE prefix=?1 AND b_id=?2"),
            params![p, id],
            basket_row,
        )
        .optional()?)
}
pub(super) fn put_prefix(db: &Connection, p: &Prefix) -> Result<()> {
    db.execute("INSERT INTO prefixes(prefix,color,weight,rev) VALUES(?1,?2,?3,?4) ON CONFLICT(prefix) DO UPDATE SET color=excluded.color,weight=excluded.weight,rev=excluded.rev",params![p.prefix,p.color,p.weight,p.rev])?;
    Ok(())
}
pub(super) fn put_ticket(db: &Connection, t: &Ticket) -> Result<()> {
    db.prepare_cached("INSERT INTO tickets(prefix,t_id,first_name,last_name,phone_number,pref,rev) VALUES(?1,?2,?3,?4,?5,?6,?7) ON CONFLICT(prefix,t_id) DO UPDATE SET first_name=excluded.first_name,last_name=excluded.last_name,phone_number=excluded.phone_number,pref=excluded.pref,rev=excluded.rev")?.execute(params![t.prefix,t.t_id,t.first_name,t.last_name,t.phone_number,t.pref,t.rev])?;
    Ok(())
}
pub(super) fn put_basket(db: &Connection, b: &Basket) -> Result<()> {
    db.execute("INSERT INTO baskets(prefix,b_id,description,donors,winning_ticket,rev,win_rev) VALUES(?1,?2,?3,?4,?5,?6,?7) ON CONFLICT(prefix,b_id) DO UPDATE SET description=excluded.description,donors=excluded.donors,winning_ticket=excluded.winning_ticket,rev=excluded.rev,win_rev=excluded.win_rev",params![b.prefix,b.b_id,b.description,b.donors,b.winning_ticket,b.rev,b.win_rev])?;
    Ok(())
}
pub(super) fn put_descriptions(db: &Connection, b: &Basket) -> Result<()> {
    if let Some(mut cur) = basket_in(db, &b.prefix, b.b_id)? {
        cur.description = b.description.clone();
        cur.donors = b.donors.clone();
        cur.rev = b.rev;
        put_basket(db, &cur)
    } else {
        put_basket(db, b)
    }
}
pub(super) fn put_winning(db: &Connection, b: &Basket) -> Result<()> {
    db.execute("INSERT INTO baskets(prefix,b_id,winning_ticket,win_rev) VALUES(?1,?2,?3,?4) ON CONFLICT(prefix,b_id) DO UPDATE SET winning_ticket=excluded.winning_ticket,win_rev=excluded.win_rev",params![b.prefix,b.b_id,b.winning_ticket,b.win_rev])?;
    Ok(())
}
pub(super) fn delete_prefix_in(db: &Connection, name: &str) -> Result<Option<Prefix>> {
    let gone = prefix_in(db, name)?;
    if gone.is_some() {
        db.execute("DELETE FROM prefixes WHERE prefix=?1", [name])?;
        if super::event::event_in(db)?.is_some() {
            let rev = super::event::stamp(db, 0)?;
            db.execute("INSERT INTO deleted_prefixes(prefix,rev) VALUES(?1,?2) ON CONFLICT(prefix) DO UPDATE SET rev=excluded.rev",params![name,rev])?;
        }
    }
    Ok(gone)
}
impl Store {
    pub fn list_prefixes(&self) -> Result<Vec<Prefix>> {
        query(
            &*self.read()?,
            &format!("SELECT {PREFIX_COLS} FROM prefixes ORDER BY weight,prefix"),
            [],
            prefix_row,
        )
    }
    pub fn prefix_by_name(&self, p: &str) -> Result<Option<Prefix>> {
        prefix_in(&*self.read()?, p)
    }
    pub fn all_tickets(&self) -> Result<Vec<Ticket>> {
        query(
            &*self.read()?,
            &format!("SELECT {TICKET_COLS} FROM tickets ORDER BY prefix,t_id"),
            [],
            ticket_row,
        )
    }
    pub fn tickets_by_prefix(&self, p: &str) -> Result<Vec<Ticket>> {
        self.ticket_range(p, i64::MIN, i64::MAX)
    }
    pub fn ticket(&self, p: &str, id: i64) -> Result<Option<Ticket>> {
        ticket_in(&*self.read()?, p, id)
    }
    pub fn ticket_range(&self, p: &str, from: i64, to: i64) -> Result<Vec<Ticket>> {
        query(
            &*self.read()?,
            &format!(
                "SELECT {TICKET_COLS} FROM tickets WHERE prefix=?1 AND t_id BETWEEN ?2 AND ?3 ORDER BY t_id"
            ),
            params![p, from, to],
            ticket_row,
        )
    }
    pub fn all_baskets(&self) -> Result<Vec<Basket>> {
        query(
            &*self.read()?,
            &format!("SELECT {BASKET_COLS} FROM baskets ORDER BY prefix,b_id"),
            [],
            basket_row,
        )
    }
    pub fn baskets_by_prefix(&self, p: &str) -> Result<Vec<Basket>> {
        self.basket_range(p, i64::MIN, i64::MAX)
    }
    pub fn basket(&self, p: &str, id: i64) -> Result<Option<Basket>> {
        basket_in(&*self.read()?, p, id)
    }
    pub fn basket_range(&self, p: &str, from: i64, to: i64) -> Result<Vec<Basket>> {
        query(
            &*self.read()?,
            &format!(
                "SELECT {BASKET_COLS} FROM baskets WHERE prefix=?1 AND b_id BETWEEN ?2 AND ?3 ORDER BY b_id"
            ),
            params![p, from, to],
            basket_row,
        )
    }
    pub fn upsert_prefixes(&self, rows: &[Prefix]) -> Result<()> {
        self.write(|tx| {
            for r in rows {
                put_prefix(tx, r)?;
            }
            Ok(())
        })
    }
    pub fn upsert_tickets(&self, rows: &[Ticket]) -> Result<()> {
        self.write(|tx| {
            for r in rows {
                put_ticket(tx, r)?;
            }
            Ok(())
        })
    }
    pub fn upsert_baskets(&self, rows: &[Basket]) -> Result<()> {
        self.write(|tx| {
            for r in rows {
                put_basket(tx, r)?;
            }
            Ok(())
        })
    }
    pub fn upsert_basket_descriptions(&self, rows: &[Basket]) -> Result<()> {
        self.write(|tx| {
            for r in rows {
                put_descriptions(tx, r)?;
            }
            Ok(())
        })
    }
    pub fn upsert_winning(&self, rows: &[Basket]) -> Result<()> {
        self.write(|tx| {
            for r in rows {
                put_winning(tx, r)?;
            }
            Ok(())
        })
    }
    pub fn delete_prefix(&self, p: &str) -> Result<Option<Prefix>> {
        self.write(|tx| delete_prefix_in(tx, p))
    }
    pub fn counts(&self) -> Result<(i64, i64, i64)> {
        Ok(self.read()?.query_row("SELECT (SELECT count(*) FROM prefixes),(SELECT count(*) FROM tickets),(SELECT count(*) FROM baskets)",[],|r|Ok((r.get(0)?,r.get(1)?,r.get(2)?)))?)
    }
    pub fn key_exists(&self, key: &str) -> Result<bool> {
        Ok(self.read()?.query_row(
            "SELECT EXISTS(SELECT 1 FROM auth_keys WHERE auth_key=?1 AND auth_key<>'')",
            [key],
            |r| r.get(0),
        )?)
    }
    pub fn list_keys(&self) -> Result<Vec<AuthKey>> {
        let db = self.read()?;
        let sql = if has_table(&db, "auth_key_activity")? {
            "SELECT k.auth_key,k.description,a.last_seen,a.last_update FROM auth_keys k LEFT JOIN auth_key_activity a ON a.auth_key=k.auth_key ORDER BY k.description,k.auth_key"
        } else {
            "SELECT auth_key,description,NULL,NULL FROM auth_keys ORDER BY description,auth_key"
        };
        query(&db, sql, [], |r| {
            Ok(AuthKey {
                auth_key: text(r, 0)?,
                description: text(r, 1)?,
                last_seen: text(r, 2)?,
                last_update: text(r, 3)?,
            })
        })
    }
    pub fn create_key(&self, description: &str) -> Result<AuthKey> {
        self.write(|tx| {
            for _ in 0..5 {
                let mut key = String::with_capacity(32);
                while key.len() < 32 {
                    let mut buf = [0; 32];
                    getrandom::fill(&mut buf).map_err(|e| Error::Internal(e.to_string()))?;
                    for b in buf {
                        if b < 252 && key.len() < 32 {
                            key.push(
                                b"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"[(b % 36) as usize] as char,
                            );
                        }
                    }
                }
                if tx.execute(
                    "INSERT OR IGNORE INTO auth_keys(auth_key,description) VALUES(?1,?2)",
                    params![key, description],
                )? == 1
                {
                    return Ok(AuthKey {
                        auth_key: key,
                        description: description.into(),
                        ..Default::default()
                    });
                }
            }
            Err(Error::Internal("could not generate a unique key".into()))
        })
    }
    pub fn delete_key(&self, key: &str) -> Result<Option<AuthKey>> {
        self.write(|tx| {
            let gone = tx
                .query_row(
                    "DELETE FROM auth_keys WHERE auth_key=?1 RETURNING auth_key,description",
                    [key],
                    |r| {
                        Ok(AuthKey {
                            auth_key: text(r, 0)?,
                            description: text(r, 1)?,
                            ..Default::default()
                        })
                    },
                )
                .optional()?;
            if gone.is_some() && has_table(tx, "auth_key_activity")? {
                tx.execute("DELETE FROM auth_key_activity WHERE auth_key=?1", [key])?;
            }
            Ok(gone)
        })
    }
    pub fn touch_key(&self, key: &str) -> Result<()> {
        self.key_activity(key, false)
    }
    pub fn mark_key_updated(&self, key: &str) -> Result<()> {
        self.key_activity(key, true)
    }
    fn key_activity(&self, key: &str, updated: bool) -> Result<()> {
        let column = if updated { "last_update" } else { "last_seen" };
        self.write(|tx|{tx.execute(&format!("INSERT INTO auth_key_activity(auth_key,{column}) SELECT auth_key,?1 FROM auth_keys WHERE auth_key=?2 ON CONFLICT(auth_key) DO UPDATE SET {column}=excluded.{column}"),params![time::OffsetDateTime::now_utc().replace_nanosecond(0).map_err(|e|Error::Internal(e.to_string()))?.format(&time::format_description::well_known::Rfc3339).map_err(|e|Error::Internal(e.to_string()))?,key])?;Ok(())})
    }
}

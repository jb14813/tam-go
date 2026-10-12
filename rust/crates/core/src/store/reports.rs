use super::rows::{TICKET_COLS, int, query, text, ticket_row};
use super::*;
use crate::models::*;
use rusqlite::{Row, params};
fn drawing_row(r: &Row<'_>) -> rusqlite::Result<DrawingLine> {
    Ok(DrawingLine {
        prefix: text(r, 0)?,
        b_id: int(r, 1)?,
        description: text(r, 2)?,
        winning_ticket: int(r, 3)?,
        last_name: text(r, 4)?,
        first_name: text(r, 5)?,
        phone_number: text(r, 6)?,
        win_rev: int(r, 7)?,
    })
}
const DRAWING: &str =
    "prefix,b_id,description,winning_ticket,last_name,first_name,phone_number,win_rev";
impl Store {
    pub fn search_tickets(&self, first: &str, last: &str, phone: &str) -> Result<Vec<Ticket>> {
        query(
            &*self.read()?,
            &format!(
                "SELECT {TICKET_COLS} FROM tickets WHERE instr(lower(first_name),lower(?1))>0 AND instr(lower(last_name),lower(?2))>0 AND instr(lower(phone_number),lower(?3))>0 ORDER BY prefix,t_id"
            ),
            params![first, last, phone],
            ticket_row,
        )
    }
    pub fn report_counts(&self) -> Result<Vec<ReportCountLine>> {
        query(
            &*self.read()?,
            "SELECT prefix,is_total,unique_buyers,total_buys FROM report_counts ORDER BY is_total,prefix",
            [],
            |r| {
                Ok(ReportCountLine {
                    prefix: text(r, 0)?,
                    is_total: r.get(1)?,
                    unique_buyers: int(r, 2)?,
                    total_buys: int(r, 3)?,
                })
            },
        )
    }
    pub fn report_by_name(&self, prefix: &str) -> Result<Vec<ReportByNameLine>> {
        query(
            &*self.read()?,
            "SELECT last_name,first_name,phone_number,pref,prefix,b_id,description,donors,winning_ticket FROM report_by_name WHERE prefix=?1 ORDER BY last_name,first_name,phone_number,prefix,b_id",
            [prefix],
            |r| {
                Ok(ReportByNameLine {
                    last_name: text(r, 0)?,
                    first_name: text(r, 1)?,
                    phone_number: text(r, 2)?,
                    pref: text(r, 3)?,
                    prefix: text(r, 4)?,
                    b_id: int(r, 5)?,
                    description: text(r, 6)?,
                    donors: text(r, 7)?,
                    winning_ticket: int(r, 8)?,
                })
            },
        )
    }
    pub fn report_by_basket(&self, prefix: &str) -> Result<Vec<ReportByBasketLine>> {
        query(
            &*self.read()?,
            "SELECT prefix,b_id,description,donors,winning_ticket,last_name,first_name,phone_number,pref FROM report_by_basket WHERE prefix=?1 ORDER BY prefix,b_id",
            [prefix],
            |r| {
                Ok(ReportByBasketLine {
                    prefix: text(r, 0)?,
                    b_id: int(r, 1)?,
                    description: text(r, 2)?,
                    donors: text(r, 3)?,
                    winning_ticket: int(r, 4)?,
                    last_name: text(r, 5)?,
                    first_name: text(r, 6)?,
                    phone_number: text(r, 7)?,
                    pref: text(r, 8)?,
                })
            },
        )
    }
    pub fn all_drawing(&self) -> Result<Vec<DrawingLine>> {
        query(
            &*self.read()?,
            &format!("SELECT {DRAWING} FROM drawing ORDER BY prefix,b_id"),
            [],
            drawing_row,
        )
    }
    pub fn drawing_by_prefix(&self, prefix: &str) -> Result<Vec<DrawingLine>> {
        self.drawing_range(prefix, i64::MIN, i64::MAX)
    }
    pub fn drawing_line(&self, prefix: &str, id: i64) -> Result<Option<DrawingLine>> {
        Ok(self.drawing_range(prefix, id, id)?.into_iter().next())
    }
    pub fn drawing_range(&self, prefix: &str, from: i64, to: i64) -> Result<Vec<DrawingLine>> {
        query(
            &*self.read()?,
            &format!(
                "SELECT {DRAWING} FROM drawing WHERE prefix=?1 AND b_id BETWEEN ?2 AND ?3 ORDER BY b_id"
            ),
            params![prefix, from, to],
            drawing_row,
        )
    }
}

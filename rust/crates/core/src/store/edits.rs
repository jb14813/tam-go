use super::rows::*;
use super::*;
use crate::{models::*, wire};
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum StoredRows {
    Prefixes(Vec<Prefix>),
    Tickets(Vec<Ticket>),
    Baskets(Vec<Basket>),
    Drawing(Vec<Basket>),
}
impl StoredRows {
    fn validate_answer(&self) -> Result<()> {
        // Stored answers use backup row semantics, which retain historical
        // prefix names/weights. Validate a copy so color normalization cannot
        // alter the server's actual answer while checking it.
        let mut backup = match self {
            Self::Prefixes(rows) => BackupFile {
                prefixes: rows.clone(),
                ..Default::default()
            },
            Self::Tickets(rows) => BackupFile {
                tickets: rows.clone(),
                ..Default::default()
            },
            Self::Baskets(rows) | Self::Drawing(rows) => BackupFile {
                baskets: rows.clone(),
                ..Default::default()
            },
        };
        backup.validate()
    }
    pub fn go_json(&self) -> Result<Vec<u8>> {
        match self {
            Self::Prefixes(r) => wire::go_json(r),
            Self::Tickets(r) => wire::go_json(r),
            Self::Baskets(r) | Self::Drawing(r) => wire::go_json(r),
        }
    }
    pub fn decode(form: Form, body: &[u8]) -> Result<Self> {
        Ok(match form {
            Form::Prefixes => {
                Self::Prefixes(wire::decode::<Option<Vec<Prefix>>>(body)?.unwrap_or_default())
            }
            Form::Tickets => {
                Self::Tickets(wire::decode::<Option<Vec<Ticket>>>(body)?.unwrap_or_default())
            }
            Form::Baskets => {
                Self::Baskets(wire::decode::<Option<Vec<Basket>>>(body)?.unwrap_or_default())
            }
            Form::Drawing => {
                Self::Drawing(wire::decode::<Option<Vec<Basket>>>(body)?.unwrap_or_default())
            }
        })
    }
}
#[derive(Debug, Clone)]
pub struct SaveResult {
    pub rows: StoredRows,
    pub conflicts: Vec<Conflict>,
    pub kept: SaveBatch,
    pub again: Option<SaveBatch>,
}
impl SaveResult {
    pub fn kept_body(&self) -> Result<Vec<u8>> {
        batch_json(&self.kept)
    }
    pub fn again_body(&self) -> Result<Option<Vec<u8>>> {
        self.again.as_ref().map(batch_json).transpose()
    }
    pub fn conflict_text(&self) -> String {
        self.conflicts.iter().map(|c|{let row=if c.kind=="prefix"{format!("prefix {}",c.prefix)}else{format!("{} {} {}",if c.kind=="drawing"{"basket"}else{&c.kind},c.prefix,c.id)};format!("{row} {}: another computer changed it to {:?} after this one loaded it; {:?} was not saved",c.field,c.now,c.yours)}).collect::<Vec<_>>().join("; ")
    }
}
pub fn result_of(path: &str, request: &[u8], answer: &[u8]) -> Result<SaveResult> {
    let form = if matches!(path, "/api/search/tickets" | "/search/tickets") {
        Some(Form::Tickets)
    } else {
        Form::from_path(path)
    }
    .ok_or_else(|| Error::Validation(format!("no save is sent to {path}")))?;
    let saves = SaveBatch::decode(form, request)?;
    let rows = StoredRows::decode(form, answer)?;
    analyze_result(&saves, rows)
}
pub(super) fn batch_json(batch: &SaveBatch) -> Result<Vec<u8>> {
    match batch {
        SaveBatch::Prefixes(r) => wire::go_json(r),
        SaveBatch::Tickets(r) => wire::go_json(r),
        SaveBatch::Baskets(r) => wire::go_json(r),
        SaveBatch::Drawing(r) => wire::go_json(r),
    }
}
pub(super) fn ticket_values(t: &Ticket) -> TicketValues {
    TicketValues {
        first_name: t.first_name.clone(),
        last_name: t.last_name.clone(),
        phone_number: t.phone_number.clone(),
        pref: t.pref.clone(),
    }
}
pub(super) fn basket_values(b: &Basket) -> BasketValues {
    BasketValues {
        description: b.description.clone(),
        donors: b.donors.clone(),
    }
}
pub(super) fn prefix_values(p: &Prefix) -> PrefixValues {
    PrefixValues {
        color: p.color.clone(),
        weight: p.weight,
    }
}
fn merge_field<T: PartialEq + Clone>(cur: &mut T, mine: &T, base: &T) -> bool {
    if mine != base && cur == base {
        *cur = mine.clone();
        true
    } else {
        false
    }
}
pub(super) fn save_in(db: &Connection, saves: &SaveBatch) -> Result<StoredRows> {
    let mut saves = saves.clone();
    saves.validate()?;
    let mut stamps = super::event::BatchStamper::new(db);
    Ok(match saves {
        SaveBatch::Tickets(saves) => {
            let mut out = Vec::with_capacity(saves.len());
            for sv in saves {
                let cur = ticket_in(db, &sv.ticket.prefix, sv.ticket.t_id)?;
                let mut next = cur.clone().unwrap_or_else(|| sv.ticket.clone());
                let changed = if let Some(cur) = cur.as_ref() {
                    let base = sv.base.unwrap_or_else(|| ticket_values(cur));
                    let mut changed = false;
                    changed |= merge_field(
                        &mut next.first_name,
                        &sv.ticket.first_name,
                        &base.first_name,
                    );
                    changed |=
                        merge_field(&mut next.last_name, &sv.ticket.last_name, &base.last_name);
                    changed |= merge_field(
                        &mut next.phone_number,
                        &sv.ticket.phone_number,
                        &base.phone_number,
                    );
                    changed |= merge_field(&mut next.pref, &sv.ticket.pref, &base.pref);
                    changed
                } else {
                    true
                };
                if changed {
                    next.rev = stamps.stamp(cur.as_ref().map_or(0, |r| r.rev))?;
                    put_ticket(db, &next)?;
                }
                out.push(next);
            }
            StoredRows::Tickets(out)
        }
        SaveBatch::Baskets(saves) => {
            let mut out = Vec::with_capacity(saves.len());
            for sv in saves {
                let cur = basket_in(db, &sv.basket.prefix, sv.basket.b_id)?;
                let mut next = cur.clone().unwrap_or_else(|| sv.basket.clone());
                let changed = if let Some(cur) = cur.as_ref() {
                    let base = sv.base.unwrap_or_else(|| basket_values(cur));
                    let mut changed = false;
                    changed |= merge_field(
                        &mut next.description,
                        &sv.basket.description,
                        &base.description,
                    );
                    changed |= merge_field(&mut next.donors, &sv.basket.donors, &base.donors);
                    changed
                } else {
                    true
                };
                if changed {
                    next.rev = stamps.stamp(cur.as_ref().map_or(0, |r| r.rev))?;
                    if cur.is_none() {
                        next.win_rev = next.rev;
                    }
                    put_basket(db, &next)?;
                }
                out.push(next);
            }
            StoredRows::Baskets(out)
        }
        SaveBatch::Drawing(saves) => {
            let mut out = Vec::with_capacity(saves.len());
            for sv in saves {
                let cur = basket_in(db, &sv.basket.prefix, sv.basket.b_id)?;
                let mut next = cur.clone().unwrap_or_else(|| Basket {
                    prefix: sv.basket.prefix.clone(),
                    b_id: sv.basket.b_id,
                    winning_ticket: sv.basket.winning_ticket,
                    ..Default::default()
                });
                let changed = if let Some(cur) = cur.as_ref() {
                    let base = sv.base.unwrap_or(DrawingValues {
                        winning_ticket: cur.winning_ticket,
                    });
                    merge_field(
                        &mut next.winning_ticket,
                        &sv.basket.winning_ticket,
                        &base.winning_ticket,
                    )
                } else {
                    true
                };
                if changed {
                    next.win_rev = stamps.stamp(cur.as_ref().map_or(0, |r| r.win_rev))?;
                    put_winning(db, &next)?;
                }
                out.push(next);
            }
            StoredRows::Drawing(out)
        }
        SaveBatch::Prefixes(saves) => {
            let mut out = Vec::with_capacity(saves.len());
            for sv in saves {
                let cur = prefix_in(db, &sv.prefix.prefix)?;
                let mut next = cur.clone().unwrap_or_else(|| sv.prefix.clone());
                let changed = if let Some(cur) = cur.as_ref() {
                    let base = sv.base.unwrap_or_else(|| prefix_values(cur));
                    let mut changed = false;
                    changed |= merge_field(&mut next.color, &sv.prefix.color, &base.color);
                    changed |= merge_field(&mut next.weight, &sv.prefix.weight, &base.weight);
                    changed
                } else {
                    true
                };
                if changed {
                    next.rev = stamps.stamp(cur.as_ref().map_or(0, |r| r.rev))?;
                    put_prefix(db, &next)?;
                    if stamps.is_server() {
                        db.execute(
                            "DELETE FROM deleted_prefixes WHERE prefix=?1",
                            [&next.prefix],
                        )?;
                    }
                }
                out.push(next);
            }
            StoredRows::Prefixes(out)
        }
    })
}
impl Store {
    pub fn save(&self, saves: &SaveBatch) -> Result<StoredRows> {
        self.write(|tx| save_in(tx, saves))
    }
    pub fn save_tickets(&self, saves: &[TicketSave]) -> Result<Vec<Ticket>> {
        if let StoredRows::Tickets(r) = self.save(&SaveBatch::Tickets(saves.to_vec()))? {
            Ok(r)
        } else {
            unreachable!()
        }
    }
    pub fn save_baskets(&self, saves: &[BasketSave]) -> Result<Vec<Basket>> {
        if let StoredRows::Baskets(r) = self.save(&SaveBatch::Baskets(saves.to_vec()))? {
            Ok(r)
        } else {
            unreachable!()
        }
    }
    pub fn save_winning(&self, saves: &[DrawingSave]) -> Result<Vec<Basket>> {
        if let StoredRows::Drawing(r) = self.save(&SaveBatch::Drawing(saves.to_vec()))? {
            Ok(r)
        } else {
            unreachable!()
        }
    }
    pub fn save_prefixes(&self, saves: &[PrefixSave]) -> Result<Vec<Prefix>> {
        if let StoredRows::Prefixes(r) = self.save(&SaveBatch::Prefixes(saves.to_vec()))? {
            Ok(r)
        } else {
            unreachable!()
        }
    }
    pub fn copy_result(&self, result: &SaveResult) -> Result<()> {
        self.write(|tx| copy_rows(tx, &result.rows))
    }
}
pub(super) fn copy_rows(db: &Connection, rows: &StoredRows) -> Result<()> {
    match rows {
        StoredRows::Prefixes(r) => {
            for row in r {
                put_prefix(db, row)?;
            }
        }
        StoredRows::Tickets(r) => {
            for row in r {
                put_ticket(db, row)?;
            }
        }
        StoredRows::Baskets(r) => {
            for row in r {
                put_descriptions(db, row)?;
            }
        }
        StoredRows::Drawing(r) => {
            for row in r {
                put_winning(db, row)?;
            }
        }
    }
    Ok(())
}
fn not_answer() -> Error {
    Error::Validation("the answer does not list the saved rows".into())
}
// Fields are represented only for comparison and diagnostics; numeric writes
// continue using their typed i64 values.
trait SaveFields: Clone {
    type Row: Clone;
    fn row(&self) -> &Self::Row;
    fn key(row: &Self::Row) -> (&str, i64);
    fn fields(row: &Self::Row) -> Vec<String>;
    fn base(&self) -> Option<Vec<String>>;
    fn names() -> &'static [&'static str];
    fn kind() -> &'static str;
    fn resave(&self, row: Self::Row, base: Vec<String>) -> Self;
    fn set(row: &mut Self::Row, index: usize, value: &str);
}
impl SaveFields for TicketSave {
    type Row = Ticket;
    fn row(&self) -> &Ticket {
        &self.ticket
    }
    fn key(t: &Ticket) -> (&str, i64) {
        (&t.prefix, t.t_id)
    }
    fn fields(t: &Ticket) -> Vec<String> {
        vec![
            t.first_name.clone(),
            t.last_name.clone(),
            t.phone_number.clone(),
            t.pref.clone(),
        ]
    }
    fn base(&self) -> Option<Vec<String>> {
        self.base.as_ref().map(|b| {
            vec![
                b.first_name.clone(),
                b.last_name.clone(),
                b.phone_number.clone(),
                b.pref.clone(),
            ]
        })
    }
    fn names() -> &'static [&'static str] {
        &[
            "first name",
            "last name",
            "phone number",
            "contact preference",
        ]
    }
    fn kind() -> &'static str {
        "ticket"
    }
    fn resave(&self, ticket: Ticket, b: Vec<String>) -> Self {
        Self {
            ticket,
            base: Some(TicketValues {
                first_name: b[0].clone(),
                last_name: b[1].clone(),
                phone_number: b[2].clone(),
                pref: b[3].clone(),
            }),
        }
    }
    fn set(t: &mut Ticket, i: usize, v: &str) {
        match i {
            0 => t.first_name = v.into(),
            1 => t.last_name = v.into(),
            2 => t.phone_number = v.into(),
            3 => t.pref = v.into(),
            _ => unreachable!(),
        }
    }
}
impl SaveFields for BasketSave {
    type Row = Basket;
    fn row(&self) -> &Basket {
        &self.basket
    }
    fn key(b: &Basket) -> (&str, i64) {
        (&b.prefix, b.b_id)
    }
    fn fields(b: &Basket) -> Vec<String> {
        vec![b.description.clone(), b.donors.clone()]
    }
    fn base(&self) -> Option<Vec<String>> {
        self.base
            .as_ref()
            .map(|b| vec![b.description.clone(), b.donors.clone()])
    }
    fn names() -> &'static [&'static str] {
        &["description", "donors"]
    }
    fn kind() -> &'static str {
        "basket"
    }
    fn resave(&self, basket: Basket, b: Vec<String>) -> Self {
        Self {
            basket,
            base: Some(BasketValues {
                description: b[0].clone(),
                donors: b[1].clone(),
            }),
        }
    }
    fn set(b: &mut Basket, i: usize, v: &str) {
        match i {
            0 => b.description = v.into(),
            1 => b.donors = v.into(),
            _ => unreachable!(),
        }
    }
}
impl SaveFields for DrawingSave {
    type Row = Basket;
    fn row(&self) -> &Basket {
        &self.basket
    }
    fn key(b: &Basket) -> (&str, i64) {
        (&b.prefix, b.b_id)
    }
    fn fields(b: &Basket) -> Vec<String> {
        vec![b.winning_ticket.to_string()]
    }
    fn base(&self) -> Option<Vec<String>> {
        self.base
            .as_ref()
            .map(|b| vec![b.winning_ticket.to_string()])
    }
    fn names() -> &'static [&'static str] {
        &["winning ticket"]
    }
    fn kind() -> &'static str {
        "drawing"
    }
    fn resave(&self, basket: Basket, b: Vec<String>) -> Self {
        Self {
            basket,
            base: Some(DrawingValues {
                winning_ticket: b[0].parse().expect("typed numeric field"),
            }),
        }
    }
    fn set(b: &mut Basket, _i: usize, v: &str) {
        b.winning_ticket = v.parse().expect("typed numeric field");
    }
}
impl SaveFields for PrefixSave {
    type Row = Prefix;
    fn row(&self) -> &Prefix {
        &self.prefix
    }
    fn key(p: &Prefix) -> (&str, i64) {
        (&p.prefix, 0)
    }
    fn fields(p: &Prefix) -> Vec<String> {
        vec![p.color.clone(), p.weight.to_string()]
    }
    fn base(&self) -> Option<Vec<String>> {
        self.base
            .as_ref()
            .map(|b| vec![b.color.clone(), b.weight.to_string()])
    }
    fn names() -> &'static [&'static str] {
        &["color", "weight"]
    }
    fn kind() -> &'static str {
        "prefix"
    }
    fn resave(&self, prefix: Prefix, b: Vec<String>) -> Self {
        Self {
            prefix,
            base: Some(PrefixValues {
                color: b[0].clone(),
                weight: b[1].parse().expect("typed numeric field"),
            }),
        }
    }
    fn set(p: &mut Prefix, i: usize, v: &str) {
        match i {
            0 => p.color = v.into(),
            1 => p.weight = v.parse().expect("typed numeric field"),
            _ => unreachable!(),
        }
    }
}
fn analyze<S: SaveFields>(saves: &[S], rows: &[S::Row]) -> Result<(Vec<Conflict>, Vec<S>, Vec<S>)> {
    if saves.len() != rows.len() {
        return Err(not_answer());
    }
    let mut conflicts = Vec::new();
    let mut kept = Vec::new();
    let mut again = Vec::new();
    for (sv, stored) in saves.iter().zip(rows) {
        let (prefix, id) = S::key(sv.row());
        if S::key(stored) != (prefix, id) {
            return Err(not_answer());
        }
        let Some(base) = sv.base() else {
            kept.push(sv.clone());
            continue;
        };
        let mine = S::fields(sv.row());
        let current = S::fields(stored);
        let mut keep = sv.row().clone();
        let mut redo = stored.clone();
        let mut refused = false;
        for (i, name) in S::names().iter().enumerate() {
            if mine[i] != base[i] && current[i] != mine[i] {
                conflicts.push(Conflict {
                    kind: S::kind().into(),
                    prefix: prefix.into(),
                    id,
                    field: (*name).into(),
                    yours: mine[i].clone(),
                    now: current[i].clone(),
                });
                S::set(&mut keep, i, &base[i]);
                S::set(&mut redo, i, &mine[i]);
                refused = true;
            }
        }
        kept.push(sv.resave(keep, base));
        if refused {
            again.push(sv.resave(redo, current));
        }
    }
    Ok((conflicts, kept, again))
}
pub fn analyze_result(request: &SaveBatch, rows: StoredRows) -> Result<SaveResult> {
    rows.validate_answer()?;
    let (conflicts, kept, again) = match (request, &rows) {
        (SaveBatch::Tickets(s), StoredRows::Tickets(r)) => {
            let (c, k, a) = analyze(s, r)?;
            (
                c,
                SaveBatch::Tickets(k),
                (!a.is_empty()).then_some(SaveBatch::Tickets(a)),
            )
        }
        (SaveBatch::Baskets(s), StoredRows::Baskets(r)) => {
            let (c, k, a) = analyze(s, r)?;
            (
                c,
                SaveBatch::Baskets(k),
                (!a.is_empty()).then_some(SaveBatch::Baskets(a)),
            )
        }
        (SaveBatch::Drawing(s), StoredRows::Drawing(r)) => {
            let (c, k, a) = analyze(s, r)?;
            (
                c,
                SaveBatch::Drawing(k),
                (!a.is_empty()).then_some(SaveBatch::Drawing(a)),
            )
        }
        (SaveBatch::Prefixes(s), StoredRows::Prefixes(r)) => {
            let (c, k, a) = analyze(s, r)?;
            (
                c,
                SaveBatch::Prefixes(k),
                (!a.is_empty()).then_some(SaveBatch::Prefixes(a)),
            )
        }
        _ => return Err(not_answer()),
    };
    Ok(SaveResult {
        rows,
        conflicts,
        kept,
        again,
    })
}
pub(super) fn copy_unless_newer(
    db: &Connection,
    request: &SaveBatch,
    rows: &StoredRows,
    marker: Option<i64>,
) -> Result<()> {
    fn eligible<S: SaveFields>(
        db: &Connection,
        sv: &S,
        cur: Option<S::Row>,
        marker: Option<i64>,
        kind: &str,
    ) -> Result<bool> {
        let (p, id) = S::key(sv.row());
        if let Some(marker) = marker {
            return Ok(row_generation(db, kind, p, id)? <= marker);
        }
        Ok(cur
            .as_ref()
            .is_none_or(|c| S::fields(c) == S::fields(sv.row())))
    }
    // Decide against the pre-answer state for every row before any writes. A
    // duplicate identity must not see the first occurrence as a newer local edit.
    let selected = match (request, rows) {
        (SaveBatch::Tickets(s), StoredRows::Tickets(r)) => {
            let mut out = Vec::new();
            for (sv, row) in s.iter().zip(r) {
                if eligible(
                    db,
                    sv,
                    ticket_in(db, &row.prefix, row.t_id)?,
                    marker,
                    "tickets",
                )? {
                    out.push(row.clone());
                }
            }
            StoredRows::Tickets(out)
        }
        (SaveBatch::Prefixes(s), StoredRows::Prefixes(r)) => {
            let mut out = Vec::new();
            for (sv, row) in s.iter().zip(r) {
                if eligible(db, sv, prefix_in(db, &row.prefix)?, marker, "prefixes")? {
                    out.push(row.clone());
                }
            }
            StoredRows::Prefixes(out)
        }
        (SaveBatch::Baskets(s), StoredRows::Baskets(r)) => {
            let mut out = Vec::new();
            for (sv, row) in s.iter().zip(r) {
                if eligible(
                    db,
                    sv,
                    basket_in(db, &row.prefix, row.b_id)?,
                    marker,
                    "basket_descriptions",
                )? {
                    out.push(row.clone());
                }
            }
            StoredRows::Baskets(out)
        }
        (SaveBatch::Drawing(s), StoredRows::Drawing(r)) => {
            let mut out = Vec::new();
            for (sv, row) in s.iter().zip(r) {
                if eligible(
                    db,
                    sv,
                    basket_in(db, &row.prefix, row.b_id)?,
                    marker,
                    "winning",
                )? {
                    out.push(row.clone());
                }
            }
            StoredRows::Drawing(out)
        }
        _ => return Err(not_answer()),
    };
    copy_rows(db, &selected)
}

use std::{collections::HashMap, sync::Mutex};
#[derive(Clone, Default)]
pub(crate) struct Record {
    pub client: String,
    pub seen: i64,
    pub updated: i64,
    pub pending: Option<i64>,
    pub touched: i64,
    pub written: i64,
}
#[derive(Default)]
pub struct Presence {
    records: Mutex<HashMap<String, Record>>,
}
impl Presence {
    pub(crate) fn seen(&self, key: &str, client: &str, pending: Option<i64>, now: i64) -> bool {
        let mut records = self.records.lock().unwrap();
        let r = records.entry(key.into()).or_default();
        r.client = client.into();
        r.seen = now;
        if pending.is_some() {
            r.pending = pending
        }
        let due = r.touched == 0 || now - r.touched >= 60;
        if due {
            r.touched = now
        }
        due
    }
    pub(crate) fn updated(&self, key: &str, now: i64) -> bool {
        let mut records = self.records.lock().unwrap();
        let r = records.entry(key.into()).or_default();
        r.updated = now;
        let due = r.written == 0 || now - r.written >= 60;
        if due {
            r.written = now
        }
        due
    }
    pub(crate) fn snapshot(&self) -> HashMap<String, Record> {
        self.records.lock().unwrap().clone()
    }
    pub fn forget(&self, key: &str) {
        self.records.lock().unwrap().remove(key);
    }
}

use hmac::{Hmac, Mac};
use r_tam_core::{Error, Result};
use sha2::Sha256;
use std::{
    collections::HashMap,
    path::{Path, PathBuf},
    sync::{Arc, Mutex, RwLock},
};
use subtle::ConstantTimeEq;
pub type Clock = Arc<dyn Fn() -> i64 + Send + Sync>;
pub fn clock() -> Clock {
    Arc::new(|| time::OffsetDateTime::now_utc().unix_timestamp())
}
pub struct Password {
    path: PathBuf,
    env: String,
    hash: RwLock<String>,
    setup: Mutex<()>,
}
impl Password {
    pub fn load(dir: &Path, env: &str) -> Result<Self> {
        let path = dir.join("server.json");
        let hash = match std::fs::read(&path) {
            Ok(data) => {
                #[derive(serde::Deserialize)]
                struct Doc {
                    #[serde(default)]
                    password_hash: Option<String>,
                }
                let doc: Option<Doc> = serde_json::from_slice(&data)?;
                let hash = doc.and_then(|v| v.password_hash).unwrap_or_default();
                if !hash.is_empty() {
                    let parts = hash
                        .parse::<bcrypt::HashParts>()
                        .map_err(|e| Error::Validation(e.to_string()))?;
                    if !hash.starts_with('$') || !(4..=31).contains(&parts.get_cost()) {
                        return Err(Error::Validation(
                            "password_hash is not a bcrypt hash".into(),
                        ));
                    }
                }
                hash
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => String::new(),
            Err(e) => return Err(e.into()),
        };
        Ok(Self {
            path,
            env: env.into(),
            hash: RwLock::new(hash),
            setup: Mutex::new(()),
        })
    }
    pub fn is_set(&self) -> bool {
        !self.hash.read().unwrap().is_empty() || !self.env.is_empty()
    }
    pub fn from_env(&self) -> bool {
        self.hash.read().unwrap().is_empty() && !self.env.is_empty()
    }
    pub fn check(&self, plain: &str) -> bool {
        if plain.is_empty() {
            return false;
        }
        let hash = self.hash.read().unwrap();
        if hash.is_empty() {
            plain.as_bytes().ct_eq(self.env.as_bytes()).into()
        } else {
            bcrypt::verify(plain, &hash).unwrap_or(false)
        }
    }
    pub fn set(&self, plain: &str) -> Result<()> {
        validate_password(plain)?;
        let hash = bcrypt::hash_with_result(plain, 10)
            .map_err(|e| Error::Internal(e.to_string()))?
            .format_for_version(bcrypt::Version::TwoA);
        let mut held = self.hash.write().unwrap();
        let data = serde_json::to_vec_pretty(&serde_json::json!({"password_hash":hash}))?;
        r_tam_core::files::atomic_write(&self.path, &data)?;
        *held = hash;
        Ok(())
    }
    pub fn setup(&self, plain: &str) -> Result<bool> {
        let _guard = self.setup.lock().unwrap();
        if self.is_set() {
            return Ok(false);
        }
        self.set(plain)?;
        Ok(true)
    }
}
pub fn validate_password(value: &str) -> Result<()> {
    if value.is_empty() {
        return Err(Error::Validation("the password must not be empty".into()));
    }
    if value.len() > 72 {
        return Err(Error::Validation(
            "the password must be at most 72 bytes".into(),
        ));
    }
    Ok(())
}
#[derive(Default)]
struct Attempts {
    failures: u8,
    last: i64,
    until: i64,
    told: bool,
}
pub struct Limiter {
    by_addr: Mutex<HashMap<String, Arc<Mutex<Attempts>>>>,
    now: Clock,
}
impl Limiter {
    pub fn new(now: Clock) -> Self {
        Self {
            by_addr: Mutex::new(HashMap::new()),
            now,
        }
    }
    pub fn check(&self, addr: &str, check: impl FnOnce() -> bool) -> (bool, i64) {
        let record = {
            let mut map = self.by_addr.lock().unwrap();
            let now = (self.now)();
            map.retain(|_, r| {
                Arc::strong_count(r) > 1
                    || r.try_lock()
                        .map_or(true, |v| now < v.until || now - v.last < 30)
            });
            map.entry(addr.into()).or_default().clone()
        };
        // Serialize only this address. Waiting attempts retain the same record,
        // so pruning cannot let concurrent requests escape the failure budget.
        let mut record = record.lock().unwrap();
        let now = (self.now)();
        if record.until > now {
            if !record.told {
                log::warn!(
                    "refusing passwords from {addr} for {} s more",
                    record.until - now
                );
                record.told = true;
            }
            return (false, record.until - now);
        }
        if now - record.last >= 30 {
            record.failures = 0;
            record.until = 0;
        }
        let right = check();
        if right {
            record.failures = 0;
            record.until = 0;
        } else {
            record.failures += 1;
            record.last = (self.now)();
            if record.failures >= 5 {
                record.until = record.last + 30;
                record.told = false;
                log::warn!("wrong password from {addr}, 5 in a row; wait 30 s");
            } else {
                log::warn!("wrong password from {addr}");
            }
        }
        (right, 0)
    }
}
pub fn random_hex() -> String {
    let mut bytes = [0u8; 32];
    getrandom::fill(&mut bytes).expect("operating system random source unavailable");
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}
#[derive(Clone)]
pub(crate) struct Session {
    pub id: String,
    pub csrf: String,
    pub logged_in: bool,
    pub expires: i64,
    pub flash: String,
}
pub(crate) struct Sessions {
    items: Mutex<HashMap<String, Session>>,
    key: Vec<u8>,
    now: Clock,
}
impl Sessions {
    pub fn new(now: Clock) -> Self {
        let mut key = vec![0; 32];
        getrandom::fill(&mut key).expect("operating system random source unavailable");
        Self {
            items: Mutex::new(HashMap::new()),
            key,
            now,
        }
    }
    fn sign(&self, raw: &[u8]) -> String {
        let mut mac = Hmac::<Sha256>::new_from_slice(&self.key).expect("valid HMAC key");
        mac.update(raw);
        mac.finalize()
            .into_bytes()
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect()
    }
    pub fn visit(&self) -> Session {
        let expires = (self.now)() + 3600;
        let mut raw = [0u8; 32];
        getrandom::fill(&mut raw[..24]).expect("operating system random source unavailable");
        raw[24..].copy_from_slice(&(expires as u64).to_be_bytes());
        let id = raw.iter().map(|b| format!("{b:02x}")).collect();
        Session {
            csrf: self.sign(&raw),
            id,
            logged_in: false,
            expires,
            flash: String::new(),
        }
    }
    pub fn get(&self, id: &str) -> Option<Session> {
        let now = (self.now)();
        let mut held = self.items.lock().unwrap();
        held.retain(|_, s| s.expires > now);
        held.get(id).cloned()
    }
    pub fn visitor(&self, id: &str) -> Option<Session> {
        if let Some(s) = self.get(id) {
            return Some(s);
        }
        if id.len() != 64 || !id.bytes().all(|b| b.is_ascii_hexdigit()) {
            return None;
        }
        let raw: Vec<u8> = (0..32)
            .map(|n| u8::from_str_radix(&id[n * 2..n * 2 + 2], 16))
            .collect::<std::result::Result<_, _>>()
            .ok()?;
        let expires = u64::from_be_bytes(raw[24..].try_into().ok()?) as i64;
        let now = (self.now)();
        if expires <= now || expires > now + 3600 {
            return None;
        }
        Some(Session {
            id: id.into(),
            csrf: self.sign(&raw),
            logged_in: false,
            expires,
            flash: String::new(),
        })
    }
    pub fn create(&self) -> Session {
        let s = Session {
            id: random_hex(),
            csrf: random_hex(),
            logged_in: true,
            expires: (self.now)() + 43200,
            flash: String::new(),
        };
        let mut held = self.items.lock().unwrap();
        held.retain(|_, s| s.expires > (self.now)());
        held.insert(s.id.clone(), s.clone());
        s
    }
    pub fn delete(&self, id: &str) {
        self.items.lock().unwrap().remove(id);
    }
    pub fn delete_others(&self, id: &str) {
        self.items.lock().unwrap().retain(|k, _| k == id);
    }
    pub fn flash(&self, id: &str, value: String) {
        if let Some(s) = self.items.lock().unwrap().get_mut(id) {
            s.flash = value;
        }
    }
    pub fn take_flash(&self, id: &str) -> String {
        self.items
            .lock()
            .unwrap()
            .get_mut(id)
            .map(|s| std::mem::take(&mut s.flash))
            .unwrap_or_default()
    }
}
impl Session {
    pub fn valid_token(&self, token: &str) -> bool {
        !token.is_empty() && bool::from(self.csrf.as_bytes().ct_eq(token.as_bytes()))
    }
}

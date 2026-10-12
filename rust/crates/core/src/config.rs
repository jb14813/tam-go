//! Go settings defaults, validated partial updates and last-good live recovery.
use crate::{Error, Result, files::atomic_write};
use serde::{
    Deserialize, Deserializer,
    de::{MapAccess, Visitor},
};
use std::{
    path::{Path, PathBuf},
    sync::Mutex,
    time::SystemTime,
};
#[derive(Clone, Debug, PartialEq, Eq, serde::Serialize)]
pub struct Settings {
    pub remote_server: String,
    pub remote_key: String,
    pub remote_port: String,
    pub remote_tls: bool,
    pub default_pref: String,
    pub venue_name: String,
    pub disable_attrib: bool,
    pub remote_name: String,
    pub remote_fingerprint: String,
}
impl Default for Settings {
    fn default() -> Self {
        Self {
            remote_server: String::new(),
            remote_key: String::new(),
            remote_port: "8000".into(),
            remote_tls: false,
            default_pref: "CALL".into(),
            venue_name: "Test Venue".into(),
            disable_attrib: false,
            remote_name: String::new(),
            remote_fingerprint: String::new(),
        }
    }
}
const KEYS: [&str; 9] = [
    "remote_server",
    "remote_key",
    "remote_port",
    "remote_tls",
    "default_pref",
    "venue_name",
    "disable_attrib",
    "remote_name",
    "remote_fingerprint",
];
impl<'de> Deserialize<'de> for Settings {
    fn deserialize<D: Deserializer<'de>>(d: D) -> std::result::Result<Self, D::Error> {
        struct Fields;
        impl<'de> Visitor<'de> for Fields {
            type Value = Settings;
            fn expecting(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
                f.write_str("settings object")
            }
            fn visit_unit<E: serde::de::Error>(self) -> std::result::Result<Settings, E> {
                Ok(Settings::default())
            }
            fn visit_map<A: MapAccess<'de>>(
                self,
                mut map: A,
            ) -> std::result::Result<Settings, A::Error> {
                let mut s = Settings::default();
                while let Some(key) = map.next_key::<String>()? {
                    let folded: String = key
                        .chars()
                        .map(|c| match c {
                            'ſ' => 's',
                            'K' => 'k',
                            c => c.to_ascii_lowercase(),
                        })
                        .collect();
                    let value = map.next_value::<serde_json::Value>()?;
                    if value.is_null() {
                        continue;
                    }
                    s.assign(&folded, value, false)
                        .map_err(serde::de::Error::custom)?;
                }
                Ok(s)
            }
        }
        d.deserialize_any(Fields)
    }
}
impl Settings {
    fn assign(&mut self, key: &str, value: serde_json::Value, strict: bool) -> Result<()> {
        if !KEYS.contains(&key) {
            return if strict {
                Err(Error::Validation(format!("unknown setting {key:?}")))
            } else {
                Ok(())
            };
        }
        if matches!(key, "remote_tls" | "disable_attrib") {
            let b = if value.is_null() {
                false
            } else {
                value.as_bool().ok_or_else(|| {
                    Error::Validation(format!("invalid settings: {key} must be a boolean"))
                })?
            };
            if key == "remote_tls" {
                self.remote_tls = b;
            } else {
                self.disable_attrib = b;
            }
        } else {
            let text = if value.is_null() {
                String::new()
            } else {
                value
                    .as_str()
                    .ok_or_else(|| {
                        Error::Validation(format!("invalid settings: {key} must be a string"))
                    })?
                    .to_owned()
            };
            *match key {
                "remote_server" => &mut self.remote_server,
                "remote_key" => &mut self.remote_key,
                "remote_port" => &mut self.remote_port,
                "default_pref" => &mut self.default_pref,
                "venue_name" => &mut self.venue_name,
                "remote_name" => &mut self.remote_name,
                _ => &mut self.remote_fingerprint,
            } = text;
        }
        Ok(())
    }
    pub fn merge(&self, patch: &[u8]) -> Result<Self> {
        let value: serde_json::Value = crate::wire::decode(patch)?;
        if value.is_null() {
            return Ok(self.clone());
        }
        let map = value
            .as_object()
            .ok_or_else(|| Error::Validation("settings patch must be an object".into()))?;
        let mut out = self.clone();
        for (k, v) in map {
            out.assign(k, v.clone(), true)?;
        }
        Ok(out)
    }
    pub fn normalize(mut self) -> Self {
        for field in [
            &mut self.remote_server,
            &mut self.remote_key,
            &mut self.remote_port,
            &mut self.default_pref,
            &mut self.venue_name,
            &mut self.remote_name,
        ] {
            *field = field.trim().to_owned();
        }
        self.remote_fingerprint = self
            .remote_fingerprint
            .trim()
            .replace(':', "")
            .to_ascii_lowercase();
        self
    }
    pub fn validate(&self) -> Result<()> {
        let fail = |s: &str| Err(Error::Validation(s.into()));
        if !self.remote_server.is_empty() {
            if self.remote_server.contains(['/', '\\', '?', '#', '@', ' '])
                || self.remote_server.contains("://")
            {
                return fail(
                    "remote_server must be a host name or IP address, without scheme, port or path",
                );
            }
            if self.remote_port.is_empty() {
                return fail("remote_port is required when remote_server is set");
            }
        }
        if !self.remote_port.is_empty()
            && self
                .remote_port
                .parse::<u16>()
                .ok()
                .filter(|n| *n > 0)
                .is_none()
        {
            return fail("remote_port must be a number between 1 and 65535");
        }
        if !matches!(self.default_pref.as_str(), "CALL" | "TEXT") {
            return fail("default_pref must be CALL or TEXT");
        }
        if !self.remote_fingerprint.is_empty()
            && (self.remote_fingerprint.len() != 64
                || !self
                    .remote_fingerprint
                    .bytes()
                    .all(|b| b.is_ascii_digit() || matches!(b, b'a'..=b'f')))
        {
            return fail("remote_fingerprint must be a SHA-256 fingerprint in hex, or empty");
        }
        Ok(())
    }
    pub fn remote_url(&self) -> String {
        let host = self.remote_server.trim();
        if host.is_empty() {
            return String::new();
        }
        let port = if self.remote_port.trim().is_empty() {
            "8000"
        } else {
            self.remote_port.trim()
        };
        format!(
            "{}://{}",
            if self.remote_tls { "https" } else { "http" },
            host_port(host, port)
        )
    }
}
pub fn host_port(host: &str, port: &str) -> String {
    if host.contains(':') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    }
}
pub fn backup_path(path: &Path) -> PathBuf {
    let mut p = path.as_os_str().to_os_string();
    p.push(".bak");
    p.into()
}
fn parse(path: &Path) -> Result<Settings> {
    crate::wire::decode(&std::fs::read(path)?)
}
fn save(path: &Path, s: &Settings) -> Result<()> {
    let mut bytes = serde_json::to_vec_pretty(s)?;
    bytes.push(b'\n');
    // Never replace a valid recovery copy with an unreadable live document.
    if let Ok(old) = std::fs::read(path)
        && crate::wire::decode::<Settings>(&old).is_ok()
    {
        atomic_write(&backup_path(path), &old)?;
    }
    atomic_write(path, &bytes)?;
    if let Err(error) = atomic_write(&backup_path(path), &bytes) {
        eprintln!("settings backup {}: {error}", backup_path(path).display());
    }
    Ok(())
}
struct State {
    cur: Settings,
    loaded: bool,
    stat: Option<(SystemTime, u64)>,
    problem: String,
}
pub struct SettingsFile {
    path: PathBuf,
    state: Mutex<State>,
}
fn stat(path: &Path) -> Option<(SystemTime, u64)> {
    let m = std::fs::metadata(path).ok()?;
    Some((m.modified().ok()?, m.len()))
}
impl SettingsFile {
    pub fn open(path: impl AsRef<Path>) -> Self {
        let f = Self {
            path: path.as_ref().to_owned(),
            state: Mutex::new(State {
                cur: Settings::default(),
                loaded: false,
                stat: None,
                problem: String::new(),
            }),
        };
        let _ = f.reload();
        if f.problem().is_empty()
            && let Ok(bytes) = std::fs::read(&f.path)
        {
            let _ = atomic_write(&backup_path(&f.path), &bytes);
        }
        f
    }
    fn reload_locked(&self, s: &mut State) -> Result<Settings> {
        let primary = parse(&self.path);
        s.stat = stat(&self.path);
        match primary {
            Ok(cur) => {
                s.cur = cur;
                s.loaded = true;
                s.problem.clear();
                Ok(s.cur.clone())
            }
            Err(e) => {
                let missing = matches!(&e,Error::Io(e) if e.kind()==std::io::ErrorKind::NotFound);
                match parse(&backup_path(&self.path)) {
                    Ok(cur) if !s.loaded || (missing && s.cur == cur) => {
                        s.cur = cur;
                        s.loaded = true;
                        s.problem = format!(
                            "settings.json could not be read ({e}); the client uses the copy it saved last (settings.json.bak). Save the settings again to repair the file."
                        );
                    }
                    Err(Error::Io(b))
                        if missing && b.kind() == std::io::ErrorKind::NotFound && !s.loaded =>
                    {
                        match save(&self.path, &s.cur) {
                            Ok(()) => {
                                s.loaded = true;
                                s.stat = stat(&self.path);
                                s.problem.clear();
                                return Ok(s.cur.clone());
                            }
                            Err(error) => {
                                s.problem = format!(
                                    "settings.json could not be created ({error}); the client runs with default settings."
                                );
                                return Err(error);
                            }
                        }
                    }
                    _ if s.loaded => {
                        s.problem = format!(
                            "settings.json was changed and cannot be read ({e}); the client keeps the settings it had. Fix the file or save the settings again."
                        );
                    }
                    _ => {
                        s.problem = format!(
                            "settings.json could not be read ({e}) and there is no good copy; the client runs with default settings, not paired with any server, until the file is fixed or the settings are saved again."
                        );
                    }
                }
                Err(e)
            }
        }
    }
    pub fn get(&self) -> Settings {
        let mut s = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if stat(&self.path) != s.stat || s.stat.is_none() {
            let _ = self.reload_locked(&mut s);
        }
        s.cur.clone()
    }
    pub fn problem(&self) -> String {
        self.get();
        self.state
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .problem
            .clone()
    }
    pub fn reload(&self) -> Result<Settings> {
        let mut s = self.state.lock().unwrap_or_else(|e| e.into_inner());
        self.reload_locked(&mut s)
    }
    pub fn update(&self, change: impl FnOnce(Settings) -> Result<Settings>) -> Result<Settings> {
        let mut s = self.state.lock().unwrap_or_else(|e| e.into_inner());
        if stat(&self.path) != s.stat || s.stat.is_none() {
            let _ = self.reload_locked(&mut s);
        }
        let next = change(s.cur.clone())?;
        save(&self.path, &next)?;
        s.cur = next.clone();
        s.loaded = true;
        s.problem.clear();
        s.stat = stat(&self.path);
        Ok(next)
    }
}

//! Go-style command-line options; parsing stops at the first positional argument.
use crate::{Error, Result};
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Role {
    Client,
    Server,
}
impl Role {
    pub fn usage(self) -> String {
        let tray_default = cfg!(windows);
        match self {
            Self::Client => format!(
                "R-TAM Client\nUsage: r-tam-client [options]\n  -addr string\n        address to listen on (default localhost:3080)\n  -open\n        open the web app in the default browser once listening (default true)\n  -tray\n        show an icon with Open and Shut Down entries (Windows; default {tray_default})\n  -h, --help\n        show this help\n"
            ),
            Self::Server => format!(
                "R-TAM Server\nUsage: r-tam-server [options] [dev]\n  -addr string\n        address to listen on (default :8000, or :8443 with -tls; dev uses localhost)\n  -announce\n        announce this server on the local network with mDNS (default true)\n  -cert string\n        TLS certificate file (PEM) for -tls; requires -key (default <data dir>/server.crt)\n  -key string\n        TLS private key file (PEM) for -cert (default <data dir>/server.key)\n  -tls\n        serve HTTPS; generate a self-signed certificate when no PEM pair is supplied\n  -tray\n        show an icon with Shut Down (Windows; default {tray_default})\n  -h, --help\n        show this help\n"
            ),
        }
    }
}
/// Expand Go's omitted listen host while preserving explicit host and IPv6 forms.
pub fn listen_address(addr: &str) -> String {
    if addr.starts_with(':') {
        format!("0.0.0.0{addr}")
    } else {
        addr.to_owned()
    }
}
#[derive(Clone, Debug)]
pub struct Options {
    pub help: bool,
    pub addr: String,
    pub open: bool,
    pub tray: bool,
    pub tls: bool,
    pub cert: String,
    pub key: String,
    pub announce: bool,
}
impl Options {
    pub fn parse(role: Role, args: impl IntoIterator<Item = impl AsRef<str>>) -> Result<Self> {
        let mut out = Self {
            help: false,
            addr: if role == Role::Client {
                "localhost:3080".into()
            } else {
                String::new()
            },
            open: role == Role::Client,
            tray: cfg!(windows),
            tls: false,
            cert: String::new(),
            key: String::new(),
            announce: true,
        };
        let args: Vec<String> = args.into_iter().map(|s| s.as_ref().to_owned()).collect();
        let mut i = 0;
        let mut dev = false;
        while i < args.len() {
            let arg = &args[i];
            if arg == "--" {
                dev = args.get(i + 1).is_some_and(|s| s == "dev");
                break;
            }
            if !arg.starts_with('-') || arg == "-" {
                dev = arg == "dev";
                break;
            }
            let text = arg
                .strip_prefix("--")
                .or_else(|| arg.strip_prefix('-'))
                .unwrap();
            let (name, inline) = text
                .split_once('=')
                .map_or((text, None), |(k, v)| (k, Some(v)));
            if matches!(name, "h" | "help") {
                out.help = true;
                return Ok(out);
            }
            let is_bool = matches!(name, "tray")
                || (role == Role::Client && name == "open")
                || (role == Role::Server && matches!(name, "tls" | "announce"));
            let is_text =
                name == "addr" || (role == Role::Server && matches!(name, "cert" | "key"));
            if !is_bool && !is_text {
                return Err(Error::Validation(format!(
                    "flag provided but not defined: -{name}"
                )));
            }
            let value = if is_bool {
                inline.unwrap_or("true").to_owned()
            } else if let Some(v) = inline {
                v.to_owned()
            } else {
                i += 1;
                args.get(i)
                    .ok_or_else(|| Error::Validation(format!("flag needs an argument: -{name}")))?
                    .clone()
            };
            if is_bool {
                let b = match value.as_str() {
                    "1" | "t" | "T" | "true" | "TRUE" | "True" => true,
                    "0" | "f" | "F" | "false" | "FALSE" | "False" => false,
                    _ => {
                        return Err(Error::Validation(format!(
                            "invalid value {value:?} for flag -{name}"
                        )));
                    }
                };
                match name {
                    "tray" => out.tray = b,
                    "open" => out.open = b,
                    "tls" => out.tls = b,
                    _ => out.announce = b,
                }
            } else {
                match name {
                    "addr" => out.addr = value,
                    "cert" => out.cert = value,
                    _ => out.key = value,
                }
            }
            i += 1;
        }
        if role == Role::Server && out.addr.is_empty() {
            out.addr = format!(
                "{}:{}",
                if dev { "localhost" } else { "" },
                if out.tls { 8443 } else { 8000 }
            );
        }
        Ok(out)
    }
}

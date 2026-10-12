//! JSON and ordered-save hashing compatible with encoding/json and net/url.
use crate::Result;
use serde::{Serialize, de::DeserializeOwned};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;

// Go replaces malformed UTF-8 one byte at a time, and unmatched UTF-16
// surrogate escapes inside JSON strings. Keep valid JSON bytes and key order.
fn go_string_bytes(bytes: &[u8]) -> Vec<u8> {
    let mut utf8 = Vec::with_capacity(bytes.len());
    let mut rest = bytes;
    while !rest.is_empty() {
        match std::str::from_utf8(rest) {
            Ok(_) => {
                utf8.extend_from_slice(rest);
                break;
            }
            Err(error) => {
                let valid = error.valid_up_to();
                utf8.extend_from_slice(&rest[..valid]);
                utf8.extend_from_slice("\u{fffd}".as_bytes());
                rest = &rest[valid + 1..];
            }
        }
    }
    fn unit(bytes: &[u8], at: usize) -> Option<u16> {
        let digits = bytes.get(at..at + 4)?;
        digits.iter().try_fold(0_u16, |n, b| {
            (*b as char).to_digit(16).map(|v| n * 16 + v as u16)
        })
    }
    let mut out = Vec::with_capacity(utf8.len());
    let mut in_string = false;
    let mut i = 0;
    while i < utf8.len() {
        if utf8[i] == b'"' {
            in_string = !in_string;
        }
        if in_string && utf8[i] == b'\\' {
            if utf8.get(i + 1) == Some(&b'u')
                && let Some(code) = unit(&utf8, i + 2)
            {
                if (0xd800..=0xdbff).contains(&code) {
                    if utf8.get(i + 6..i + 8) == Some(b"\\u")
                        && unit(&utf8, i + 8).is_some_and(|low| (0xdc00..=0xdfff).contains(&low))
                    {
                        out.extend_from_slice(&utf8[i..i + 12]);
                        i += 12;
                        continue;
                    }
                    out.extend_from_slice(br"\ufffd");
                    i += 6;
                    continue;
                }
                if (0xdc00..=0xdfff).contains(&code) {
                    out.extend_from_slice(br"\ufffd");
                    i += 6;
                    continue;
                }
            }
            out.push(utf8[i]);
            i += 1;
            if let Some(&next) = utf8.get(i) {
                out.push(next);
                i += 1;
            }
            continue;
        }
        out.push(utf8[i]);
        i += 1;
    }
    out
}
pub fn decode<T: DeserializeOwned>(bytes: &[u8]) -> Result<T> {
    Ok(serde_json::from_slice(&go_string_bytes(bytes))?)
}
pub fn go_json<T: Serialize + ?Sized>(value: &T) -> Result<Vec<u8>> {
    let encoded = serde_json::to_vec(value)?;
    let mut out = Vec::with_capacity(encoded.len());
    let mut i = 0;
    while i < encoded.len() {
        match encoded[i] {
            b'<' => out.extend_from_slice(br"\u003c"),
            b'>' => out.extend_from_slice(br"\u003e"),
            b'&' => out.extend_from_slice(br"\u0026"),
            0xe2 if encoded.get(i + 1) == Some(&0x80)
                && matches!(encoded.get(i + 2), Some(0xa8 | 0xa9)) =>
            {
                out.extend_from_slice(if encoded[i + 2] == 0xa8 {
                    br"\u2028"
                } else {
                    br"\u2029"
                });
                i += 2;
            }
            b => out.push(b),
        }
        i += 1;
    }
    Ok(out)
}
fn unescape(text: &str) -> Option<Vec<u8>> {
    let mut out = Vec::with_capacity(text.len());
    let bytes = text.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        match bytes[i] {
            b'+' => out.push(b' '),
            b'%' => {
                let high = (*bytes.get(i + 1)? as char).to_digit(16)?;
                let low = (*bytes.get(i + 2)? as char).to_digit(16)?;
                out.push((high * 16 + low) as u8);
                i += 2;
            }
            b => out.push(b),
        }
        i += 1;
    }
    Some(out)
}
fn escape(bytes: &[u8]) -> String {
    const HEX: &[u8] = b"0123456789ABCDEF";
    let mut out = String::new();
    for &b in bytes {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            b' ' => out.push('+'),
            _ => {
                out.push('%');
                out.push(HEX[(b >> 4) as usize] as char);
                out.push(HEX[(b & 15) as usize] as char);
            }
        }
    }
    out
}
/// Mirrors URL.Query().Encode(), including ignored malformed query pairs.
pub fn canonical_query(query: &str) -> String {
    let mut values: BTreeMap<Vec<u8>, Vec<Vec<u8>>> = BTreeMap::new();
    for pair in query
        .split('&')
        .filter(|p| !p.is_empty() && !p.contains(';'))
    {
        let (key, value) = pair.split_once('=').unwrap_or((pair, ""));
        if let (Some(key), Some(value)) = (unescape(key), unescape(value)) {
            values.entry(key).or_default().push(value);
        }
    }
    values
        .into_iter()
        .flat_map(|(k, vs)| {
            vs.into_iter()
                .map(move |v| format!("{}={}", escape(&k), escape(&v)))
        })
        .collect::<Vec<_>>()
        .join("&")
}
/// `body` must be validated, normalized, typed `go_json` bytes. DELETE content
/// is `b"null"`, as in Go's digest(r, nil). Never reparse into unordered objects.
pub fn save_digest(method: &str, path: &str, query: &str, body: &[u8]) -> String {
    let mut hash = Sha256::new();
    hash.update(format!("{method} {path}?{}\n", canonical_query(query)));
    hash.update(body);
    format!("{:x}", hash.finalize())
}

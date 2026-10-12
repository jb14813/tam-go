//! Typed wire fields match internal/store/{models,edits,order}.go.
use crate::{Error, Result};
use serde::{
    Deserialize, Deserializer, Serialize,
    de::{self, MapAccess, Visitor},
};
use serde_json::value::RawValue;

pub const MAX_EXACT: i64 = (1_i64 << 53) - 1;
pub const COLORS: [&str; 7] = [
    "white", "blue", "yellow", "green", "orange", "purple", "red",
];
// Protocol field names are ASCII. Go's Unicode fold additionally recognizes
// long s and the Kelvin sign as S and K when matching those names.
fn field_matches(input: &str, field: &str) -> bool {
    input
        .chars()
        .map(|c| match c {
            'ſ' => 's',
            'K' => 'k',
            _ => c.to_ascii_lowercase(),
        })
        .eq(field.chars())
}
fn is_zero(n: &i64) -> bool {
    *n == 0
}
fn integer(raw: &RawValue, null_ok: bool) -> std::result::Result<i64, String> {
    let text = raw.get();
    if null_ok && text == "null" {
        return Ok(0);
    }
    text.parse::<i64>()
        .ok()
        .filter(|n| (-MAX_EXACT..=MAX_EXACT).contains(n))
        .ok_or_else(|| {
            format!(
                "{text} is not a whole number from {} to {MAX_EXACT}",
                -MAX_EXACT
            )
        })
}
fn string_field(slot: &mut String, raw: &RawValue) -> std::result::Result<(), String> {
    if let Some(value) =
        serde_json::from_str::<Option<String>>(raw.get()).map_err(|e| e.to_string())?
    {
        *slot = value;
    }
    Ok(())
}
fn number_field(slot: &mut i64, raw: &RawValue, null_ok: bool) -> std::result::Result<(), String> {
    *slot = integer(raw, null_ok)?;
    Ok(())
}
fn revision_field(slot: &mut i64, raw: &RawValue) -> std::result::Result<(), String> {
    let value = integer(raw, true)?;
    if value < 0 {
        return Err(format!("an order number cannot be below 0: {value}"));
    }
    *slot = value;
    Ok(())
}
fn boolean_field(slot: &mut bool, raw: &RawValue) -> std::result::Result<(), String> {
    if let Some(value) =
        serde_json::from_str::<Option<bool>>(raw.get()).map_err(|e| e.to_string())?
    {
        *slot = value;
    }
    Ok(())
}
fn list_field<T: serde::de::DeserializeOwned>(
    slot: &mut Vec<T>,
    raw: &RawValue,
) -> std::result::Result<(), String> {
    *slot = serde_json::from_str::<Option<Vec<T>>>(raw.get())
        .map_err(|e| e.to_string())?
        .unwrap_or_default();
    Ok(())
}
macro_rules! assign {
    (string,$slot:expr,$raw:expr) => {
        string_field(&mut $slot, &$raw)
    };
    (number,$slot:expr,$raw:expr) => {
        number_field(&mut $slot, &$raw, true)
    };
    (required,$slot:expr,$raw:expr) => {
        number_field(&mut $slot, &$raw, true)
    };
    (revision,$slot:expr,$raw:expr) => {
        revision_field(&mut $slot, &$raw)
    };
    (boolean,$slot:expr,$raw:expr) => {
        boolean_field(&mut $slot, &$raw)
    };
    (list,$slot:expr,$raw:expr) => {
        list_field(&mut $slot, &$raw)
    };
}
macro_rules! check_required {
    (required,$seen:expr,$field:ident) => {
        if !$seen.contains(stringify!($field)) {
            let kind = if stringify!($field) == "t_id" {
                "ticket"
            } else {
                "basket"
            };
            return Err(de::Error::custom(format!(
                "{kind}: {} is required",
                stringify!($field)
            )));
        }
    };
    ($other:ident,$seen:expr,$field:ident) => {};
}
macro_rules! mark_seen {
    (required,$seen:expr,$field:ident,$raw:expr) => {
        if $raw.get() == "null" {
            $seen.remove(stringify!($field));
        } else {
            $seen.insert(stringify!($field));
        }
    };
    ($other:ident,$seen:expr,$field:ident,$raw:expr) => {
        $seen.insert(stringify!($field));
    };
}
// Sequential fields retain Go's last-key behavior, case folding and null strings.
macro_rules! go_struct {
    ($name:ident { $( $(#[$attr:meta])* $kind:ident $field:ident : $ty:ty ),* $(,)? }) => {
        #[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
        pub struct $name { $( $(#[$attr])* pub $field: $ty, )* }
        impl $name {
            /// Merge object fields into an existing value, as Go does for a
            /// repeated non-null pointer to base values in the same document.
            pub fn merge_json(&mut self,raw:&RawValue)->serde_json::Result<()> {
                let mut decoder=serde_json::Deserializer::from_str(raw.get());
                *self=Self::read_with(self.clone(),&mut decoder)?;
                decoder.end()
            }
            fn read_with<'de,D:Deserializer<'de>>(initial:Self,d:D)->std::result::Result<Self,D::Error> {
                struct GoVisitor {initial:$name}
                impl<'de> Visitor<'de> for GoVisitor {
                    type Value=$name;
                    fn expecting(&self,f:&mut std::fmt::Formatter)->std::fmt::Result { f.write_str("a Go protocol object") }
                    fn visit_unit<E:de::Error>(self)->std::result::Result<$name,E> {
                        let seen=std::collections::BTreeSet::<String>::new();
                        $(check_required!($kind,seen,$field);)*
                        let _=&seen;
                        Ok(self.initial)
                    }
                    fn visit_map<M:MapAccess<'de>>(self,mut map:M)->std::result::Result<$name,M::Error> {
                        let mut out=self.initial;
                        let mut seen=std::collections::BTreeSet::new();
                        while let Some(key)=map.next_key::<String>()? {
                            let raw=map.next_value::<Box<RawValue>>()?;
                            $(if field_matches(&key,stringify!($field)) {
                                assign!($kind,out.$field,raw).map_err(de::Error::custom)?;
                                mark_seen!($kind,seen,$field,raw);
                                continue;
                            })*
                        }
                        $(check_required!($kind,seen,$field);)*
                        Ok(out)
                    }
                }
                d.deserialize_any(GoVisitor {initial})
            }
        }
        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D:Deserializer<'de>>(d:D)->std::result::Result<Self,D::Error> { Self::read_with(Self::default(),d) }
        }
    };
}

go_struct!(Prefix { string prefix:String, string color:String, number weight:i64, #[serde(skip_serializing_if="is_zero")] revision rev:i64 });
go_struct!(Ticket { string prefix:String, required t_id:i64, string first_name:String, string last_name:String, string phone_number:String, string pref:String, #[serde(skip_serializing_if="is_zero")] revision rev:i64 });
go_struct!(Basket { string prefix:String, required b_id:i64, string description:String, string donors:String, number winning_ticket:i64, #[serde(skip_serializing_if="is_zero")] revision rev:i64, #[serde(skip_serializing_if="is_zero")] revision win_rev:i64 });
go_struct!(DrawingLine { string prefix:String, number b_id:i64, string description:String, number winning_ticket:i64, string last_name:String, string first_name:String, string phone_number:String, #[serde(skip_serializing_if="is_zero")] revision win_rev:i64 });
go_struct!(ReportByNameLine { string last_name:String, string first_name:String, string phone_number:String, string pref:String, string prefix:String, number b_id:i64, string description:String, string donors:String, number winning_ticket:i64 });
go_struct!(ReportByBasketLine { string prefix:String, number b_id:i64, string description:String, string donors:String, number winning_ticket:i64, string last_name:String, string first_name:String, string phone_number:String, string pref:String });
go_struct!(ReportCountLine { string prefix:String, boolean is_total:bool, number unique_buyers:i64, number total_buys:i64 });
go_struct!(AuthKey { string auth_key:String, string description:String, #[serde(skip_serializing_if="String::is_empty")] string last_seen:String, #[serde(skip_serializing_if="String::is_empty")] string last_update:String });
go_struct!(BackupFile { #[serde(skip_serializing_if="String::is_empty")] string event:String, list prefixes:Vec<Prefix>, list baskets:Vec<Basket>, list tickets:Vec<Ticket> });
go_struct!(TicketValues { string first_name:String,string last_name:String,string phone_number:String,string pref:String });
go_struct!(BasketValues { string description:String,string donors:String });
go_struct!(DrawingValues { number winning_ticket:i64 });
go_struct!(PrefixValues { string color:String,number weight:i64 });
go_struct!(Conflict { string kind:String,string prefix:String,number id:i64,string field:String,string yours:String,string now:String });
go_struct!(Event { string event:String,string started:String,revision last_rev:i64 });

struct ObjectFields(Vec<(String, Box<RawValue>)>);
impl<'de> Deserialize<'de> for ObjectFields {
    fn deserialize<D: Deserializer<'de>>(d: D) -> std::result::Result<Self, D::Error> {
        struct FieldsVisitor;
        impl<'de> Visitor<'de> for FieldsVisitor {
            type Value = ObjectFields;
            fn expecting(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
                f.write_str("an object")
            }
            fn visit_unit<E: de::Error>(self) -> std::result::Result<Self::Value, E> {
                Ok(ObjectFields(Vec::new()))
            }
            fn visit_map<M: MapAccess<'de>>(
                self,
                mut map: M,
            ) -> std::result::Result<Self::Value, M::Error> {
                let mut fields = Vec::new();
                while let Some(field) = map.next_entry()? {
                    fields.push(field);
                }
                Ok(ObjectFields(fields))
            }
        }
        d.deserialize_any(FieldsVisitor)
    }
}
macro_rules! save_struct {
    ($name:ident,$field:ident,$model:ident,$base:ident) => {
        #[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
        pub struct $name {
            #[serde(flatten)]
            pub $field: $model,
            #[serde(skip_serializing_if = "Option::is_none")]
            pub base: Option<$base>,
        }
        impl<'de> Deserialize<'de> for $name {
            fn deserialize<D: Deserializer<'de>>(d: D) -> std::result::Result<Self, D::Error> {
                let raw = Box::<RawValue>::deserialize(d)?;
                let model = serde_json::from_str(raw.get()).map_err(de::Error::custom)?;
                let extra: ObjectFields =
                    serde_json::from_str(raw.get()).map_err(de::Error::custom)?;
                let mut base: Option<$base> = None;
                for (key, value) in extra.0 {
                    if field_matches(&key, "base") {
                        if value.get() == "null" {
                            base = None;
                        } else {
                            base.get_or_insert_with($base::default)
                                .merge_json(&value)
                                .map_err(de::Error::custom)?;
                        }
                    }
                }
                Ok(Self {
                    $field: model,
                    base,
                })
            }
        }
    };
}
save_struct!(TicketSave, ticket, Ticket, TicketValues);
save_struct!(BasketSave, basket, Basket, BasketValues);
save_struct!(DrawingSave, basket, Basket, DrawingValues);
save_struct!(PrefixSave, prefix, Prefix, PrefixValues);

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Order {
    pub client: String,
    pub save: i64,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Outcome {
    Applied,
    Repeat,
    Behind,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Form {
    Prefixes,
    Tickets,
    Baskets,
    Drawing,
}
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(untagged)]
pub enum SaveBatch {
    Prefixes(Vec<PrefixSave>),
    Tickets(Vec<TicketSave>),
    Baskets(Vec<BasketSave>),
    Drawing(Vec<DrawingSave>),
}
impl Form {
    pub fn from_path(path: &str) -> Option<Self> {
        match path.strip_prefix("/api").unwrap_or(path) {
            "/prefixes" => Some(Self::Prefixes),
            "/tickets" => Some(Self::Tickets),
            "/baskets" => Some(Self::Baskets),
            "/drawing" => Some(Self::Drawing),
            _ => None,
        }
    }
}
impl SaveBatch {
    pub fn decode(form: Form, body: &[u8]) -> Result<Self> {
        Ok(match form {
            Form::Prefixes => Self::Prefixes(
                crate::wire::decode::<Option<Vec<PrefixSave>>>(body)?.unwrap_or_default(),
            ),
            Form::Tickets => Self::Tickets(
                crate::wire::decode::<Option<Vec<TicketSave>>>(body)?.unwrap_or_default(),
            ),
            Form::Baskets => Self::Baskets(
                crate::wire::decode::<Option<Vec<BasketSave>>>(body)?.unwrap_or_default(),
            ),
            Form::Drawing => Self::Drawing(
                crate::wire::decode::<Option<Vec<DrawingSave>>>(body)?.unwrap_or_default(),
            ),
        })
    }
    pub fn form(&self) -> Form {
        match self {
            Self::Prefixes(_) => Form::Prefixes,
            Self::Tickets(_) => Form::Tickets,
            Self::Baskets(_) => Form::Baskets,
            Self::Drawing(_) => Form::Drawing,
        }
    }
    pub fn validate(&mut self) -> Result<()> {
        match self {
            Self::Prefixes(rows) => {
                for (i, row) in rows.iter_mut().enumerate() {
                    validate_prefix(&mut row.prefix, i + 1)?;
                }
            }
            Self::Tickets(rows) => {
                for (i, row) in rows.iter_mut().enumerate() {
                    validate_ticket(&mut row.ticket, i + 1)?;
                    if let Some(base) = &mut row.base {
                        base.pref = base.pref.trim().to_owned();
                    }
                }
            }
            Self::Baskets(rows) => {
                for (i, row) in rows.iter_mut().enumerate() {
                    validate_basket(&mut row.basket, i + 1)?;
                }
            }
            Self::Drawing(rows) => {
                for (i, row) in rows.iter_mut().enumerate() {
                    validate_basket(&mut row.basket, i + 1)?;
                }
                for (i, row) in rows.iter().enumerate() {
                    if row.base.as_ref().is_some_and(|b| b.winning_ticket < 0) {
                        return invalid(format!(
                            "basket {} ({}/{}): winning ticket must be zero or more",
                            i + 1,
                            row.basket.prefix,
                            row.basket.b_id
                        ));
                    }
                }
            }
        }
        Ok(())
    }
}
fn invalid<T>(text: String) -> Result<T> {
    Err(Error::Validation(text))
}
fn diagnostic_quote(value: &str) -> String {
    // Keep Rust's Unicode printability handling, using Go's escape spelling.
    let debug = format!("{value:?}");
    let mut out = String::with_capacity(debug.len());
    let mut chars = debug.chars();
    while let Some(ch) = chars.next() {
        if ch != '\\' {
            out.push(ch);
            continue;
        }
        match chars.next() {
            Some('0') => out.push_str("\\x00"),
            Some('u') => {
                let mut escape = String::from("\\u");
                if chars.next() == Some('{') {
                    let mut digits = String::new();
                    for digit in chars.by_ref() {
                        if digit == '}' {
                            break;
                        }
                        digits.push(digit);
                    }
                    if let Ok(code) = u32::from_str_radix(&digits, 16) {
                        escape = match code {
                            7 => "\\a".to_owned(),
                            8 => "\\b".to_owned(),
                            11 => "\\v".to_owned(),
                            12 => "\\f".to_owned(),
                            0..=31 | 127 => format!("\\x{code:02x}"),
                            32..=65535 => format!("\\u{code:04x}"),
                            _ => format!("\\U{code:08x}"),
                        };
                    }
                }
                out.push_str(&escape);
            }
            Some(escaped) => {
                out.push('\\');
                out.push(escaped);
            }
            None => out.push('\\'),
        }
    }
    out
}
pub fn validate_prefix_name(name: &str) -> Result<String> {
    let name = name.trim();
    if name.is_empty() {
        return invalid("prefix name must not be empty".into());
    }
    if name.chars().count() > 100 {
        return invalid("prefix name must be at most 100 characters".into());
    }
    if name.contains(['/', '\\']) {
        return invalid("prefix name must not contain / or \\".into());
    }
    if name == "." || name == ".." {
        return invalid("prefix name must not be . or ..".into());
    }
    if name.chars().any(char::is_control) {
        return invalid("prefix name must not contain control characters".into());
    }
    Ok(name.to_owned())
}
pub fn validate_prefix(p: &mut Prefix, index: usize) -> Result<()> {
    p.prefix = validate_prefix_name(&p.prefix)
        .map_err(|e| Error::Validation(format!("prefix {index}: {e}")))?;
    if !COLORS.contains(&p.color.as_str()) {
        return invalid(format!(
            "prefix {index} ({}): color {} is not one of {}",
            p.prefix,
            diagnostic_quote(&p.color),
            COLORS.join(", ")
        ));
    }
    if p.weight < 0 {
        return invalid(format!(
            "prefix {index} ({}): weight must be zero or more",
            p.prefix
        ));
    }
    Ok(())
}
pub fn validate_ticket(t: &mut Ticket, index: usize) -> Result<()> {
    t.prefix = t.prefix.trim().to_owned();
    if t.prefix.is_empty() {
        return invalid(format!("ticket {index}: prefix must not be empty"));
    }
    if t.t_id < 0 {
        return invalid(format!(
            "ticket {index} ({}/{}): id must be zero or more",
            t.prefix, t.t_id
        ));
    }
    t.pref = t.pref.trim().to_owned();
    Ok(())
}
pub fn validate_basket(b: &mut Basket, index: usize) -> Result<()> {
    b.prefix = b.prefix.trim().to_owned();
    if b.prefix.is_empty() {
        return invalid(format!("basket {index}: prefix must not be empty"));
    }
    if b.b_id < 0 {
        return invalid(format!(
            "basket {index} ({}/{}): id must be zero or more",
            b.prefix, b.b_id
        ));
    }
    if b.winning_ticket < 0 {
        return invalid(format!(
            "basket {index} ({}/{}): winning ticket must be zero or more",
            b.prefix, b.b_id
        ));
    }
    Ok(())
}
impl BackupFile {
    pub fn validate(&mut self) -> Result<()> {
        for (i, p) in self.prefixes.iter_mut().enumerate() {
            if p.prefix.is_empty() {
                return invalid(format!("prefix {}: prefix name must not be empty", i + 1));
            }
            if !COLORS.contains(&p.color.as_str()) {
                p.color = "white".to_owned();
            }
        }
        for (i, b) in self.baskets.iter().enumerate() {
            if b.prefix.is_empty() {
                return invalid(format!("basket {}: prefix must not be empty", i + 1));
            }
            if b.b_id < 0 || b.winning_ticket < 0 {
                return invalid(format!(
                    "basket {} ({}/{}): ids must be zero or more",
                    i + 1,
                    b.prefix,
                    b.b_id
                ));
            }
        }
        for (i, t) in self.tickets.iter().enumerate() {
            if t.prefix.is_empty() {
                return invalid(format!("ticket {}: prefix must not be empty", i + 1));
            }
            if t.t_id < 0 {
                return invalid(format!(
                    "ticket {} ({}/{}): id must be zero or more",
                    i + 1,
                    t.prefix,
                    t.t_id
                ));
            }
        }
        Ok(())
    }
}

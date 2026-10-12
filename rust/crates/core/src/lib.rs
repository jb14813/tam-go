//! Shared Go-compatible protocol and persistence foundations.
pub mod cli;
pub mod config;
pub mod desktop;
pub mod discovery;
pub mod error;
pub mod files;
pub mod models;
pub mod remote;
pub mod schema;
pub mod store;
pub mod tls;
pub mod wire;
pub use error::{Error, Result};

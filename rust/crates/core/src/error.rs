/// Errors distinguish details safe to return to callers from daemon diagnostics.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("{0}")]
    Validation(String),
    #[error("{0}")]
    Decode(#[from] serde_json::Error),
    #[error("{0}")]
    Database(#[from] rusqlite::Error),
    #[error("{0}")]
    Io(#[from] std::io::Error),
    #[error("{0}")]
    Internal(String),
}
pub type Result<T> = std::result::Result<T, Error>;
impl Error {
    pub fn public_detail(&self) -> String {
        match self {
            Self::Validation(detail) => detail.clone(),
            Self::Decode(error) => error.to_string(),
            _ => "Internal error; see the daemon log".to_owned(),
        }
    }
    pub fn is_validation(&self) -> bool {
        matches!(self, Self::Validation(_) | Self::Decode(_))
    }
}

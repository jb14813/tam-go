//! Durable replacement and immutable event archives.
use crate::{Error, Result, models::BackupFile};
use std::{
    fs::OpenOptions,
    io::Write,
    path::{Path, PathBuf},
};
pub fn atomic_write(path: &Path, data: &[u8]) -> Result<()> {
    let parent = path
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let mut tmp = tempfile::Builder::new()
        .prefix(".r-tam-")
        .tempfile_in(parent)?;
    tmp.write_all(data)?;
    tmp.as_file().sync_all()?;
    #[cfg(not(windows))]
    {
        tmp.persist(path).map_err(|e| e.error)?;
    }
    #[cfg(windows)]
    {
        use std::os::windows::ffi::OsStrExt;
        use windows_sys::Win32::Storage::FileSystem::{
            MOVEFILE_REPLACE_EXISTING, MOVEFILE_WRITE_THROUGH, MoveFileExW,
        };
        let temp = tmp.into_temp_path();
        let from: Vec<u16> = temp.as_os_str().encode_wide().chain(Some(0)).collect();
        let to: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
        // SAFETY: both pointers refer to terminated, live UTF-16 path buffers.
        if unsafe {
            MoveFileExW(
                from.as_ptr(),
                to.as_ptr(),
                MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
            )
        } == 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
    }
    sync_directory(parent)
}
pub(crate) fn sync_directory(dir: &Path) -> Result<()> {
    #[cfg(unix)]
    {
        std::fs::File::open(dir)?.sync_all()?;
    }
    #[cfg(not(unix))]
    {
        let _ = dir;
    }
    Ok(())
}
pub(crate) fn create_synced(path: &Path, data: &[u8]) -> Result<()> {
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut f = options.open(path)?;
    let result = (|| {
        f.write_all(data)?;
        f.sync_all()?;
        drop(f);
        sync_directory(path.parent().unwrap_or(Path::new(".")))
    })();
    if result.is_err() {
        let _ = std::fs::remove_file(path);
    }
    result
}
pub fn preserve_event(dir: &Path, label: &str, backup: &BackupFile) -> Result<PathBuf> {
    if label.is_empty()
        || label
            .chars()
            .any(|c| !c.is_ascii_alphanumeric() && !matches!(c, '-' | '_'))
    {
        return Err(Error::Validation("invalid event archive label".into()));
    }
    let data = crate::wire::go_json(backup)?;
    for _ in 0..32 {
        let mut random = [0u8; 16];
        getrandom::fill(&mut random).map_err(|e| Error::Internal(e.to_string()))?;
        let suffix: String = random.iter().map(|b| format!("{b:02x}")).collect();
        let path = dir.join(format!("{label}{suffix}.json"));
        match create_synced(&path, &data) {
            Ok(()) => return Ok(path),
            Err(Error::Io(e)) if e.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(e),
        }
    }
    Err(Error::Internal(
        "cannot allocate unique event archive".into(),
    ))
}

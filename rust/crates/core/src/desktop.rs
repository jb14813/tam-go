//! Folder ownership, append logs, browser startup and graceful desktop lifecycle.
use crate::{Error, Result};
use fs2::FileExt;
use std::{
    fs::{File, OpenOptions},
    io::Write,
    net::SocketAddr,
    path::{Path, PathBuf},
    sync::{Arc, Condvar, Mutex},
};
pub struct InstanceGuard {
    file: File,
}
impl InstanceGuard {
    pub fn acquire(dir: &Path) -> Result<Self> {
        std::fs::create_dir_all(dir)?;
        let mut file = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(dir.join(".r-tam.lock"))?;
        FileExt::try_lock_exclusive(&file).map_err(|e| {
            Error::Internal(format!(
                "another R-TAM process owns data folder {}: {e}",
                dir.display()
            ))
        })?;
        file.set_len(0)?;
        writeln!(file, "{}", std::process::id())?;
        file.sync_all()?;
        Ok(Self { file })
    }
}
impl Drop for InstanceGuard {
    fn drop(&mut self) {
        let _ = FileExt::unlock(&self.file);
    }
}
pub fn data_dir() -> Result<PathBuf> {
    let path = std::env::var_os("TAM_DATA_DIR")
        .filter(|s| !s.is_empty())
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("./data"));
    std::fs::create_dir_all(&path)?;
    Ok(path)
}
pub fn open_log(dir: &Path, name: &str) -> Result<File> {
    if Path::new(name).components().count() != 1 || name.is_empty() || name == "." || name == ".." {
        return Err(Error::Validation("log name must be a file name".into()));
    }
    Ok(OpenOptions::new()
        .create(true)
        .append(true)
        .open(dir.join(name))?)
}
pub fn browser_url(bound: SocketAddr, tls: bool) -> String {
    let host = if bound.ip().is_unspecified() {
        format!("localhost:{}", bound.port())
    } else {
        bound.to_string()
    };
    format!("{}://{host}/", if tls { "https" } else { "http" })
}
pub fn browser_command(url: &str) -> (&'static str, Vec<&str>) {
    if cfg!(windows) {
        ("rundll32", vec!["url.dll,FileProtocolHandler", url])
    } else if cfg!(target_os = "macos") {
        ("open", vec![url])
    } else {
        ("xdg-open", vec![url])
    }
}
pub fn open_browser(url: &str) -> Result<()> {
    let (program, args) = browser_command(url);
    let mut child = std::process::Command::new(program).args(args).spawn()?;
    std::thread::spawn(move || {
        let _ = child.wait();
    });
    Ok(())
}
pub fn open_browser_after_bind(listener: &tokio::net::TcpListener, tls: bool) -> Result<String> {
    let url = browser_url(listener.local_addr()?, tls);
    open_browser(&url)?;
    Ok(url)
}
#[derive(Clone, Copy)]
struct State {
    requested: bool,
    active: usize,
}
struct ShutdownInner {
    state: Mutex<State>,
    changed: Condvar,
    watch: tokio::sync::watch::Sender<State>,
}
#[derive(Clone)]
pub struct Shutdown {
    inner: Arc<ShutdownInner>,
}
pub struct Activity {
    inner: Arc<ShutdownInner>,
}
impl Default for Shutdown {
    fn default() -> Self {
        Self::new()
    }
}
impl Shutdown {
    pub fn new() -> Self {
        let state = State {
            requested: false,
            active: 0,
        };
        let (watch, _) = tokio::sync::watch::channel(state);
        Self {
            inner: Arc::new(ShutdownInner {
                state: Mutex::new(state),
                changed: Condvar::new(),
                watch,
            }),
        }
    }
    pub fn enter(&self) -> Option<Activity> {
        let mut s = self.inner.state.lock().unwrap_or_else(|e| e.into_inner());
        if s.requested {
            return None;
        }
        s.active += 1;
        self.inner.watch.send_replace(*s);
        Some(Activity {
            inner: self.inner.clone(),
        })
    }
    pub fn request(&self) {
        let mut s = self.inner.state.lock().unwrap_or_else(|e| e.into_inner());
        s.requested = true;
        self.inner.watch.send_replace(*s);
        self.inner.changed.notify_all();
    }
    pub fn is_requested(&self) -> bool {
        self.inner
            .state
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .requested
    }
    pub async fn requested(&self) {
        let mut rx = self.inner.watch.subscribe();
        let _ = rx.wait_for(|s| s.requested).await;
    }
    pub async fn drain(&self) {
        let mut rx = self.inner.watch.subscribe();
        let _ = rx.wait_for(|s| s.requested && s.active == 0).await;
    }
    #[cfg(windows)]
    fn wait_requested(&self) {
        let mut s = self.inner.state.lock().unwrap_or_else(|e| e.into_inner());
        while !s.requested {
            s = self
                .inner
                .changed
                .wait(s)
                .unwrap_or_else(|e| e.into_inner());
        }
    }
}
impl Drop for Activity {
    fn drop(&mut self) {
        let mut s = self.inner.state.lock().unwrap_or_else(|e| e.into_inner());
        s.active -= 1;
        self.inner.watch.send_replace(*s);
        self.inner.changed.notify_all();
    }
}
pub async fn stop_on_signal(shutdown: Shutdown) -> Result<()> {
    #[cfg(windows)]
    let _console = ConsoleCloseGuard::install(shutdown.clone())?;
    #[cfg(unix)]
    {
        let mut term = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())?;
        tokio::select! {r=tokio::signal::ctrl_c()=>{r?;shutdown.request();},_=term.recv()=>shutdown.request(),_=shutdown.requested()=>{}}
    }
    #[cfg(not(unix))]
    {
        tokio::select! {r=tokio::signal::ctrl_c()=>{r?;shutdown.request();},_=shutdown.requested()=>{}}
    }
    Ok(())
}
pub const CLIENT_ICON: &[u8] = include_bytes!("../../../../cmd/tam-client/icon.ico");
pub const SERVER_ICON: &[u8] = include_bytes!("../../../../cmd/tam-server/icon.ico");
#[derive(Clone)]
pub struct TrayOptions {
    pub tooltip: String,
    pub icon: &'static [u8],
    pub open_url: String,
    pub quit_label: String,
}
#[derive(Clone, Copy)]
pub enum TrayAction {
    Open,
    Quit,
}
pub struct TrayHandle {
    #[cfg(windows)]
    window: usize,
    thread: Option<std::thread::JoinHandle<()>>,
}
impl TrayHandle {
    pub fn dispatch(&self, action: TrayAction) -> Result<()> {
        #[cfg(windows)]
        {
            use windows_sys::Win32::UI::WindowsAndMessaging::{PostMessageW, WM_COMMAND};
            let id = match action {
                TrayAction::Open => 1,
                TrayAction::Quit => 2,
            };
            // SAFETY: window was created by the owned tray thread; PostMessage is cross-thread.
            if unsafe { PostMessageW(self.window as _, WM_COMMAND, id, 0) } == 0 {
                return Err(std::io::Error::last_os_error().into());
            }
        }
        #[cfg(not(windows))]
        {
            let _ = action;
        }
        Ok(())
    }
    pub fn join(mut self) -> Result<()> {
        if let Some(thread) = self.thread.take() {
            thread
                .join()
                .map_err(|_| Error::Internal("tray thread panicked".into()))?;
        }
        Ok(())
    }
}
pub fn tray(options: TrayOptions, shutdown: Shutdown) -> Result<TrayHandle> {
    #[cfg(windows)]
    {
        native::start(options, shutdown)
    }
    #[cfg(not(windows))]
    {
        let _ = (options, shutdown);
        Ok(TrayHandle { thread: None })
    }
}
pub fn set_console_title(title: &str) {
    #[cfg(windows)]
    {
        let wide: Vec<u16> = title.encode_utf16().chain(Some(0)).collect();
        // SAFETY: wide is a live terminated UTF-16 buffer.
        unsafe {
            windows_sys::Win32::System::Console::SetConsoleTitleW(wide.as_ptr());
        }
    }
    #[cfg(not(windows))]
    {
        let _ = title;
    }
}
#[cfg(windows)]
pub use native::ConsoleCloseGuard;
#[cfg(windows)]
mod native {
    use super::*;
    use std::sync::mpsc;
    use windows_sys::Win32::{
        Foundation::*,
        System::{Console::*, LibraryLoader::GetModuleHandleW},
        UI::{Shell::*, WindowsAndMessaging::*},
    };
    const CALLBACK: u32 = WM_APP + 1;
    static CONSOLE: Mutex<Option<Shutdown>> = Mutex::new(None);
    pub struct ConsoleCloseGuard;
    unsafe extern "system" fn console_handler(event: u32) -> i32 {
        if matches!(
            event,
            CTRL_C_EVENT
                | CTRL_BREAK_EVENT
                | CTRL_CLOSE_EVENT
                | CTRL_LOGOFF_EVENT
                | CTRL_SHUTDOWN_EVENT
        ) {
            let stop = CONSOLE.lock().unwrap_or_else(|e| e.into_inner()).clone();
            if let Some(stop) = stop {
                stop.request();
                if matches!(
                    event,
                    CTRL_CLOSE_EVENT | CTRL_LOGOFF_EVENT | CTRL_SHUTDOWN_EVENT
                ) {
                    let state = stop.inner.state.lock().unwrap_or_else(|e| e.into_inner());
                    let _ = stop.inner.changed.wait_timeout_while(
                        state,
                        std::time::Duration::from_secs(3),
                        |s| s.active > 0,
                    );
                }
                return 1;
            }
        }
        0
    }
    impl ConsoleCloseGuard {
        pub fn install(stop: Shutdown) -> Result<Self> {
            let mut slot = CONSOLE.lock().unwrap_or_else(|e| e.into_inner());
            if slot.is_some() {
                return Err(Error::Internal(
                    "console shutdown handler already installed".into(),
                ));
            }
            // SAFETY: process-global function pointer has static lifetime; registration is scoped.
            if unsafe { SetConsoleCtrlHandler(Some(console_handler), 1) } == 0 {
                return Err(std::io::Error::last_os_error().into());
            }
            *slot = Some(stop);
            Ok(Self)
        }
        /// Dispatch an OS console event through the registered handler, useful to
        /// hosts embedding R-TAM in an existing console event dispatcher.
        pub fn handle_event(&self, event: u32) -> bool {
            unsafe { console_handler(event) != 0 }
        }
    }
    impl Drop for ConsoleCloseGuard {
        fn drop(&mut self) {
            unsafe {
                SetConsoleCtrlHandler(Some(console_handler), 0);
            }
            *CONSOLE.lock().unwrap_or_else(|e| e.into_inner()) = None;
        }
    }
    struct WindowState {
        options: TrayOptions,
        shutdown: Shutdown,
        icon: HICON,
        notify: NOTIFYICONDATAW,
    }
    fn wide(s: &str) -> Vec<u16> {
        s.encode_utf16().chain(Some(0)).collect()
    }
    unsafe extern "system" fn window_proc(
        window: HWND,
        message: u32,
        w: WPARAM,
        l: LPARAM,
    ) -> LRESULT {
        // SAFETY: USERDATA belongs to this window and is held until the message loop ends.
        unsafe {
            let ptr = GetWindowLongPtrW(window, GWLP_USERDATA) as *mut WindowState;
            if ptr.is_null() {
                return DefWindowProcW(window, message, w, l);
            }
            let state = &*ptr;
            match message {
                WM_COMMAND => {
                    match w & 0xffff {
                        1 => {
                            if !state.options.open_url.is_empty() {
                                let _ = open_browser(&state.options.open_url);
                            }
                        }
                        2 => state.shutdown.request(),
                        _ => {}
                    }
                    0
                }
                CALLBACK => {
                    match l as u32 {
                        WM_LBUTTONUP => {
                            if !state.options.open_url.is_empty() {
                                let _ = open_browser(&state.options.open_url);
                            }
                        }
                        WM_RBUTTONUP => {
                            let menu = CreatePopupMenu();
                            if !state.options.open_url.is_empty() {
                                AppendMenuW(menu, MF_STRING, 1, wide("Open R-TAM").as_ptr());
                                AppendMenuW(menu, MF_SEPARATOR, 0, std::ptr::null());
                            }
                            AppendMenuW(
                                menu,
                                MF_STRING,
                                2,
                                wide(&state.options.quit_label).as_ptr(),
                            );
                            let mut point: POINT = std::mem::zeroed();
                            GetCursorPos(&mut point);
                            SetForegroundWindow(window);
                            TrackPopupMenu(
                                menu,
                                TPM_RIGHTBUTTON,
                                point.x,
                                point.y,
                                0,
                                window,
                                std::ptr::null(),
                            );
                            DestroyMenu(menu);
                            PostMessageW(window, WM_NULL, 0, 0);
                        }
                        _ => {}
                    }
                    0
                }
                WM_QUERYENDSESSION => {
                    state.shutdown.request();
                    1
                }
                WM_CLOSE => {
                    state.shutdown.request();
                    Shell_NotifyIconW(NIM_DELETE, &state.notify);
                    DestroyWindow(window);
                    0
                }
                WM_DESTROY => {
                    state.shutdown.request();
                    PostQuitMessage(0);
                    0
                }
                _ => DefWindowProcW(window, message, w, l),
            }
        }
    }
    pub fn start(options: TrayOptions, shutdown: Shutdown) -> Result<TrayHandle> {
        let (tx, rx) = mpsc::sync_channel(1);
        let end = shutdown.clone();
        let thread = std::thread::spawn(move || {
            let result = (|| -> Result<usize> {
                // SAFETY: all HWND/icon/menu operations run on the owning message thread.
                unsafe {
                    let module = GetModuleHandleW(std::ptr::null());
                    let class = wide("R-TAM Tray Window");
                    let wc = WNDCLASSW {
                        lpfnWndProc: Some(window_proc),
                        hInstance: module,
                        lpszClassName: class.as_ptr(),
                        ..std::mem::zeroed()
                    };
                    RegisterClassW(&wc);
                    let window = CreateWindowExW(
                        0,
                        class.as_ptr(),
                        class.as_ptr(),
                        0,
                        0,
                        0,
                        0,
                        0,
                        std::ptr::null_mut(),
                        std::ptr::null_mut(),
                        module,
                        std::ptr::null(),
                    );
                    if window.is_null() {
                        return Err(std::io::Error::last_os_error().into());
                    }
                    let icon_bytes = options.icon;
                    if icon_bytes.len() < 22 {
                        DestroyWindow(window);
                        return Err(Error::Validation("invalid ICO icon".into()));
                    }
                    let size = u32::from_le_bytes(icon_bytes[14..18].try_into().unwrap()) as usize;
                    let offset =
                        u32::from_le_bytes(icon_bytes[18..22].try_into().unwrap()) as usize;
                    let Some(image) = icon_bytes.get(offset..offset.saturating_add(size)) else {
                        DestroyWindow(window);
                        return Err(Error::Validation("invalid ICO image range".into()));
                    };
                    let icon = CreateIconFromResourceEx(
                        image.as_ptr(),
                        image.len() as u32,
                        1,
                        0x00030000,
                        32,
                        32,
                        LR_DEFAULTCOLOR,
                    );
                    if icon.is_null() {
                        DestroyWindow(window);
                        return Err(std::io::Error::last_os_error().into());
                    }
                    let mut notify: NOTIFYICONDATAW = std::mem::zeroed();
                    notify.cbSize = std::mem::size_of::<NOTIFYICONDATAW>() as u32;
                    notify.hWnd = window;
                    notify.uID = 1;
                    notify.uFlags = NIF_ICON | NIF_TIP | NIF_MESSAGE;
                    notify.uCallbackMessage = CALLBACK;
                    notify.hIcon = icon;
                    for (dst, src) in notify
                        .szTip
                        .iter_mut()
                        .take(127)
                        .zip(options.tooltip.encode_utf16())
                    {
                        *dst = src;
                    }
                    let mut state = Box::new(WindowState {
                        options,
                        shutdown: shutdown.clone(),
                        icon,
                        notify,
                    });
                    SetWindowLongPtrW(window, GWLP_USERDATA, (&mut *state) as *mut _ as isize);
                    if Shell_NotifyIconW(NIM_ADD, &state.notify) == 0 {
                        DestroyWindow(window);
                        DestroyIcon(icon);
                        return Err(std::io::Error::last_os_error().into());
                    }
                    let address = window as usize;
                    tx.send(Ok(address))
                        .map_err(|e| Error::Internal(e.to_string()))?;
                    let stop = shutdown.clone();
                    std::thread::spawn(move || {
                        stop.wait_requested();
                        PostMessageW(address as _, WM_CLOSE, 0, 0);
                    });
                    let mut message: MSG = std::mem::zeroed();
                    while GetMessageW(&mut message, std::ptr::null_mut(), 0, 0) > 0 {
                        TranslateMessage(&message);
                        DispatchMessageW(&message);
                    }
                    Shell_NotifyIconW(NIM_DELETE, &state.notify);
                    SetWindowLongPtrW(window, GWLP_USERDATA, 0);
                    DestroyWindow(window);
                    DestroyIcon(state.icon);
                    state.shutdown.request();
                    Ok(address)
                }
            })();
            if let Err(error) = result {
                let _ = tx.send(Err(error));
                end.request();
            }
        });
        match rx.recv().map_err(|e| Error::Internal(e.to_string()))? {
            Ok(window) => Ok(TrayHandle {
                window,
                thread: Some(thread),
            }),
            Err(error) => {
                let _ = thread.join();
                Err(error)
            }
        }
    }
}

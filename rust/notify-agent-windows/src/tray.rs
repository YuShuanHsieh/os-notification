//! System tray icon for the Windows agent (design: ported from the C# TrayApplicationContext).
//! Owns a hidden window whose sole purpose is to host a `Shell_NotifyIconW` tray icon and pump
//! the Win32 messages it needs (tray click callback, context menu). Runs on the thread that
//! calls `run_message_loop`, which blocks for the app's whole lifetime — the tokio runtime and
//! `AgentHost` live on a separate thread (see `main.rs`), since a Win32 message pump and a
//! blocking `block_on` can't share one thread.

use std::sync::mpsc;
use std::time::Duration;

use windows::core::{HSTRING, PCWSTR};
use windows::Win32::Foundation::{HINSTANCE, HWND, LPARAM, LRESULT, WPARAM};
use windows::Win32::System::LibraryLoader::GetModuleHandleW;
use windows::Win32::UI::Shell::{
    Shell_NotifyIconW, NIF_ICON, NIF_MESSAGE, NIF_TIP, NIM_ADD, NIM_DELETE, NIM_MODIFY,
    NOTIFYICONDATAW,
};
use windows::Win32::UI::WindowsAndMessaging::{
    AppendMenuW, CreatePopupMenu, CreateWindowExW, DefWindowProcW, DispatchMessageW, GetCursorPos,
    GetMessageW, GetWindowLongPtrW, LoadCursorW, LoadIconW, PostMessageW, RegisterClassExW,
    SetForegroundWindow, SetWindowLongPtrW, TrackPopupMenu, TranslateMessage, CREATESTRUCTW,
    GWLP_USERDATA, HMENU, IDC_ARROW, MF_DISABLED, MF_SEPARATOR, MF_STRING, MSG,
    TPM_BOTTOMALIGN, TPM_LEFTALIGN, TPM_RIGHTBUTTON, WM_APP, WM_COMMAND, WM_LBUTTONUP, WM_NCCREATE,
    WM_NULL, WM_RBUTTONUP, WNDCLASSEXW, WS_OVERLAPPED,
};

const CLASS_NAME: &str = "NotifyAgentRustTrayWindow";
const BASE_TOOLTIP: &str = "Desktop Notification Agent";
const TRAY_UID: u32 = 1;
const WM_TRAYICON: u32 = WM_APP + 1;
const ID_MENU_VERSION: usize = 1001;
const ID_MENU_CLOSE: usize = 1002;
const CLOSE_TIMEOUT: Duration = Duration::from_secs(5);
/// Resource id of the embedded icon (see `icon.rc` / `assets/app.ico`, compiled in via `build.rs`).
const APP_ICON_ID: u16 = 1;

/// `Shell_NotifyIconW` is documented safe to call from any thread, so this small handle (just
/// the window handle) is shared with the agent thread to flag a startup failure directly,
/// without hopping back onto the window thread the way the WinForms `NotifyIcon` needs to.
#[derive(Clone, Copy)]
pub struct TrayHandle(HWND);

unsafe impl Send for TrayHandle {}

impl TrayHandle {
    pub fn set_start_failed(&self) {
        let mut data = NOTIFYICONDATAW {
            cbSize: std::mem::size_of::<NOTIFYICONDATAW>() as u32,
            hWnd: self.0,
            uID: TRAY_UID,
            uFlags: NIF_TIP,
            ..Default::default()
        };
        set_tip(
            &mut data,
            &format!("{BASE_TOOLTIP} (agent failed to start)"),
        );
        unsafe {
            let _ = Shell_NotifyIconW(NIM_MODIFY, &data);
        }
    }
}

struct TrayState {
    menu: HMENU,
    close_tx: tokio::sync::mpsc::UnboundedSender<()>,
    done_rx: std::cell::RefCell<Option<mpsc::Receiver<()>>>,
}

fn set_tip(data: &mut NOTIFYICONDATAW, text: &str) {
    let wide: Vec<u16> = text.encode_utf16().take(data.szTip.len() - 1).collect();
    data.szTip[..wide.len()].copy_from_slice(&wide);
    data.szTip[wide.len()] = 0;
}

/// Creates the hidden tray window and shows the icon immediately (before the agent has
/// finished starting, matching the C# design's "icon appears without waiting on NATS connect").
/// `close_tx` is signaled (from the window thread) when the user clicks Close; `done_rx` is
/// consumed by the Close handler's watcher thread, which waits up to `CLOSE_TIMEOUT` for the
/// agent thread to report shutdown finished, then hard-exits the process regardless.
pub fn create(
    close_tx: tokio::sync::mpsc::UnboundedSender<()>,
    done_rx: mpsc::Receiver<()>,
) -> anyhow::Result<TrayHandle> {
    unsafe {
        let hinstance: HINSTANCE = GetModuleHandleW(PCWSTR::null())?.into();
        let class_name = HSTRING::from(CLASS_NAME);

        let wc = WNDCLASSEXW {
            cbSize: std::mem::size_of::<WNDCLASSEXW>() as u32,
            lpfnWndProc: Some(wndproc),
            hInstance: hinstance,
            hCursor: LoadCursorW(None, IDC_ARROW)?,
            lpszClassName: PCWSTR(class_name.as_ptr()),
            ..Default::default()
        };
        if RegisterClassExW(&wc) == 0 {
            anyhow::bail!(
                "RegisterClassExW failed: {:?}",
                windows::core::Error::from_win32()
            );
        }

        let menu = CreatePopupMenu()?;
        AppendMenuW(
            menu,
            MF_STRING | MF_DISABLED,
            ID_MENU_VERSION,
            &HSTRING::from(format!("Version {}", env!("CARGO_PKG_VERSION"))),
        )?;
        AppendMenuW(menu, MF_SEPARATOR, 0, PCWSTR::null())?;
        AppendMenuW(menu, MF_STRING, ID_MENU_CLOSE, &HSTRING::from("Close"))?;

        let state = Box::into_raw(Box::new(TrayState {
            menu,
            close_tx,
            done_rx: std::cell::RefCell::new(Some(done_rx)),
        }));

        let hwnd = CreateWindowExW(
            Default::default(),
            &class_name,
            &HSTRING::from(CLASS_NAME),
            WS_OVERLAPPED,
            0,
            0,
            0,
            0,
            None,
            None,
            hinstance,
            Some(state as *const std::ffi::c_void),
        )?;

        let mut data = NOTIFYICONDATAW {
            cbSize: std::mem::size_of::<NOTIFYICONDATAW>() as u32,
            hWnd: hwnd,
            uID: TRAY_UID,
            uFlags: NIF_MESSAGE | NIF_ICON | NIF_TIP,
            uCallbackMessage: WM_TRAYICON,
            hIcon: LoadIconW(hinstance, PCWSTR(APP_ICON_ID as _))?,
            ..Default::default()
        };
        set_tip(&mut data, BASE_TOOLTIP);
        Shell_NotifyIconW(NIM_ADD, &data).ok()?;

        Ok(TrayHandle(hwnd))
    }
}

/// Blocks the calling thread pumping Win32 messages for the app's whole lifetime. Only
/// returns if the message loop is torn down (WM_QUIT) or GetMessageW errors — in practice
/// the process exits via `std::process::exit` from the Close watcher thread before that
/// ever happens.
pub fn run_message_loop() {
    let mut msg = MSG::default();
    unsafe {
        loop {
            // GetMessageW returns 0 on WM_QUIT, -1 on error, nonzero otherwise — a plain
            // bool conversion would treat -1 as "message received" and dispatch `msg`
            // uninitialized/stale.
            if GetMessageW(&mut msg, None, 0, 0).0 <= 0 {
                break;
            }
            let _ = TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }
    }
}

fn state_from_hwnd(hwnd: HWND) -> Option<&'static TrayState> {
    unsafe {
        let ptr = GetWindowLongPtrW(hwnd, GWLP_USERDATA) as *const TrayState;
        ptr.as_ref()
    }
}

unsafe extern "system" fn wndproc(hwnd: HWND, msg: u32, wparam: WPARAM, lparam: LPARAM) -> LRESULT {
    if msg == WM_NCCREATE {
        let create_struct = &*(lparam.0 as *const CREATESTRUCTW);
        SetWindowLongPtrW(hwnd, GWLP_USERDATA, create_struct.lpCreateParams as isize);
        return DefWindowProcW(hwnd, msg, wparam, lparam);
    }

    let Some(state) = state_from_hwnd(hwnd) else {
        return DefWindowProcW(hwnd, msg, wparam, lparam);
    };

    match msg {
        WM_TRAYICON if matches!(lparam.0 as u32, WM_RBUTTONUP | WM_LBUTTONUP) => {
            let mut pt = Default::default();
            let _ = GetCursorPos(&mut pt);
            let _ = SetForegroundWindow(hwnd); // so the menu dismisses on click-away
            let _ = TrackPopupMenu(
                state.menu,
                TPM_RIGHTBUTTON | TPM_BOTTOMALIGN | TPM_LEFTALIGN,
                pt.x,
                pt.y,
                0,
                hwnd,
                None,
            );
            // Required after TrackPopupMenu per MSDN, else the menu can fail to close.
            let _ = PostMessageW(hwnd, WM_NULL, WPARAM(0), LPARAM(0));
            LRESULT(0)
        }
        // WM_COMMAND packs the notification code in HIWORD(wParam); it's 0 for menu clicks,
        // but mask it explicitly rather than relying on that always being the case.
        WM_COMMAND if (wparam.0 & 0xFFFF) == ID_MENU_CLOSE => {
            let data = NOTIFYICONDATAW {
                cbSize: std::mem::size_of::<NOTIFYICONDATAW>() as u32,
                hWnd: hwnd,
                uID: TRAY_UID,
                ..Default::default()
            };
            let _ = Shell_NotifyIconW(NIM_DELETE, &data);
            let _ = state.close_tx.send(());
            if let Some(done_rx) = state.done_rx.borrow_mut().take() {
                std::thread::spawn(move || {
                    let _ = done_rx.recv_timeout(CLOSE_TIMEOUT);
                    std::process::exit(0);
                });
            }
            LRESULT(0)
        }
        _ => DefWindowProcW(hwnd, msg, wparam, lparam),
    }
}

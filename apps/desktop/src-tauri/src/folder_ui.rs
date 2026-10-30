//! Native system dialogs/open-folder only, called on the platform UI thread.
#[cfg(target_os="macos")]
#[path="folder_macos.rs"]mod platform;
#[cfg(target_os="linux")]
#[path="folder_linux.rs"]mod platform;
#[cfg(any(target_os="macos",target_os="linux"))]
pub use platform::{choose,cancel,cancel_all,open};

#[cfg(not(any(target_os="macos",target_os="linux")))]
mod unsupported{
    use crate::boundary::{Error,Result};
    use std::{path::{Path,PathBuf},sync::mpsc::SyncSender};
    pub fn choose(_: &tauri::WebviewWindow,_:&str,_:&Path,_:SyncSender<Result<Option<PathBuf>>>)->Result<()>{Err(Error::Unsupported)}
    pub fn cancel(_:&str){}pub fn cancel_all(){}
    pub fn open(_:&Path)->Result<()>{Err(Error::Unsupported)}
}
#[cfg(not(any(target_os="macos",target_os="linux")))]
pub use unsupported::{choose,cancel,cancel_all,open};

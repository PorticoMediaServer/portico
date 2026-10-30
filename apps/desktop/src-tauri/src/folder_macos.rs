//! AppKit chooser on its UI thread; cancellation never owns a Downloads lock.
use crate::boundary::{Error,Result};
use std::{collections::HashMap,ffi::{c_char,c_int,c_void,CString},path::{Path,PathBuf},sync::{Mutex,OnceLock,mpsc::SyncSender,atomic::{AtomicU64,Ordering}}};
type Reply=SyncSender<Result<Option<PathBuf>>>;
static REPLIES:OnceLock<Mutex<HashMap<u64,Reply>>>=OnceLock::new();
static NEXT:AtomicU64=AtomicU64::new(1);
unsafe extern "C"{
    fn portico_folder_choose(window:*mut c_void,operation:*const c_char,initial:*const c_char,request:u64);
    fn portico_folder_cancel(operation:*const c_char);
    fn portico_folder_cancel_all();
    fn portico_folder_open(path:*const c_char)->c_int;
}
fn path(value:&Path)->Result<CString>{CString::new(value.to_str().ok_or(Error::DownloadsUnavailable)?).map_err(|_|Error::DownloadsUnavailable)}
pub fn choose(window:&tauri::WebviewWindow,token:&str,initial:&Path,reply:Reply)->Result<()>{
    let operation=CString::new(token).map_err(|_|Error::InvalidCommand)?;let initial=path(initial)?;
    let parent=window.ns_window().map_err(|_|Error::WindowUnavailable)?;
    if parent.is_null(){return Err(Error::WindowUnavailable)}
    let request=NEXT.fetch_update(Ordering::AcqRel,Ordering::Acquire,|value|value.checked_add(1)).map_err(|_|Error::DownloadsUnavailable)?;
    REPLIES.get_or_init(Default::default).lock().map_err(|_|Error::DownloadsUnavailable)?.insert(request,reply);
    unsafe{portico_folder_choose(parent,operation.as_ptr(),initial.as_ptr(),request)};Ok(())
}
/// C may report a duplicate or late completion; only the retained sender can fire.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn portico_folder_completed(request:u64,bytes:*const u8,length:usize,status:c_int){
    let reply=REPLIES.get().and_then(|map|map.lock().ok()?.remove(&request));let Some(reply)=reply else{return};
    let value=match status{
        1=>Ok(None),
        0 if !bytes.is_null()&&length>0&&length<=32768=>{
            let raw=unsafe{std::slice::from_raw_parts(bytes,length)};
            std::str::from_utf8(raw).map(PathBuf::from).map(Some).map_err(|_|Error::DownloadsUnavailable)
        },
        _=>Err(Error::DownloadsUnavailable),
    };
    let _=reply.send(value);
}
pub fn cancel(token:&str){if let Ok(token)=CString::new(token){unsafe{portico_folder_cancel(token.as_ptr());}}}
pub fn cancel_all(){unsafe{portico_folder_cancel_all();}}
pub fn open(directory:&Path)->Result<()>{let directory=path(directory)?;if unsafe{portico_folder_open(directory.as_ptr())}==1{Ok(())}else{Err(Error::DownloadsUnavailable)}}

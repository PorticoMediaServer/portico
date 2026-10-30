use crate::boundary::{Error,Result};
use gtk::{prelude::*,glib::translate::IntoGlib};
use std::{cell::RefCell,path::{Path,PathBuf},sync::mpsc::SyncSender};
thread_local!{static ACTIVE:RefCell<Option<(String,gtk::FileChooserNative)>>=const{RefCell::new(None)};}
pub fn choose(window:&tauri::WebviewWindow,token:&str,initial:&Path,reply:SyncSender<Result<Option<PathBuf>>>)->Result<()>{
    if ACTIVE.with(|active|active.borrow().is_some()){return Err(Error::DownloadsConflict)}
    let parent=window.gtk_window().map_err(|_|Error::WindowUnavailable)?;
    let dialog=gtk::FileChooserNative::new(None,Some(&parent),gtk::FileChooserAction::SelectFolder,None,None);
    dialog.set_current_folder(initial);dialog.set_create_folders(true);
    let key=token.to_owned();let reply=RefCell::new(Some(reply));
    dialog.connect_response(move|dialog,response|{
        let Some(reply)=reply.borrow_mut().take()else{return};
        ACTIVE.with(|active|{let mut active=active.borrow_mut();if active.as_ref().is_some_and(|(token,_)|token==&key){active.take();}});
        dialog.hide();
        let result=if response==gtk::ResponseType::Accept{dialog.filename().map(Some).ok_or(Error::DownloadsUnavailable)}else{Ok(None)};
        let _=reply.send(result);
    });
    ACTIVE.with(|active|*active.borrow_mut()=Some((token.to_owned(),dialog.clone())));dialog.show();Ok(())
}
pub fn cancel(token:&str){
    let dialog=ACTIVE.with(|active|active.borrow().as_ref().filter(|(key,_)|key==token).map(|(_,dialog)|dialog.clone()));
    if let Some(dialog)=dialog{dialog.hide();dialog.emit_by_name::<()>("response",&[&gtk::ResponseType::Cancel.into_glib()]);}
}
pub fn cancel_all(){let token=ACTIVE.with(|active|active.borrow().as_ref().map(|(token,_)|token.clone()));if let Some(token)=token{cancel(&token);}}
pub fn open(directory:&Path)->Result<()>{
    let uri=gtk::glib::filename_to_uri(directory,None).map_err(|_|Error::DownloadsUnavailable)?;
    gtk::gio::AppInfo::launch_default_for_uri(&uri,None::<&gtk::gio::AppLaunchContext>).map_err(|_|Error::DownloadsUnavailable)
}

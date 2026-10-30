use crate::{audio::AudioOutput,boundary::{self,Command,Error,Observation,Result},mpv::{Api,Mpv}};
use std::{path::PathBuf,sync::{Arc,Mutex}};
use crate::shutdown::ShutdownState;
use tauri::{Manager,WebviewWindow};
use crate::{equalizer::{EqualizerChange,EqualizerSnapshot},equalizer_store::EqualizerStore};

#[cfg(target_os="linux")]
#[path="surface_linux.rs"] mod surface;
#[cfg(target_os="macos")]
#[path="surface_macos.rs"] mod surface;
#[cfg(target_os="windows")]
#[path="surface_windows.rs"] mod surface;

pub struct EngineState(pub Mutex<std::result::Result<Arc<Mpv>,Error>>);
pub struct EqualizerState(pub Mutex<std::result::Result<EqualizerStore,Error>>);
pub struct InstallationState(pub Mutex<std::result::Result<crate::installation_store::InstallationStore,Error>>);
pub struct DownloadsState(pub Arc<Mutex<std::result::Result<crate::download_commands::DownloadsSession,Error>>>);
fn caller(window:&WebviewWindow)->Result<()> {
    if !window.state::<ShutdownState>().accepts_commands(){return Err(Error::EngineUnavailable)}
    boundary::local_origin(window.label(),window.url().map_err(|_|Error::ForbiddenOrigin)?.as_str(),cfg!(debug_assertions))
}

#[tauri::command]
fn desktop_measurement_options(window:WebviewWindow,state:tauri::State<crate::measurement::MeasurementState>)->Result<Option<serde_json::Value>>{
    caller(&window)?;state.options(window.url().map_err(|_|Error::ForbiddenOrigin)?.as_str())
}
#[tauri::command]
fn desktop_measurement_write(window:WebviewWindow,state:tauri::State<crate::measurement::MeasurementState>,report:serde_json::Value)->Result<()>{
    caller(&window)?;state.write(window.url().map_err(|_|Error::ForbiddenOrigin)?.as_str(),report)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{download_commands::{Bytes,DownloadOperation as O,DownloadReply as R,DownloadsSession,LocalLoadRequest,LocalSelection,ViewerScope},download_store::DownloadTitle,mpv::VideoTarget};
    #[test]
    #[ignore="requires explicit native libmpv and an owned PCM fixture"]
    fn verified_local_load_ack_and_journal_follow_actual_native_observations(){
        let bytes=std::fs::read(std::env::var_os("PORTICO_TEST_PCM_FIXTURE").expect("owned PCM fixture")).unwrap();
        let sha=ring::digest::digest(&ring::digest::SHA256,&bytes).as_ref().iter().map(|b|format!("{b:02x}")).collect::<String>();
        let root=std::env::temp_dir().join(format!("desktop-local-real-{}",std::process::id()));
        let mut session=DownloadsSession::open(&root).unwrap();
        let scope=ViewerScope{authority:"local".into(),server_id:"server".into(),account_id:"account".into(),profile_id:"profile".into(),session_family_id:"family".into()};
        let lease=session.bind(scope.clone()).unwrap();
        let R::Entry(mut row)=session.execute(&lease,O::Enroll{preparation_id:"preparation".into(),item_id:"item".into(),quality:"original".into(),sha256:sha.clone(),bytes:bytes.len() as u64,
            title:DownloadTitle{kind:"song".into(),title:"Owned PCM".into(),artist:String::new(),parent_id:String::new(),parent_title:String::new(),group_id:String::new(),group_title:String::new(),group_number:None,number:None,year:2026,duration_ms:2000,on_server:false}}).unwrap()else{panic!("native enrollment")};
        for chunk in bytes.chunks(262144){let R::Entry(next)=session.execute(&lease,O::Append{id:row.id.clone(),expected_revision:row.revision,offset:row.bytes_done,bytes:Bytes(chunk.to_vec())}).unwrap()else{panic!("native append")};row=next;}
        let R::Entry(row)=session.execute(&lease,O::Seal{id:row.id,expected_revision:row.revision}).unwrap()else{panic!("native seal")};
        let viewer=crate::download_receipt::DownloadViewer{authority:scope.authority.clone(),server_id:scope.server_id.clone(),account_id:scope.account_id.clone(),profile_id:scope.profile_id.clone()};
        let (receipt,key)=crate::download_receipt::tests::signed(&viewer,&sha,bytes.len() as u64);
        session.execute(&lease,O::ReceiptKey{key_id:"key".into(),public_key:key}).unwrap();session.execute(&lease,O::Receipt{id:row.id.clone(),envelope:receipt}).unwrap();
        let R::LocalQueue(prepared)=session.execute(&lease,O::PrepareLocalQueue{selection:LocalSelection::Entry{entry_id:row.id.clone(),expected_revision:row.revision}}).unwrap()else{panic!("native preparation")};
        let library=std::env::var_os("PORTICO_TEST_MPV_LIBRARY").expect("native libmpv");
        let engine=Mpv::new(Api::open(std::path::Path::new(&library)).unwrap(),&AudioOutput::default(),VideoTarget::Headless).unwrap();
        let request=LocalLoadRequest{preparation_id:prepared.preparation_id,local_source_id:prepared.items[0].local_source_id.clone(),generation:1,start_position_ms:0.0,autoplay:true};
        let ack=session.load_local(&lease,&request,&engine.observation(),|path|engine.load_local(path,1,0.0,true)).unwrap();
        assert_eq!(ack.artifact.sha256,sha);assert_eq!(ack.artifact.bytes,bytes.len() as u64);
        let until=std::time::Instant::now()+std::time::Duration::from_secs(5);
        let observed=loop{let reading=engine.observation();if reading.state=="playing"&&reading.position_ms>10.0{break reading}assert_ne!(reading.state,"error");assert!(std::time::Instant::now()<until,"native local file did not advance");std::thread::sleep(std::time::Duration::from_millis(10));};
        session.record_observation(&observed,true).unwrap();
        let R::PendingPlays(plays)=session.execute(&lease,O::PendingPlays{}).unwrap()else{panic!("native journal")};
        assert_eq!(plays.len(),1);assert_eq!(plays[0].play_id,ack.play_id);assert_eq!(plays[0].position_ms,observed.position_ms.round() as u64);
        session.retire_local_load(&lease,1,&observed,||engine.execute(Command::Stop{generation:1})).unwrap();
        assert_eq!(engine.observation().state,"idle");assert_eq!(session.active_generation(),None);
        let result=serde_json::to_string(&ack).unwrap();assert!(!result.contains("path"));assert!(!result.contains("url"));assert!(!result.contains(root.to_str().unwrap()));
        drop(engine);drop(session);std::fs::remove_dir_all(root).unwrap();
    }
}
#[tauri::command]
async fn desktop_network_cost(window:WebviewWindow)->Result<crate::network::NetworkCost>{
    caller(&window)?;
    tauri::async_runtime::spawn_blocking(crate::network::current).await.map_err(|_|Error::EngineUnavailable)
}
#[tauri::command]
async fn desktop_discover(window:WebviewWindow,request_id:u64)->Result<Vec<crate::discovery::DiscoveryRoute>>{
    caller(&window)?;
    let browser=window.state::<Arc<crate::discovery::Browser>>().inner().clone();
    tauri::async_runtime::spawn_blocking(move||browser.browse(request_id).map_err(|_|Error::DiscoveryUnavailable))
        .await.map_err(|_|Error::DiscoveryUnavailable)?
}
#[tauri::command]
fn desktop_discovery_cancel(window:WebviewWindow,request_id:u64)->Result<()>{
    caller(&window)?;
    window.state::<Arc<crate::discovery::Browser>>().cancel(request_id);Ok(())
}
#[tauri::command]
async fn desktop_device_facts(window:WebviewWindow)->Result<crate::device_facts::DeviceFacts>{
    caller(&window)?;
    tauri::async_runtime::spawn_blocking(crate::device_facts::current).await.map_err(|_|Error::EngineUnavailable)
}
#[derive(serde::Serialize)]
struct DecoderInventory {version:String,decoders:Vec<String>}
#[derive(serde::Serialize)]
#[serde(rename_all="camelCase")]
struct NativeInstallation {installation_id:String,name:String,platform:&'static str,app:&'static str,app_version:&'static str}
#[tauri::command]
async fn desktop_installation(window:WebviewWindow)->Result<NativeInstallation>{
    caller(&window)?;
    let app=window.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move||{
        let state=app.state::<InstallationState>();let id=state.0.lock().map_err(|_|Error::PreferencesUnavailable)?.as_ref().map_err(|error|*error)?.read()?;
        Ok(NativeInstallation{installation_id:id,name:crate::device_facts::current().computer_name().unwrap_or("").to_owned(),platform:std::env::consts::OS,app:"Portico",app_version:env!("CARGO_PKG_VERSION")})
    }).await.map_err(|_|Error::PreferencesUnavailable)?
}
#[tauri::command]
async fn desktop_capabilities(window:WebviewWindow,state:tauri::State<'_,EngineState>)->Result<DecoderInventory>{
    caller(&window)?;let native=state.0.lock().map_err(|_|Error::EngineFailure)?.clone()?;
    tauri::async_runtime::spawn_blocking(move||Ok(DecoderInventory{version:native.property("mpv-version").ok_or(Error::EngineFailure)?,decoders:native.decoder_inventory()?})).await.map_err(|_|Error::EngineFailure)?
}
fn request_app_close(app:&tauri::AppHandle){
    if !app.state::<ShutdownState>().request_web(){return}
    crate::spike::record("close-web-requested",0);
    let sent=app.get_webview_window("main").is_some_and(|window|window.eval("window.dispatchEvent(new Event('portico-native-close'))").is_ok());
    if !sent{request_shutdown(app,false);return}
    let app=app.clone();
    std::thread::spawn(move||{
        std::thread::sleep(std::time::Duration::from_secs(5));
        if app.state::<ShutdownState>().waiting_for_web(){
            crate::spike::record("close-web-timeout",-1);request_shutdown(&app,false);
        }
    });
}
#[tauri::command]
fn desktop_app_close(window:WebviewWindow)->Result<()>{
    // This terminal acknowledgement is idempotent even if native teardown has
    // already started, but it never admits a foreign WebView/origin.
    boundary::local_origin(window.label(),window.url().map_err(|_|Error::ForbiddenOrigin)?.as_str(),cfg!(debug_assertions))?;
    request_shutdown(window.app_handle(),true);Ok(())
}
fn request_shutdown(app:&tauri::AppHandle,web_completed:bool){
    app.state::<Arc<crate::discovery::Browser>>().cancel_all();
    if !app.state::<ShutdownState>().begin_native(){return}
    app.state::<Arc<crate::folder_operations::FolderOperations>>().cancel_all();
    crate::folder_ui::cancel_all();
    let app=app.clone();
    tauri::async_runtime::spawn_blocking(move||{
        crate::spike::record("close-started",0);
        // Synchronize with native initialization and preference mutations before taking
        // the engine. The AppKit/GTK event loop remains live throughout native teardown.
        let equalizer=app.state::<EqualizerState>();
        let preferences=equalizer.0.lock().unwrap_or_else(|e|e.into_inner());
        let native=std::mem::replace(&mut *app.state::<EngineState>().0.lock().unwrap_or_else(|e|e.into_inner()),Err(Error::EngineUnavailable)).ok();
        drop(preferences);
        let mut clean=web_completed;
        if let Some(native)=native.as_ref(){
            let downloads=app.state::<DownloadsState>();
            if let Ok(mut guard)=downloads.0.lock(){if let Ok(session)=guard.as_mut(){
                clean&=session.record_observation(&native.observation(),true).is_ok();
                session.retire_local();
            }}else{clean=false;}
            clean&=native.retire().is_ok();
        }
        if let Some(window)=app.get_webview_window("main"){
            let (send,receive)=std::sync::mpsc::sync_channel(1);
            if window.with_webview(move|webview|{crate::network::stop();let _=send.send(surface::retire(&webview));}).is_ok(){
                clean&=receive.recv_timeout(std::time::Duration::from_secs(5)).is_ok_and(|r|r.is_ok());
            }else{clean=false;}
        }
        if let Some(native)=native{
            let start=std::time::Instant::now();
            while Arc::strong_count(&native)>1&&start.elapsed()<std::time::Duration::from_secs(5){std::thread::sleep(std::time::Duration::from_millis(10));}
            clean&=Arc::strong_count(&native)==1;
            // The last owned client is destroyed on this worker while native callbacks
            // can still execute on the live platform event loop.
            drop(native);
        }
        crate::spike::record("close-finished",if clean{0}else{-1});
        app.state::<ShutdownState>().finish();
        app.exit(if clean{0}else{1});
    });
}
#[derive(serde::Serialize)]
#[serde(rename_all="camelCase")]
struct DownloadFolderLocation{display_path:String}
#[tauri::command]
async fn desktop_download_folder_location(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,lease:String)->Result<DownloadFolderLocation>{
    caller(&window)?;let state=state.0.clone();
    tauri::async_runtime::spawn_blocking(move||{
        let guard=state.lock().map_err(|_|Error::DownloadsUnavailable)?;let session=guard.as_ref().map_err(|error|*error)?;
        let path=session.folder_path(&lease)?.to_str().ok_or(Error::DownloadsUnavailable)?.to_owned();Ok(DownloadFolderLocation{display_path:path})
    }).await.map_err(|_|Error::DownloadsUnavailable)?
}
#[tauri::command]
async fn desktop_download_folder_begin(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,operations:tauri::State<'_,Arc<crate::folder_operations::FolderOperations>>,lease:String)->Result<String>{
    caller(&window)?;let state=state.0.clone();let operations=operations.inner().clone();
    tauri::async_runtime::spawn_blocking(move||{
        let guard=state.lock().map_err(|_|Error::DownloadsUnavailable)?;guard.as_ref().map_err(|error|*error)?.folder_path(&lease)?;operations.begin(&lease)
    }).await.map_err(|_|Error::DownloadsUnavailable)?
}
#[tauri::command]
fn desktop_download_folder_cancel(window:WebviewWindow,operations:tauri::State<'_,Arc<crate::folder_operations::FolderOperations>>,token:String)->Result<()>{
    caller(&window)?;operations.cancel(&token);operations.finish(&token);
    window.run_on_main_thread(move||crate::folder_ui::cancel(&token)).map_err(|_|Error::WindowUnavailable)
}
#[tauri::command]
async fn desktop_download_folder_choose(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,operations:tauri::State<'_,Arc<crate::folder_operations::FolderOperations>>,token:String)->Result<()>{
    caller(&window)?;let state=state.0.clone();let operations=operations.inner().clone();
    let result=async{
        let operation=operations.get(&token)?;let current=operation.clone();let store=state.clone();
        let initial=tauri::async_runtime::spawn_blocking(move||{
            if !current.current(){return Err(Error::DownloadsConflict)}
            let guard=store.lock().map_err(|_|Error::DownloadsUnavailable)?;Ok(guard.as_ref().map_err(|error|*error)?.folder_path(&current.lease)?.to_path_buf())
        }).await.map_err(|_|Error::DownloadsUnavailable)??;
        let (send,receive)=std::sync::mpsc::sync_channel(1);let panel=window.clone();let current=operation.clone();let key=token.clone();
        window.run_on_main_thread(move||{
            if !current.current(){let _=send.send(Ok(None));return}
            if let Err(error)=crate::folder_ui::choose(&panel,&key,&initial,send.clone()){let _=send.send(Err(error));}
        }).map_err(|_|Error::WindowUnavailable)?;
        tauri::async_runtime::spawn_blocking(move||{
            let selected=receive.recv().map_err(|_|Error::DownloadsUnavailable)??;
            if !operation.current(){return Err(Error::DownloadsConflict)}
            let Some(selected)=selected else{return Ok(())};
            let mut guard=state.lock().map_err(|_|Error::DownloadsUnavailable)?;
            guard.as_mut().map_err(|error|*error)?.relocate_folder(&operation.lease,&selected,||operation.current())
        }).await.map_err(|_|Error::DownloadsUnavailable)?
    }.await;
    operations.finish(&token);result
}
#[tauri::command]
async fn desktop_download_folder_open(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,operations:tauri::State<'_,Arc<crate::folder_operations::FolderOperations>>,token:String)->Result<()>{
    caller(&window)?;let state=state.0.clone();let operations=operations.inner().clone();
    let result=async{
        let operation=operations.get(&token)?;let current=operation.clone();
        let path=tauri::async_runtime::spawn_blocking(move||{
            if !current.current(){return Err(Error::DownloadsConflict)}
            let guard=state.lock().map_err(|_|Error::DownloadsUnavailable)?;Ok(guard.as_ref().map_err(|error|*error)?.folder_path(&current.lease)?.to_path_buf())
        }).await.map_err(|_|Error::DownloadsUnavailable)??;
        let (send,receive)=std::sync::mpsc::sync_channel(1);
        window.run_on_main_thread(move||{let _=send.send(if operation.current(){crate::folder_ui::open(&path)}else{Err(Error::DownloadsConflict)});}).map_err(|_|Error::WindowUnavailable)?;
        tauri::async_runtime::spawn_blocking(move||receive.recv().unwrap_or(Err(Error::DownloadsUnavailable))).await.map_err(|_|Error::DownloadsUnavailable)?
    }.await;
    operations.finish(&token);result
}
#[tauri::command]
async fn desktop_downloads_bind(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,engine:tauri::State<'_,EngineState>,operations:tauri::State<'_,Arc<crate::folder_operations::FolderOperations>>,scope:crate::download_commands::ViewerScope)->Result<String>{
    caller(&window)?;
    let state=state.0.clone();
    let operations=operations.inner().clone();
    let native=engine.0.lock().map_err(|_|Error::EngineFailure)?.as_ref().ok().cloned();
    let (lease,changed)=tauri::async_runtime::spawn_blocking(move||{
        let mut guard=state.lock().map_err(|_|Error::DownloadsUnavailable)?;let session=guard.as_mut().map_err(|e|*e)?;
        let changed=session.binding_changes(&scope)?;
        if changed{operations.cancel_all();retire_offline(session,native.as_ref())?;}
        Ok::<_,Error>((session.bind(scope)?,changed))
    }).await.map_err(|_|Error::DownloadsUnavailable)??;
    if changed{let _=window.run_on_main_thread(crate::folder_ui::cancel_all);}
    Ok(lease)
}
#[tauri::command]
async fn desktop_downloads_release(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,lease:String)->Result<()>{
    caller(&window)?;
    let state=state.0.clone();
    tauri::async_runtime::spawn_blocking(move||{state.lock().map_err(|_|Error::DownloadsUnavailable)?.as_mut().map_err(|e|*e)?.release(&lease);Ok(())}).await.map_err(|_|Error::DownloadsUnavailable)?
}
#[tauri::command]
async fn desktop_downloads_command(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,engine:tauri::State<'_,EngineState>,lease:String,operation:crate::download_commands::DownloadOperation)->Result<crate::download_commands::DownloadReply>{
    caller(&window)?;
    let state=state.0.clone();
    let native=engine.0.lock().map_err(|_|Error::EngineFailure)?.as_ref().ok().cloned();
    tauri::async_runtime::spawn_blocking(move||{
        let mut guard=state.lock().map_err(|_|Error::DownloadsUnavailable)?;let session=guard.as_mut().map_err(|e|*e)?;
        let can_remove=matches!(&operation,crate::download_commands::DownloadOperation::Revoke{..}|crate::download_commands::DownloadOperation::Remove{..}|crate::download_commands::DownloadOperation::GroupAction{..});
        if can_remove {if let Some(native)=native.as_ref(){let _=session.record_observation(&native.observation(),true);}}
        let reply=session.execute(&lease,operation)?;
        if can_remove&&!session.active_file_valid(){retire_offline(session,native.as_ref())?;}
        Ok(reply)
    }).await.map_err(|_|Error::DownloadsUnavailable)?
}
#[tauri::command]
async fn desktop_load_local(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,engine:tauri::State<'_,EngineState>,lease:String,source:crate::download_commands::LocalLoadRequest)->Result<crate::download_commands::LocalLoadReceipt>{
    caller(&window)?;
    let downloads=state.0.clone();
    let native=engine.0.lock().map_err(|_|Error::EngineFailure)?.clone()?;
    tauri::async_runtime::spawn_blocking(move||{
        let mut guard=downloads.lock().map_err(|_|Error::DownloadsUnavailable)?;
        let session=guard.as_mut().map_err(|e|*e)?;
        let previous=native.observation();
        session.load_local(&lease,&source,&previous,|path|native.load_local(path,source.generation,source.start_position_ms,source.autoplay))
    }).await.map_err(|_|Error::DownloadsUnavailable)?
}
#[tauri::command]
async fn desktop_retire_local(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,engine:tauri::State<'_,EngineState>,lease:String,generation:u64)->Result<()>{
    caller(&window)?;let downloads=state.0.clone();let native=engine.0.lock().map_err(|_|Error::EngineFailure)?.as_ref().ok().cloned();
    tauri::async_runtime::spawn_blocking(move||{
        let mut guard=downloads.lock().map_err(|_|Error::DownloadsUnavailable)?;let session=guard.as_mut().map_err(|e|*e)?;
        // Cancellation may arrive after replacement. It owns only this exact load.
        let previous=native.as_ref().map(|native|native.observation()).unwrap_or_else(||crate::boundary::Observation{observed_playback:Default::default(),generation:0,state:"idle".into(),position_ms:0.0,rate:1.0,volume:1.0,muted:false,dropped_frames:0,native_surface_active:false,eq_applied:false,leveling_applied:false});
        session.retire_local_load(&lease,generation,&previous,||{
            if let Some(native)=native.as_ref(){if native.matches_generation(generation){native.execute(Command::Stop{generation})?;}}
            Ok(())
        })?;
        Ok(())
    }).await.map_err(|_|Error::DownloadsUnavailable)?
}
fn retire_offline(session:&mut crate::download_commands::DownloadsSession,native:Option<&Arc<Mpv>>)->Result<()>{
    if let (Some(generation),Some(native))=(session.active_generation(),native){
        if native.matches_generation(generation){let _=session.record_observation(&native.observation(),true);native.execute(Command::Stop{generation})?;}
    }
    session.retire_local();Ok(())
}
#[tauri::command]
async fn desktop_downloads_retire(window:WebviewWindow,state:tauri::State<'_,DownloadsState>,engine:tauri::State<'_,EngineState>,lease:String)->Result<()>{
    caller(&window)?;let downloads=state.0.clone();let native=engine.0.lock().map_err(|_|Error::EngineFailure)?.as_ref().ok().cloned();
    tauri::async_runtime::spawn_blocking(move||{let mut guard=downloads.lock().map_err(|_|Error::DownloadsUnavailable)?;let session=guard.as_mut().map_err(|e|*e)?;if session.lease_current(&lease){retire_offline(session,native.as_ref())?;session.force_release(&lease);}Ok(())}).await.map_err(|_|Error::DownloadsUnavailable)?
}
#[tauri::command]
async fn desktop_command(window:WebviewWindow,state:tauri::State<'_,EngineState>,downloads:tauri::State<'_,DownloadsState>,command:Command)->Result<()> {
    caller(&window)?;
    let engine=state.0.lock().map_err(|_|Error::EngineFailure)?.clone()?;let downloads=downloads.0.clone();
    tauri::async_runtime::spawn_blocking(move||{
        let mut guard=downloads.lock().map_err(|_|Error::DownloadsUnavailable)?;
        if matches!(&command,Command::Load{..}|Command::Stop{..}){if let Ok(session)=guard.as_mut(){
            let previous=engine.observation();
            return session.transition_output(&previous,||engine.execute(command));
        }}
        engine.execute(command)
    }).await.map_err(|_|Error::EngineFailure)?
}
#[tauri::command]
async fn desktop_observe(window:WebviewWindow,state:tauri::State<'_,EngineState>,downloads:tauri::State<'_,DownloadsState>)->Result<Observation> {
    caller(&window)?;
    let engine=state.0.lock().map_err(|_|Error::EngineFailure)?.clone()?;let downloads=downloads.0.clone();
    tauri::async_runtime::spawn_blocking(move||{
        let observation=engine.observation();
        if let Ok(mut guard)=downloads.lock(){if let Ok(session)=guard.as_mut(){let _=session.record_observation(&observation,false);}}
        Ok(observation)
    }).await.map_err(|_|Error::EngineFailure)?
}
#[tauri::command]
async fn desktop_audio_facts(window:WebviewWindow,state:tauri::State<'_,EngineState>)->Result<crate::audio::AudioFacts> {
    caller(&window)?;
    let native=state.0.lock().map_err(|_|Error::EngineFailure)?.clone()?;
    tauri::async_runtime::spawn_blocking(move||Ok(native.audio_facts())).await.map_err(|_|Error::EngineFailure)?
}
#[tauri::command]
async fn desktop_equalizer(window:WebviewWindow)->Result<EqualizerSnapshot> {
    caller(&window)?;
    let app=window.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move||{
        let equalizer=app.state::<EqualizerState>();
        let store=equalizer.0.lock().map_err(|_|Error::PreferencesUnavailable)?;
        let saved=store.as_ref().map_err(|error|*error)?.read()?;
        let engine=app.state::<EngineState>();
        let native=engine.0.lock().map_err(|_|Error::EngineFailure)?.as_ref().ok().cloned();
        let applied=native.as_ref().map(|player|player.equalizer_matches(&saved.settings)).unwrap_or(false);
        Ok(EqualizerSnapshot{revision:saved.revision,settings:saved.settings,applied})
    }).await.map_err(|_|Error::PreferencesUnavailable)?
}
#[tauri::command]
async fn desktop_equalizer_change(window:WebviewWindow,change:EqualizerChange)->Result<EqualizerSnapshot> {
    caller(&window)?;
    let app=window.app_handle().clone();
    tauri::async_runtime::spawn_blocking(move||{
        // Resolve the renderer only after taking the store lock, as initialization does.
        // A request queued before initialization must apply to the renderer now present.
        let equalizer=app.state::<EqualizerState>();
        let mut store=equalizer.0.lock().map_err(|_|Error::PreferencesUnavailable)?;
        let engine=app.state::<EngineState>();
        let native=engine.0.lock().map_err(|_|Error::EngineFailure)?.as_ref().ok().cloned();
        let saved=store.as_mut().map_err(|error|*error)?.change(change,|settings|native.as_ref().map(|player|player.apply_equalizer(settings)).unwrap_or(Ok(())))?;
        let applied=native.as_ref().map(|player|player.equalizer_matches(&saved.settings)).unwrap_or(false);
        Ok(EqualizerSnapshot{revision:saved.revision,settings:saved.settings,applied})
    }).await.map_err(|_|Error::PreferencesUnavailable)?
}
#[tauri::command]
fn desktop_window_state(window:WebviewWindow,state:tauri::State<Arc<crate::window::ChromeState>>)->Result<crate::window::WindowSnapshot>{
    caller(&window)?;let result=crate::window::snapshot(&window,&state);crate::spike::record("window-state-confirmed",if result.is_ok(){0}else{-1});result
}
#[tauri::command]
fn desktop_window_action(window:WebviewWindow,action:crate::window::WindowAction)->Result<()>{
    caller(&window)?;crate::window::action(&window,action)
}
#[tauri::command]
async fn desktop_window_chrome(window:WebviewWindow,state:tauri::State<'_,Arc<crate::window::ChromeState>>,chrome:crate::window::ChromeUpdate)->Result<()>{
    caller(&window)?;
    let native=window.clone();let state=state.inner().clone();
    let (send,receive)=std::sync::mpsc::sync_channel(1);
    window.run_on_main_thread(move||{let result=crate::window::update(&native,state,chrome);let _=send.send(result);}).map_err(|_|Error::WindowUnavailable)?;
    tauri::async_runtime::spawn_blocking(move||receive.recv().unwrap_or(Err(Error::WindowUnavailable))).await.map_err(|_|Error::WindowUnavailable)?
}
#[tauri::command]
async fn desktop_surface(window:WebviewWindow,state:tauri::State<'_,EngineState>,bounds:crate::boundary::SurfaceBounds,generation:u64)->Result<()> {
    caller(&window)?;bounds.validate()?;
    let engine=state.0.lock().map_err(|_|Error::EngineFailure)?.clone()?;
    if !engine.matches_generation(generation){return Err(Error::StaleGeneration);}
    let (send,receive)=std::sync::mpsc::sync_channel(1);
    window.with_webview(move|webview|{
        let result=if engine.matches_generation(generation){let result=surface::bounds(&webview,bounds);engine.set_geometry_ready(result.is_ok()&&bounds.width>0.0&&bounds.height>0.0);result}else{Err(Error::StaleGeneration)};
        let _=send.send(result);
    }).map_err(|_|Error::EngineFailure)?;
    tauri::async_runtime::spawn_blocking(move||receive.recv().unwrap_or(Err(Error::EngineFailure))).await.map_err(|_|Error::EngineFailure)?
}

#[derive(serde::Deserialize)]
#[serde(rename_all="camelCase",deny_unknown_fields)]
struct UiProof{frames:u64,elapsed_ms:f64,longest_frame_ms:f64,width:u32,height:u32,root_mounted:bool,desktop_host:bool}
#[tauri::command]
async fn desktop_spike_report(window:WebviewWindow,state:tauri::State<'_,EngineState>,report:UiProof)->Result<()> {
    caller(&window)?;
    if !cfg!(debug_assertions)||report.frames>10000||!report.elapsed_ms.is_finite()||!report.longest_frame_ms.is_finite(){return Err(Error::InvalidCommand);}
    #[cfg(debug_assertions)]
    if let Some(path)=std::env::var_os("PORTICO_SPIKE_REPORT"){
        let engine=state.0.lock().map_err(|_|Error::EngineFailure)?.as_ref().ok().cloned();
        let (send,receive)=std::sync::mpsc::sync_channel(1);window.with_webview(move|_|{let _=send.send(surface::proof());}).map_err(|_|Error::EngineFailure)?;
        let value=tauri::async_runtime::spawn_blocking(move||{
            let native=receive.recv().map_err(|_|Error::EngineFailure)?;
            let (observation,audio)=match engine.as_ref(){Some(e)=>(Some(e.observation()),Some(e.audio_facts())),None=>(None,None)};
            Ok(serde_json::json!({"platform":std::env::consts::OS,"networkCost":crate::network::current(),"ui":{"frames":report.frames,"elapsedMs":report.elapsed_ms,"longestFrameMs":report.longest_frame_ms,"width":report.width,"height":report.height,"rootMounted":report.root_mounted,"desktopHost":report.desktop_host},"native":native,"player":observation,"audio":audio}))
        }).await.map_err(|_|Error::EngineFailure)??;
        let path=PathBuf::from(path);let temp=path.with_extension("tmp");
        std::fs::write(&temp,serde_json::to_vec_pretty(&value).map_err(|_|Error::EngineFailure)?).map_err(|_|Error::EngineFailure)?;
        std::fs::rename(temp,path).map_err(|_|Error::EngineFailure)?;
    }
    Ok(())
}

fn library(app:&tauri::AppHandle)->Result<PathBuf> {
    // Debug proof only; release always loads its own native resource. No web-selected library.
    if cfg!(debug_assertions) {if let Some(path)=std::env::var_os("PORTICO_MPV_LIBRARY") {return Ok(path.into());}}
    let name=if cfg!(target_os="windows"){"mpv-2.dll"}else if cfg!(target_os="macos"){"libmpv.2.dylib"}else{"libmpv.so.2"};
    Ok(app.path().resource_dir().map_err(|_|Error::EngineUnavailable)?.join("native").join(name))
}
fn local_data(app:&tauri::AppHandle)->Result<PathBuf>{
    #[cfg(debug_assertions)]
    if let Some(path)=std::env::var_os("PORTICO_SPIKE_DATA"){
        let path=PathBuf::from(path);if !path.is_absolute(){return Err(Error::InvalidCommand)}return Ok(path)
    }
    app.path().app_local_data_dir().map_err(|_|Error::PreferencesUnavailable)
}

pub fn run() {
    let builder=tauri::Builder::default()
        .manage(EngineState(Mutex::new(Err(Error::EngineUnavailable))))
        .manage(EqualizerState(Mutex::new(Err(Error::PreferencesUnavailable))))
        .manage(InstallationState(Mutex::new(Err(Error::PreferencesUnavailable))))
        .manage(DownloadsState(Arc::new(Mutex::new(Err(Error::DownloadsUnavailable)))))
        .manage(ShutdownState::default())
        .manage(Arc::new(crate::discovery::Browser::default()))
        .manage(Arc::new(crate::folder_operations::FolderOperations::default()))
        .manage(Arc::new(crate::window::ChromeState::default()))
        .on_window_event(|window,event|{
            if window.label()=="main"{if let tauri::WindowEvent::CloseRequested{api,..}=event{api.prevent_close();request_app_close(window.app_handle());}}
        })
        .invoke_handler(tauri::generate_handler![desktop_app_close,desktop_command,desktop_observe,desktop_audio_facts,desktop_network_cost,desktop_discover,desktop_discovery_cancel,desktop_device_facts,desktop_installation,desktop_capabilities,desktop_surface,desktop_spike_report,desktop_equalizer,desktop_equalizer_change,desktop_window_state,desktop_window_action,desktop_window_chrome,desktop_downloads_bind,desktop_downloads_release,desktop_downloads_command,desktop_load_local,desktop_retire_local,desktop_downloads_retire,desktop_download_folder_location,desktop_download_folder_begin,desktop_download_folder_cancel,desktop_download_folder_choose,desktop_download_folder_open,desktop_measurement_options,desktop_measurement_write])
        .setup(|app| {
            // app_local_data_dir selects LocalAppData on Windows, not roaming/account state.
            let preference_error=||std::io::Error::other("device_preferences_unavailable");
            let data=local_data(app.handle()).map_err(|_|preference_error())?;
            let installation=crate::installation_store::InstallationStore::open(&data.join("installation.sqlite3")).map_err(|_|preference_error())?;
            *app.state::<InstallationState>().0.lock().map_err(|_|preference_error())?=Ok(installation);
            let path=data.join("equalizer.sqlite3");
            let store=EqualizerStore::open(&path).map_err(|_|preference_error())?;
            *app.state::<EqualizerState>().0.lock().map_err(|_|preference_error())?=Ok(store);
            let downloads=data.join("offline");
            *app.state::<DownloadsState>().0.lock().map_err(|_|preference_error())?=crate::download_commands::DownloadsSession::open(&downloads);
            let measurement=crate::measurement::MeasurementState::from_env().map_err(|_|std::io::Error::other("desktop_measurement_unavailable"))?;
            let (route,width,height)=measurement.window().map_err(|_|std::io::Error::other("desktop_measurement_unavailable"))?;
            app.manage(measurement);
            let main=tauri::WebviewWindowBuilder::new(app,"main",tauri::WebviewUrl::App(route.trim_start_matches('/').into()))
                .title("Portico").inner_size(width,height).min_inner_size(600.0,600.0)
                // Linux uses the desktop's title bar. Windows keeps OS controls until the
                // installed web caption host supplies its actual control rectangles.
                .decorations(true)
                .incognito(cfg!(debug_assertions)&&std::env::var_os("PORTICO_SPIKE_DATA").is_some())
                .transparent(true)
                .on_navigation(|url|boundary::local_origin("main",url.as_str(),cfg!(debug_assertions)).is_ok())
                .on_page_load(|window,payload| {
                    crate::spike::record(if payload.event()==tauri::webview::PageLoadEvent::Finished{"page-finished"}else{"page-started"},0);
                    if cfg!(debug_assertions)&&std::env::var_os("PORTICO_SPIKE_REPORT").is_some()&&payload.event()==tauri::webview::PageLoadEvent::Finished {
                        if std::env::var_os("PORTICO_SPIKE_MEDIA").is_some(){let _=window.eval(r#"window.__porticoNativeProbe=setInterval(()=>window.__TAURI_INTERNALS__.invoke('desktop_observe').then(o=>o.generation?window.__TAURI_INTERNALS__.invoke('desktop_surface',{generation:o.generation,bounds:{x:0,y:0,width:innerWidth,height:innerHeight}}):undefined).catch(()=>{}),200);setTimeout(()=>clearInterval(window.__porticoNativeProbe),9000)"#);}
                        let _=window.eval(r#"setTimeout(()=>{let frames=0,start=performance.now(),last=start,longest=0;function measure(now){longest=Math.max(longest,now-last);last=now;frames++;if(now-start<5000)requestAnimationFrame(measure);else window.__TAURI_INTERNALS__.invoke('desktop_spike_report',{report:{frames,elapsedMs:now-start,longestFrameMs:longest,width:innerWidth,height:innerHeight,rootMounted:!!document.getElementById('root')?.childElementCount,desktopHost:!!document.querySelector('[data-desktop-window-chrome]')}})}requestAnimationFrame(measure)},3000)"#);
                    }
                });
            let main=main.build()?;
            crate::network::start();
            #[cfg(debug_assertions)]
            if std::env::var_os("PORTICO_SPIKE_CLOSE").is_some(){
                let window=main.clone();
                std::thread::spawn(move||{std::thread::sleep(std::time::Duration::from_secs(13));let _=window.close();});
            }
            let handle=app.handle().clone();
            let lib=library(app.handle()).and_then(|p|Api::open(&p));
            #[cfg(target_os="macos")]
            {
                let window=main.clone();
                tauri::async_runtime::spawn_blocking(move||{
                    crate::spike::record("worker-started",0);
                    // macvk initializes Cocoa through DispatchQueue.main.sync. Never wait for
                    // its client API on AppKit's main thread, including the first VO load.
                    let native=lib.and_then(|api|Mpv::new(api,&AudioOutput::default(),crate::mpv::VideoTarget::NativeView(0)));
                    crate::spike::record("worker-engine",if native.is_ok(){0}else{-1});
                    let equalizer=handle.state::<EqualizerState>();let preferences=equalizer.0.lock();
                    if !handle.state::<ShutdownState>().running(){
                        if let Ok(native)=native{let _=native.retire();}return
                    }
                    let result=native.and_then(|native|{
                        let store=preferences.as_ref().map_err(|_|Error::PreferencesUnavailable)?.as_ref().map_err(|e|*e)?;
                        native.apply_equalizer(&store.read()?.settings)?;Ok(native)
                    });
                    *handle.state::<EngineState>().0.lock().unwrap_or_else(|e|e.into_inner())=result.clone();
                    drop(preferences);
                    if let Ok(native)=result{
                        let attached=native.clone();
                        let _=window.with_webview(move|webview|{let _=surface::attach_existing(&webview,&attached);});
                        #[cfg(debug_assertions)]
                        if let Some(path)=std::env::var_os("PORTICO_SPIKE_MEDIA"){let _=native.load_spike_fixture(&PathBuf::from(path));}
                    }
                });
            }
            #[cfg(not(target_os="macos"))]
            main.with_webview(move |webview| {
                // Keep the same store->engine lock order as preference commands. A setting
                // saved while the webview starts must not be replaced by an earlier copy.
                let equalizer_state=handle.state::<EqualizerState>();
                let preferences=equalizer_state.0.lock();
                let result=(||->Result<Arc<Mpv>>{
                    let store=preferences.as_ref().map_err(|_|Error::PreferencesUnavailable)?;
                    let equalizer=store.as_ref().map_err(|error|*error)?.read()?.settings;
                    let native=lib.and_then(|api|surface::attach(&webview,api,&AudioOutput::default()))?;
                    native.apply_equalizer(&equalizer)?;Ok(native)
                })();
                #[cfg(debug_assertions)]
                if let Ok(engine)=&result {if let Some(path)=std::env::var_os("PORTICO_SPIKE_MEDIA"){
                    let ready=surface::bounds(&webview,crate::boundary::SurfaceBounds{x:0.0,y:0.0,width:1440.0,height:900.0}).is_ok();
                    engine.set_geometry_ready(ready);
                    let native=engine.clone();
                    // Client API calls may wait for the GL renderer. Never make them on
                    // the thread that owns and services that render context.
                    tauri::async_runtime::spawn_blocking(move||{let _=native.load_spike_fixture(&PathBuf::from(path));});
                }}
                *handle.state::<EngineState>().0.lock().unwrap_or_else(|e|e.into_inner())=result;
            })?;
            #[cfg(feature="updater")]
            {
                // The public trust root and real endpoint are release inputs, not placeholders.
                let key=option_env!("PORTICO_DESKTOP_UPDATER_PUBLIC_KEY").ok_or("updater public key missing")?;
                let endpoint=option_env!("PORTICO_DESKTOP_UPDATER_ENDPOINT").ok_or("updater endpoint missing")?;
                if !endpoint.starts_with("https://") {return Err("updater endpoint must use HTTPS".into());}
                app.handle().plugin(tauri_plugin_updater::Builder::new().pubkey(key).endpoints(vec![endpoint.parse()?])?.build())?;
            }
            Ok(())
        });
    // Error text from native libraries is deliberately not printed.
    match builder.build(tauri::generate_context!()){
        Ok(app)=>app.run(|handle,event|{
            if let tauri::RunEvent::ExitRequested{api,..}=event{
                if !handle.state::<ShutdownState>().finished(){api.prevent_exit();request_app_close(handle);}
            }
        }),
        Err(_)=>eprintln!("desktop_start_failed"),
    }
}

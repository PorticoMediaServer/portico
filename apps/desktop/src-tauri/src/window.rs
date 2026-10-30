//! Typed, local-window controls. No path, URL, script, arbitrary native message or HWND IPC.
use crate::boundary::{Error, Result};
use serde::{Deserialize, Serialize};
use std::sync::{Arc, Mutex, atomic::{AtomicBool, AtomicU8}};
use tauri::WebviewWindow;

#[cfg(target_os="macos")]
#[path="window_macos.rs"] mod platform;
#[cfg(target_os="windows")]
#[path="window_windows.rs"] mod platform;

#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all="camelCase", deny_unknown_fields)]
pub struct Rect { pub x:f64, pub y:f64, pub width:f64, pub height:f64 }
impl Rect {
    fn valid(&self, width:f64, height:f64)->bool {
        [self.x,self.y,self.width,self.height].iter().all(|n|n.is_finite())
            && self.x>=0.0 && self.y>=0.0 && self.width>0.0 && self.height>0.0
            && self.x+self.width<=width+1.0 && self.y+self.height<=height+1.0
    }
    #[cfg(target_os="windows")]
    fn contains(&self,x:f64,y:f64)->bool{x>=self.x&&x<self.x+self.width&&y>=self.y&&y<self.y+self.height}
}
#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all="camelCase", deny_unknown_fields)]
pub struct CaptionRects { pub minimize:Rect, pub maximize:Rect, pub close:Rect, #[serde(skip)] pub scale:f64 }
#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all="camelCase")]
pub enum Surface { Pages, Video, Music }
#[derive(Clone, Deserialize)]
#[serde(rename_all="camelCase", deny_unknown_fields)]
pub struct ChromeUpdate {
    pub enabled:bool,
    pub surface:Surface,
    pub controls_visible:bool,
    pub fullscreen:bool,
    pub captions:Option<CaptionRects>,
}
#[derive(Default)]
pub struct ChromeState {
    rects:Mutex<Option<CaptionRects>>,
    hover:AtomicU8,
    attached:AtomicBool,
    active:AtomicBool,
    #[cfg(target_os="macos")]
    mac_layout:Mutex<Option<platform::SavedLayout>>,
}
#[derive(Serialize)]
#[serde(rename_all="camelCase")]
pub struct CaptionNames { pub minimize:String, pub maximize:String, pub restore:String, pub close:String }
#[derive(Serialize)]
#[serde(rename_all="camelCase")]
pub struct WindowSnapshot {
    pub os:&'static str,
    pub fullscreen:bool,
    pub maximized:bool,
    pub focused:bool,
    pub captions:Option<CaptionNames>,
    pub caption_hover:u8,
}
#[derive(Deserialize)]
#[serde(rename_all="camelCase")]
pub enum WindowAction { Minimize, ToggleMaximize, Close, Drag }

pub fn snapshot(window:&WebviewWindow,state:&ChromeState)->Result<WindowSnapshot>{
    #[cfg(target_os="windows")]
    let captions=platform::names(window)?;
    #[cfg(not(target_os="windows"))]
    let captions=None;
    Ok(WindowSnapshot{os:std::env::consts::OS,
        fullscreen:window.is_fullscreen().map_err(|_|Error::WindowUnavailable)?,
        maximized:window.is_maximized().map_err(|_|Error::WindowUnavailable)?,
        focused:window.is_focused().map_err(|_|Error::WindowUnavailable)?,captions,
        caption_hover:state.hover.load(std::sync::atomic::Ordering::Acquire)})
}
pub fn action(window:&WebviewWindow,action:WindowAction)->Result<()> {
    let result=match action {
        WindowAction::Minimize=>window.minimize(),
        WindowAction::ToggleMaximize=>if window.is_maximized().map_err(|_|Error::WindowUnavailable)?{window.unmaximize()}else{window.maximize()},
        WindowAction::Close=>window.close(),
        WindowAction::Drag=>window.start_dragging(),
    };
    result.map_err(|_|Error::WindowUnavailable)
}

/// Runs on Tauri's UI thread. Caption geometry is CSS pixels from actual mounted controls,
/// validated against the native content size and converted using the native scale factor.
pub fn update(window:&WebviewWindow,state:Arc<ChromeState>,mut value:ChromeUpdate)->Result<()> {
    let scale=window.scale_factor().map_err(|_|Error::WindowUnavailable)?;
    if !scale.is_finite()||scale<=0.0{return Err(Error::WindowUnavailable);}
    let size=window.inner_size().map_err(|_|Error::WindowUnavailable)?;
    let width=f64::from(size.width)/scale;let height=f64::from(size.height)/scale;
    if let Some(rects)=value.captions.as_mut(){
        let all=[rects.minimize,rects.maximize,rects.close];
        if all.iter().any(|rect|!rect.valid(width,height)||rect.y>1.0||(rect.width-46.0).abs()>1.0||(rect.height-32.0).abs()>1.0)
            ||(rects.maximize.x-(rects.minimize.x+rects.minimize.width)).abs()>1.0
            ||(rects.close.x-(rects.maximize.x+rects.maximize.width)).abs()>1.0
            ||(rects.close.x+rects.close.width-width).abs()>1.0{return Err(Error::InvalidCommand);}
        rects.scale=scale;
    }
    let fullscreen=value.fullscreen||window.is_fullscreen().map_err(|_|Error::WindowUnavailable)?;
    #[cfg(target_os="windows")]
    {
        if value.enabled&&value.captions.is_some()&&!fullscreen&&!state.active.load(std::sync::atomic::Ordering::Acquire){
            // Keep OS controls until actual web controls and their hit rectangles exist.
            window.set_decorations(false).map_err(|_|Error::WindowUnavailable)?;
            let result=if state.attached.load(std::sync::atomic::Ordering::Acquire){platform::configure(window)}else{platform::attach(window,state.clone())};
            if let Err(error)=result{
                let _=window.set_decorations(true);return Err(error);
            }
            state.attached.store(true,std::sync::atomic::Ordering::Release);
            state.active.store(true,std::sync::atomic::Ordering::Release);
        }else if !value.enabled&&state.active.load(std::sync::atomic::Ordering::Acquire){
            window.set_decorations(true).map_err(|_|Error::WindowUnavailable)?;
            state.active.store(false,std::sync::atomic::Ordering::Release);
        }
        *state.rects.lock().map_err(|_|Error::WindowUnavailable)?=if fullscreen||!value.enabled{None}else{value.captions};
    }
    #[cfg(target_os="macos")]
    {
        if value.enabled&&!state.active.load(std::sync::atomic::Ordering::Acquire){
            window.set_title_bar_style(tauri::TitleBarStyle::Overlay).map_err(|_|Error::WindowUnavailable)?;
            platform::title_hidden(window,true)?;
            state.active.store(true,std::sync::atomic::Ordering::Release);
        }else if !value.enabled&&state.active.load(std::sync::atomic::Ordering::Acquire){
            platform::restore(window,&mut *state.mac_layout.lock().map_err(|_|Error::WindowUnavailable)?)?;
            window.set_title_bar_style(tauri::TitleBarStyle::Visible).map_err(|_|Error::WindowUnavailable)?;
            platform::title_hidden(window,false)?;
            state.active.store(false,std::sync::atomic::Ordering::Release);
        }
        if value.enabled{platform::position(window,value.surface,value.controls_visible,fullscreen,&mut *state.mac_layout.lock().map_err(|_|Error::WindowUnavailable)?)?;}
    }
    #[cfg(target_os="linux")]
    {let _=(state,value,fullscreen); /* The window manager owns all Linux decorations. */}
    Ok(())
}

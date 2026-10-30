//! mpv's macvk VO owns its NSWindow and Metal layer. AppKit attaches that window below
//! the unchanged WKWebView window. HDR remains an actual monitor/output qualification.
use crate::{boundary::{Error,Result,SurfaceBounds},mpv::Mpv};
use std::{cell::RefCell,ffi::{c_char,c_void,CStr},sync::{Arc,Weak}};
type Id=*mut c_void;
type Sel=*mut c_void;
#[repr(C)]#[derive(Clone,Copy,Default)]struct Rect{x:f64,y:f64,width:f64,height:f64}
#[link(name="objc")]
unsafe extern "C" {fn sel_registerName(name:*const c_char)->Sel;fn objc_msgSend();#[cfg(target_arch="x86_64")]fn objc_msgSend_stret();}
#[link(name="AppKit",kind="framework")]
unsafe extern "C" {}
unsafe fn selector(name:&CStr)->Sel{unsafe{sel_registerName(name.as_ptr())}}
unsafe fn get(object:Id,name:&CStr)->Id{unsafe{let f:unsafe extern "C" fn(Id,Sel)->Id=std::mem::transmute(objc_msgSend as *const ());f(object,selector(name))}}
unsafe fn flag(object:Id,name:&CStr)->bool{unsafe{let f:unsafe extern "C" fn(Id,Sel)->bool=std::mem::transmute(objc_msgSend as *const ());f(object,selector(name))}}
unsafe fn boolean(object:Id,name:&CStr,value:bool){unsafe{let f:unsafe extern "C" fn(Id,Sel,bool)=std::mem::transmute(objc_msgSend as *const ());f(object,selector(name),value)}}
unsafe fn call(object:Id,name:&CStr){unsafe{let f:unsafe extern "C" fn(Id,Sel)=std::mem::transmute(objc_msgSend as *const ());f(object,selector(name))}}
unsafe fn with_object(object:Id,name:&CStr,value:Id){unsafe{let f:unsafe extern "C" fn(Id,Sel,Id)=std::mem::transmute(objc_msgSend as *const ());f(object,selector(name),value)}}
unsafe fn geometry(object:Id,name:&CStr)->Rect{
    #[cfg(target_arch="x86_64")]
    unsafe{let mut value=Rect::default();let f:unsafe extern "C" fn(*mut Rect,Id,Sel)=std::mem::transmute(objc_msgSend_stret as *const ());f(&mut value,object,selector(name));value}
    #[cfg(not(target_arch="x86_64"))]
    unsafe{let f:unsafe extern "C" fn(Id,Sel)->Rect=std::mem::transmute(objc_msgSend as *const ());f(object,selector(name))}
}
unsafe fn to_window(view:Id,rect:Rect)->Rect{
    #[cfg(target_arch="x86_64")]
    unsafe{let mut value=Rect::default();let f:unsafe extern "C" fn(*mut Rect,Id,Sel,Rect,Id)=std::mem::transmute(objc_msgSend_stret as *const ());f(&mut value,view,selector(c"convertRect:toView:"),rect,std::ptr::null_mut());value}
    #[cfg(not(target_arch="x86_64"))]
    unsafe{let f:unsafe extern "C" fn(Id,Sel,Rect,Id)->Rect=std::mem::transmute(objc_msgSend as *const ());f(view,selector(c"convertRect:toView:"),rect,std::ptr::null_mut())}
}
unsafe fn to_screen(window:Id,rect:Rect)->Rect{
    #[cfg(target_arch="x86_64")]
    unsafe{let mut value=Rect::default();let f:unsafe extern "C" fn(*mut Rect,Id,Sel,Rect)=std::mem::transmute(objc_msgSend_stret as *const ());f(&mut value,window,selector(c"convertRectToScreen:"),rect);value}
    #[cfg(not(target_arch="x86_64"))]
    unsafe{let f:unsafe extern "C" fn(Id,Sel,Rect)->Rect=std::mem::transmute(objc_msgSend as *const ());f(window,selector(c"convertRectToScreen:"),rect)}
}
struct Surface{parent:usize,child:usize,engine:Weak<Mpv>}
impl Surface{
    unsafe fn detach(&mut self){unsafe{
        if self.child!=0{with_object(self.child as Id,c"orderOut:",std::ptr::null_mut());with_object(self.parent as Id,c"removeChildWindow:",self.child as Id);call(self.child as Id,c"release");self.child=0;}
    }}
}
impl Drop for Surface{fn drop(&mut self){unsafe{self.detach();call(self.parent as Id,c"release");}}}
thread_local!{static SURFACE:RefCell<Option<Surface>>=const{RefCell::new(None)};}
pub fn proof()->serde_json::Value{SURFACE.with(|slot|serde_json::json!({"output":"macvk Metal NSWindow below WKWebView window","attached":slot.borrow().as_ref().is_some_and(|s|s.child!=0),"hdrVerified":false,"renderedFrames":null}))}
pub fn attach_existing(webview:&tauri::webview::PlatformWebview,engine:&Arc<Mpv>)->Result<()>{
    unsafe{
        let web=webview.inner();if web.is_null(){return Err(Error::Unsupported)}
        let parent=get(web,c"window");if parent.is_null(){return Err(Error::Unsupported)}
        call(parent,c"retain");
        SURFACE.with(|slot|*slot.borrow_mut()=Some(Surface{parent:parent as usize,child:0,engine:Arc::downgrade(engine)}));
        Ok(())
    }
}
pub fn bounds(webview:&tauri::webview::PlatformWebview,bounds:SurfaceBounds)->Result<()>{
    bounds.validate()?;
    SURFACE.with(|slot|unsafe{
        let mut slot=slot.borrow_mut();let surface=slot.as_mut().ok_or(Error::Unsupported)?;
        let engine=surface.engine.upgrade().ok_or(Error::EngineUnavailable)?;
        let child=engine.cached_native_window();
        if child==0||bounds.width<=0.0||bounds.height<=0.0{
            if surface.child!=0{with_object(surface.child as Id,c"orderOut:",std::ptr::null_mut());}
            engine.set_surface_ready(false);return if bounds.width<=0.0||bounds.height<=0.0{Ok(())}else{Err(Error::EngineUnavailable)}
        }
        if child!=surface.child{
            surface.detach();call(child as Id,c"retain");surface.child=child;
            let style:unsafe extern "C" fn(Id,Sel,usize)=std::mem::transmute(objc_msgSend as *const ());style(child as Id,selector(c"setStyleMask:"),0);
            boolean(child as Id,c"setIgnoresMouseEvents:",true);boolean(child as Id,c"setHasShadow:",false);boolean(child as Id,c"setExcludedFromWindowsMenu:",true);
            let add:unsafe extern "C" fn(Id,Sel,Id,isize)=std::mem::transmute(objc_msgSend as *const ());add(surface.parent as Id,selector(c"addChildWindow:ordered:"),child as Id,-1);
        }
        let web=webview.inner();let view_bounds=geometry(web,c"bounds");
        let local=Rect{x:view_bounds.x+bounds.x,y:view_bounds.y+if flag(web,c"isFlipped"){bounds.y}else{view_bounds.height-bounds.y-bounds.height},width:bounds.width,height:bounds.height};
        let rect=to_screen(surface.parent as Id,to_window(web,local));
        if ![rect.x,rect.y,rect.width,rect.height].iter().all(|v|v.is_finite()){engine.set_surface_ready(false);return Err(Error::InvalidCommand)}
        let set:unsafe extern "C" fn(Id,Sel,Rect,bool)=std::mem::transmute(objc_msgSend as *const ());set(child as Id,selector(c"setFrame:display:"),rect,true);
        if flag(child as Id,c"isMiniaturized"){with_object(child as Id,c"deminiaturize:",std::ptr::null_mut());}
        let order:unsafe extern "C" fn(Id,Sel,isize,isize)=std::mem::transmute(objc_msgSend as *const ());let number:unsafe extern "C" fn(Id,Sel)->isize=std::mem::transmute(objc_msgSend as *const ());order(child as Id,selector(c"orderWindow:relativeTo:"),-1,number(surface.parent as Id,selector(c"windowNumber")));
        engine.set_surface_ready(true);Ok(())
    })
}

/// AppKit owns window relationships; no client API is called on this thread.
pub fn retire(_webview:&tauri::webview::PlatformWebview)->Result<()>{
    SURFACE.with(|slot|{slot.borrow_mut().take();});Ok(())
}

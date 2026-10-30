use super::Surface;
use crate::boundary::{Error,Result};
use std::ffi::{c_char,c_void,CStr};
type Id=*mut c_void;
type Sel=*mut c_void;
#[repr(C)]#[derive(Clone,Copy)]struct Point{x:f64,y:f64}
#[repr(C)]#[derive(Clone,Copy,Default)]struct Rect{x:f64,y:f64,width:f64,height:f64}
#[link(name="objc")]
unsafe extern "C" {fn sel_registerName(name:*const c_char)->Sel;fn objc_msgSend();#[cfg(target_arch="x86_64")]fn objc_msgSend_stret();}
unsafe fn selector(name:&CStr)->Sel{unsafe{sel_registerName(name.as_ptr())}}
unsafe fn frame(view:Id)->Rect{
    unsafe{geometry(view,selector(c"frame"))}
}
unsafe fn geometry(view:Id,kind:Sel)->Rect{
    #[cfg(target_arch="x86_64")]
    unsafe{let mut value=Rect::default();let get:unsafe extern "C" fn(*mut Rect,Id,Sel)=std::mem::transmute(objc_msgSend_stret as *const ());get(&mut value,view,kind);value}
    #[cfg(not(target_arch="x86_64"))]
    unsafe{let get:unsafe extern "C" fn(Id,Sel)->Rect=std::mem::transmute(objc_msgSend as *const ());get(view,kind)}
}
pub(super) struct SavedLayout{height:f64,buttons:[Rect;3],hidden:[bool;3]}
pub fn title_hidden(window:&tauri::WebviewWindow,hidden:bool)->Result<()> {
    let native=window.ns_window().map_err(|_|Error::WindowUnavailable)?;
    if native.is_null(){return Err(Error::WindowUnavailable);}
    unsafe{let set:unsafe extern "C" fn(Id,Sel,isize)=std::mem::transmute(objc_msgSend as *const ());set(native,selector(c"setTitleVisibility:"),if hidden{1}else{0});}
    Ok(())
}
pub fn position(window:&tauri::WebviewWindow,surface:Surface,controls:bool,fullscreen:bool,saved:&mut Option<SavedLayout>)->Result<()> {
    let native=window.ns_window().map_err(|_|Error::WindowUnavailable)?;
    if native.is_null(){return Err(Error::WindowUnavailable);}
    unsafe{
        let button:unsafe extern "C" fn(Id,Sel,usize)->Id=std::mem::transmute(objc_msgSend as *const ());
        let get:unsafe extern "C" fn(Id,Sel)->Id=std::mem::transmute(objc_msgSend as *const ());
        let boolean:unsafe extern "C" fn(Id,Sel,bool)=std::mem::transmute(objc_msgSend as *const ());
        let flag:unsafe extern "C" fn(Id,Sel)->bool=std::mem::transmute(objc_msgSend as *const ());
        let point:unsafe extern "C" fn(Id,Sel,Point)=std::mem::transmute(objc_msgSend as *const ());
        let set_frame:unsafe extern "C" fn(Id,Sel,Rect)=std::mem::transmute(objc_msgSend as *const ());
        let convert:unsafe extern "C" fn(Id,Sel,Point,Id)->Point=std::mem::transmute(objc_msgSend as *const ());
        let views=[button(native,selector(c"standardWindowButton:"),0),button(native,selector(c"standardWindowButton:"),1),button(native,selector(c"standardWindowButton:"),2)];
        if views.iter().any(|view|view.is_null()){return Err(Error::WindowUnavailable);}
        let parent=get(views[0],selector(c"superview"));if parent.is_null(){return Err(Error::WindowUnavailable);}
        let container=get(parent,selector(c"superview"));let content=get(native,selector(c"contentView"));
        if container.is_null()||content.is_null(){return Err(Error::WindowUnavailable);}
        if saved.is_none(){*saved=Some(SavedLayout{height:frame(container).height,buttons:views.map(|view|frame(view)),hidden:views.map(|view|flag(view,selector(c"isHidden")))});}
        let hidden=fullscreen||(!matches!(surface,Surface::Pages)&&!controls);
        let center=if matches!(surface,Surface::Pages){38.0}else{54.0};
        // AppKit clips the native buttons to this container. Moving the buttons alone
        // leaves them outside the original title bar. Preserve native views/hit testing.
        if !hidden{
            let mut bounds=frame(container);bounds.height=center*2.0;bounds.y=frame(native).height-bounds.height;
            set_frame(container,selector(c"setFrame:"),bounds);
        }
        let content_bounds=geometry(content,selector(c"bounds"));
        for (index,view) in views.into_iter().enumerate(){
            boolean(view,selector(c"setHidden:"),hidden);
            if hidden{continue;}
            let parent=get(view,selector(c"superview"));if parent.is_null(){return Err(Error::WindowUnavailable);}
            let size=frame(view);
            let desired=Point{x:content_bounds.x+26.0+index as f64*20.0+size.width/2.0,y:content_bounds.y+if flag(content,selector(c"isFlipped")){center}else{content_bounds.height-center}};
            let local=convert(parent,selector(c"convertPoint:fromView:"),desired,content);
            point(view,selector(c"setFrameOrigin:"),Point{x:local.x-size.width/2.0,y:local.y-size.height/2.0});
        }
    }
    Ok(())
}
pub fn restore(window:&tauri::WebviewWindow,saved:&mut Option<SavedLayout>)->Result<()> {
    let Some(original)=saved.as_ref()else{return Ok(());};
    let native=window.ns_window().map_err(|_|Error::WindowUnavailable)?;
    if native.is_null(){return Err(Error::WindowUnavailable);}
    unsafe{
        let button:unsafe extern "C" fn(Id,Sel,usize)->Id=std::mem::transmute(objc_msgSend as *const ());
        let get:unsafe extern "C" fn(Id,Sel)->Id=std::mem::transmute(objc_msgSend as *const ());
        let set_frame:unsafe extern "C" fn(Id,Sel,Rect)=std::mem::transmute(objc_msgSend as *const ());
        let boolean:unsafe extern "C" fn(Id,Sel,bool)=std::mem::transmute(objc_msgSend as *const ());
        let views=[button(native,selector(c"standardWindowButton:"),0),button(native,selector(c"standardWindowButton:"),1),button(native,selector(c"standardWindowButton:"),2)];
        if views.iter().any(|view|view.is_null()){return Err(Error::WindowUnavailable);}
        let parent=get(views[0],selector(c"superview"));if parent.is_null(){return Err(Error::WindowUnavailable);}
        let container=get(parent,selector(c"superview"));if container.is_null(){return Err(Error::WindowUnavailable);}
        let mut bounds=frame(container);bounds.height=original.height;bounds.y=frame(native).height-bounds.height;
        set_frame(container,selector(c"setFrame:"),bounds);
        for (index,view) in views.into_iter().enumerate(){set_frame(view,selector(c"setFrame:"),original.buttons[index]);boolean(view,selector(c"setHidden:"),original.hidden[index]);}
    }
    *saved=None;
    Ok(())
}

//! GTK Overlay: GLArea below the unchanged transparent WebKitGTK widget. SDR only.
use crate::{audio::AudioOutput,boundary::{Error,Result},mpv::{Api,Mpv,VideoTarget}};
use gtk::{prelude::*,glib};
use std::{cell::RefCell,ffi::{c_char,c_int,c_void},ptr,rc::Rc,sync::Arc,time::Duration};
use webkit2gtk::WebViewExt;
use std::sync::{atomic::{AtomicU64,AtomicU32,Ordering},OnceLock};
static RENDER_CALLS:AtomicU64=AtomicU64::new(0);
static FRAMES:AtomicU64=AtomicU64::new(0);
static COLORED:AtomicU64=AtomicU64::new(0);
static PIXEL:AtomicU32=AtomicU32::new(0);
static READ_ERROR:AtomicU32=AtomicU32::new(0);
static TARGET_ID:AtomicU32=AtomicU32::new(0);
static RENDERER:OnceLock<String>=OnceLock::new();
pub fn proof()->serde_json::Value{serde_json::json!({"renderCalls":RENDER_CALLS.load(Ordering::Relaxed),"renderedFrames":FRAMES.load(Ordering::Relaxed),"nonBlackFrames":COLORED.load(Ordering::Relaxed),"debugTargetFbo":TARGET_ID.load(Ordering::Relaxed),"debugCenterRgba":PIXEL.load(Ordering::Relaxed).to_be_bytes(),"debugReadError":READ_ERROR.load(Ordering::Relaxed),"renderer":RENDERER.get(),"output":"SDR OpenGL","hdrVerified":false})}

#[repr(C)]struct Param{kind:c_int,data:*mut c_void}
#[repr(C)]struct Init{get:unsafe extern "C" fn(*mut c_void,*const c_char)->*mut c_void,context:*mut c_void}
#[repr(C)]struct Fbo{id:c_int,width:c_int,height:c_int,format:c_int}
type Create=unsafe extern "C" fn(*mut *mut c_void,*mut c_void,*mut Param)->c_int;
type Render=unsafe extern "C" fn(*mut c_void,*mut Param)->c_int;
#[repr(C)]struct FrameInfo{flags:u64,target_time:i64}
type Update=unsafe extern "C" fn(*mut c_void)->u64;
type GetInfo=unsafe extern "C" fn(*mut c_void,Param)->c_int;
type Free=unsafe extern "C" fn(*mut c_void);
type GetProc=unsafe extern "C" fn(*const c_char)->*mut c_void;
type GetInt=unsafe extern "C" fn(u32,*mut i32);

struct Context{raw:*mut c_void,engine:Arc<Mpv>,free:Free,gl:Box<libloading::Library>}
impl Drop for Context{fn drop(&mut self){unsafe{(self.free)(self.raw)}}}
unsafe extern "C" fn get_proc(ctx:*mut c_void,name:*const c_char)->*mut c_void {
    unsafe {
        let lib=&*(ctx as *const libloading::Library);
        if let Ok(get)=lib.get::<GetProc>(b"eglGetProcAddress\0"){let p=get(name);if !p.is_null(){return p;}}
        lib.get::<*mut c_void>(std::ffi::CStr::from_ptr(name).to_bytes_with_nul()).map(|x|*x).unwrap_or(ptr::null_mut())
    }
}
pub fn attach(webview:&tauri::webview::PlatformWebview,api:Arc<Api>,output:&AudioOutput)->Result<Arc<Mpv>> {
    let engine=Mpv::new(api,output,VideoTarget::OpenGl)?;
    let web=webview.inner();
    web.set_background_color(&gtk::gdk::RGBA::new(0.0,0.0,0.0,0.0));
    let parent=web.parent().ok_or(Error::Unsupported)?.downcast::<gtk::Box>().map_err(|_|Error::Unsupported)?;
    parent.remove(&web);
    let overlay=gtk::Overlay::new();let fixed=gtk::Fixed::builder().hexpand(true).vexpand(true).build();
    let area=gtk::GLArea::builder().auto_render(false).has_alpha(false).hexpand(true).vexpand(true).build();
    area.set_required_version(3,2);
    area.set_widget_name("portico-native-video");fixed.put(&area,0,0);
    overlay.add(&fixed);overlay.add_overlay(&web);parent.pack_start(&overlay,true,true,0);
    let context:Rc<RefCell<Option<Context>>>=Rc::new(RefCell::new(None));
    let current=context.clone();let source=Arc::downgrade(&engine);
    area.connect_realize(move |area| {
        let Some(source)=source.upgrade()else{return};
        area.make_current();if area.error().is_some(){return;}
        let created=(||->Result<Context>{unsafe{
            let gl=Box::new(libloading::Library::new("libEGL.so.1").map_err(|_|Error::EngineUnavailable)?);
            let create=source.api.symbol::<Create>(b"mpv_render_context_create\0")?;
            let free=source.api.symbol::<Free>(b"mpv_render_context_free\0")?;
            let mut init=Init{get:get_proc,context:(&*gl as *const libloading::Library).cast_mut().cast()};
            let mut params=[Param{kind:1,data:c"opengl".as_ptr().cast_mut().cast()},Param{kind:2,data:(&mut init as *mut Init).cast()},Param{kind:0,data:ptr::null_mut()}];
            let mut raw=ptr::null_mut();if create(&mut raw,source.handle(),params.as_mut_ptr())<0{return Err(Error::EngineFailure);}
            source.set_surface_ready(true);Ok(Context{raw,engine:source.clone(),free,gl})
        }})();
        *current.borrow_mut()=created.ok();
    });
    let current=context.clone();
    area.connect_render(move |area,_|{
        if let Some(ctx)=current.borrow().as_ref(){unsafe{
            // Rebind GTK's target: libmpv restores standard GL defaults, not the
            // caller's previous FBO. GTK may reuse the same GLArea context.
            area.attach_buffers();
            let mut id=0;
            let p=get_proc((&*ctx.gl as *const libloading::Library).cast_mut().cast(),c"glGetIntegerv".as_ptr());
            if p.is_null(){return glib::Propagation::Proceed;}
            let get:GetInt=std::mem::transmute(p);get(0x8CA6,&mut id);
            TARGET_ID.store(id as u32,Ordering::Relaxed);
            let disable=get_proc((&*ctx.gl as *const libloading::Library).cast_mut().cast(),c"glDisable".as_ptr());
            if !disable.is_null(){let disable:unsafe extern "C" fn(u32)=std::mem::transmute(disable);for flag in [0x0BE2,0x0C11,0x0B71,0x0B90,0x0B44]{disable(flag);}}
            let p=get_proc((&*ctx.gl as *const libloading::Library).cast_mut().cast(),c"glGetString".as_ptr());
            if !p.is_null(){let get:unsafe extern "C" fn(u32)->*const c_char=std::mem::transmute(p);let name=get(0x1F01);if !name.is_null(){let _=RENDERER.set(std::ffi::CStr::from_ptr(name).to_string_lossy().into_owned());}}
            let scale=area.scale_factor();let mut target=Fbo{id,width:area.allocated_width()*scale,height:area.allocated_height()*scale,format:0};let mut flip=1;
            let mut params=[Param{kind:3,data:(&mut target as *mut Fbo).cast()},Param{kind:4,data:(&mut flip as *mut i32).cast()},Param{kind:0,data:ptr::null_mut()}];
            if let Ok(update)=ctx.engine.api.symbol::<Update>(b"mpv_render_context_update\0"){let _=update(ctx.raw);}
            let generation=ctx.engine.render_generation();
            let mut info=FrameInfo{flags:0,target_time:0};
            let actual_frame=ctx.engine.api.symbol::<GetInfo>(b"mpv_render_context_get_info\0").map(|get| get(ctx.raw,Param{kind:11,data:(&mut info as *mut FrameInfo).cast()})>=0 && info.flags&1!=0 && info.flags&2==0).unwrap_or(false);
            if let Ok(render)=ctx.engine.api.symbol::<Render>(b"mpv_render_context_render\0"){
                if render(ctx.raw,params.as_mut_ptr())>=0 {
                    RENDER_CALLS.fetch_add(1,Ordering::Relaxed);
                    if actual_frame{FRAMES.fetch_add(1,Ordering::Relaxed);ctx.engine.frame_drawn(generation);}
                    #[cfg(debug_assertions)]
                    {let p=get_proc((&*ctx.gl as *const libloading::Library).cast_mut().cast(),c"glReadPixels".as_ptr());
                     let bind=get_proc((&*ctx.gl as *const libloading::Library).cast_mut().cast(),c"glBindFramebuffer".as_ptr());
                     let error=get_proc((&*ctx.gl as *const libloading::Library).cast_mut().cast(),c"glGetError".as_ptr());
                     if !p.is_null()&&!bind.is_null()&&!error.is_null(){
                       let read:unsafe extern "C" fn(i32,i32,i32,i32,u32,u32,*mut c_void)=std::mem::transmute(p);
                       let bind:unsafe extern "C" fn(u32,u32)=std::mem::transmute(bind);
                       let error:unsafe extern "C" fn()->u32=std::mem::transmute(error);
                       let mut previous=0;get(0x8CAA,&mut previous);
                       // mpv may change the read target. Inspect the actual GTK output FBO,
                       // then restore GL state instead of reading the default framebuffer.
                       bind(0x8CA8,target.id as u32);let _=error();let mut pixel=[0u8;4];
                       read(target.width/2,target.height/2,1,1,0x1908,0x1401,pixel.as_mut_ptr().cast());
                       let code=error();READ_ERROR.store(code,Ordering::Relaxed);bind(0x8CA8,previous as u32);
                       if actual_frame&&code==0{PIXEL.store(u32::from_be_bytes(pixel),Ordering::Relaxed);if pixel[..3].iter().any(|x|*x>12){COLORED.fetch_add(1,Ordering::Relaxed);}}
                     }}
                }
            }
        }}
        glib::Propagation::Stop
    });
    let current=context.clone();
    let source=Arc::downgrade(&engine);
    area.connect_unrealize(move |area|{area.make_current();if let Some(source)=source.upgrade(){source.set_surface_ready(false);}current.borrow_mut().take();});
    let weak=area.downgrade();
    glib::timeout_add_local(Duration::from_millis(16),move||{
        if let Some(area)=weak.upgrade(){area.queue_render();glib::ControlFlow::Continue}else{glib::ControlFlow::Break}
    });
    overlay.show_all();
    Ok(engine)
}
pub fn bounds(webview:&tauri::webview::PlatformWebview,bounds:crate::boundary::SurfaceBounds)->Result<()> {
    let overlay=webview.inner().parent().ok_or(Error::Unsupported)?.downcast::<gtk::Overlay>().map_err(|_|Error::Unsupported)?;
    let fixed=overlay.child().ok_or(Error::Unsupported)?.downcast::<gtk::Fixed>().map_err(|_|Error::Unsupported)?;
    let area=fixed.children().into_iter().find(|w|w.widget_name()=="portico-native-video").ok_or(Error::Unsupported)?;
    area.set_size_request(bounds.width.ceil().max(1.0) as i32,bounds.height.ceil().max(1.0) as i32);
    fixed.move_(&area,bounds.x.round() as i32,bounds.y.round() as i32);
    area.set_visible(bounds.width>0.0&&bounds.height>0.0);Ok(())
}

/// Called on GTK's thread while the native client is retained by the close worker.
pub fn retire(webview:&tauri::webview::PlatformWebview)->Result<()>{
    retire_parent(webview.inner().parent().ok_or(Error::Unsupported)?)
}
fn retire_parent(parent:gtk::Widget)->Result<()>{
    // A failed native-library/engine initialization leaves Wry's original Box
    // intact. There is no owned GL surface to retire in that case.
    if parent.is::<gtk::Box>(){return Ok(())}
    let overlay=parent.downcast::<gtk::Overlay>().map_err(|_|Error::Unsupported)?;
    let fixed=overlay.child().ok_or(Error::Unsupported)?.downcast::<gtk::Fixed>().map_err(|_|Error::Unsupported)?;
    if let Some(area)=fixed.children().into_iter().find(|w|w.widget_name()=="portico-native-video"){
        area.unrealize();fixed.remove(&area);
    }
    Ok(())
}

#[cfg(test)]mod tests{
    use super::*;
    #[test]fn retirement_preserves_unattached_webview_parent_and_removes_only_owned_area(){
        gtk::init().expect("requires an explicit owned headless GTK display");
        let original=gtk::Box::new(gtk::Orientation::Vertical,0);
        let child=gtk::Label::new(Some("public fixture"));original.add(&child);
        assert_eq!(retire_parent(original.clone().upcast()),Ok(()));assert_eq!(original.children().len(),1);
        let overlay=gtk::Overlay::new();let fixed=gtk::Fixed::new();overlay.add(&fixed);
        let owned=gtk::GLArea::new();owned.set_widget_name("portico-native-video");fixed.put(&owned,0,0);
        let foreign=gtk::GLArea::new();foreign.set_widget_name("foreign-area");fixed.put(&foreign,0,0);
        assert_eq!(retire_parent(overlay.clone().upcast()),Ok(()));assert!(owned.parent().is_none());assert_eq!(foreign.parent(),Some(fixed.clone().upcast()));
        // The real Wry parent retains its overlay while retirement runs. Keep
        // that owner alive when checking repeated retirement and foreign children.
        assert_eq!(retire_parent(overlay.clone().upcast()),Ok(()));assert_eq!(fixed.children().len(),1);
        assert_eq!(retire_parent(gtk::Overlay::new().upcast()),Err(Error::Unsupported));
    }
}

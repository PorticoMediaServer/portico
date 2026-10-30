//! Native non-client hits preserve Windows caption actions and the maximise Snap affordance.
//! This source is unverified on Windows; it is not DirectComposition/HDR acceptance.
use super::{CaptionNames,ChromeState};
use crate::boundary::{Error,Result};
use std::{ffi::c_void,sync::{Arc,atomic::Ordering}};
type Hwnd=*mut c_void;
type Subclass=unsafe extern "system" fn(Hwnd,u32,usize,isize,usize,usize)->isize;
struct Hook {state:Arc<ChromeState>,root:Hwnd,parent:bool}
#[repr(C)]struct Point{x:i32,y:i32}
#[repr(C)]struct Track{size:u32,flags:u32,window:Hwnd,time:u32}
#[link(name="user32")]
unsafe extern "system" {
    fn ScreenToClient(window:Hwnd,point:*mut Point)->i32;
    fn GetWindowLongPtrW(window:Hwnd,index:i32)->isize;
    fn SetWindowLongPtrW(window:Hwnd,index:i32,value:isize)->isize;
    fn SetWindowPos(window:Hwnd,after:Hwnd,x:i32,y:i32,width:i32,height:i32,flags:u32)->i32;
    fn GetSystemMenu(window:Hwnd,revert:i32)->*mut c_void;
    fn GetMenuStringW(menu:*mut c_void,id:u32,value:*mut u16,length:i32,flags:u32)->i32;
    fn TrackMouseEvent(event:*mut Track)->i32;
    fn EnumChildWindows(window:Hwnd,callback:unsafe extern "system" fn(Hwnd,isize)->i32,data:isize)->i32;
}
#[link(name="comctl32")]
unsafe extern "system" {
    fn SetWindowSubclass(window:Hwnd,callback:Subclass,id:usize,data:usize)->i32;
    fn RemoveWindowSubclass(window:Hwnd,callback:Subclass,id:usize)->i32;
    fn DefSubclassProc(window:Hwnd,message:u32,w:usize,l:isize)->isize;
}
const SUBCLASS:usize=0x504f5254;
pub fn names(window:&tauri::WebviewWindow)->Result<Option<CaptionNames>>{
    let hwnd=window.hwnd().map_err(|_|Error::WindowUnavailable)?.0 as Hwnd;
    unsafe{
        let menu=GetSystemMenu(hwnd,0);if menu.is_null(){return Ok(None);}
        let text=|id|->Option<String>{
            let mut buffer=[0u16;256];let count=GetMenuStringW(menu,id,buffer.as_mut_ptr(),256,0);
            if count<=0{return None;}
            let raw=String::from_utf16_lossy(&buffer[..count as usize]);
            let label=raw.split('\t').next()?.replace("&&","\0").replace('&',"").replace('\0',"&");
            if label.is_empty(){None}else{Some(label)}
        };
        Ok(match (text(0xf020),text(0xf030),text(0xf120),text(0xf060)){
            (Some(minimize),Some(maximize),Some(restore),Some(close))=>Some(CaptionNames{minimize,maximize,restore,close}),_=>None})
    }
}
pub fn configure(window:&tauri::WebviewWindow)->Result<()> {
    let hwnd=window.hwnd().map_err(|_|Error::WindowUnavailable)?.0 as Hwnd;
    unsafe{
        let style=GetWindowLongPtrW(hwnd,-16);
        SetWindowLongPtrW(hwnd,-16,style|0x0008_0000|0x0002_0000|0x0001_0000|0x0004_0000);
        // FRAMECHANGED|NOACTIVATE|NOZORDER|NOMOVE|NOSIZE. Do not resize the web content.
        if SetWindowPos(hwnd,std::ptr::null_mut(),0,0,0,0,0x0037)==0{return Err(Error::WindowUnavailable);}
    }
    Ok(())
}
pub fn attach(window:&tauri::WebviewWindow,state:Arc<ChromeState>)->Result<()> {
    configure(window)?;
    let hwnd=window.hwnd().map_err(|_|Error::WindowUnavailable)?.0 as Hwnd;
    unsafe{
        let mut children=Vec::<Hwnd>::new();
        let _=EnumChildWindows(hwnd,collect,(&mut children as *mut Vec<Hwnd>) as isize);
        let mut installed=Vec::new();
        for target in std::iter::once(hwnd).chain(children){
            let pointer=Box::into_raw(Box::new(Hook{state:state.clone(),root:hwnd,parent:target==hwnd}));
            if SetWindowSubclass(target,subclass,SUBCLASS,pointer as usize)==0{
                drop(Box::from_raw(pointer));
                for (previous,hook) in installed{let _=RemoveWindowSubclass(previous,subclass,SUBCLASS);drop(Box::from_raw(hook));}
                return Err(Error::WindowUnavailable);
            }
            installed.push((target,pointer));
        }
    }
    Ok(())
}
unsafe extern "system" fn collect(window:Hwnd,data:isize)->i32{
    unsafe{(&mut *(data as *mut Vec<Hwnd>)).push(window);}1
}
unsafe extern "system" fn subclass(hwnd:Hwnd,message:u32,w:usize,l:isize,_id:usize,data:usize)->isize {
    unsafe{
        let hook=&*(data as *const Hook);let state=&hook.state;
        match message {
            0x0084=>{
                let mut point=Point{x:(l as u16 as i16) as i32,y:((l as usize>>16) as u16 as i16) as i32};
                if ScreenToClient(hook.root,&mut point)!=0{
                    if let Ok(rects)=state.rects.lock(){if let Some(rects)=*rects{
                        let (x,y)=(f64::from(point.x)/rects.scale,f64::from(point.y)/rects.scale);
                        let hit=if rects.minimize.contains(x,y){Some(8)}else if rects.maximize.contains(x,y){Some(9)}else if rects.close.contains(x,y){Some(20)}else{None};
                        // WebView2's child HWND must yield the caption rectangles to its
                        // parent. Otherwise DOM clicks work but Windows never sees HTMAXBUTTON.
                        if let Some(hit)=hit{return if hook.parent{hit}else{-1};}
                    }}
                }
            },
            0x00a0=>{
                state.hover.store(match w{8=>1,9=>2,20=>3,_=>0},Ordering::Release);
                let mut track=Track{size:std::mem::size_of::<Track>() as u32,flags:0x12,window:hwnd,time:0};let _=TrackMouseEvent(&mut track);
            },
            0x02a2=>state.hover.store(0,Ordering::Release),
            0x0082=>{
                let _=RemoveWindowSubclass(hwnd,subclass,SUBCLASS);
                let result=DefSubclassProc(hwnd,message,w,l);drop(Box::from_raw(data as *mut Hook));return result;
            },
            _=>{},
        }
        DefSubclassProc(hwnd,message,w,l)
    }
}

//! Read-only computer identity from the OS, never a login name, path or guessed model.
use serde::Serialize;

#[derive(Serialize)]
#[serde(rename_all="camelCase")]
pub struct DeviceName {value:String,read_only:bool}
#[derive(Serialize)]
#[serde(rename_all="camelCase")]
pub struct DeviceFacts {
    #[serde(skip_serializing_if="Option::is_none")]
    name:Option<DeviceName>,
    app_version:&'static str,
}
fn project(name:Option<String>)->DeviceFacts {
    DeviceFacts{
        name:name.filter(|v|!v.trim().is_empty()&&v.len()<=1024&&v.chars().count()<=255&&!v.chars().any(char::is_control))
            .map(|value|DeviceName{value,read_only:true}),
        app_version:env!("CARGO_PKG_VERSION"),
    }
}
pub fn current()->DeviceFacts {project(computer_name())}
impl DeviceFacts{pub fn computer_name(&self)->Option<&str>{self.name.as_ref().map(|name|name.value.as_str())}}

#[cfg(target_os="linux")]
fn computer_name()->Option<String> {
    let mut name=[0u8;256];
    if unsafe{libc::gethostname(name.as_mut_ptr().cast(),name.len())}!=0{return None}
    let end=name.iter().position(|c|*c==0)?;
    std::str::from_utf8(&name[..end]).ok().map(str::to_owned)
}
#[cfg(target_os="windows")]
fn computer_name()->Option<String> {
    // GetComputerNameW returns the actual OS NetBIOS name, not an account label.
    // learn.microsoft.com/windows/win32/api/winbase/nf-winbase-getcomputernamew
    #[link(name="kernel32")]
    unsafe extern "system" {fn GetComputerNameW(buffer:*mut u16,size:*mut u32)->i32;}
    let mut name=[0u16;256];let mut size=name.len() as u32;
    if unsafe{GetComputerNameW(name.as_mut_ptr(),&mut size)}==0||size as usize>=name.len(){return None}
    String::from_utf16(&name[..size as usize]).ok()
}
#[cfg(target_os="macos")]
fn computer_name()->Option<String> {
    use std::{ffi::{c_char,c_void},ptr};
    // SDK SystemConfiguration/SCDynamicStoreCopySpecific.h: a null store creates
    // a temporary session. The returned CFString is owned by this caller.
    #[link(name="SystemConfiguration",kind="framework")]
    unsafe extern "C" {fn SCDynamicStoreCopyComputerName(store:*const c_void,encoding:*mut u32)->*const c_void;}
    #[link(name="CoreFoundation",kind="framework")]
    unsafe extern "C" {
        fn CFStringGetCString(value:*const c_void,buffer:*mut c_char,size:isize,encoding:u32)->u8;
        fn CFRelease(value:*const c_void);
    }
    let value=unsafe{SCDynamicStoreCopyComputerName(ptr::null(),ptr::null_mut())};
    if value.is_null(){return None}
    let mut name=[0u8;4097];
    let copied=unsafe{CFStringGetCString(value,name.as_mut_ptr().cast(),name.len() as isize,0x08000100)};
    unsafe{CFRelease(value)};
    if copied==0{return None}
    let end=name.iter().position(|c|*c==0)?;
    std::str::from_utf8(&name[..end]).ok().map(str::to_owned)
}
#[cfg(not(any(target_os="linux",target_os="macos",target_os="windows")))]
fn computer_name()->Option<String>{None}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn exact_read_only_name_and_package_version_without_invented_update_or_document(){
        let value=serde_json::to_value(project(Some("Studio — 设备".into()))).unwrap();
        assert_eq!(value,serde_json::json!({"name":{"value":"Studio — 设备","readOnly":true},"appVersion":env!("CARGO_PKG_VERSION")}));
        for name in [None,Some(" ".into()),Some("private\nname".into()),Some("x".repeat(256))]{
            let value=serde_json::to_value(project(name)).unwrap();assert!(value.get("name").is_none());
            assert_eq!(value.as_object().unwrap().len(),1);
        }
    }
    #[test]
    fn real_os_query_is_bounded_and_contains_no_other_identity_fields(){
        let value=serde_json::to_value(current()).unwrap();
        assert_eq!(value["appVersion"],env!("CARGO_PKG_VERSION"));
        assert!(value.as_object().unwrap().keys().all(|key|matches!(key.as_str(),"name"|"appVersion")));
        if let Some(name)=value.get("name"){assert_eq!(name["readOnly"],true);assert!(!name["value"].as_str().unwrap().is_empty());}
    }
}

//! Wry 0.57 currently supplies an HWND WebView2 controller. A DirectComposition media visual
//! cannot be certified below that controller by setting d3d11-output-mode alone. Fail closed
//! until the composition-controller integration is built and measured on Windows.
use crate::{audio::AudioOutput,boundary::{Error,Result},mpv::{Api,Mpv}};
use std::sync::Arc;
pub fn proof()->serde_json::Value{serde_json::json!({"output":"composition controller unavailable","hdrVerified":false,"renderedFrames":null})}
pub fn attach(_webview:&tauri::webview::PlatformWebview,_api:Arc<Api>,_output:&AudioOutput)->Result<Arc<Mpv>> {
    Err(Error::Unsupported)
}
pub fn bounds(_webview:&tauri::webview::PlatformWebview,_bounds:crate::boundary::SurfaceBounds)->Result<()> {Err(Error::Unsupported)}

pub fn retire(_webview:&tauri::webview::PlatformWebview)->Result<()>{Ok(())}

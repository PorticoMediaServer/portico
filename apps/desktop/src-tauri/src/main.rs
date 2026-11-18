// No console window behind the app on Windows.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::sync::Arc;
use tauri::{Manager, Url, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_opener::OpenerExt;

mod mpv;

/// The app's own pages: Tauri serves the built web app at tauri://localhost (macOS, Linux) and
/// http://tauri.localhost (Windows); a debug build may load the web app's dev server instead.
fn own_page(url: &Url) -> bool {
    match (url.scheme(), url.host_str(), url.port()) {
        ("tauri", Some("localhost"), None) | ("http", Some("tauri.localhost"), None) => true,
        ("http", Some("127.0.0.1"), Some(5174)) => cfg!(debug_assertions),
        _ => false,
    }
}

/// A link to anywhere else (the terms, help, web.getportico.tv) opens in the person's browser,
/// so the app never turns into a website with no way back.
fn outside(url: &Url) -> bool {
    matches!(url.scheme(), "http" | "https" | "mailto")
}

type Player<'a> = tauri::State<'a, Arc<mpv::Mpv>>;

/// mpv's IPC answers block, so each command runs off the window's thread.
async fn blocking<T: Send + 'static>(run: impl FnOnce() -> Result<T, String> + Send + 'static) -> Result<T, String> {
    tauri::async_runtime::spawn_blocking(run).await.map_err(|_| "mpv_unavailable".to_string())?
}

#[tauri::command]
async fn mpv_load(player: Player<'_>, url: String, start_ms: f64, autoplay: bool) -> Result<(), String> {
    let mpv = player.inner().clone();
    blocking(move || mpv.load(&url, start_ms, autoplay, &[])).await
}

#[tauri::command]
async fn mpv_command(player: Player<'_>, action: String, value: f64) -> Result<(), String> {
    let mpv = player.inner().clone();
    blocking(move || mpv.command(&action, value)).await
}

#[tauri::command]
async fn mpv_subtitle(player: Player<'_>, url: Option<String>) -> Result<(), String> {
    let mpv = player.inner().clone();
    blocking(move || mpv.subtitle(url.as_deref())).await
}

#[tauri::command]
async fn mpv_observe(player: Player<'_>) -> Result<mpv::Reading, String> {
    let mpv = player.inner().clone();
    blocking(move || Ok(mpv.observe())).await
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .manage(Arc::new(mpv::Mpv::default()))
        .invoke_handler(tauri::generate_handler![smoke::report, mpv_load, mpv_command, mpv_subtitle, mpv_observe])
        .setup(|app| {
            let handle = app.handle().clone();
            let browser = app.handle().clone();
            WebviewWindowBuilder::new(app, "main", WebviewUrl::App("index.html".into()))
                .title("Portico")
                .inner_size(1280.0, 800.0)
                .min_inner_size(600.0, 600.0)
                .on_navigation(move |url| {
                    if own_page(url) {
                        return true;
                    }
                    if outside(url) {
                        let _ = handle.opener().open_url(url.as_str(), None::<&str>);
                    }
                    false
                })
                .on_new_window(move |url, _| {
                    if outside(&url) {
                        let _ = browser.opener().open_url(url.as_str(), None::<&str>);
                    }
                    tauri::webview::NewWindowResponse::Deny
                })
                .initialization_script(smoke::collect())
                .on_page_load(|window, payload| smoke::loaded(&window, payload.event()))
                .build()?;
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("Portico could not start")
        .run(|app, event| {
            if let tauri::RunEvent::Exit = event {
                app.state::<Arc<mpv::Mpv>>().quit();
            }
        });
}

/// The start-up check CI runs on each system (debug builds only, with PORTICO_DESKTOP_SMOKE set
/// to a file): the page's errors are collected from its first script on; once the web app has
/// handed over from its boot screen to a page (or 30 seconds have passed) the result is written
/// to that file and the app quits. `scripts/smoke.mjs` reads it.
mod smoke {
    use tauri::{Manager, WebviewWindow, webview::PageLoadEvent};

    fn on() -> bool {
        cfg!(debug_assertions) && std::env::var_os("PORTICO_DESKTOP_SMOKE").is_some()
    }

    pub fn collect() -> &'static str {
        if !on() {
            return "";
        }
        "window.__porticoErrors=[];{const keep=m=>window.__porticoErrors.length<20&&window.__porticoErrors.push(String(m).slice(0,300));\
         addEventListener('error',e=>keep(e.message+' '+e.filename+':'+e.lineno));\
         addEventListener('unhandledrejection',e=>keep('rejected '+(e.reason&&e.reason.stack||e.reason)));\
         document.addEventListener('securitypolicyviolation',e=>keep('blocked '+e.violatedDirective+' '+e.blockedURI));}"
    }

    pub fn loaded(window: &WebviewWindow, event: PageLoadEvent) {
        if on() && event == PageLoadEvent::Finished {
            let _ = window.eval(
                "(()=>{const start=Date.now();const check=()=>{const ready=!document.getElementById('boot')&&!!document.getElementById('root')?.childElementCount;\
                 if(ready||Date.now()-start>30000)setTimeout(()=>window.__TAURI_INTERNALS__.invoke('report',{ready,path:location.pathname,\
                 errors:window.__porticoErrors||[],text:document.body.innerText.slice(0,400)}),ready?1000:0);else setTimeout(check,250);};check();})()",
            );
        }
    }

    #[tauri::command]
    pub fn report(window: WebviewWindow, ready: bool, path: String, errors: Vec<String>, text: String) {
        let Some(file) = std::env::var_os("PORTICO_DESKTOP_SMOKE").filter(|_| cfg!(debug_assertions)) else { return };
        let body = serde_json::json!({"ready": ready, "path": path, "errors": errors, "text": text});
        let _ = std::fs::write(file, body.to_string());
        window.app_handle().exit(0);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_the_apps_own_pages_stay_in_the_window() {
        for page in ["tauri://localhost/", "tauri://localhost/home", "http://tauri.localhost/servers"] {
            assert!(own_page(&Url::parse(page).unwrap()), "{page}");
        }
        for page in ["https://getportico.tv/terms", "https://web.getportico.tv/servers?open=x", "http://tauri.localhost:8080/",
            "https://tauri.localhost/", "tauri://evil.example/", "http://192.168.1.5:32400/"] {
            assert!(!own_page(&Url::parse(page).unwrap()), "{page}");
        }
    }

    #[test]
    fn only_web_and_mail_links_open_in_the_browser() {
        assert!(outside(&Url::parse("https://getportico.tv/privacy").unwrap()));
        assert!(outside(&Url::parse("mailto:support@getportico.tv").unwrap()));
        for link in ["file:///etc/passwd", "javascript:alert(1)", "smb://server/share"] {
            assert!(!outside(&Url::parse(link).unwrap()), "{link}");
        }
    }
}

// No console window behind the app on Windows.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use tauri::{Url, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_opener::OpenerExt;

/// The app's own pages: Tauri serves the built web app at tauri://localhost (macOS, Linux) and
/// http://tauri.localhost (Windows); a debug build may load the web app's dev server instead.
fn own_page(url: &Url) -> bool {
    // A file the page made itself (recovery codes) is a blob: of the page's own address.
    if url.scheme() == "blob" {
        return Url::parse(url.path()).is_ok_and(|inner| inner.scheme() != "blob" && own_page(&inner));
    }
    match (url.scheme(), url.host_str(), url.port()) {
        ("tauri", Some("localhost"), None) | ("http", Some("tauri.localhost"), None) => true,
        ("http", Some("127.0.0.1"), Some(5174)) => cfg!(debug_assertions),
        _ => false,
    }
}

/// Save on a title: the server's own file at its media address with `save=1`, which the server
/// sends as an attachment, so the window downloads it instead of leaving the app.
fn server_save(url: &Url) -> bool {
    matches!(url.scheme(), "http" | "https") && url.path().starts_with("/v1/media/") && url.query_pairs().any(|(key, value)| key == "save" && value == "1")
}

/// Downloads go to the person's Downloads folder under the name the page gave, as in a browser,
/// numbered when that name is taken.
fn download_destination(folder: &std::path::Path, suggested: &std::path::Path) -> std::path::PathBuf {
    let name = suggested.file_name().map(|name| std::path::PathBuf::from(name)).unwrap_or_else(|| "Portico download".into());
    let (stem, extension) = (name.file_stem().unwrap_or_default().to_string_lossy().into_owned(), name.extension().map(|e| e.to_string_lossy().into_owned()));
    (0..).map(|n| {
        let base = if n == 0 { stem.clone() } else { format!("{stem} ({n})") };
        folder.join(match &extension { Some(extension) => format!("{base}.{extension}"), None => base })
    }).find(|path| !path.exists()).expect("a free name")
}

/// A link to anywhere else (the terms, help, web.getportico.tv) opens in the person's browser,
/// so the app never turns into a website with no way back.
fn outside(url: &Url) -> bool {
    matches!(url.scheme(), "http" | "https" | "mailto")
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .invoke_handler(tauri::generate_handler![smoke::report])
        .setup(|app| {
            let handle = app.handle().clone();
            let browser = app.handle().clone();
            WebviewWindowBuilder::new(app, "main", WebviewUrl::App("index.html".into()))
                .title("Portico")
                .inner_size(1280.0, 800.0)
                .min_inner_size(600.0, 600.0)
                .on_navigation(move |url| {
                    if own_page(url) || server_save(url) {
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
                .on_download(|webview, event| {
                    use tauri::Manager;
                    if let tauri::webview::DownloadEvent::Requested { destination, .. } = event {
                        let paths = webview.app_handle().path();
                        // Without a desktop's folder settings, the usual Downloads in the home folder.
                        let Some(folder) = paths.download_dir().or_else(|_| paths.home_dir().map(|home| home.join("Downloads"))).ok() else { return false };
                        let _ = std::fs::create_dir_all(&folder);
                        *destination = download_destination(&folder, destination);
                    }
                    true
                })
                .initialization_script(smoke::collect())
                .on_page_load(|window, payload| smoke::loaded(&window, payload.event()))
                .build()?;
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("Portico could not start");
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
    fn the_pages_own_files_and_the_servers_saves_download_in_the_window() {
        assert!(own_page(&Url::parse("blob:tauri://localhost/0b6c1d3e-1f2a-4b5c-8d9e-0f1a2b3c4d5e").unwrap()));
        assert!(own_page(&Url::parse("blob:http://tauri.localhost/0b6c1d3e").unwrap()));
        assert!(!own_page(&Url::parse("blob:https://evil.example/0b6c1d3e").unwrap()));
        assert!(server_save(&Url::parse("http://192.168.1.5:32500/v1/media/g/source?save=1").unwrap()));
        for not_a_save in ["http://192.168.1.5:32500/v1/media/g/source", "https://evil.example/download?save=1", "https://server/v1/items?save=1"] {
            assert!(!server_save(&Url::parse(not_a_save).unwrap()), "{not_a_save}");
        }
    }

    #[test]
    fn a_download_takes_a_free_name_in_the_downloads_folder() {
        let folder = std::env::temp_dir().join(format!("portico-downloads-{}", std::process::id()));
        std::fs::create_dir_all(&folder).unwrap();
        let first = download_destination(&folder, std::path::Path::new("/somewhere/recovery-codes.txt"));
        assert_eq!(first, folder.join("recovery-codes.txt"));
        std::fs::write(&first, "x").unwrap();
        assert_eq!(download_destination(&folder, std::path::Path::new("recovery-codes.txt")), folder.join("recovery-codes (1).txt"));
        std::fs::remove_dir_all(&folder).unwrap();
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

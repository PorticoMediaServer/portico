// No console window behind the app on Windows.
#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::sync::Arc;
use tauri::{Manager, Url, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_opener::OpenerExt;

mod downloads;
mod mpv;
mod services;

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
async fn services_request(request: services::Request) -> Result<services::Answer, String> {
    tauri::async_runtime::spawn_blocking(move || services::send(request)).await.map_err(|_| "services_unreachable".to_string())?
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase")]
struct DownloadKey {
    server_id: String,
    profile_id: String,
    download_id: String,
    item_id: String,
}
impl DownloadKey {
    fn parts(&self) -> (&str, &str, &str, &str) {
        (&self.server_id, &self.profile_id, &self.download_id, &self.item_id)
    }
}
type Store<'a> = tauri::State<'a, Arc<downloads::Downloads>>;

#[tauri::command]
fn downloads_list(store: Store<'_>, server_id: String, profile_id: String) -> Result<downloads::Listing, String> {
    store.list(&server_id, &profile_id)
}

#[tauri::command]
fn downloads_fetch(store: Store<'_>, ask: downloads::Ask) -> Result<(), String> {
    store.fetch(ask)
}

#[tauri::command]
fn downloads_change(store: Store<'_>, key: DownloadKey, action: String) -> Result<(), String> {
    match action.as_str() {
        "pause" => store.pause(key.parts()),
        "remove" => store.remove(key.parts()),
        "received" => store.received(key.parts()),
        _ => Err("invalid_command".into()),
    }
}

#[tauri::command]
fn downloads_permission(store: Store<'_>, server_id: String, profile_id: String, until_ms: u64) -> Result<(), String> {
    store.permission(&server_id, &profile_id, until_ms)
}

/// Plays a downloaded title in mpv, and keeps where it got to for the server (the state batch),
/// with or without the server: mpv is followed until its window closes or it is given anything else.
#[tauri::command]
async fn downloads_play(store: Store<'_>, player: Player<'_>, key: DownloadKey, play_id: String) -> Result<(), String> {
    let files = store.files(key.parts())?;
    // Where this computer last left it, else where the person was when it was downloaded.
    let start_ms = store.position(key.parts())? as f64;
    let (mpv, downloads) = (player.inner().clone(), store.inner().clone());
    let follower = mpv.clone();
    let opening = blocking(move || mpv.load_files(&files, start_ms, &[])).await?;
    let started = std::time::Instant::now();
    std::thread::spawn(move || {
        // Until the window closes, or mpv is given anything else (whose position is not this title's).
        if let Some((position, ended)) = follower.follow(opening, std::time::Duration::from_secs(1)) {
            let _ = downloads.record_play(downloads::Play { server_id: key.server_id, profile_id: key.profile_id, item_id: key.item_id, play_id,
                position_ms: position as u64, played_ms: started.elapsed().as_millis() as u64,
                made_at_ms: std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_millis() as u64).unwrap_or(0) }, ended);
        }
    });
    Ok(())
}

#[tauri::command]
fn downloads_plays(store: Store<'_>, server_id: String, profile_id: String) -> Result<Vec<downloads::Play>, String> {
    store.plays(&server_id, &profile_id)
}

#[tauri::command]
fn downloads_sent(store: Store<'_>, play_ids: Vec<String>) -> Result<(), String> {
    store.sent(&play_ids)
}

/// Opens the downloads folder in the system's file manager.
#[tauri::command]
fn downloads_open_folder(app: tauri::AppHandle, store: Store<'_>) -> Result<(), String> {
    app.opener().open_path(store.folder_root().to_string_lossy(), None::<&str>).map_err(|_| "folder_unavailable".to_string())
}

#[tauri::command]
async fn mpv_available() -> Result<bool, String> {
    blocking(|| Ok(mpv::available())).await
}

#[tauri::command]
async fn mpv_tracks(player: Player<'_>, audio: Option<i64>, subtitle: Option<i64>) -> Result<(), String> {
    let mpv = player.inner().clone();
    blocking(move || mpv.tracks(audio, subtitle)).await
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
        .invoke_handler(tauri::generate_handler![
            smoke::report,
            services_request,
            mpv_available,
            mpv_load,
            mpv_command,
            mpv_subtitle,
            mpv_tracks,
            mpv_observe,
            downloads_list,
            downloads_fetch,
            downloads_change,
            downloads_permission,
            downloads_play,
            downloads_plays,
            downloads_sent,
            downloads_open_folder,
        ])
        .setup(|app| {
            // The app's own data folder (LocalAppData on Windows), never a folder the page names.
            app.manage(downloads::Downloads::open(app.path().app_local_data_dir()?.join("downloads")));
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
        let body = serde_json::json!({"ready": ready, "path": path, "errors": errors, "text": text, "mpv": super::mpv::available()});
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

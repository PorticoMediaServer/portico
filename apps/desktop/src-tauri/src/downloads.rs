//! Downloads kept inside the app (downloads rule 3): each title's files in a folder of its own
//! under the app's data folder, and one index of what is there. Files are fetched here, by byte
//! range, so a lost connection or a restart carries on where it stopped; the page never handles
//! the bytes. What to fetch, and when to remove, comes from the server (`/v1/downloads/sync`).
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::io::{Read, Write};
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

#[derive(Serialize, Deserialize, Clone, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct File {
    pub name: String,
    pub bytes: u64,
    pub done: u64,
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Entry {
    pub server_id: String,
    pub profile_id: String,
    pub download_id: String,
    pub item_id: String,
    pub quality: String,
    /// The title's facts as the page shows them; this side never reads them.
    pub title: Value,
    pub files: Vec<File>,
    /// downloading, paused, failed or ready.
    pub state: String,
    /// The server has been told the whole file is here.
    pub received: bool,
    pub added_ms: u64,
    /// Where Play starts: the person's place when it was downloaded, then where each play here left it.
    #[serde(default)]
    pub position_ms: u64,
}

/// A playing of a downloaded title, kept until the server has it (through the state batch).
#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct Play {
    pub server_id: String,
    pub profile_id: String,
    pub item_id: String,
    pub play_id: String,
    pub position_ms: u64,
    pub played_ms: u64,
    pub made_at_ms: u64,
}

#[derive(Serialize, Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct Index {
    entries: Vec<Entry>,
    #[serde(default)]
    plays: Vec<Play>,
    /// Until when downloads play with no server, per server and profile (from `sync`).
    playable_until_ms: HashMap<String, u64>,
}

/// One file to fetch: the address the server gave for it, its size and its container.
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Fetch {
    pub url: String,
    pub bytes: u64,
    pub container: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Ask {
    pub server_id: String,
    pub profile_id: String,
    pub download_id: String,
    pub item_id: String,
    pub quality: String,
    pub title: Value,
    pub files: Vec<Fetch>,
    /// The person's place in the title when it was downloaded.
    #[serde(default)]
    pub position_ms: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Listing {
    pub entries: Vec<Entry>,
    pub playable_until_ms: u64,
}

pub struct Downloads {
    root: PathBuf,
    index: Mutex<Index>,
    running: Mutex<HashMap<String, Arc<AtomicBool>>>,
}

fn id(value: &str) -> Result<&str, String> {
    if value.is_empty() || value.len() > 128 || !value.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-') {
        return Err("invalid_command".into());
    }
    Ok(value)
}
fn scope(server: &str, profile: &str) -> String {
    format!("{server}/{profile}")
}
fn now_ms() -> u64 {
    std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_millis() as u64).unwrap_or(0)
}

impl Downloads {
    pub fn open(root: PathBuf) -> Arc<Downloads> {
        let index = std::fs::read(root.join("index.json")).ok().and_then(|bytes| serde_json::from_slice::<Index>(&bytes).ok()).unwrap_or_default();
        let downloads = Arc::new(Downloads { root, index: Mutex::new(index), running: Mutex::new(HashMap::new()) });
        // A fetch the app was closed in the middle of waits for the page to give fresh addresses.
        if let Ok(mut index) = downloads.index.lock() {
            for entry in index.entries.iter_mut().filter(|e| e.state == "downloading") {
                entry.state = "paused".into();
            }
        }
        downloads
    }

    fn folder(&self, e: &Entry) -> PathBuf {
        self.root.join(&e.server_id).join(&e.profile_id).join(format!("{}-{}", e.download_id, e.item_id))
    }

    fn save(&self, index: &Index) {
        let _ = std::fs::create_dir_all(&self.root);
        let temporary = self.root.join("index.json.tmp");
        if let Ok(bytes) = serde_json::to_vec(index) {
            if std::fs::write(&temporary, bytes).is_ok() {
                let _ = std::fs::rename(&temporary, self.root.join("index.json"));
            }
        }
    }

    fn change(&self, key: (&str, &str, &str, &str), apply: impl FnOnce(&mut Entry)) -> Result<(), String> {
        let mut index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        let entry = index.entries.iter_mut().find(|e| (e.server_id.as_str(), e.profile_id.as_str(), e.download_id.as_str(), e.item_id.as_str()) == key)
            .ok_or("download_not_found")?;
        apply(entry);
        self.save(&index);
        Ok(())
    }

    pub fn list(&self, server: &str, profile: &str) -> Result<Listing, String> {
        let index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        Ok(Listing {
            entries: index.entries.iter().filter(|e| e.server_id == server && e.profile_id == profile).cloned().collect(),
            playable_until_ms: index.playable_until_ms.get(&scope(server, profile)).copied().unwrap_or(0),
        })
    }

    /// Starts, or carries on, fetching a title's files into its folder.
    pub fn fetch(self: &Arc<Self>, ask: Ask) -> Result<(), String> {
        let key = (id(&ask.server_id)?.to_owned(), id(&ask.profile_id)?.to_owned(), id(&ask.download_id)?.to_owned(), id(&ask.item_id)?.to_owned());
        if ask.files.is_empty() || ask.files.iter().any(|f| !matches!(tauri::Url::parse(&f.url).map(|u| u.scheme().to_owned()).as_deref(), Ok("http" | "https"))) {
            return Err("invalid_command".into());
        }
        let files: Vec<File> = ask.files.iter().enumerate().map(|(n, f)| {
            let container: String = f.container.chars().filter(|c| c.is_ascii_alphanumeric()).take(8).collect();
            File { name: format!("{}.{}", n + 1, if container.is_empty() { "bin".into() } else { container }), bytes: f.bytes, done: 0 }
        }).collect();
        {
            let mut index = self.index.lock().map_err(|_| "downloads_unavailable")?;
            let at = index.entries.iter().position(|e| (&e.server_id, &e.profile_id, &e.download_id, &e.item_id) == (&key.0, &key.1, &key.2, &key.3));
            match at {
                // The same files again carry on from what is on disk; different ones start over.
                Some(n) if index.entries[n].files.iter().map(|f| (&f.name, f.bytes)).eq(files.iter().map(|f| (&f.name, f.bytes))) => {
                    if index.entries[n].state == "ready" { return Ok(()); }
                    index.entries[n].state = "downloading".into();
                }
                Some(n) => {
                    let old = self.folder(&index.entries[n]);
                    let _ = std::fs::remove_dir_all(old);
                    index.entries[n].files = files;
                    index.entries[n].state = "downloading".into();
                    index.entries[n].received = false;
                }
                None => index.entries.push(Entry { server_id: key.0.clone(), profile_id: key.1.clone(), download_id: key.2.clone(), item_id: key.3.clone(),
                    quality: ask.quality.clone(), title: ask.title.clone(), files, state: "downloading".into(), received: false, added_ms: now_ms(), position_ms: ask.position_ms }),
            }
            self.save(&index);
        }
        let label = format!("{}/{}/{}/{}", key.0, key.1, key.2, key.3);
        let stop = Arc::new(AtomicBool::new(false));
        {
            let mut running = self.running.lock().map_err(|_| "downloads_unavailable")?;
            if running.contains_key(&label) { return Ok(()); }
            running.insert(label.clone(), stop.clone());
        }
        let downloads = self.clone();
        let urls: Vec<String> = ask.files.into_iter().map(|f| f.url).collect();
        std::thread::spawn(move || {
            let result = downloads.transfer((&key.0, &key.1, &key.2, &key.3), &urls, &stop);
            let state = match result { Ok(true) => "ready", Ok(false) => "paused", Err(_) => "failed" };
            let _ = downloads.change((&key.0, &key.1, &key.2, &key.3), |e| { if e.state == "downloading" { e.state = state.into(); } });
            if let Ok(mut running) = downloads.running.lock() { running.remove(&label); }
        });
        Ok(())
    }

    /// Fetches what is missing of each file. Ok(false) when stopped.
    fn transfer(&self, key: (&str, &str, &str, &str), urls: &[String], stop: &AtomicBool) -> Result<bool, String> {
        let entry = { let index = self.index.lock().map_err(|_| "downloads_unavailable")?; index.entries.iter().find(|e| (e.server_id.as_str(), e.profile_id.as_str(), e.download_id.as_str(), e.item_id.as_str()) == key).cloned().ok_or("download_not_found")? };
        let folder = self.folder(&entry);
        std::fs::create_dir_all(&folder).map_err(|_| "storage_unavailable")?;
        let agent: ureq::Agent = ureq::Agent::config_builder().http_status_as_error(false).timeout_connect(Some(std::time::Duration::from_secs(20))).build().into();
        for (n, (file, url)) in entry.files.iter().zip(urls).enumerate() {
            let path = folder.join(&file.name);
            let mut have = std::fs::metadata(&path).map(|m| m.len()).unwrap_or(0);
            if have > file.bytes { std::fs::remove_file(&path).map_err(|_| "storage_unavailable")?; have = 0; }
            if have < file.bytes {
                let mut request = agent.get(url.as_str());
                if have > 0 { request = request.header("Range", format!("bytes={have}-")); }
                let mut response = request.call().map_err(|_| "unreachable")?;
                let status = response.status().as_u16();
                if have > 0 && status == 200 { have = 0; } else if !(status == 200 || status == 206) { return Err("refused".into()); }
                let mut out = std::fs::OpenOptions::new().create(true).write(true).append(have > 0).truncate(have == 0).open(&path).map_err(|_| "storage_unavailable")?;
                let mut body = response.body_mut().as_reader();
                let mut buffer = vec![0u8; 1 << 20];
                let mut since_saved = 0u64;
                loop {
                    if stop.load(Ordering::Acquire) { self.record(key, n, have); return Ok(false); }
                    let read = match body.read(&mut buffer) { Ok(read) => read, Err(_) => { self.record(key, n, have); return Err("unreachable".into()); } };
                    if read == 0 { break; }
                    if out.write_all(&buffer[..read]).is_err() { self.record(key, n, have); return Err("storage_full".into()); }
                    have += read as u64;
                    since_saved += read as u64;
                    if since_saved >= 8 << 20 { self.record(key, n, have); since_saved = 0; }
                }
                out.sync_all().map_err(|_| "storage_unavailable")?;
            }
            self.record(key, n, have);
            if have != file.bytes { return Err("short".into()); }
        }
        Ok(true)
    }

    fn record(&self, key: (&str, &str, &str, &str), file: usize, done: u64) {
        let _ = self.change(key, |e| { if let Some(f) = e.files.get_mut(file) { f.done = done; } });
    }

    pub fn pause(&self, key: (&str, &str, &str, &str)) -> Result<(), String> {
        if let Some(stop) = self.running.lock().map_err(|_| "downloads_unavailable")?.get(&format!("{}/{}/{}/{}", key.0, key.1, key.2, key.3)) {
            stop.store(true, Ordering::Release);
        }
        self.change(key, |e| { if e.state == "downloading" { e.state = "paused".into(); } })
    }

    pub fn remove(&self, key: (&str, &str, &str, &str)) -> Result<(), String> {
        let _ = self.pause(key);
        let mut index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        if let Some(n) = index.entries.iter().position(|e| (e.server_id.as_str(), e.profile_id.as_str(), e.download_id.as_str(), e.item_id.as_str()) == key) {
            let entry = index.entries.remove(n);
            let _ = std::fs::remove_dir_all(self.folder(&entry));
            self.save(&index);
        }
        Ok(())
    }

    pub fn received(&self, key: (&str, &str, &str, &str)) -> Result<(), String> {
        self.change(key, |e| e.received = true)
    }

    /// Keeps a play to send, and where the title's next Play starts (the beginning again after
    /// one played to the end).
    pub fn record_play(&self, play: Play, ended: bool) -> Result<(), String> {
        let mut index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        for e in index.entries.iter_mut().filter(|e| (&e.server_id, &e.profile_id, &e.item_id) == (&play.server_id, &play.profile_id, &play.item_id)) {
            e.position_ms = if ended { 0 } else { play.position_ms };
        }
        index.plays.push(play);
        self.save(&index);
        Ok(())
    }

    /// The plays not yet sent for a server and profile.
    pub fn plays(&self, server: &str, profile: &str) -> Result<Vec<Play>, String> {
        let index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        Ok(index.plays.iter().filter(|p| p.server_id == server && p.profile_id == profile).cloned().collect())
    }

    /// Forgets plays the server now has.
    pub fn sent(&self, play_ids: &[String]) -> Result<(), String> {
        let mut index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        index.plays.retain(|p| !play_ids.contains(&p.play_id));
        self.save(&index);
        Ok(())
    }

    pub fn folder_root(&self) -> PathBuf {
        let _ = std::fs::create_dir_all(&self.root);
        self.root.clone()
    }

    pub fn permission(&self, server: &str, profile: &str, until_ms: u64) -> Result<(), String> {
        let mut index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        index.playable_until_ms.insert(scope(id(server)?, id(profile)?), until_ms);
        self.save(&index);
        Ok(())
    }

    /// Where the title's Play starts.
    pub fn position(&self, key: (&str, &str, &str, &str)) -> Result<u64, String> {
        let index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        index.entries.iter().find(|e| (e.server_id.as_str(), e.profile_id.as_str(), e.download_id.as_str(), e.item_id.as_str()) == key)
            .map(|e| e.position_ms).ok_or_else(|| "download_not_found".into())
    }

    /// The finished files of a title, in order, for playing it here.
    pub fn files(&self, key: (&str, &str, &str, &str)) -> Result<Vec<PathBuf>, String> {
        let index = self.index.lock().map_err(|_| "downloads_unavailable")?;
        let entry = index.entries.iter().find(|e| (e.server_id.as_str(), e.profile_id.as_str(), e.download_id.as_str(), e.item_id.as_str()) == key).ok_or("download_not_found")?;
        if entry.state != "ready" { return Err("download_not_ready".into()); }
        let until = index.playable_until_ms.get(&scope(&entry.server_id, &entry.profile_id)).copied().unwrap_or(0);
        if until < now_ms() { return Err("needs_check_in".into()); }
        Ok(entry.files.iter().map(|f| self.folder(entry).join(&f.name)).collect())
    }

}

#[cfg(test)]
mod tests {
    use super::*;

    fn ask(url: &str, bytes: u64) -> Ask {
        Ask { server_id: "srv_1".into(), profile_id: "prf_1".into(), download_id: "dl_1".into(), item_id: "itm_1".into(), quality: "original".into(),
            title: serde_json::json!({"title": "A film"}), files: vec![Fetch { url: url.into(), bytes, container: "mkv".into() }], position_ms: 0 }
    }
    const KEY: (&str, &str, &str, &str) = ("srv_1", "prf_1", "dl_1", "itm_1");

    fn wait_for(downloads: &Downloads, state: &str) -> Entry {
        let started = std::time::Instant::now();
        loop {
            let entry = downloads.list("srv_1", "prf_1").unwrap().entries.into_iter().next().unwrap();
            if entry.state == state { return entry; }
            assert!(started.elapsed() < std::time::Duration::from_secs(20), "waiting for {state}: {entry:?}");
            std::thread::sleep(std::time::Duration::from_millis(50));
        }
    }

    /// A small server answering byte ranges, which drops its first answer halfway (a lost connection).
    fn server(body: Vec<u8>) -> (String, std::thread::JoinHandle<()>) {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("http://{}/v1/media/g/file/1", listener.local_addr().unwrap());
        let handle = std::thread::spawn(move || {
            for (n, stream) in listener.incoming().take(2).enumerate() {
                let mut stream = stream.unwrap();
                let mut request = [0u8; 2048];
                let read = stream.read(&mut request).unwrap();
                let text = String::from_utf8_lossy(&request[..read]).to_string();
                let from = text.lines().find_map(|l| l.strip_prefix("Range: bytes=").or_else(|| l.strip_prefix("range: bytes="))).and_then(|r| r.trim_end_matches('-').parse::<usize>().ok()).unwrap_or(0);
                let part = &body[from..];
                let head = if from > 0 { format!("HTTP/1.1 206 Partial Content\r\nContent-Length: {}\r\nContent-Range: bytes {from}-{}/{}\r\n\r\n", part.len(), body.len() - 1, body.len()) }
                    else { format!("HTTP/1.1 200 OK\r\nContent-Length: {}\r\n\r\n", part.len()) };
                stream.write_all(head.as_bytes()).unwrap();
                let send = if n == 0 { &part[..part.len() / 2] } else { part };
                let _ = stream.write_all(send);
            }
        });
        (url, handle)
    }

    #[test]
    fn a_title_is_fetched_whole_and_carries_on_after_a_lost_connection() {
        let root = std::env::temp_dir().join(format!("portico-downloads-test-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        let body: Vec<u8> = (0..3_000_000u32).map(|n| (n % 251) as u8).collect();
        let (url, server) = server(body.clone());
        let downloads = Downloads::open(root.clone());
        downloads.fetch(ask(&url, body.len() as u64)).unwrap();
        assert_eq!(wait_for(&downloads, "failed").files[0].done, 1_500_000, "the first answer stopped halfway");
        downloads.fetch(ask(&url, body.len() as u64)).unwrap();
        let ready = wait_for(&downloads, "ready");
        assert_eq!(ready.files[0].done, body.len() as u64);
        server.join().unwrap();
        assert!(downloads.files(KEY).is_err(), "no offline permission yet");
        downloads.permission("srv_1", "prf_1", now_ms() + 60_000).unwrap();
        let files = downloads.files(KEY).unwrap();
        assert_eq!(std::fs::read(&files[0]).unwrap(), body);
        // What was saved comes back after a restart.
        assert_eq!(Downloads::open(root.clone()).list("srv_1", "prf_1").unwrap().entries[0].state, "ready");
        downloads.remove(KEY).unwrap();
        assert!(downloads.list("srv_1", "prf_1").unwrap().entries.is_empty() && !files[0].exists());
        std::fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn play_starts_where_it_was_downloaded_then_where_the_last_play_here_left_it() {
        let root = std::env::temp_dir().join(format!("portico-downloads-resume-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        let downloads = Downloads::open(root.clone());
        // Nothing listens on port 9: the fetch fails, the entry stays.
        downloads.fetch(Ask { position_ms: 600_000, ..ask("http://127.0.0.1:9/v1/media/g/file/1", 10) }).unwrap();
        wait_for(&downloads, "failed");
        assert_eq!(downloads.position(KEY).unwrap(), 600_000, "the person's place when it was downloaded");
        let play = |position_ms| Play { server_id: "srv_1".into(), profile_id: "prf_1".into(), item_id: "itm_1".into(), play_id: "p".into(),
            position_ms, played_ms: 1000, made_at_ms: now_ms() };
        downloads.record_play(play(900_000), false).unwrap();
        assert_eq!(Downloads::open(root.clone()).position(KEY).unwrap(), 900_000, "kept across a restart");
        downloads.sent(&["p".into()]).unwrap();
        assert_eq!(downloads.position(KEY).unwrap(), 900_000, "still there once the server has the play");
        downloads.record_play(play(5_400_000), true).unwrap();
        assert_eq!(downloads.position(KEY).unwrap(), 0, "played to the end starts again");
        std::fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn ids_and_addresses_are_checked_before_anything_is_written() {
        let downloads = Downloads::open(std::env::temp_dir().join("portico-downloads-unused"));
        let mut bad = ask("file:///etc/passwd", 1);
        assert!(downloads.fetch(bad).is_err());
        bad = ask("https://server/v1/media/g/file/1", 1);
        bad.download_id = "../x".into();
        assert!(downloads.fetch(bad).is_err());
    }
}

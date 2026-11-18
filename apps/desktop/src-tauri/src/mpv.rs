//! mpv in its own window, for the presentations the server plans for mpv (playback rule 15 as
//! ruled 18 Nov: only where it saves converting or a burn). mpv draws HDR and passes surround
//! through itself, which a picture inside the page cannot. The app starts mpv when first needed,
//! idle, and drives it over mpv's JSON IPC, one request at a time.
use serde::Serialize;
use serde_json::{Value, json};
use std::io::{BufRead, BufReader, Write};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::time::{Duration, Instant};

#[cfg(unix)]
type Pipe = std::os::unix::net::UnixStream;
#[cfg(windows)]
type Pipe = std::fs::File;

struct Running {
    child: Child,
    writer: Pipe,
    reader: BufReader<Pipe>,
    next: u64,
    folder: Option<std::path::PathBuf>,
}
impl Drop for Running {
    fn drop(&mut self) {
        if let Some(folder) = &self.folder {
            let _ = std::fs::remove_dir_all(folder);
        }
    }
}

/// The one mpv this app drives, started on first use.
#[derive(Default)]
pub struct Mpv(Mutex<Option<Running>>);

/// What the player screen needs to follow mpv.
#[derive(Serialize, Default, Debug, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Reading {
    /// `idle` (nothing loaded or the window was closed), `loading`, `playing`, `paused`,
    /// `buffering` or `ended`.
    pub state: &'static str,
    pub position_ms: f64,
    pub duration_ms: f64,
    pub rate: f64,
    pub volume: f64,
    pub muted: bool,
}

/// Only the server's own http(s) media addresses go to mpv: never a file, a playlist protocol or
/// an option.
pub fn media_url(url: &str) -> Result<&str, String> {
    let parsed = tauri::Url::parse(url).map_err(|_| "invalid_source")?;
    if !matches!(parsed.scheme(), "http" | "https") || parsed.host_str().is_none() {
        return Err("invalid_source".into());
    }
    Ok(url)
}

/// mpv as shipped beside the app (`mpv/` next to the executable), else the one on the PATH.
fn binary() -> std::path::PathBuf {
    let name = if cfg!(windows) { "mpv.exe" } else { "mpv" };
    std::env::current_exe().ok().and_then(|exe| exe.parent().map(|dir| dir.join("mpv").join(name))).filter(|path| path.exists())
        .unwrap_or_else(|| name.into())
}

/// Whether mpv runs here. The capability document names mpv only when it does, so a computer
/// without mpv is never sent a file only mpv could play.
pub fn available() -> bool {
    static FOUND: std::sync::OnceLock<bool> = std::sync::OnceLock::new();
    *FOUND.get_or_init(|| Command::new(binary()).arg("--version").stdin(Stdio::null()).stdout(Stdio::null()).stderr(Stdio::null())
        .status().is_ok_and(|status| status.success()))
}

/// 96 random bits from the standard library's randomly keyed hasher.
fn random_name() -> String {
    use std::hash::{BuildHasher, Hasher};
    let part = || std::collections::hash_map::RandomState::new().build_hasher().finish();
    format!("{:016x}{:08x}", part(), part() as u32)
}

/// Where mpv listens: a socket in a folder only this account can open (the session's runtime
/// folder where there is one), or on Windows a pipe. Its name is random, so another account can't
/// claim it first, and mpv answers a client only what that client asks for.
fn ipc_address() -> Result<(String, Option<std::path::PathBuf>), String> {
    if cfg!(windows) {
        return Ok((format!(r"\\.\pipe\portico-mpv-{}", random_name()), None));
    }
    let base = std::env::var_os("XDG_RUNTIME_DIR").map(std::path::PathBuf::from).filter(|dir| dir.is_dir()).unwrap_or_else(std::env::temp_dir);
    let folder = base.join(format!("portico-{}", random_name()));
    let mut builder = std::fs::DirBuilder::new();
    #[cfg(unix)]
    std::os::unix::fs::DirBuilderExt::mode(&mut builder, 0o700);
    builder.create(&folder).map_err(|_| "mpv_unavailable")?;
    Ok((folder.join("mpv.sock").to_string_lossy().into_owned(), Some(folder)))
}

fn connect(address: &str) -> std::io::Result<(Pipe, Pipe)> {
    #[cfg(unix)]
    let pipe = Pipe::connect(address)?;
    #[cfg(windows)]
    let pipe = std::fs::OpenOptions::new().read(true).write(true).open(address)?;
    Ok((pipe.try_clone()?, pipe))
}

impl Mpv {
    fn start(extra: &[&str]) -> Result<Running, String> {
        let (address, folder) = ipc_address()?;
        let mut child = Command::new(binary())
            .args(["--idle=yes", "--force-window=no", "--keep-open=yes", "--no-config", "--no-terminal", "--ytdl=no", "--title=Portico",
                "--fullscreen", "--vo=gpu-next,gpu", "--hwdec=auto-safe", "--target-colorspace-hint=yes"])
            .args(extra)
            .arg(format!("--input-ipc-server={address}"))
            .stdin(Stdio::null()).stdout(Stdio::null()).stderr(Stdio::null())
            .spawn().map_err(|_| {
                if let Some(folder) = &folder { let _ = std::fs::remove_dir_all(folder); }
                "mpv_unavailable"
            })?;
        let started = Instant::now();
        loop {
            if let Ok((writer, reader)) = connect(&address) {
                return Ok(Running { child, writer, reader: BufReader::new(reader), next: 0, folder });
            }
            if started.elapsed() > Duration::from_secs(10) || child.try_wait().ok().flatten().is_some() {
                let _ = child.kill();
                if let Some(folder) = &folder { let _ = std::fs::remove_dir_all(folder); }
                return Err("mpv_unavailable".into());
            }
            std::thread::sleep(Duration::from_millis(50));
        }
    }

    /// One command and its answer. Events mpv sends meanwhile are skipped: the screen reads state.
    fn request(running: &mut Running, command: Value) -> Result<Value, String> {
        running.next += 1;
        let id = running.next;
        let line = json!({"command": command, "request_id": id}).to_string() + "\n";
        running.writer.write_all(line.as_bytes()).map_err(|_| "mpv_closed")?;
        let mut answer = String::new();
        loop {
            answer.clear();
            if running.reader.read_line(&mut answer).map_err(|_| "mpv_closed")? == 0 {
                return Err("mpv_closed".into());
            }
            let Ok(value) = serde_json::from_str::<Value>(&answer) else { continue };
            if value.get("request_id").and_then(Value::as_u64) != Some(id) {
                continue;
            }
            return match value.get("error").and_then(Value::as_str) {
                Some("success") => Ok(value.get("data").cloned().unwrap_or(Value::Null)),
                _ => Err("mpv_refused".into()),
            };
        }
    }

    fn with<T>(&self, start: bool, extra: &[&str], run: impl FnOnce(&mut Running) -> Result<T, String>) -> Result<T, String> {
        let mut held = self.0.lock().map_err(|_| "mpv_unavailable")?;
        // The person may have closed mpv's window, which ends mpv: start a new one when asked to play.
        if held.as_mut().is_some_and(|running| running.child.try_wait().ok().flatten().is_some()) {
            *held = None;
        }
        if held.is_none() {
            if !start {
                return Err("mpv_closed".into());
            }
            *held = Some(Self::start(extra)?);
        }
        let result = run(held.as_mut().expect("started above"));
        if result.as_ref().err().is_some_and(|error| error == "mpv_closed") {
            *held = None;
        }
        result
    }

    /// Opens the presentation at `start_ms`, playing or paused.
    pub fn load(&self, url: &str, start_ms: f64, autoplay: bool, extra: &[&str]) -> Result<(), String> {
        let url = media_url(url)?;
        self.with(true, extra, |running| {
            // `start` and `pause` are set first: loadfile's own options moved between mpv versions.
            Self::request(running, json!(["set_property", "start", format!("{:.3}", start_ms.max(0.0) / 1000.0)]))?;
            Self::request(running, json!(["set_property", "pause", !autoplay]))?;
            Self::request(running, json!(["loadfile", url, "replace"]))?;
            Ok(())
        })
    }

    /// play, pause, seek (milliseconds), rate, volume (0 to 100), mute, stop.
    pub fn command(&self, action: &str, value: f64) -> Result<(), String> {
        let command = match action {
            "play" => json!(["set_property", "pause", false]),
            "pause" => json!(["set_property", "pause", true]),
            "seek" => json!(["seek", value.max(0.0) / 1000.0, "absolute+exact"]),
            "rate" => json!(["set_property", "speed", value]),
            "volume" => json!(["set_property", "volume", value.clamp(0.0, 100.0)]),
            "mute" => json!(["set_property", "mute", value != 0.0]),
            "stop" => json!(["stop"]),
            _ => return Err("invalid_command".into()),
        };
        match self.with(false, &[], |running| Self::request(running, command).map(|_| ())) {
            // Pausing or stopping a closed window is already done.
            Err(error) if error == "mpv_closed" && matches!(action, "pause" | "stop") => Ok(()),
            other => other,
        }
    }

    pub fn subtitle(&self, url: Option<&str>) -> Result<(), String> {
        self.with(false, &[], |running| match url {
            Some(url) => Self::request(running, json!(["sub-add", media_url(url)?, "select"])).map(|_| ()),
            None => Self::request(running, json!(["set_property", "sid", "no"])).map(|_| ()),
        })
    }

    /// Shows the file's own audio track and subtitle the presentation names, by their stream
    /// number in the file (the server's id for them): `None` leaves mpv's choice, a subtitle of
    /// -1 turns subtitles off.
    pub fn tracks(&self, audio: Option<i64>, subtitle: Option<i64>) -> Result<(), String> {
        self.with(false, &[], |running| {
            let list = Self::request(running, json!(["get_property", "track-list"]))?;
            if let Some(stream) = audio {
                let id = track(&list, "audio", stream).ok_or("track_unavailable")?;
                Self::request(running, json!(["set_property", "aid", id]))?;
            }
            match subtitle {
                Some(-1) => { Self::request(running, json!(["set_property", "sid", "no"]))?; }
                Some(stream) => { let id = track(&list, "sub", stream).ok_or("track_unavailable")?; Self::request(running, json!(["set_property", "sid", id]))?; }
                None => {}
            }
            Ok(())
        })
    }

    pub fn observe(&self) -> Reading {
        let read = |running: &mut Running, name: &str| Self::request(running, json!(["get_property", name])).ok();
        self.with(false, &[], |running| {
            let flag = |value: Option<Value>| value.and_then(|v| v.as_bool()).unwrap_or(false);
            let number = |value: Option<Value>| value.and_then(|v| v.as_f64());
            let idle = flag(read(running, "idle-active"));
            let position = number(read(running, "time-pos"));
            let reading = Reading {
                state: if idle { "idle" } else if flag(read(running, "eof-reached")) { "ended" } else if position.is_none() { "loading" }
                    else if flag(read(running, "paused-for-cache")) { "buffering" } else if flag(read(running, "pause")) { "paused" } else { "playing" },
                position_ms: position.unwrap_or(0.0) * 1000.0,
                duration_ms: number(read(running, "duration")).unwrap_or(0.0) * 1000.0,
                rate: number(read(running, "speed")).unwrap_or(1.0),
                volume: number(read(running, "volume")).unwrap_or(100.0),
                muted: flag(read(running, "mute")),
            };
            Ok(reading)
        }).unwrap_or(Reading { state: "idle", rate: 1.0, volume: 100.0, ..Reading::default() })
    }

    /// Ends mpv with the app.
    pub fn quit(&self) {
        if let Ok(mut held) = self.0.lock() {
            if let Some(mut running) = held.take() {
                let _ = Self::request(&mut running, json!(["quit"]));
                let _ = running.child.wait();
            }
        }
    }
}

/// mpv's id for the track of `kind` that is stream `stream` in the file (`ff-index`).
fn track(list: &Value, kind: &str, stream: i64) -> Option<i64> {
    list.as_array()?.iter().find(|t| t["type"] == kind && t["ff-index"] == stream && t["external"] != true)?.get("id")?.as_i64()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_track_is_found_by_its_stream_number_in_the_file() {
        let list = json!([{"id": 1, "type": "video", "ff-index": 0}, {"id": 1, "type": "audio", "ff-index": 1}, {"id": 2, "type": "audio", "ff-index": 2},
            {"id": 1, "type": "sub", "ff-index": 3}, {"id": 2, "type": "sub", "ff-index": 4}, {"id": 3, "type": "sub", "ff-index": 0, "external": true}]);
        assert_eq!(track(&list, "audio", 2), Some(2));
        assert_eq!(track(&list, "sub", 4), Some(2));
        assert_eq!(track(&list, "sub", 0), None, "a subtitle added beside the file is not a stream of it");
        assert_eq!(track(&list, "audio", 9), None);
    }

    #[cfg(unix)]
    #[test]
    fn mpvs_socket_is_in_a_private_folder_with_a_name_nobody_can_guess() {
        use std::os::unix::fs::PermissionsExt;
        let (first, folder) = ipc_address().unwrap();
        let (second, other) = ipc_address().unwrap();
        let folder = folder.unwrap();
        assert_ne!(first, second);
        assert_eq!(std::fs::metadata(&folder).unwrap().permissions().mode() & 0o777, 0o700);
        assert!(first.len() < 104, "{first}");
        std::fs::remove_dir_all(folder).unwrap();
        std::fs::remove_dir_all(other.unwrap()).unwrap();
    }

    #[test]
    fn only_http_media_addresses_reach_mpv() {
        assert!(media_url("https://server.example/v1/media/g/file/1").is_ok());
        assert!(media_url("http://192.168.1.5:32500/v1/media/g/master.m3u8").is_ok());
        for refused in ["file:///etc/passwd", "https://", "--script=x", "edl://a", "lavfi://sine", "ytdl://x"] {
            assert!(media_url(refused).is_err(), "{refused}");
        }
    }

    /// Plays a real file through a real mpv over the IPC, with no window or sound. Runs where a
    /// test file and mpv are given (CI), as `PORTICO_TEST_MEDIA_URL`.
    #[test]
    fn mpv_plays_pauses_seeks_and_ends() {
        let Some(url) = std::env::var("PORTICO_TEST_MEDIA_URL").ok() else { return };
        let mpv = Mpv::default();
        let quiet = ["--vo=null", "--ao=null", "--no-fullscreen"];
        mpv.load(&url, 2000.0, true, &quiet).expect("load");
        let wait = |want: &str| {
            let started = Instant::now();
            loop {
                let reading = mpv.observe();
                if reading.state == want { return reading; }
                assert!(started.elapsed() < Duration::from_secs(20), "waiting for {want}, last {reading:?}");
                std::thread::sleep(Duration::from_millis(100));
            }
        };
        let playing = wait("playing");
        assert!(playing.position_ms >= 1900.0 && playing.duration_ms > 5000.0, "{playing:?}");
        mpv.command("pause", 0.0).unwrap();
        wait("paused");
        mpv.command("seek", playing.duration_ms - 500.0).unwrap();
        mpv.command("play", 0.0).unwrap();
        wait("ended");
        mpv.quit();
        assert_eq!(mpv.observe().state, "idle");
    }
}

use serde::{Deserialize, Serialize};
use url::Url;

/// Fixed errors only. libmpv's diagnostic strings may contain capability URLs and paths.
#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Error {
    ForbiddenOrigin,
    InvalidSource,
    InvalidCommand,
    StaleGeneration,
    EngineUnavailable,
    EngineFailure,
    PreferencesUnavailable,
    PreferencesConflict,
    WindowUnavailable,
    DownloadsUnavailable,
    DownloadsConflict,
    DownloadNotReady,
    DownloadVerificationFailed,
    InvalidReceipt,
    StorageFull,
    Unsupported,
    DiscoveryUnavailable,
}
pub type Result<T> = std::result::Result<T, Error>;

pub fn local_origin(label: &str, value: &str, development: bool) -> Result<()> {
    let u = Url::parse(value).map_err(|_| Error::ForbiddenOrigin)?;
    let production = u.port().is_none() && ((u.scheme() == "tauri" && u.host_str() == Some("localhost"))
        || (u.scheme() == "http" && u.host_str() == Some("tauri.localhost")));
    let dev = development && u.scheme() == "http" && u.host_str() == Some("127.0.0.1") && u.port() == Some(14229);
    if label != "main" || !u.username().is_empty() || u.password().is_some() || !(production || dev) {
        return Err(Error::ForbiddenOrigin);
    }
    Ok(())
}

/// Only progressive, server-issued capabilities enter libmpv. HLS is deliberately rejected until a
/// same-origin segment broker exists: a hostile playlist otherwise opens arbitrary network URLs.
/// No file, disc, pipe, lavfi, edl, script or arbitrary FFmpeg protocol is an IPC input.
pub fn media_url(value: &str) -> Result<Url> {
    if value.len() > 4096 || value.bytes().any(|c| c < 0x20 || c == 0x7f) { return Err(Error::InvalidSource); }
    let u = Url::parse(value).map_err(|_| Error::InvalidSource)?;
    if !matches!(u.scheme(), "http" | "https") || u.host_str().is_none() || !u.username().is_empty()
        || u.password().is_some() || u.fragment().is_some() || !u.path().starts_with("/v1/")
        || [".m3u8",".m3u",".pls"].iter().any(|suffix|u.path().to_ascii_lowercase().ends_with(suffix))
        || value.contains('\\') || value.to_ascii_lowercase().contains("%2e") || value.to_ascii_lowercase().contains("%2f") {
        return Err(Error::InvalidSource);
    }
    Ok(u)
}

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Source {
    pub url: String,
    pub generation: u64,
    pub mode: String,
    pub start_position_ms: f64,
    pub autoplay: bool,
}
impl Source {
    pub fn validate(&self) -> Result<()> {
        media_url(&self.url)?;
        if self.generation == 0 || self.generation > 9_007_199_254_740_991 || !matches!(self.mode.as_str(),"direct"|"stream")
            || !self.start_position_ms.is_finite() || !(0.0..=1e12).contains(&self.start_position_ms) {
            return Err(Error::InvalidSource);
        }
        Ok(())
    }
}

#[derive(Deserialize)]
#[serde(tag = "action", rename_all = "camelCase", deny_unknown_fields)]
pub enum Command {
    Load { source: Source },
    Play { generation: u64 },
    Pause { generation: u64 },
    Seek { generation: u64, milliseconds: f64 },
    Rate { generation: u64, rate: f64 },
    FrameStep { generation:u64, direction:i8 },
    Processing { generation:u64, gain:f64 },
    Volume { generation: u64, volume: f64, muted: bool },
    Stop { generation: u64 },
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Observation {
    #[serde(default)]
    pub observed_playback: RendererOutput,
    pub generation: u64,
    pub state: String,
    pub position_ms: f64,
    pub rate: f64,
    pub volume: f64,
    pub muted: bool,
    pub dropped_frames: u64,
    pub native_surface_active: bool,
    pub eq_applied:bool,
    pub leveling_applied:bool,
}

/// Exact subset of canonical playback-v1/observed-playback.ts, measured at the
/// engine's PCM output. No physical-device rate, codec, bit depth, exclusive or
/// bit-perfect claim can be inferred from these parameters.
#[derive(Debug,Clone,Default,Serialize,Deserialize)]
pub struct RendererOutput {
    #[serde(skip_serializing_if="Option::is_none")]
    audio:Option<PcmOutput>,
}
#[derive(Debug,Clone,Serialize,Deserialize)]
#[serde(rename_all="camelCase")]
struct PcmOutput {
    handling:String,
    #[serde(skip_serializing_if="Option::is_none")]
    sample_rate:Option<u32>,
    #[serde(skip_serializing_if="Option::is_none")]
    channels:Option<u8>,
}
impl RendererOutput {
    pub fn pcm(format:Option<&str>,rate:Option<f64>,channels:Option<f64>)->Self {
        // Encoded SPDIF uses carrier parameters; do not report those as the
        // song's PCM output. Unknown formats remain absent too.
        if !format.is_some_and(|v|matches!(v,"u8"|"u8p"|"s16"|"s16p"|"s24"|"s24p"|"s32"|"s32p"|"float"|"floatp"|"double"|"doublep")){return Self::default()}
        let positive=|value:Option<f64>,max:f64|value.filter(|v|v.is_finite()&&v.fract()==0.0&&*v>0.0&&*v<=max);
        let sample_rate=positive(rate,1_536_000.0).map(|v|v as u32);
        let channels=positive(channels,64.0).map(|v|v as u8);
        if sample_rate.is_none()&&channels.is_none(){return Self::default()}
        Self{audio:Some(PcmOutput{handling:"pcm".into(),sample_rate,channels})}
    }
}

#[cfg(test)]
mod renderer_output_tests {
    use super::*;
    #[test]
    fn only_measured_bounded_pcm_scalars_are_projected_without_source_or_physical_guesses(){
        let value=serde_json::to_value(RendererOutput::pcm(Some("float"),Some(96000.0),Some(2.0))).unwrap();
        assert_eq!(value,serde_json::json!({"audio":{"handling":"pcm","sampleRate":96000,"channels":2}}));
        for format in [None,Some("spdif-ac3"),Some("spdif-truehd"),Some("unknown")]{
            assert_eq!(serde_json::to_value(RendererOutput::pcm(format,Some(48000.0),Some(2.0))).unwrap(),serde_json::json!({}));
        }
        for value in [f64::NAN,f64::INFINITY,-1.0,0.0,48000.5,1_536_001.0]{
            assert_eq!(serde_json::to_value(RendererOutput::pcm(Some("s32"),Some(value),None)).unwrap(),serde_json::json!({}));
        }
    }
}

#[derive(Clone,Copy,Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SurfaceBounds{pub x:f64,pub y:f64,pub width:f64,pub height:f64}
impl SurfaceBounds{pub fn validate(&self)->Result<()>{
    if [self.x,self.y,self.width,self.height].iter().any(|x|!x.is_finite())||self.x.abs()>32768.0||self.y.abs()>32768.0||!(0.0..=32768.0).contains(&self.width)||!(0.0..=32768.0).contains(&self.height){return Err(Error::InvalidCommand);}Ok(())
}}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn only_packaged_main_or_exact_development_origin() {
        for v in ["tauri://localhost/player", "http://tauri.localhost/player"] { assert!(local_origin("main", v, false).is_ok()); }
        for v in ["https://evil.test/", "tauri://evil/", "http://tauri.localhost:32500/", "tauri://localhost:14229/", "http://127.0.0.1:14229/", "http://localhost:14229/", "file:///tmp/a"] { assert_eq!(local_origin("main", v, false), Err(Error::ForbiddenOrigin)); }
        assert!(local_origin("main", "http://127.0.0.1:14229/player", true).is_ok());
        assert!(local_origin("popup", "tauri://localhost/", false).is_err());
    }
    #[test]
    fn deny_local_protocols_playlists_traversal_credentials_and_options() {
        assert!(media_url("https://server.test/v1/media/abc?grant=opaque").is_ok());
        for v in ["file:///etc/passwd", "edl://a", "https://u:p@server/v1/a", "https://server/v1/a#x", "https://server/v1/%2e%2e/private", "https://server/v1/a.m3u8", "https://server/private", "--script=/tmp/a"] { assert!(media_url(v).is_err(), "{v}"); }
        assert!(serde_json::from_str::<Command>(r#"{"action":"command","args":["run","sh"]}"#).is_err());
        assert!(serde_json::from_str::<Command>(r#"{"action":"play","generation":1,"path":"/tmp/a"}"#).is_err());
        let mut source=Source{url:"https://server.test/v1/media/opaque".into(),generation:1,mode:"stream".into(),start_position_ms:0.0,autoplay:true};
        assert!(source.validate().is_ok());source.mode="playlist".into();assert_eq!(source.validate(),Err(Error::InvalidSource));
    }
}

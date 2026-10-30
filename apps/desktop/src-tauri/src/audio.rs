use crate::boundary::{Error, Result};
use serde::{Deserialize, Serialize};

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AudioOutput {
    pub exclusive: bool,
    pub match_sample_rate: bool,
    pub device: String,
    pub passthrough: Vec<String>,
}
impl Default for AudioOutput {
    fn default() -> Self { Self { exclusive: false, match_sample_rate: true, device: "auto".into(), passthrough: vec![] } }
}
impl AudioOutput {
    pub fn options(&self, platform: &str) -> Result<Vec<(&'static str, String)>> {
        // Off needs the actual OS/device's current rate. No provider exists on
        // this boundary yet; a guessed 48 kHz must never masquerade as that rate.
        if !self.match_sample_rate{return Err(Error::Unsupported)}
        let formats: &[&str] = match platform {
            "macos" => &["ac3", "dts"],
            "windows" => &["ac3", "eac3", "dts", "dts-hd", "truehd"],
            "linux" => &["ac3", "eac3", "dts", "dts-hd", "truehd"],
            _ => return Err(Error::Unsupported),
        };
        if self.device.len() > 256 || self.device.contains(['\0', '\n', '\r'])
            || self.passthrough.iter().any(|x| !formats.contains(&x.as_str())) { return Err(Error::InvalidCommand); }
        if platform == "linux" && self.exclusive && !self.device.starts_with("alsa/hw:") { return Err(Error::InvalidCommand); }
        Ok(vec![("audio-device", self.device.clone()), ("audio-exclusive", if self.exclusive { "yes" } else { "no" }.into()),
            ("audio-samplerate", "0".into()),
            ("audio-spdif", self.passthrough.join(",")), ("gapless-audio", "yes".into()),
            ("replaygain", "no".into()), ("volume", "100".into()), ("speed", "1".into())])
    }
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AudioFacts {
    pub generation:u64,
    pub requested_exclusive: bool,
    pub source_rate: Option<u64>,
    pub output_rate: Option<u64>,
    pub output_channels: Option<u64>,
    pub output_format: Option<String>,
    /// No inferred bit-perfect flag: only an output capture can prove it.
    pub bit_exact_capture_verified: bool,
    pub equalizer_enabled: bool,
    pub eq_applied: bool,
    /// True only for the actual current PCM route and observed labelled filter.
    pub leveling_applied: bool,
    /// The signal-path UI must use the approved i18n text "EQ on, not bit-perfect".
    /// This fact cannot be promoted to a physical output/capture claim.
    pub bit_perfect_excluded_by_equalizer: bool,
}

/// Compare interleaved decoded PCM, preserving depth/rate/channels and exact frame count.
/// A digest equality alone is insufficient when the formats differ.
pub fn compare_pcm(reference: &[u8], captured: &[u8], bytes_per_sample: usize, channels: usize) -> Result<usize> {
    let frame = bytes_per_sample.checked_mul(channels).filter(|x| *x > 0 && *x <= 256).ok_or(Error::InvalidCommand)?;
    if reference.is_empty() || reference.len() % frame != 0 || reference != captured { return Err(Error::EngineFailure); }
    Ok(reference.len() / frame)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn exclusive_linux_requires_unmixed_hardware_route_and_variant_allowlist() {
        let mut x=AudioOutput {exclusive:true,..Default::default()};
        assert!(x.options("linux").is_err());
        x.device="alsa/hw:0,0".into(); assert!(x.options("linux").is_ok());
        x.device="alsa/hw:CARD=USB".into(); assert!(x.options("linux").is_ok());
        x.passthrough=vec!["truehd".into()]; assert!(x.options("macos").is_err());
    }
    #[test]
    fn catches_one_sample_changes_missing_frames_and_partial_frames() {
        let source=[1,2,3,4,5,6];
        assert_eq!(compare_pcm(&source,&source,3,2).unwrap(),1);
        assert!(compare_pcm(&source,&[1,2,3,4,5,7],3,2).is_err());
        assert!(compare_pcm(&source,&source[..3],3,2).is_err());
        assert!(compare_pcm(&source,&source,4,2).is_err());
    }
    #[test]
    fn disabling_rate_matching_requires_real_device_rate_instead_of_a_48khz_guess(){
        let output=AudioOutput{match_sample_rate:false,..Default::default()};
        for platform in ["linux","macos","windows"]{assert_eq!(output.options(platform),Err(Error::Unsupported));}
        assert!(AudioOutput::default().options("linux").unwrap().iter().any(|(key,value)|*key=="audio-samplerate"&&value=="0"));
    }
}

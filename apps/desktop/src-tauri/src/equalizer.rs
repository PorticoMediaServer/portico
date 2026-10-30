use crate::boundary::{Error, Result};
use serde::{Deserialize, Serialize};

/// Engine centres for the octave bands labelled 31, 62, 125, 250, 500, 1k..16k.
pub const BAND_FREQUENCIES_HZ: [f64; 10] = [31.25, 62.5, 125.0, 250.0, 500.0, 1000.0, 2000.0, 4000.0, 8000.0, 16000.0];
pub const FILTER_LABEL: &str = "portico-equalizer";

/// One setting for this app installation, independent of content, account and output.
/// There is deliberately no output key, preset, profile selector or migration shape.
#[derive(Clone, Debug, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct EqualizerSettings {
    pub enabled: bool,
    pub preamp_db: f64,
    pub bands_db: [f64; 10],
}
impl Default for EqualizerSettings {
    fn default() -> Self { Self { enabled: false, preamp_db: 0.0, bands_db: [0.0; 10] } }
}
impl EqualizerSettings {
    pub fn validate(&self) -> Result<()> {
        if std::iter::once(&self.preamp_db).chain(self.bands_db.iter())
            .any(|value| !value.is_finite() || !(-12.0..=12.0).contains(value) || (value * 2.0).fract() != 0.0) {
            return Err(Error::InvalidCommand);
        }
        Ok(())
    }
    /// Reset levels; the separate on/off switch keeps its state.
    pub fn flat(&self) -> Self { Self { enabled: self.enabled, ..Self::default() } }

    /// Fixed libavfilter graph built exclusively from bounded numbers, never raw web commands.
    /// Use a labelled mpv filter so native trim/loudness filters remain independent.
    pub fn filter(&self) -> Result<Option<String>> {
        self.validate()?;
        if !self.enabled { return Ok(None); }
        let mut filters = vec![format!("volume=volume={}dB:precision=double", self.preamp_db)];
        for (frequency, gain) in BAND_FREQUENCIES_HZ.iter().zip(self.bands_db) {
            filters.push(format!("equalizer=frequency={frequency}:width_type=o:width=1:gain={gain}:precision=f64"));
        }
        Ok(Some(format!("@{FILTER_LABEL}:lavfi=[{}]", filters.join(","))))
    }
}

#[derive(Clone, Debug, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct SavedEqualizer {
    pub revision: u64,
    pub settings: EqualizerSettings,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EqualizerSnapshot {
    pub revision: u64,
    pub settings: EqualizerSettings,
    pub applied: bool,
}

#[derive(Deserialize)]
#[serde(tag = "action", rename_all = "camelCase", deny_unknown_fields)]
pub enum EqualizerChange {
    Set { #[serde(rename = "expectedRevision")] expected_revision: u64, settings: EqualizerSettings },
    ResetFlat { #[serde(rename = "expectedRevision")] expected_revision: u64 },
}
impl EqualizerChange {
    pub fn expected_revision(&self) -> u64 {
        match self { Self::Set { expected_revision, .. } | Self::ResetFlat { expected_revision } => *expected_revision }
    }
    pub fn target(&self, current: &EqualizerSettings) -> Result<EqualizerSettings> {
        let target = match self { Self::Set { settings, .. } => settings.clone(), Self::ResetFlat { .. } => current.flat() };
        target.validate()?;
        Ok(target)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn off_is_exact_filter_bypass_and_reset_keeps_the_switch() {
        let settings = EqualizerSettings { preamp_db: -4.0, bands_db: [3.0; 10], ..Default::default() };
        assert_eq!(settings.filter().unwrap(), None);
        let on = EqualizerSettings { enabled: true, ..settings };
        assert_eq!(on.flat(), EqualizerSettings { enabled: true, ..Default::default() });
    }

    #[test]
    fn rejects_output_preset_account_shapes_and_non_finite_values() {
        for field in ["outputId", "deviceId", "preset", "accountId"] {
            let payload = format!(r#"{{"enabled":false,"preampDb":0,"bandsDb":[0,0,0,0,0,0,0,0,0,0],"{field}":"x"}}"#);
            assert!(serde_json::from_str::<EqualizerSettings>(&payload).is_err());
        }
        assert!(serde_json::from_str::<EqualizerSettings>(r#"{"enabled":true,"preampDb":0,"bandsDb":[0]}"#).is_err());
        assert!(EqualizerSettings { preamp_db: f64::NAN, ..Default::default() }.validate().is_err());
        assert!(EqualizerSettings { bands_db: [25.0; 10], ..Default::default() }.validate().is_err());
        assert!(EqualizerSettings { preamp_db: 0.25, ..Default::default() }.validate().is_err());
        assert!(EqualizerSettings { preamp_db: -12.0, bands_db: [12.0; 10], ..Default::default() }.validate().is_ok());
    }
}

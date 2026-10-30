// SPDX-License-Identifier: GPL-3.0-only
// Procedural metadata, not a copy of any licensed picture or RPU.
use dolby_vision::rpu::{
    ConversionMode, dovi_rpu::DoviRpu, generate::GenerateConfig, rpu_data_nlq::DoviELType,
};
use std::{error::Error, fs, path::PathBuf};

fn main() -> Result<(), Box<dyn Error>> {
    let output = PathBuf::from(std::env::args().nth(1).ok_or("output directory required")?);
    fs::create_dir_all(&output)?;
    let mut inventory = Vec::new();
    for cm in ["V29", "V40"] {
        for profile in ["5", "8.1", "8.4", "7-MEL", "7-FEL"] {
            let generated = if profile.starts_with('7') {
                "8.1"
            } else {
                profile
            };
            let recipe = serde_json::json!({
                "cm_version": cm, "profile": generated, "length": 24,
                "level6": null, "shots": [{"start": 0, "duration": 24}]
            });
            let config: GenerateConfig = serde_json::from_value(recipe.clone())?;
            let rpus = config.generate_rpu_list()?;
            let mut bytes = Vec::new();
            for mut rpu in rpus {
                if profile.starts_with('7') {
                    rpu.convert_with_mode(ConversionMode::ToMel)?;
                    if profile == "7-FEL" {
                        // A nonzero residual slope is valid FEL metadata. This
                        // is a parser/BSF fixture, not mastered FEL imagery.
                        rpu.rpu_data_mapping
                            .as_mut()
                            .ok_or("mapping absent")?
                            .nlq
                            .as_mut()
                            .ok_or("NLQ absent")?
                            .linear_deadzone_slope_int
                            .fill(1);
                        rpu.modified = true;
                    }
                }
                let nal = rpu.write_hevc_unspec62_nalu()?;
                // Reparse serialized bytes, including CRC, rather than trusting
                // the in-memory profile changed by the recipe.
                let decoded = DoviRpu::parse_unspec62_nalu(&nal)?;
                let dm = decoded.vdr_dm_data.as_ref().ok_or("CM metadata absent")?;
                if dm.cmv29_metadata.is_none() || dm.cmv40_metadata.is_some() != (cm == "V40") {
                    return Err(format!("{profile}/{cm}: serialized CM metadata mismatch").into());
                }
                let mut corrupt = nal.clone();
                let at = corrupt.len() - 3;
                corrupt[at] ^= 1;
                if DoviRpu::parse_unspec62_nalu(&corrupt).is_ok() {
                    return Err("corrupt RPU CRC accepted".into());
                }
                let expected_profile = if profile == "5" {
                    5
                } else if profile.starts_with('7') {
                    7
                } else {
                    8
                };
                if decoded.dovi_profile != expected_profile {
                    return Err(
                        format!("{profile}/{cm}: parsed profile {}", decoded.dovi_profile).into(),
                    );
                }
                if profile == "7-MEL" && decoded.el_type != Some(DoviELType::MEL)
                    || profile == "7-FEL" && decoded.el_type != Some(DoviELType::FEL)
                {
                    return Err(format!("{profile}/{cm}: enhancement type mismatch").into());
                }
                if profile.starts_with('7') {
                    let mut converted = decoded.clone();
                    converted.convert_with_mode(ConversionMode::To81)?;
                    let converted =
                        DoviRpu::parse_unspec62_nalu(&converted.write_hevc_unspec62_nalu()?)?;
                    if converted.dovi_profile != 8
                        || converted.el_type.is_some()
                        || !converted.header.disable_residual_flag
                    {
                        return Err("profile 8.1 conversion retained residual dependency".into());
                    }
                }
                bytes.extend_from_slice(&[0, 0, 0, 1]);
                bytes.extend_from_slice(&nal);
            }
            let name = format!("dv-{profile}-{cm}.rpu");
            fs::write(output.join(&name), &bytes)?;
            inventory.push(serde_json::json!({
                "path": name, "frames": 24, "profile": profile, "cm": cm,
                "bytes": bytes.len(), "recipe": recipe,
                "scope": "generated RPU parser/BSF fixture; no licensed imagery or FEL reconstruction proof"
            }));
        }
    }
    println!("{}", serde_json::to_string_pretty(&inventory)?);
    Ok(())
}

package assets

import "encoding/json"

// analyzeDolbyVisionFrames consumes actual decoded-frame side data. A container
// configuration's rpu_present_flag describes its declaration, not an observed
// RPU; only the decoder's parsed metadata establishes these packet facts.
func analyzeDolbyVisionFrames(raw []byte, d *StreamDetail) {
	var doc struct {
		Frames []struct {
			MediaType string `json:"media_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Side      []struct {
				Type                   string `json:"side_data_type"`
				RPUType                *int   `json:"rpu_type"`
				BaseDepth              *int   `json:"bl_bit_depth"`
				DisableResidual        *int   `json:"disable_residual_flag"`
				NLQMethod              *int   `json:"nlq_method_idc"`
				CoefficientDenominator *int   `json:"coef_log2_denom"`
				Components             []struct {
					Offset    *uint64 `json:"nlq_offset"`
					Maximum   *uint64 `json:"vdr_in_max"`
					Slope     *uint64 `json:"linear_deadzone_slope"`
					Threshold *uint64 `json:"linear_deadzone_threshold"`
				} `json:"components"`
			} `json:"side_data_list"`
		} `json:"frames"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Frames) == 0 || len(doc.Frames) > 240 {
		return
	}
	for _, frame := range doc.Frames {
		if frame.MediaType != "video" || frame.Width <= 0 || frame.Width > 32768 || frame.Height <= 0 || frame.Height > 32768 {
			continue
		}
		for _, side := range frame.Side {
			if side.Type != "Dolby Vision Metadata" || side.RPUType == nil || *side.RPUType != 2 || side.BaseDepth == nil || *side.BaseDepth < 8 || *side.BaseDepth > 16 {
				continue
			}
			d.DolbyVisionMetadataKnown = true
			d.DolbyVisionRPU, d.DolbyVisionBaseLayer = true, true
			// Match libdovi's RpuDataNlq::is_mel across all three
			// components. MEL also enables residual parsing; the header's
			// disable_residual_flag alone cannot distinguish MEL and FEL.
			if d.DolbyVisionProfile == 7 && d.EnhancementLayer && side.DisableResidual != nil && *side.DisableResidual == 0 && side.NLQMethod != nil && *side.NLQMethod == 0 && side.CoefficientDenominator != nil && *side.CoefficientDenominator >= 0 && *side.CoefficientDenominator <= 32 && len(side.Components) == 3 {
				complete, mel := true, true
				one := uint64(1) << uint(*side.CoefficientDenominator)
				for _, c := range side.Components {
					if c.Offset == nil || c.Maximum == nil || c.Slope == nil || c.Threshold == nil {
						complete = false
						break
					}
					mel = mel && *c.Offset == 0 && *c.Maximum == one && *c.Slope == 0 && *c.Threshold == 0
				}
				if complete {
					d.DolbyVisionEnhancementType = "FEL"
					if mel {
						d.DolbyVisionEnhancementType = "MEL"
					}
				}
			}
			// ffprobe's frame metadata does not expose the complete CM
			// extension/version fact. Leave it unknown rather than infer it.
		}
	}
}

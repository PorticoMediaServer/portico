package decoder

import "errors"

// DolbyVisionProfile7To81 rewrites RPU metadata and removes the enhancement
// layer while retaining the HEVC base picture. It does not reconstruct FEL's
// residual image. Source profile/compatibility remain source facts on Target.
const DolbyVisionProfile7To81 = "profile7_to_81"

// DolbyVisionCopyBitstreamFilter is consumed immediately before copy args.
// The bundle's packet-qualified fact is mandatory; a stock BSF name or options
// probe alone cannot authorize a rewrite. The planner must also have observed
// the source RPU/base-layer facts before selecting the target.
func DolbyVisionCopyBitstreamFilter(t ConversionTarget, facts *ToolchainFacts) (string, error) {
	if t.DolbyVisionConversion == "" {
		return "", nil
	}
	normalized, err := t.Normalized()
	if err != nil {
		return "", err
	}
	if facts == nil || !facts.DolbyVisionProfile7To81 || facts.Identity == "" || !facts.BitstreamFilters["dovi_profile"] {
		return "", errors.New("dolby_vision_profile7_to_81_unavailable")
	}
	if normalized.DolbyVisionConversion != DolbyVisionProfile7To81 {
		return "", ErrInvalidConfiguration
	}
	return "dovi_profile=mode=2", nil
}

// DeliveredDolbyVision describes the selected copy output for HLS signalling.
// It never mutates the source profile or implies that execution has succeeded.
func (t ConversionTarget) DeliveredDolbyVision() (profile, compatibility int) {
	normalized, err := t.Normalized()
	if err != nil || !normalized.CopyDolbyVision {
		return 0, 0
	}
	if normalized.DolbyVisionConversion == DolbyVisionProfile7To81 {
		return 8, 1
	}
	return normalized.DolbyVisionProfile, normalized.DolbyVisionCompatibility
}

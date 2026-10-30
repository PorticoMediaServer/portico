//go:build !linux

package decoder

import "context"

// Other targets acquire grants only after their own bundle and loader gates.
func QualifyToolchainBundle(ctx context.Context, ffmpeg, ffprobe string, facts ToolchainFacts) (ToolchainFacts, error) {
	facts.DolbyVisionProfile7To81, facts.DolbyVisionDualLayerJoin = false, false
	facts.HDR10PlusLibX265Encode = false
	return facts, ctx.Err()
}

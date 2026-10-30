// Package qualityladder owns the quality vocabulary shared by delivery,
// downloads, prepared recipes and generated client contracts.
package qualityladder

type Preset struct {
	ID                 string  `json:"id"`
	Label              string  `json:"label"`
	Height             int     `json:"targetDisplayHeight"`
	VideoBitrateBPS    int     `json:"maxVideoBitrateBps"`
	MaxAudioBitrateBPS int     `json:"maxAudioBitrateBps"`
	DataGBPerHour      float64 `json:"dataGBPerHour"`
}

// Fixed returns a copy, highest ceiling first. Data is decimal GB, video
// Mbps ×0.45; display rounding never changes these canonical numeric values.
func Fixed() []Preset {
	out := []Preset{
		{ID: "2160p", Label: "4K", Height: 2160, VideoBitrateBPS: 40_000_000, MaxAudioBitrateBPS: 192_000},
		{ID: "1080p-high", Label: "1080p High", Height: 1080, VideoBitrateBPS: 20_000_000, MaxAudioBitrateBPS: 192_000},
		{ID: "1080p-low", Label: "1080p Low", Height: 1080, VideoBitrateBPS: 8_000_000, MaxAudioBitrateBPS: 192_000},
		{ID: "720p-high", Label: "720p High", Height: 720, VideoBitrateBPS: 4_000_000, MaxAudioBitrateBPS: 128_000},
		{ID: "720p-low", Label: "720p Low", Height: 720, VideoBitrateBPS: 2_000_000, MaxAudioBitrateBPS: 128_000},
		{ID: "480p", Label: "480p", Height: 480, VideoBitrateBPS: 1_500_000, MaxAudioBitrateBPS: 96_000},
		{ID: "240p", Label: "240p", Height: 240, VideoBitrateBPS: 500_000, MaxAudioBitrateBPS: 96_000},
	}
	for i := range out {
		out[i].DataGBPerHour = float64(out[i].VideoBitrateBPS) * 45 / 100_000_000
	}
	return out
}
func IDs() []string {
	out := []string{"original", "automatic"}
	for _, p := range Fixed() {
		out = append(out, p.ID)
	}
	return out
}
func Find(id string) (Preset, bool) {
	for _, p := range Fixed() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
func (p Preset) Width() int {
	switch p.Height {
	case 2160:
		return 3840
	case 1080:
		return 1920
	case 720:
		return 1280
	case 480:
		return 854
	case 240:
		return 426
	}
	return 0
}

// SameAsSource requires both measured facts. Resolution alone cannot show
// whether a same-resolution step reduces a high bitrate file; whole-file
// bitrate is a conservative upper bound when stream bitrate is unavailable.
func (p Preset) SameAsSource(height int, videoBitrateBPS int64) bool {
	return height > 0 && videoBitrateBPS > 0 && height <= p.Height && videoBitrateBPS <= int64(p.VideoBitrateBPS)
}

// Menu is the complete nine-choice order. Original and Automatic have no
// numeric bitrate or data-use claim of their own.
func Menu() []Preset {
	return append([]Preset{{ID: "original", Label: "Original"}, {ID: "automatic", Label: "Automatic"}}, Fixed()...)
}

// AtBitrate recognizes an exact canonical away-limit value. An arbitrary
// numeric ceiling has no invented resolution or nearest-step alias.
func AtBitrate(bits int) (Preset, bool) {
	for _, p := range Fixed() {
		if p.VideoBitrateBPS == bits {
			return p, true
		}
	}
	return Preset{}, false
}

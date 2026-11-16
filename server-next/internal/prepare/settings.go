package prepare

import (
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Settings is Settings › Downloads: what the owner allows. There is no download space: a copy
// the server makes is temporary (area 9 rules 24 and 45, Justin 16 Nov), and the general
// low-disk warning is the only guard on space.
type Settings struct {
	// On offers downloads on this server. Off, nobody downloads and the apps have no
	// Downloads tab.
	On bool `json:"on"`
	// HighestQuality is the highest quality a download may be: "original" for no limit, or a
	// step of the video ladder ("1080p-high"). A limit below Original means an original file
	// is not offered as a download, only a copy the server makes.
	HighestQuality string `json:"highestQuality"`
	// KeepDays is what Settings › Storage › Keep generated files last chose (storage/keeping).
	// It no longer keeps anything: a download copy lasts until the device has it, or 48 hours.
	KeepDays int `json:"keepDays"`
}

// KeepChoices are the keeping times the owner chooses from.
var KeepChoices = []int{7, 14, 30, 90, 365}

// Document is the downloads settings document.
var Document = settings.Define("downloads", Settings{On: true, HighestQuality: "original", KeepDays: 30},
	func(s *Settings) error {
		if _, ok := qualityladder.Find(s.HighestQuality); !ok && s.HighestQuality != "original" {
			return &settings.Invalid{Field: "highestQuality", Message: "Choose Original or one of the video qualities."}
		}
		if !slices.Contains(KeepChoices, s.KeepDays) {
			return &settings.Invalid{Field: "keepDays", Message: "Keep prepared copies for 7, 14, 30 or 90 days, or a year."}
		}
		return nil
	})

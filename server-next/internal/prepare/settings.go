package prepare

import (
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Settings is Settings › Downloads: what the owner allows and how much the server's copies
// may take.
type Settings struct {
	// On offers downloads on this server. Off, nobody downloads and the apps have no
	// Downloads tab.
	On bool `json:"on"`
	// HighestQuality is the highest quality a download may be: "original" for no limit, or a
	// step of the video ladder ("1080p-high"). A limit below Original means an original file
	// is not offered as a download, only a copy the server makes.
	HighestQuality string `json:"highestQuality"`
	// SpaceGB is how much room the copies the server makes may take, in gigabytes; 0 is no
	// limit. Each copy counts once, however many people asked for it. Originals take none.
	SpaceGB int64 `json:"spaceGB"`
	// KeepDays is how long a copy nobody has asked for or fetched is kept.
	KeepDays int `json:"keepDays"`
	// ConversionsAtOnce is how many copies are made at the same time. Each one runs behind
	// people who are watching.
	ConversionsAtOnce int `json:"conversionsAtOnce"`
}

// KeepChoices are the keeping times the owner chooses from.
var KeepChoices = []int{7, 14, 30, 90, 365}

// Document is the downloads settings document.
var Document = settings.Define("downloads", Settings{On: true, HighestQuality: "original", KeepDays: 30, ConversionsAtOnce: 1},
	func(s *Settings) error {
		if _, ok := qualityladder.Find(s.HighestQuality); !ok && s.HighestQuality != "original" {
			return &settings.Invalid{Field: "highestQuality", Message: "Choose Original or one of the video qualities."}
		}
		if s.SpaceGB < 0 || s.SpaceGB > 1_000_000 {
			return &settings.Invalid{Field: "spaceGB", Message: "Give a size in gigabytes, or 0 for no limit."}
		}
		if !slices.Contains(KeepChoices, s.KeepDays) {
			return &settings.Invalid{Field: "keepDays", Message: "Keep prepared copies for 7, 14, 30 or 90 days, or a year."}
		}
		if s.ConversionsAtOnce < 1 || s.ConversionsAtOnce > 8 {
			return &settings.Invalid{Field: "conversionsAtOnce", Message: "Make between 1 and 8 copies at once."}
		}
		return nil
	})

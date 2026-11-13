package prepare

import (
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
}

// Document is the downloads settings document.
var Document = settings.Define("downloads", Settings{On: true, HighestQuality: "original"},
	func(s *Settings) error {
		if _, ok := qualityladder.Find(s.HighestQuality); !ok && s.HighestQuality != "original" {
			return &settings.Invalid{Field: "highestQuality", Message: "Choose Original or one of the video qualities."}
		}
		if s.SpaceGB < 0 || s.SpaceGB > 1_000_000 {
			return &settings.Invalid{Field: "spaceGB", Message: "Give a size in gigabytes, or 0 for no limit."}
		}
		return nil
	})

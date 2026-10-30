package ingestion

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// A folder is a disc title when its own page holds BDMV or VIDEO_TS; ordinary
// folders and loose files never become directory objects.
func TestDiscRootOnPage(t *testing.T) {
	for name, page := range map[string]storage.InventoryPage{
		"bluray":   {Entries: []storage.Snapshot{{Name: "BDMV", Directory: true}}},
		"dvd":      {Entries: []storage.Snapshot{{Name: "VIDEO_TS", Directory: true}}},
		"position": {Entries: []storage.Snapshot{{Name: "notes.txt"}, {Name: "video_ts", Directory: true}}},
	} {
		if !discRootOnPage("Film", page) {
			t.Errorf("%s: disc layout not recognised", name)
		}
	}
	for name, page := range map[string]storage.InventoryPage{
		"loose files":     {Entries: []storage.Snapshot{{Name: "Film.mkv"}, {Name: "Film.srt"}}},
		"ordinary":        {Entries: []storage.Snapshot{{Name: "Extras", Directory: true}}},
		"file named BDMV": {Entries: []storage.Snapshot{{Name: "BDMV"}}},
	} {
		if discRootOnPage("Film", page) {
			t.Errorf("%s: ordinary folder became a disc title", name)
		}
	}
}

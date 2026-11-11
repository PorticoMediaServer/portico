package dvr

import (
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Two airings of a programme on one day without episode numbers (the 6 o'clock and the
// 11 o'clock news, or one title on two channels at once) are two files: one never appends to
// the other, and deleting one never deletes the other.
func TestTwoAiringsOnOneDayAreTwoFiles(t *testing.T) {
	day := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)
	_, early := Layout(db.Recording{ID: 1, ChannelID: 1, Title: "News", StartsAt: day})
	_, late := Layout(db.Recording{ID: 2, ChannelID: 1, Title: "News", StartsAt: day.Add(5 * time.Hour)})
	_, elsewhere := Layout(db.Recording{ID: 3, ChannelID: 2, Title: "News", StartsAt: day})
	if early == late || early == elsewhere {
		t.Fatalf("two recordings share a file: %q, %q, %q", early, late, elsewhere)
	}
}

// A guide title or episode title up to the guide's own 512-byte limit still makes names a
// disk accepts (255 bytes a name), so the recording is made.
func TestALongGuideTitleMakesNamesTheDiskAccepts(t *testing.T) {
	season, episode := 1, 4
	long := strings.Repeat("長", 170) // 510 bytes
	dir, name := Layout(db.Recording{Title: long, EpisodeTitle: long, Season: &season, Episode: &episode, StartsAt: time.Now()})
	for _, part := range append(strings.Split(dir, "/"), name, strings.TrimSuffix(name, ".ts")+".nfo") {
		if len(part) > 255 {
			t.Fatalf("a name of %d bytes: %.40q", len(part), part)
		}
	}
}

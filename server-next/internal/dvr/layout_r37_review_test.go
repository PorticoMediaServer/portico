package dvr

import (
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Two sources often carry channels of the same name (two HDHomeRun boxes on one aerial):
// one programme booked on each at the same time is two files, never one.
func TestReviewSameNamedChannelsAreTwoFiles(t *testing.T) {
	at := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)
	_, a := Layout(db.Recording{ID: 1, ChannelID: 1, ChannelName: "WCBS-HD", Title: "News", StartsAt: at})
	_, b := Layout(db.Recording{ID: 2, ChannelID: 9, ChannelName: "WCBS-HD", Title: "News", StartsAt: at})
	if a == b {
		t.Fatalf("two channels named alike write one file: %q", a)
	}
}

// A show whose title is a name Windows keeps for a device still records there.
func TestAShowNamedLikeADeviceRecords(t *testing.T) {
	for _, title := range []string{"Con", "aux", "Nul.ts", "COM1"} {
		dir, name := Layout(db.Recording{Title: title, StartsAt: time.Now()})
		if !strings.HasPrefix(dir, "_") || !strings.HasPrefix(name, "_") {
			t.Errorf("%q makes %q and %q", title, dir, name)
		}
	}
}

package api

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A library's landing page is Home's rows for that library alone: a film started is on the
// films library's Continue Watching and on no other library's, every title in it is from that
// library, and a library that is not there is not found.
func TestALibraryLandingHoldsOnlyItsOwnTitles(t *testing.T) {
	th := newTheatre(t)
	token := th.owner.AccessToken
	th.clock = time.Now()
	stop := int64(30 * 60_000)
	session := th.play(token, th.film)
	status(t, th.s, "DELETE", "/v1/playback/sessions/"+session.ID, token, PlaybackStop{PositionMs: &stop})
	for kind, lib := range th.libs {
		started := false
		for _, row := range get[Home](t, th.s, token, "/v1/libraries/"+lib.PublicID+"/landing").Rows {
			if len(row.Items) == 0 {
				t.Errorf("%s landing sent an empty row %q", kind, row.ID)
			}
			for _, item := range row.Items {
				if item.LibraryID != lib.PublicID {
					t.Errorf("%s landing row %q holds %q from another library", kind, row.ID, item.Title)
				}
				started = started || row.ID == "continue" && item.ID == th.film
			}
		}
		if started != (kind == db.LibraryMovies) {
			t.Errorf("%s landing: the started film on Continue Watching is %v", kind, started)
		}
	}
	if code := status(t, th.s, "GET", "/v1/libraries/lib_nothere/landing", token, nil); code != 404 {
		t.Errorf("the landing of a library that is not there answered %d", code)
	}
}

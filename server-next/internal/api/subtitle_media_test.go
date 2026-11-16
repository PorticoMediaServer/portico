package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Even a previously issued subtitle/font address stops at the title's current
// library and rating restrictions, without waiting for the playback sweep.
func TestSubtitleMediaChecksCurrentProfileVisibility(t *testing.T) {
	for _, restriction := range []string{"library", "rating"} {
		t.Run(restriction, func(t *testing.T) {
			th := newTheatre(t)
			s, owner := th.s, th.owner.AccessToken
			var profile Profile
			call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Viewer"}, &profile)
			var viewer Session
			call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &viewer)
			playing := th.play(viewer.AccessToken, th.film)
			grant := strings.Split(playing.Presentation.URL, "/")[3]
			// The stand-in serves bytes only once the real API grants access.
			s.ServeMediaWith(func(w http.ResponseWriter, _ *http.Request, _, _ string) { w.WriteHeader(200) })
			paths := []string{"original.ass", "fonts", "fonts/0", "original.sup", "original.idx", "original.sub", "lines"}
			fetch := func(suffix string) int {
				return status(t, s, "GET", "/v1/media/subtitles/"+grant+"/track/1/"+suffix, "", nil)
			}
			if fetch(paths[0]) != 200 {
				t.Fatal("visible title's grant was refused")
			}
			if restriction == "library" {
				call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: false}, nil)
			} else {
				age := 13
				call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: &age}, nil)
				locks, err := s.db.MetadataLocks(context.Background(), th.film)
				rating := "NC-17"
				if err == nil {
					_, err = s.db.EditMetadata(context.Background(), th.film, locks.Revision, db.MetadataEdit{ContentRating: &rating})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range paths {
				if code := fetch(path); code != 404 {
					t.Fatalf("hidden %s answered %d", path, code)
				}
			}
		})
	}
}

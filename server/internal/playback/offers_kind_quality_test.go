package playback

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func offeredIDs(rungs []QualityRung) []string {
	out := []string{}
	for _, r := range rungs {
		out = append(out, r.ID)
	}
	return out
}

// Each kind publishes its own ladder, and a live session's current quality is
// always one of the rungs offered for its source.
func TestOfferedQualityLadderPerKindAndCurrentIsOffered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	video := compactcatalog.Asset{Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60}
	audio := compactcatalog.Asset{Size: 1, ModifiedNS: 1, Container: "mp4", AudioCodec: "aac", Duration: 60}
	videoLadder := "original,automatic,2160p,1080p-high,1080p-low,720p-high,720p-low,480p,240p"
	musicLadder := "original,automatic,cd-quality,limit-320,limit-96"
	cases := []struct {
		name     string
		kind     compactcatalog.Kind
		asset    compactcatalog.Asset
		ladder   string
		sessions []string
	}{
		{"film", compactcatalog.Movie, video, videoLadder, []string{"original", "automatic"}},
		{"episode", compactcatalog.Episode, video, videoLadder, []string{"original", "automatic"}},
		{"song", compactcatalog.Track, audio, musicLadder, []string{"original", "cd-quality", "limit-320", "limit-96"}},
		{"audiobook", compactcatalog.Part, audio, "original,automatic", []string{"original"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			a := tc.asset
			a.Path = "/media/" + tc.name
			_, item, _ := catalogFixture(t, db, "lib", "/media", tc.kind, tc.name, tc.name, a)
			p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
			insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
			service := New(db)
			scope := OffersScope{"server", "lib", item, "fence"}
			base, err := service.Offers(context.Background(), p, scope, "", "")
			if err != nil || len(base.Sources) != 1 {
				t.Fatal(base, err)
			}
			if got := strings.Join(offeredIDs(base.Sources[0].Qualities), ","); got != tc.ladder {
				t.Fatalf("ladder %s, want %s", got, tc.ladder)
			}
			for _, q := range tc.sessions {
				session, err := service.Create(p, item, q, "request-"+q)
				if err != nil {
					t.Fatalf("create %s: %v", q, err)
				}
				o, err := service.Offers(context.Background(), p, scope, session.ID, "")
				if err != nil || o.Current == nil {
					t.Fatalf("offers %s: %v", q, err)
				}
				found := false
				for _, id := range offeredIDs(o.Sources[0].Qualities) {
					found = found || id == o.Current.QualityID
				}
				if !found {
					t.Fatalf("session %s: current %q not offered in %v", q, o.Current.QualityID, offeredIDs(o.Sources[0].Qualities))
				}
				if o.Current.QualityID != q {
					t.Fatalf("session %s reports current %q", q, o.Current.QualityID)
				}
			}
		})
	}
}

func TestCurrentRungFallsBackToAnOfferedRung(t *testing.T) {
	music := MusicQualityOffers(true, true, true, 0)
	for id, want := range map[string]string{"limit": "original", "limit-200": "limit-96", "limit-500": "limit-320", "limit-50": "limit-96", "automatic:720p-high": "automatic", "cd-quality": "cd-quality"} {
		if got := currentRungID(id, music); got != want {
			t.Fatalf("%s -> %s, want %s", id, got, want)
		}
	}
	if _, ok := namedQualityChoice("limit-0320"); ok {
		t.Fatal("non-canonical id accepted")
	}
}

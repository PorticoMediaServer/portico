package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// An inherited policy and an explicit value equal to the registry default are
// different choices. Exercise their actual storage/provenance and video reader;
// the Settings presentation of "server" is tested by its binding owner.
func TestPlayedThresholdInheritanceExplicitDefaultAndClear(t *testing.T) {
	for _, choice := range []string{"inherited", "explicit95", "cleared"} {
		t.Run(choice, func(t *testing.T) {
			s, db, viewer := savedFixture(t)
			item := phase34SavedFixtureItem(t, db)
			key := identity.PersonalKey(viewer)
			if _, err := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, key, item.ID); err != nil {
				t.Fatal(err)
			}
			// Use the existing canonical library document fixture, with no credits
			// completion route that could conceal a percentage/provenance error.
			libraryPolicy := DefaultContinueWatchingSettings()
			libraryPolicy.VideoPlayedThreshold = 80
			libraryPolicy.VideoCompletion = "threshold"
			body, err := json.Marshal(map[string]any{"continueWatching": libraryPolicy})
			if err != nil {
				t.Fatal(err)
			}
			var library string
			if err = db.QueryRow(`SELECT l.library_id FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id WHERE i.public_id=pid_blob(?)`, item.Public).Scan(&library); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO admin_documents(scope,revision,body,updated_ms) VALUES(?,1,?,0) ON CONFLICT(scope) DO UPDATE SET revision=revision+1,body=excluded.body`, "library:"+library, string(body)); err != nil {
				t.Fatal(err)
			}

			reload := func(wantStored bool, wantRevision int64) int64 {
				t.Helper()
				// A new service must recover the choice from the actual preference
				// rows, rather than retain an in-memory patch or infer from value95.
				read, err := operations.New(db).Preferences(context.Background(), identity.Principal{Viewer: viewer}, "web", func(context.Context, *sql.Tx, string) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				wantSource := "default"
				if wantStored {
					wantSource = operations.ScopeProfileServer
				}
				if read.Effective.Int("playback.playedThresholdPercent") != 95 || read.EffectiveSource["playback.playedThresholdPercent"] != wantSource {
					t.Fatalf("effective threshold/provenance: %+v", read)
				}
				for _, document := range read.Documents {
					if document.Scope != operations.ScopeProfileServer {
						continue
					}
					_, stored := document.Values["playback.playedThresholdPercent"]
					if stored != wantStored || wantRevision != 0 && document.Revision != wantRevision {
						t.Fatalf("stored=%v revision=%d, want stored=%v revision=%d", stored, document.Revision, wantStored, wantRevision)
					}
					return document.Revision
				}
				t.Fatal("profile preference document missing")
				return 0
			}
			revision := reload(false, 0)
			if choice != "inherited" {
				storePreference(t, db, viewer, map[string]any{"playback.playedThresholdPercent": 95})
				revision = reload(true, revision+1)
			}
			accept(t, s, db, viewer, item.Public, 1, 80, 100, "playing")
			personal, err := s.Personal(key, item.Public)
			if err != nil || personal.Watched != (choice == "inherited") {
				t.Fatalf("choice=%s at80: %+v %v", choice, personal, err)
			}
			if choice == "cleared" {
				storePreference(t, db, viewer, map[string]any{"playback.playedThresholdPercent": nil})
				reload(false, revision+1)
				accept(t, s, db, viewer, item.Public, 2, 80, 100, "playing")
			} else if choice == "explicit95" {
				accept(t, s, db, viewer, item.Public, 2, 95, 100, "playing")
			} else {
				return
			}
			if personal, err = s.Personal(key, item.Public); err != nil || !personal.Watched {
				t.Fatalf("choice=%s completion: %+v %v", choice, personal, err)
			}
		})
	}
}

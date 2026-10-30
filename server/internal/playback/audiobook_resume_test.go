package playback

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
	"time"
)

func TestAudiobookStoredResumeUsesProfileChoiceOnce(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, item, _ := catalogFixture(t, db, "books", "/books", compactcatalog.Part, "part", "Part", compactcatalog.Asset{Path: "/books/part.m4a", Size: 1, ModifiedNS: 1, Container: "m4a", AudioCodec: "aac", Duration: 600})
	p := identity.Principal{Hash: "resume-session", Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile", Role: "owner"}}
	insertFixtureAccount(t, db, p.AccountID, p.ProfileID)
	insertFixtureSession(t, db, p.Hash, p.AccountID, p.ProfileID, p.Role, "2999-01-01T00:00:00Z")
	service := New(db)
	initial, err := service.Create(p, item, "original", "initial")
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Progress(p, initial.ID, initial.Generation, 1, 100, "paused"); err != nil {
		t.Fatal(err)
	}
	if err = service.Stop(p, initial.ID); err != nil {
		t.Fatal(err)
	}
	prefs := operations.New(db)
	allow := func(context.Context, *sql.Tx, string) error { return nil }
	for i, ms := range []int{0, 5000, 15000, 30000} {
		snap, err := prefs.Preferences(context.Background(), p, "web", allow)
		if err != nil {
			t.Fatal(err)
		}
		revision := int64(1)
		for _, doc := range snap.Documents {
			if doc.Scope == operations.ScopeProfileServer {
				revision = doc.Revision
			}
		}
		_, err = prefs.ApplyPreferences(context.Background(), p, allow, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: revision, IdempotencyKey: fmt.Sprintf("choice-%d", i), Values: operations.PreferencePatch{"audiobooks.resumeRewindMs": ms}})
		if err != nil {
			t.Fatal(err)
		}
		for j, gap := range []time.Duration{time.Minute, 6 * time.Minute, 2 * time.Hour} {
			if _, err = db.Exec(`UPDATE personal_items SET last_played_at=? WHERE profile_id=?`, time.Now().Add(-gap).UTC().Format(time.RFC3339Nano), identity.PersonalKey(p.Viewer)); err != nil {
				t.Fatal(err)
			}
			key := fmt.Sprintf("resume-%d-%d", i, j)
			choice := V1Choice{QualityID: "original", PendingActivation: true, UseStoredResume: true}
			got, err := service.CreateV1(context.Background(), p, item, key, choice, nil)
			want := 100 - float64(ms)/1000
			if err != nil || got.ResumeSeconds != want {
				t.Fatalf("gap=%v choice=%d: %+v %v; want %v", gap, ms, got, err, want)
			}
			replay, err := service.CreateV1(context.Background(), p, item, key, choice, nil)
			if err != nil || replay.ID != got.ID || replay.ResumeSeconds != want {
				t.Fatalf("retry applied rewind twice: %+v %v", replay, err)
			}
			var stored int64
			if err = db.QueryRow(`SELECT position FROM progress WHERE profile_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, identity.PersonalKey(p.Viewer), item).Scan(&stored); err != nil || stored != 100000 {
				t.Fatalf("preparation mutated progress: %d %v", stored, err)
			}
			if err = service.StopV1(context.Background(), got.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A replan/transfer/explicit start does not choose the stored-resume intent.
	explicit, err := service.CreateV1(context.Background(), p, item, "chosen-position", V1Choice{QualityID: "original", PendingActivation: true}, nil)
	if err != nil || explicit.ResumeSeconds != 100 {
		t.Fatalf("continuation applied automatic rewind: %+v %v", explicit, err)
	}
}

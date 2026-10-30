package administration

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestLibrarySonicEligibilityUsesCurrentReadinessAndSameSnapshotAuthority(t *testing.T) {
	s, db, root := newService(t)
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('music','Music','music',?)`, root); err != nil {
		t.Fatal(err)
	}
	ready := false
	s.SonicRuntimeAvailable = func() bool { return ready }
	called := 0
	owner := func(ctx context.Context, tx *sql.Tx) (bool, error) {
		called++
		var count int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM libraries WHERE id='music'`).Scan(&count)
		return count == 1, err
	}
	has := func(doc LibraryDocument) bool {
		t.Helper()
		analysis, status := false, false
		for _, id := range doc.EligibleControls {
			analysis = analysis || id == "libraries.sonicAnalysis"
			status = status || id == "libraries.sonicStatus"
		}
		if analysis != status {
			t.Fatal("partial Sonic eligibility", doc.EligibleControls)
		}
		return analysis
	}
	doc, err := s.LibrarySettingsFor(ctx, allow, "music", owner)
	if err != nil || has(doc) || called != 0 {
		t.Fatal("unavailable runtime offered a control", err, called)
	}
	ready = true
	doc, err = s.LibrarySettingsFor(ctx, allow, "music", owner)
	if err != nil || !has(doc) || called != 1 {
		t.Fatal("ready owner control missing", err, called)
	}
	doc, err = s.LibrarySettingsFor(ctx, allow, "music", func(context.Context, *sql.Tx) (bool, error) { return false, nil })
	if err != nil || has(doc) {
		t.Fatal("nonowner offered Sonic controls", err)
	}
	denied := errors.New("revoked during same snapshot")
	if _, err = s.LibrarySettingsFor(ctx, allow, "music", func(context.Context, *sql.Tx) (bool, error) { return false, denied }); !errors.Is(err, denied) {
		t.Fatal("authority read failure swallowed", err)
	}
	doc, err = s.LibrarySettingsFor(ctx, allow, "music", owner)
	if err != nil {
		t.Fatal(err)
	}
	change := Change[LibrarySettings]{ExpectedRevision: doc.Revision, OperationID: "sonic-eligibility-replay", Settings: doc.Settings}
	saved, err := s.SaveLibrarySettings(ctx, allow, "music", change, owner)
	if err != nil || !has(saved) {
		t.Fatal("save lost live eligibility", err)
	}
	ready = false
	replayed, err := s.SaveLibrarySettings(ctx, allow, "music", change, owner)
	if err != nil || has(replayed) || replayed.Revision != saved.Revision {
		t.Fatal("receipt replay advertised retired runtime or rewrote settings", err)
	}
}

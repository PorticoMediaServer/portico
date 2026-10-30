package playback

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func personalWriterFixture(t *testing.T) (*sql.DB, identity.Principal, string, string, int64, int64) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "writers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	idA, itemA, _ := catalogFixture(t, db, "library", "/movies", compactcatalog.Movie, "a", "A", compactcatalog.Asset{Path: "/movies/a"})
	idB, itemB, _ := catalogFixture(t, db, "library", "/movies", compactcatalog.Movie, "b", "B", compactcatalog.Asset{Path: "/movies/b"})
	return db, identity.Principal{Hash: "token", Viewer: identity.Viewer{Authority: "local", AccountID: "account", ProfileID: "profile", Role: "owner"}}, itemA, itemB, idA, idB
}
func writerTx(t *testing.T, db *sql.DB, work func(*sql.Tx) error) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = work(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func expectWriter(t *testing.T, db *sql.DB, p identity.Principal, id int64, want string) {
	t.Helper()
	var actual string
	if err := db.QueryRow(`SELECT playback_id FROM progress WHERE profile_id=? AND item_id=?`, identity.PersonalKey(p.Viewer), id).Scan(&actual); err != nil || actual != want {
		t.Fatalf("writer %d=%q, want %q: %v", id, actual, want, err)
	}
}

func TestPersonalWriterUsesAcceptanceNotPreparationOrder(t *testing.T) {
	db, p, itemA, _, idA, _ := personalWriterFixture(t)
	var first, second int64
	writerTx(t, db, func(tx *sql.Tx) (err error) { first, err = reservePersonalIntent(tx, "first"); return })
	writerTx(t, db, func(tx *sql.Tx) (err error) {
		second, err = reservePersonalIntent(tx, "second")
		if err != nil {
			return
		}
		return activatePersonalWriter(tx, p, itemA, "fast", 0, second)
	})
	writerTx(t, db, func(tx *sql.Tx) error { return activatePersonalWriter(tx, p, itemA, "slow", 0, first) })
	expectWriter(t, db, p, idA, "fast")
	writerTx(t, db, func(tx *sql.Tx) error {
		again, err := reservePersonalIntent(tx, "first")
		if err == nil && again != first {
			t.Fatal("retry elected a new writer")
		}
		return err
	})
	var retained int
	if err := db.QueryRow(`SELECT count(*) FROM playback_personal_claims`).Scan(&retained); err != nil || retained != 2 {
		t.Fatal("older occurrence attribution lost", retained, err)
	}
}
func TestQueueContinuationInheritsWriterAndCannotOvertakeNewExplicitPlay(t *testing.T) {
	db, p, itemA, itemB, idA, idB := personalWriterFixture(t)
	var root, newer int64
	writerTx(t, db, func(tx *sql.Tx) (err error) {
		root, err = reservePersonalIntent(tx, "queue-root")
		if err != nil {
			return
		}
		return activatePersonalWriter(tx, p, itemA, "old-a", 0, root)
	})
	writerTx(t, db, func(tx *sql.Tx) error {
		ordinal, err := inheritedPersonalIntent(tx, p, "old-a")
		if err != nil {
			return err
		}
		return activatePersonalWriter(tx, p, itemA, "repeat-a", 0, ordinal)
	})
	expectWriter(t, db, p, idA, "repeat-a")
	writerTx(t, db, func(tx *sql.Tx) (err error) {
		newer, err = reservePersonalIntent(tx, "other-device")
		if err != nil {
			return
		}
		return activatePersonalWriter(tx, p, itemB, "explicit-b", 0, newer)
	})
	writerTx(t, db, func(tx *sql.Tx) error { return activatePersonalWriter(tx, p, itemB, "auto-b", 0, root) })
	expectWriter(t, db, p, idB, "explicit-b")
	other := p
	other.Authority = "hosted"
	writerTx(t, db, func(tx *sql.Tx) error { return activatePersonalWriter(tx, other, itemB, "hosted-b", 0, root) })
	expectWriter(t, db, other, idB, "hosted-b")
	expectWriter(t, db, p, idB, "explicit-b")
}
func TestManualFenceCoversPendingAndAutomaticButAllowsLaterExplicit(t *testing.T) {
	db, p, itemA, _, idA, _ := personalWriterFixture(t)
	var pending int64
	writerTx(t, db, func(tx *sql.Tx) (err error) {
		pending, err = reservePersonalIntent(tx, "pending")
		if err != nil {
			return
		}
		if _, err = tx.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,25000,0,0,'manual')`, identity.PersonalKey(p.Viewer), idA); err != nil {
			return
		}
		_, err = tx.Exec(`INSERT INTO playback_personal_fences VALUES(?,?,?)`, identity.PersonalKey(p.Viewer), idA, pending)
		return
	})
	writerTx(t, db, func(tx *sql.Tx) error { return activatePersonalWriter(tx, p, itemA, "delayed", 0, pending) })
	expectWriter(t, db, p, idA, "manual")
	writerTx(t, db, func(tx *sql.Tx) error {
		ordinal, err := reservePersonalIntent(tx, "deliberate-new-play")
		if err != nil {
			return err
		}
		return activatePersonalWriter(tx, p, itemA, "new", 25, ordinal)
	})
	expectWriter(t, db, p, idA, "new")
}

func TestPausedHistorySessionStartPreservesResume(t *testing.T) {
	db, p, item, _, entity, _ := personalWriterFixture(t)
	key := identity.PersonalKey(p.Viewer)
	if _, err := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,123000,0,'old')`, key, entity); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.New(db).ApplyPreferences(context.Background(), p, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "pause", Values: operations.PreferencePatch{"privacy.pauseWatchHistory": true}}); err != nil {
		t.Fatal(err)
	}
	writerTx(t, db, func(tx *sql.Tx) error { return claimSessionProgress(tx, p, item, "new", 0) })
	var position int64
	if err := db.QueryRow(`SELECT position FROM progress WHERE profile_id=? AND item_id=?`, key, entity).Scan(&position); err != nil || position != 123000 {
		t.Fatal(position, err)
	}
	expectWriter(t, db, p, entity, "new")
}

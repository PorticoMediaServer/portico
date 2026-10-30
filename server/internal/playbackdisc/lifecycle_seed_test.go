//go:build linux || darwin

package playbackdisc

import (
	"fmt"
	"testing"
	"time"
)

// A direct SQL insert cannot bypass the normal complete-graph seal transition.
// This is a persistence constraint test, never publication/actor authority.
func TestDiscLifecycleCannotBeginActiveOrSealed(t *testing.T) {
	for _, initial := range []struct {
		state   string
		sealed  int
		retired int64
	}{{"active", 1, 0}, {"pending", 1, 0}, {"retired", 0, 1}, {"pending", 0, 1}} {
		t.Run(fmt.Sprintf("%s/sealed%d/retired%d", initial.state, initial.sealed, initial.retired), func(t *testing.T) {
			f := newDiscFixture(t)
			if err := f.exec(`INSERT INTO playback_disc_snapshots(id,session_id,generation,input_kind) VALUES(?,?,1,'conditional')`, f.snapshot.ID, discSessionID); err != nil {
				t.Fatal(err)
			}
			if err := f.exec(`INSERT INTO playback_disc_lifecycle(snapshot_id,state,sealed,created_ms,expires_ms,retired_ms) VALUES(?,?,?,?,?,?)`, f.snapshot.ID, initial.state, initial.sealed, time.Now().UnixMilli(), f.until, initial.retired); err == nil {
				t.Fatal("lifecycle bypassed pending, complete-graph sealing")
			}
			if n := mustInt(t, f.db, `SELECT count(*) FROM playback_disc_lifecycle`); n != 0 {
				t.Fatal("refused lifecycle was retained")
			}
		})
	}
}

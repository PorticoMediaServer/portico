package dvr

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// No capture driver or provider is created: the claim was already invalidated
// before dispatch. Only the exact physically owned allocation may be retired.
func TestProfileInvalidationBeforeCaptureRetiresExactAllocation(t *testing.T) {
	for _, successor := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-generation", true: "successor-generation"}[successor], func(t *testing.T) {
			db, e := persistence.Open(filepath.Join(canonicalFixtureDir(t), "server.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			id, source := strings.Repeat("a", 48), strings.Repeat("b", 48)
			if _, e = db.Exec(`INSERT INTO live_source_identities(id) VALUES(?)`, source); e != nil {
				t.Fatal(e)
			}
			generation, token := int64(1), "old-token"
			if successor {
				generation, token = 2, "successor-token"
			}
			if _, e = db.Exec(`INSERT INTO live_allocations(id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms) VALUES(?,?,'channel','recording','recording','local','account','profile',?,?,'active',?,1)`, id, source, token, generation, time.Now().Add(time.Minute).UnixMilli()); e != nil {
				t.Fatal(e)
			}
			if _, e = db.Exec(`INSERT INTO live_source_dependencies(source_id,kind,id) VALUES(?,'allocation',?)`, source, id); e != nil {
				t.Fatal(e)
			}
			locks, e := livechannels.NewPhysicalLocks(filepath.Join(canonicalFixtureDir(t), "locks"))
			if e != nil {
				t.Fatal(e)
			}
			store := &Store{db: db, now: time.Now, locks: locks}
			c := &claim{request: CaptureRequest{Recording: Recording{ID: "recording"}, Owner: livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}, Allocation: livechannels.Allocation{ID: id, SourceID: source, Token: "old-token", Generation: 1}, Generation: 1}, token: "invalid-claim"}
			store.runClaim(context.Background(), c)
			var state string
			var dependencies int
			if e = db.QueryRow(`SELECT state FROM live_allocations WHERE id=?`, id).Scan(&state); e != nil {
				t.Fatal(e)
			}
			if e = db.QueryRow(`SELECT count(*) FROM live_source_dependencies WHERE kind='allocation' AND id=?`, id).Scan(&dependencies); e != nil {
				t.Fatal(e)
			}
			expected, count := "released", 0
			if successor {
				expected, count = "active", 1
			}
			if state != expected || dependencies != count {
				t.Fatal("allocation fence failed", state, dependencies)
			}
			lock, e := locks.Lock(c.request.Allocation)
			if e != nil {
				t.Fatal("physical lock leaked", e)
			}
			lock.Close()
		})
	}
}

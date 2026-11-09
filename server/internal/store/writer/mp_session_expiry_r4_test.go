package writer

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// One exact r4 target: native dead-head progress, actual application of the
// healthy successor, the two changed statements' Capture, and EQP either side
// of ANALYZE. It does not run the unrelated domain or race suites.
func TestMPSessionExpiryPhysicalOccurrenceRegression(t *testing.T) {
	w := mpSessionLogicalFixture(t)
	for _, row := range []struct {
		id, kind, media, execution                   string
		physicalGeneration, logicalGeneration, lease int64
	}{
		{"purged", "vod", "purged-media", "purged-execution", 2, 2, 1},
		{"empty-execution", "vod", "unlaunched-media", "", 2, 2, 2},
		{"stale-generation", "vod", "old-generation-media", "old-execution", 2, 3, 3},
		// Pin kind independently: a channel may have an otherwise usable media row.
		{"channel-with-media", "channel", "retire-media", "execution", 2, 2, 4},
	} {
		if row.media != "retire-media" {
			wc008NativeExec(t, w, `INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_execution_id) VALUES(?,'hash','account','profile',2111,'retire-asset',?,'playing',?,'opaque','2099-01-01T00:00:00Z',?,1,?)`, row.media, row.physicalGeneration, row.id+"-grant", row.id+"-source-request", row.execution)
		}
		wc008NativeExec(t, w, `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,media_session_id,media_generation,lease_expires_ms,created_ms,updated_ms) VALUES(?,?,'account','profile','local','key','digest',?,'player','playing','',1,1,?,?,?,1,1)`, row.id, row.id, row.kind, row.media, row.logicalGeneration, row.lease)
	}
	// Model the exact catalog/purge.go parent deletion, leaving the v1 lease live.
	wc008NativeExec(t, w, `DELETE FROM playback_sessions WHERE id='purged-media'`)
	wc008NativeExec(t, w, `UPDATE playback_v1_sessions SET message='old message' WHERE id='logical'`)
	var changed []stmt.Definition
	var expirySQL string
	for _, d := range mpSessionStopDefinitions() {
		if d.ID == mpSessionStopExpiryNext {
			expirySQL = d.SQL
		}
		if d.ID == mpSessionStopExpiryNext || d.ID == mpSessionStopLogical {
			changed = append(changed, d)
		}
	}
	if len(changed) != 2 || expirySQL == "" {
		t.Fatal("missing changed statements")
	}
	// Capture proves the unique-PK existence join fits the closed grammar and key
	// bindings. No environment/golden refresh or partial DDL is installed.
	catalogue, err := stmt.Describe(changed)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	c, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("mp14-r4-dead-physical-heads-v1")))
	golden, err := cachedCapture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hash, Statistics: plan.Unanalyzed, Implementations: "MP14 r4 full F physical occurrence regression; changed statements only"}, policy)
	closeErr := c.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if _, err = stmt.New(changed, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			wc008NativeExec(t, w, `ANALYZE`)
		}
		rows, err := w.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+expirySQL, int64(101))
		if err != nil {
			t.Fatal(err)
		}
		lease, physical := false, false
		for rows.Next() {
			var id, parent, unused int64
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN") || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatal(analyzed, detail)
			}
			lease = lease || strings.Contains(detail, "SEARCH playback_v1_sessions USING INDEX playback_v1_sessions_lease")
			physical = physical || strings.Contains(detail, "SEARCH p USING INDEX sqlite_autoindex_playback_sessions_1")
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil || !lease || !physical {
			t.Fatal("both native SEARCHes required", analyzed, lease, physical, err)
		}
		// Repeated attempts must all reach the healthy row, never a dead physical head.
		turn := wc008NativeTurn(t, w, 716, mpSessionStopDefinitions())
		a := mpSessionStopBodyAuthority(turn, 716)
		for attempt := 0; attempt < 3; attempt++ {
			id, err := nextMPSessionExpiry(t.Context(), turn, a)
			if err != nil || id != "logical" {
				t.Fatal("dead head starved healthy expiry", analyzed, attempt, id, err)
			}
		}
		_, _ = turn.transaction.rollback(nil)
	}
	turn := wc008NativeTurn(t, w, 716, mpSessionStopDefinitions())
	a := mpSessionStopBodyAuthority(turn, 716)
	got, err := applyMPSessionStop(t.Context(), turn, a, mpSessionStopInputFixture())
	if err != nil || !got.Changed {
		t.Fatal(got, err)
	}
	if _, err = turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var reason, state, message string
	var ended int64
	if err = w.db.QueryRow(`SELECT end_reason,state,message,ended_ms FROM playback_v1_sessions WHERE id='logical'`).Scan(&reason, &state, &message, &ended); err != nil || reason != "lease_expired" || state != "playing" || message != "" || ended != 101 {
		t.Fatal("retained end semantics", reason, state, message, ended, err)
	}
	var held int64
	if err = w.db.QueryRow(`SELECT count(*) FROM playback_v1_sessions WHERE id<>'logical' AND ended_ms=0`).Scan(&held); err != nil || held != 4 {
		t.Fatal("unsupported row mutated", held, err)
	}
	turn = wc008NativeTurn(t, w, 716, mpSessionStopDefinitions())
	a = mpSessionStopBodyAuthority(turn, 716)
	if id, err := nextMPSessionExpiry(t.Context(), turn, a); id != "" || !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("dead head selected after healthy expiry", id, err)
	}
}

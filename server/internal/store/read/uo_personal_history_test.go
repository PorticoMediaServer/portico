package read

import (
	"fmt"
	"strings"
	"testing"
)

func newUOPersonalHistorySource(t *testing.T) (*browseFixture, browseQuery, uoPersonalHistoryHead) {
	t.Helper()
	f := newUOPersonalReadSource(t)
	f.exec(t, `INSERT INTO personal_activity_revisions(profile_id,policy_authority,policy_account_id,policy_profile_id,policy_member_incarnation,policy_profile_lifetime,history_policy_generation,history_visible_from_id) VALUES('p','local','a','p',?, ?,1,71)`, strings.Repeat("a", 32), strings.Repeat("b", 32))
	f.exec(t, `BEGIN`)
	for n := 1; n <= 140; n++ {
		// Same-time ties followed by an older-time stream, both account-indexed.
		at := fmt.Sprintf("2026-11-%02dT00:00:00Z", 1+(n-1)/70)
		f.exec(t, `INSERT INTO play_history(occurred_at,event_kind,account_key,profile_id,playback_id,occurrence_generation,accepted_sequence,subject_id,item_public_id,activity_generation,membership_incarnation,profile_lifetime) VALUES(?,'started','a','p','playing',1,?,1,(SELECT public_id FROM catalog_subjects WHERE id=1),1,?,?)`, at, int64(n), strings.Repeat("a", 32), strings.Repeat("b", 32))
	}
	f.exec(t, `COMMIT`)
	q := uoPersonalNativeQuery(t, f)
	head, e := readUOPersonalHistoryHead(q, "p", "local", "a", "p", strings.Repeat("a", 32), strings.Repeat("b", 32), 1)
	if e != nil {
		t.Fatal(e)
	}
	return f, q, head
}
func TestUOPersonalHistoryBoundedKeysetAndPrivacy(t *testing.T) {
	f, q, head := newUOPersonalHistorySource(t)
	_ = f
	var next *uoPersonalHistoryCursor
	seen := map[int64]bool{}
	emptyPending := false
	for n := 0; n < 10; n++ {
		p, e := readUOPersonalHistory(q, "a", "p", strings.Repeat("a", 32), strings.Repeat("b", 32), head, next, 64)
		if e != nil || p.rawRows > 64 {
			t.Fatal(p, e)
		}
		for _, row := range p.rows {
			if row.id < 71 || seen[row.id] {
				t.Fatal("hidden/duplicate history", row)
			}
			seen[row.id] = true
		}
		if len(p.rows) == 0 && !p.exhausted {
			emptyPending = true
		}
		if p.exhausted {
			break
		}
		if p.next == nil || n == 9 {
			t.Fatal("missing history continuation")
		}
		next = p.next
	}
	if len(seen) != 70 {
		t.Fatal("history keyset lost ties", len(seen))
	}
	// All history hidden still advances <=64 raw positions and nonterminal cursor.
	head.cutoff = 1000
	p, e := readUOPersonalHistory(q, "a", "p", strings.Repeat("a", 32), strings.Repeat("b", 32), head, nil, 64)
	if e != nil || len(p.rows) != 0 || p.exhausted || p.next == nil {
		t.Fatal(p, e)
	}
	head.activity++
	if _, e = readUOPersonalHistory(q, "a", "p", strings.Repeat("a", 32), strings.Repeat("b", 32), head, p.next, 64); e != ErrStale {
		t.Fatal("generation drift accepted", e)
	}
	_ = emptyPending
}
func TestUOPersonalHistoryActualOctetsBeforePayload(t *testing.T) {
	f, q, head := newUOPersonalHistorySource(t)
	// The native stored value exceeds the caller's point grant. No guessed
	// maximum or generated size stands in for the actual UTF-8 octet count.
	f.exec(t, `UPDATE play_history SET occurred_at=? WHERE id>70`, strings.Repeat("音", 100))
	body := false
	preflight := false
	wrapped := func(name string, args ...any) ([][]any, error) {
		if name == "uo.personal.history.octets" {
			preflight = true
		}
		if name == "uo.personal.history.anchor" {
			body = true
		}
		return q(name, args...)
	}
	p, e := readUOPersonalHistory(wrapped, "a", "p", strings.Repeat("a", 32), strings.Repeat("b", 32), head, nil, 64)
	if e != ErrUnqualified || !preflight || body || len(p.rows) != 0 || p.next != nil {
		t.Fatal("variable history payload escaped point preflight", p, e, preflight, body)
	}
}

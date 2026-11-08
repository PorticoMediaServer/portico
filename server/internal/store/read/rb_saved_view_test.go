package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestRBSavedView(t *testing.T) { synctest.Test(t, testRBSavedView) }
func testRBSavedView(t *testing.T) {
	f := newBrowseSource(t)
	definition := `{"libraryId":"movies","pivot":"movies","query":null,"sort":[{"field":"title","direction":"asc"}],"presentation":"grid"}`
	add := func(id, account, name, visibility string) {
		f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,definition,created_at,visibility) VALUES(?,'view','untrusted-key','local',?,'p',?,?, '2026-11-07',?)`, id, account, name, definition, visibility)
	}
	add("", "a", "empty-id", "private")
	for n := 0; n < 3; n++ {
		add(fmt.Sprintf("hidden-%03d", n), "foreign", "SECRET-NAME", "server")
	}
	add("collection-000", "a", "collection", "private")
	f.exec(t, `UPDATE saved_resources SET kind='collection' WHERE id='collection-000'`)
	add("deleted", "a", "deleted", "private")
	f.exec(t, `UPDATE saved_resources SET deleted=1 WHERE id='deleted'`)
	add("saved-view", "a", "é\x00actual", "private")
	add("shared", "other", "shared name", "private")
	f.exec(t, `INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES('shared','local','a','p','viewer')`)
	f.exec(t, `INSERT OR REPLACE INTO saved_resource_generation(id,revision) VALUES(1,1)`)
	data, e := os.ReadFile("rb_saved_view_test.go")
	if e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	defs := rbSavedViewDefinitions()
	f = composeReadFixture(t, f, append(browseDefinitions(), defs...), readfixture.LiveConfig(time.Now), false, hash)
	reader, e := NewSavedViewReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	lastAttempts := 0
	directory := func(after *SavedViewCandidateCursor) (SavedViewCandidates, error) {
		var page SavedViewCandidates
		q, request, output := liveRequests("saved-view")
		_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
			var e error
			var cost BrowseReadResult
			page, cost, e = reader.DirectoryCandidates(ctx, request, time.Second, out, SavedViewCandidateInput{View: f.input, After: after})
			lastAttempts = cost.Attempts
			return e
		})
		return page, e
	}
	page, e := directory(nil)

	if e != nil || !page.CandidateOnly || page.Exhausted || page.RawPositions != 1 || len(page.Items) != 1 || page.Items[0].ID != "" || page.Next == nil || page.Next.rawID != "" {
		t.Fatal(page, e)
	}
	cursor := page.Next
	var got []SavedViewCandidate
	var beforeShared *SavedViewCandidateCursor
	for n := 0; n < 8; n++ {
		tail, e := directory(cursor)
		if e != nil {
			t.Fatal(tail, e)
		}
		got = append(got, tail.Items...)
		if tail.Next != nil && tail.Next.rawID == "saved-view" {
			beforeShared = tail.Next
		}
		if tail.Exhausted {
			break
		}
		if tail.Next == nil || tail.RawPositions != 1 {
			t.Fatal(tail)
		}
		cursor = tail.Next
	}
	if len(got) != 2 || got[0].ID != "saved-view" || got[1].Role != "viewer" {
		t.Fatal(got)
	}
	if beforeShared == nil {
		t.Fatal("lost raw binding")
	}
	original := reader.snapshots
	reader.snapshots = &savedViewMutation{browseSnapshots: original, beforeFinal: func() { f.exec(t, `DELETE FROM saved_resource_shares WHERE resource_id='shared'`) }}
	revoked, e := directory(beforeShared)
	reader.snapshots = original
	if e != nil || len(revoked.Items) != 0 || lastAttempts != 2 || revoked.Next == nil || revoked.Next.rawID != "shared" {
		t.Fatal("stale shared candidate published", revoked, lastAttempts, e)
	}
	f.exec(t, `DELETE FROM saved_resource_shares WHERE resource_id='shared'`)
	tail, e := directory(cursor)
	if e != nil || !tail.Exhausted || len(tail.Items) != 0 {
		t.Fatal(tail, e)
	}
	f.exec(t, `UPDATE saved_resource_generation SET revision=revision+1`)
	if _, e = directory(page.Next); !errors.Is(e, ErrStale) {
		t.Fatal("generation cursor drift", e)
	}

	// Actual merged closed byte points hydrate only the currently owned row.
	var actualFence browseFence
	_, e = f.snapshots.Read(t.Context(), savedViewRequest(), time.Second, func(scope *governor.ReadScope) error {
		q := func(name string, args ...any) ([][]any, error) { return scope.Query(readStatementID(name), args...) }
		var e error
		actualFence, e = readBrowseFence(q, f.input)
		if e != nil {
			return e
		}
		r, e := readSavedViewResource(scope, actualFence, "saved-view")
		if e != nil || r.name != "é\x00actual" || r.role != "owner" {
			t.Fatal("qualified owner point", r, e)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	// Pure definition validation, not an execution/publication qualification.
	resource := savedViewResource{fence: actualFence, id: "saved-view", definition: definition, role: "owner", revision: 1}
	if _, e := savedViewExecutionInput(resource, f.input); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{
		strings.Replace(definition, `"query":null`, `"query":{"op":"unsupported"},"query":null`, 1),
		strings.Replace(definition, `"query":null`, `"query":{"op":"unsupported"}`, 1),
		strings.Replace(definition, "movies", "other", 1),
		strings.Replace(definition, `"field":"title"`, `"field":"random"`, 1),
	} {
		resource.definition = bad
		if _, e := savedViewExecutionInput(resource, f.input); !errors.Is(e, ErrUnqualified) {
			t.Fatal("definition broadened", e)
		}
	}

	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		captureBrowse(t, f, defs, stats, hash)
	}
}

// Mutation occurs after the private draft window has physically returned and
// before the fresh final snapshot. It deliberately does not bump generation.
type savedViewMutation struct {
	browseSnapshots
	calls       int
	beforeFinal func()
}

func (s *savedViewMutation) Read(ctx context.Context, r governor.Request, budget time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s.calls++
	if s.calls == 3 {
		s.beforeFinal()
	}
	return s.browseSnapshots.Read(ctx, r, budget, fn)
}

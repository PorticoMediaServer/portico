package read

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func savedViewRequest() governor.Request {
	r := foregroundRequest()
	r.Budget.RawRows = 300
	r.Budget.VMOps = 100000
	r.Budget.Pages = 4096
	return r
}

const savedViewTestDefinition = `{"libraryId":"movies","pivot":"movies","query":null,"sort":[{"field":"title","direction":"asc"}],"presentation":"grid"}`

func savedViewTestKey() string {
	raw, _ := json.Marshal([3]string{"local", "a", "p"})
	return "viewer:" + base64.RawURLEncoding.EncodeToString(raw)
}
func savedViewAdd(t *testing.T, f *browseFixture, id, name, account, definition string) {
	t.Helper()
	f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,definition,created_at) VALUES(?,'view','deliberately-not-an-authority-proof','local',?,'p',?,?,'2026-11-07')`, id, account, name, definition)
}
func savedViewFixture(t *testing.T, count int) (*browseFixture, *SavedViewReader) {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `UPDATE catalog_entities SET retired=0,admission_own_age=10`)
	f.exec(t, `INSERT OR REPLACE INTO saved_resource_generation(id,revision) VALUES(1,1)`)
	for n := 0; n < count; n++ {
		savedViewAdd(t, f, fmt.Sprintf("view-%02d", n), fmt.Sprintf("Name %02d", n), "a", savedViewTestDefinition)
	}
	data, e := os.ReadFile("rb_saved_view_publication_test.go")
	if e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	f = composeReadFixture(t, f, append(browseDefinitions(), rbSavedViewDefinitions()...), readfixture.LiveConfig(time.Now), false, hash)
	r, e := NewSavedViewReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func savedViewExecute(t *testing.T, f *browseFixture, r *SavedViewReader, in SavedViewExecutionInput) (SavedViewExecutionPage, BrowseReadResult, error) {
	t.Helper()
	var p SavedViewExecutionPage
	var cost BrowseReadResult
	live, request, output := liveRequests("saved-view-execute")
	request.Budget = savedViewRequest().Budget
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		p, cost, e = r.Execute(ctx, request, time.Second, out, in)
		return e
	})
	return p, cost, e
}
func savedViewDirectory(t *testing.T, f *browseFixture, r *SavedViewReader, in SavedViewDirectoryInput) (SavedViewDirectoryPage, BrowseReadResult, error) {
	t.Helper()
	var p SavedViewDirectoryPage
	var cost BrowseReadResult
	live, request, output := liveRequests("saved-view-directory")
	request.Budget = savedViewRequest().Budget
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		p, cost, e = r.Directory(ctx, request, time.Second, out, in)
		return e
	})
	return p, cost, e
}

type savedViewWindowHook struct {
	base      *governor.Snapshots
	calls     int
	hook      func(int)
	deadlines []time.Time
	memory    []int64
}

func (h *savedViewWindowHook) NativeRowReservation() int64 { return h.base.NativeRowReservation() }
func (h *savedViewWindowHook) before(r governor.Request) {
	h.calls++
	h.deadlines = append(h.deadlines, r.Deadline)
	h.memory = append(h.memory, r.Need[governor.MemoryBytes])
	if h.hook != nil {
		h.hook(h.calls)
	}
}
func (h *savedViewWindowHook) Read(ctx context.Context, r governor.Request, budget time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	h.before(r)
	return h.base.Read(ctx, r, budget, fn)
}
func (h *savedViewWindowHook) ReadForecasted(ctx context.Context, r governor.Request, budget time.Duration, forecast governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	h.before(r)
	return h.base.ReadForecasted(ctx, r, budget, forecast, fn)
}
func savedViewSameDeadline(t *testing.T, h *savedViewWindowHook) {
	t.Helper()
	for _, d := range h.deadlines {
		if !d.Equal(h.deadlines[0]) {
			t.Fatal("deadline restarted", h.deadlines)
		}
	}
}

func TestRBSavedViewPublication(t *testing.T) {
	savedViewRun(t, "actual-definition-and-cursor", func(t *testing.T) {
		f, r := savedViewFixture(t, 1)
		in := SavedViewExecutionInput{View: f.input, Resource: "view-00"}
		in.View.Limit = 1
		in.View.Descending = true
		in.View.KeepArticles = true
		in.View.Library = 2
		in.View.Kind = 2 // stale/unauthorized hints cannot choose the target.
		page, cost, e := savedViewExecute(t, f, r, in)
		if e != nil || len(page.Page.Items) != 1 || page.Page.Items[0].ID != 1 || page.Next == nil || page.ResourceRevision != 1 || cost.LookupStatements != 12 || cost.PublicationStatements != 8 {
			t.Fatal(page, cost, e)
		}
		in.After = page.Next
		page, _, e = savedViewExecute(t, f, r, in)
		if e != nil || len(page.Page.Items) != 1 || page.Page.Items[0].ID != 2 {
			t.Fatal(page, e)
		}
		f.exec(t, `UPDATE saved_resources SET revision=revision+1 WHERE id='view-00'`)
		page, _, e = savedViewExecute(t, f, r, in)
		if !errors.Is(e, ErrStale) || !reflect.DeepEqual(page, SavedViewExecutionPage{}) {
			t.Fatal("resource cursor drift", page, e)
		}
		in.After = nil
		f.exec(t, `UPDATE saved_resources SET definition=? WHERE id='view-00'`, strings.Replace(savedViewTestDefinition, `"asc"`, `"desc"`, 1))
		page, _, e = savedViewExecute(t, f, r, in)
		if e != nil || page.Page.Items[0].ID != 1100 {
			t.Fatal("caller order overrode stored direction", page, e)
		}
	})
	savedViewRun(t, "stored-library-authorization", func(t *testing.T) {
		f, r := savedViewFixture(t, 1)
		f.exec(t, `UPDATE saved_resources SET definition=? WHERE id='view-00'`, strings.Replace(savedViewTestDefinition, "movies", "other", 1))
		in := SavedViewExecutionInput{View: f.input, Resource: "view-00"}
		in.View.Library = 0
		in.View.Kind = 0
		page, _, e := savedViewExecute(t, f, r, in)
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, SavedViewExecutionPage{}) {
			t.Fatal("stored library bypassed current browse authorization", page, e)
		}
		f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
		page, _, e = savedViewExecute(t, f, r, in)
		if e != nil || len(page.Page.Items) != 1 || page.Page.Items[0].ID != 1040 {
			t.Fatal("actual stored library not resolved", page, e)
		}
	})

	savedViewRun(t, "definition-drift-without-revision", func(t *testing.T) {
		f, r := savedViewFixture(t, 1)
		in := SavedViewExecutionInput{View: f.input, Resource: "view-00"}
		in.View.Limit = 1
		replacement := strings.Replace(savedViewTestDefinition, `"query":null`, `"query":{"variant":"predicate","field":"title","operator":"equals","value":"Title 2"}`, 1)
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 3 {
				f.exec(t, `UPDATE saved_resources SET definition=? WHERE id='view-00'`, replacement)
			}
		}
		r.snapshots = hook
		page, cost, e := savedViewExecute(t, f, r, in)
		if e != nil || cost.Attempts != 2 || len(page.Page.Items) != 1 || page.Page.Items[0].ID != 2 {
			t.Fatal("stale definition published", page, cost, e)
		}
		savedViewSameDeadline(t, hook)
	})
	t.Run("current-share-and-birth", func(t *testing.T) {
		for _, birth := range []bool{false, true} {
			savedViewRun(t, fmt.Sprint(birth), func(t *testing.T) {
				f, r := savedViewFixture(t, 1)
				f.exec(t, `UPDATE saved_resources SET owner_account='foreign',visibility='server' WHERE id='view-00'`)
				f.exec(t, `INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES('view-00','local','a','p','viewer')`)
				hook := &savedViewWindowHook{base: f.snapshots}
				hook.hook = func(n int) {
					if n == 3 {
						if birth {
							f.exec(t, `UPDATE direct_profiles SET lifetime='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' WHERE id='p'`)
						} else {
							f.exec(t, `DELETE FROM saved_resource_shares WHERE resource_id='view-00'`)
						}
					}
				}
				r.snapshots = hook
				page, cost, e := savedViewExecute(t, f, r, SavedViewExecutionInput{View: f.input, Resource: "view-00"})
				if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, SavedViewExecutionPage{}) {
					t.Fatal("revoked resource/principal published", page, cost, e)
				}
				savedViewSameDeadline(t, hook)
			})
		}
	})
	savedViewRun(t, "cumulative-copy-budget", func(t *testing.T) {
		f, r := savedViewFixture(t, 1)
		f.exec(t, `UPDATE catalog_entities SET title=printf('%04d',id)||?,sort_key=printf('%04d',id)||?,sort_key_keep_articles=printf('%04d',id)||? WHERE id<=64`, strings.Repeat("X", 10<<10), strings.Repeat("x", 10<<10), strings.Repeat("x", 10<<10))
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 3 {
				f.exec(t, `UPDATE saved_resources SET revision=revision+1 WHERE id='view-00'`)
			}
		}
		r.snapshots = hook
		page, cost, e := savedViewExecute(t, f, r, SavedViewExecutionInput{View: f.input, Resource: "view-00"})
		if !errors.Is(e, governor.ErrSnapshotBudget) || cost.Attempts != 2 || !reflect.DeepEqual(page, SavedViewExecutionPage{}) {
			t.Fatal("copy allowance reset across retry", page, cost, e)
		}
		savedViewSameDeadline(t, hook)
	})
	savedViewRun(t, "preflight-before-wide-payload", func(t *testing.T) {
		f, r := savedViewFixture(t, 1)
		f.exec(t, `UPDATE saved_resources SET name=? WHERE id='view-00'`, strings.Repeat("N", 2<<20))
		page, cost, e := savedViewExecute(t, f, r, SavedViewExecutionInput{View: f.input, Resource: "view-00"})
		if !errors.Is(e, governor.ErrSnapshotBudget) || cost.LookupStatements != 4 || !reflect.DeepEqual(page, SavedViewExecutionPage{}) {
			t.Fatal("wide copy before actual byte admission", page, cost, e)
		}
	})
}

func savedViewDirectoryOracle(t *testing.T, f *browseFixture, pinned bool) []string {
	t.Helper()
	order := "lower(r.name)"
	extra := ""
	if pinned {
		order = "printf('%08d',COALESCE(o.position,99999999))||char(31)||lower(r.name)"
		extra = " AND p.pinned=1"
	}
	rows, e := f.conn.QueryContext(t.Context(), `SELECT r.id FROM saved_resources r
 LEFT JOIN saved_resource_shares s ON s.resource_id=r.id AND s.authority='local' AND s.account_id='a' AND s.profile_id='p'
 LEFT JOIN saved_pins p ON p.owner_key=? AND p.kind=r.kind AND p.resource_id=r.id
 LEFT JOIN saved_pin_order o ON o.owner_key=p.owner_key AND o.kind=r.kind AND o.resource_id=r.id
 WHERE r.kind='view' AND r.deleted=0 AND ((r.owner_authority='local' AND r.owner_account='a' AND r.owner_profile='p') OR s.role IN('viewer','editor'))`+extra+` ORDER BY `+order+`,r.id`, savedViewTestKey())
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		ids = append(ids, id)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return ids
}
func TestRBSavedViewDirectory(t *testing.T) {
	savedViewRun(t, "complete-public-order-and-pagination", func(t *testing.T) {
		f, r := savedViewFixture(t, 8)
		names := []string{"Zulu", "alpha", "A\x00B", "a\x00a", "Éclair", "_prefix", "alpha", "SECRET"}
		for n, name := range names {
			f.exec(t, `UPDATE saved_resources SET name=? WHERE id=?`, name, fmt.Sprintf("view-%02d", n))
		}
		f.exec(t, `UPDATE saved_resources SET owner_account='foreign',visibility='server' WHERE id IN('view-06','view-07')`)
		f.exec(t, `UPDATE saved_resources SET definition=? WHERE id='view-07'`, strings.Repeat("SECRET", 500000))
		f.exec(t, `INSERT INTO saved_resource_shares(resource_id,authority,account_id,profile_id,role) VALUES('view-06','local','a','p','viewer')`)
		positions := []int64{100000000, -1, 1, 99999999, 1000000000, -9223372036854775808, 0}
		for n, pos := range positions {
			id := fmt.Sprintf("view-%02d", n)
			pinned := 1
			if n == 6 {
				pinned = 0
			}
			f.exec(t, `INSERT INTO saved_pins(owner_key,kind,resource_id,pinned,revision) VALUES(?,'view',?,?,1)`, savedViewTestKey(), id, pinned)
			if n != 3 {
				f.exec(t, `INSERT INTO saved_pin_order(owner_key,kind,resource_id,position) VALUES(?,'view',?,?)`, savedViewTestKey(), id, pos)
			}
		}
		for _, pinned := range []bool{false, true} {
			in := SavedViewDirectoryInput{View: f.input, Pinned: pinned}
			in.View.Limit = 2
			var got []string
			var first *SavedViewDirectoryCursor
			for n := 0; n < 10; n++ {
				page, cost, e := savedViewDirectory(t, f, r, in)
				if e != nil || page.Pending || !page.SourceExhausted || page.RawRows != 8 || cost.Attempts != 1 {
					t.Fatal(page, cost, e)
				}
				for _, item := range page.Items {
					if strings.Contains(item.Name, "SECRET") {
						t.Fatal("foreign private name", item)
					}
					got = append(got, item.ID)
				}
				if first == nil {
					first = page.Next
				}
				if page.Exhausted {
					if page.Next != nil {
						t.Fatal(page)
					}
					break
				}
				if page.Next == nil {
					t.Fatal("lost exact cursor", page)
				}
				in.After = page.Next
			}
			if want := savedViewDirectoryOracle(t, f, pinned); !reflect.DeepEqual(got, want) {
				t.Fatal("public ordering drift", pinned, got, want)
			}
			if first == nil {
				t.Fatal("missing cursor")
			}
			f.exec(t, `UPDATE saved_pin_order SET position=position+1 WHERE resource_id='view-02'`)
			in.After = first
			page, _, e := savedViewDirectory(t, f, r, in)
			if !errors.Is(e, ErrStale) || !reflect.DeepEqual(page, SavedViewDirectoryPage{}) {
				t.Fatal("changed pin/resource cursor", page, e)
			}
		}
		data, e := os.ReadFile("rb_saved_view_publication_test.go")
		if e != nil {
			t.Fatal(e)
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
			if stats == plan.Analyzed {
				f.exec(t, "ANALYZE")
			}
			captureBrowse(t, f, rbSavedViewDefinitions(), stats, hash)
		}
	})
	savedViewRun(t, "principal-only-no-library-anchor", func(t *testing.T) {
		f, r := savedViewFixture(t, 1)
		f.exec(t, `UPDATE direct_memberships SET allowed_libraries='[]' WHERE account_id='a'`)
		in := SavedViewDirectoryInput{View: f.input}
		in.View.Library = 0
		in.View.Kind = 0
		page, _, e := savedViewDirectory(t, f, r, in)
		if e != nil || page.Pending || !page.SourceExhausted || !page.Exhausted || len(page.Items) != 1 || page.Items[0].ID != "view-00" {
			t.Fatal("fabricated catalogue anchor required", page, e)
		}
	})

	savedViewRun(t, "larger-source-is-pending", func(t *testing.T) {
		f, r := savedViewFixture(t, savedViewDirectoryCapacity+1)
		p, cost, e := savedViewDirectory(t, f, r, SavedViewDirectoryInput{View: f.input})
		if e != nil || !p.Pending || p.SourceExhausted || p.Exhausted || p.Next != nil || len(p.Items) != 0 || p.RawRows != 17 || p.RawPositions != 17 || cost.LookupStatements != 4 || cost.PublicationStatements != 4 {
			t.Fatal("truncated exact success", p, cost, e)
		}
	})
	savedViewRun(t, "membership-crosses-bound-before-publication", func(t *testing.T) {
		f, r := savedViewFixture(t, savedViewDirectoryCapacity)
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				savedViewAdd(t, f, "new-view", "new", "a", savedViewTestDefinition)
			}
		}
		r.snapshots = hook
		p, cost, e := savedViewDirectory(t, f, r, SavedViewDirectoryInput{View: f.input})
		if e != nil || cost.Attempts != 2 || !p.Pending || p.SourceExhausted || p.Exhausted || len(p.Items) != 0 || p.Next != nil {
			t.Fatal("old bounded universe published", p, cost, e)
		}
		savedViewSameDeadline(t, hook)
		for _, memory := range hook.memory {
			if memory != 6<<20 {
				t.Fatal("forecast changed original 4 MiB copy allowance", hook.memory)
			}
		}
	})
	savedViewRun(t, "current-pin-facts-without-generation", func(t *testing.T) {
		f, r := savedViewFixture(t, 2)
		for n := 0; n < 2; n++ {
			id := fmt.Sprintf("view-%02d", n)
			f.exec(t, `INSERT INTO saved_pins(owner_key,kind,resource_id,pinned) VALUES(?,'view',?,1)`, savedViewTestKey(), id)
			f.exec(t, `INSERT INTO saved_pin_order(owner_key,kind,resource_id,position) VALUES(?,'view',?,?)`, savedViewTestKey(), id, n)
		}
		hook := &savedViewWindowHook{base: f.snapshots}
		hook.hook = func(n int) {
			if n == 2 {
				f.exec(t, `UPDATE saved_pin_order SET position=-1 WHERE resource_id='view-01'`)
			}
		}
		r.snapshots = hook
		page, cost, e := savedViewDirectory(t, f, r, SavedViewDirectoryInput{View: f.input, Pinned: true})
		if e != nil || cost.Attempts != 2 || len(page.Items) != 2 || page.Items[0].ID != "view-01" {
			t.Fatal("stale pin order published", page, cost, e)
		}
		savedViewSameDeadline(t, hook)
	})
}

func savedViewRun(t *testing.T, name string, fn func(*testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) { synctest.Test(t, fn) })
}

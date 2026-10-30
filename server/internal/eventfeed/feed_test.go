package eventfeed

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestCommittedNotificationScopeAndRollback(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hub := New(db)
	member := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "one"}}
	other := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "two"}}
	put := func(scope string, commit bool) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, err = notify.Bump(tx, time.Now().UnixMilli(), scope, "profile")
		if err != nil {
			t.Fatal(err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	put(operations.ViewerKey(member), false)
	page, err := hub.readAt(context.Background(), member, false, 0, true)
	if err != nil || len(page.Events) != 0 {
		t.Fatalf("rolled-back notification was visible: %+v %v", page, err)
	}
	put(operations.ViewerKey(other), true)
	put(operations.ViewerKey(member), true)
	page, err = hub.readAt(context.Background(), member, false, 0, true)
	if err != nil || len(page.Events) != 1 || page.Events[0].Type != "notification.changed" || page.Events[0].Resource.ID != "inbox" {
		t.Fatalf("member-scope events: %+v %v", page, err)
	}
	if page.NextAfter != "2" {
		t.Fatalf("cursor failed to advance over invisible event: %q", page.NextAfter)
	}
	if _, err = db.Exec(`DELETE FROM api_events WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	page, err = hub.readAt(context.Background(), member, false, 0, true)
	if err != nil || len(page.Events) != 1 || page.Events[0].Type != "stream.resync" {
		t.Fatalf("pruned cursor needs resync: %+v %v", page, err)
	}
}

func TestHubWakesOnCommittedOutboxWrite(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake, release := h.Subscribe()
	defer release()
	started := make(chan struct{})
	go func() {
		close(started)
		h.Run(ctx)
	}()
	<-started
	_, err = dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO api_events(audience,type,at_ms) VALUES('server','settings.changed',?)`, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	case <-time.After(3 * time.Second):
		t.Fatal("committed outbox write did not wake subscribers")
	}
}

func TestStreamReplacementCancelsOldReader(t *testing.T) {
	h := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release, ok := h.AdmitStream("installation", cancel)
	if !ok {
		t.Fatal("first stream refused")
	}
	defer release()
	// A reconnect takes over: it is admitted, and the stream it replaces ends.
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	releaseSecond, ok := h.AdmitStream("installation", cancelSecond)
	if !ok {
		t.Fatal("a reconnecting stream was refused")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("old stream stayed live")
	}
	// The retired stream's release must not evict its successor.
	release()
	if second.Err() != nil {
		t.Fatal("the new stream was cancelled")
	}
	third, cancelThird := context.WithCancel(context.Background())
	defer cancelThird()
	if _, ok = h.AdmitStream("installation", cancelThird); !ok {
		t.Fatal("a third stream was refused")
	}
	select {
	case <-second.Done():
	case <-time.After(time.Second):
		t.Fatal("the second stream was not retired by the third")
	}
	releaseSecond()
	if third.Err() != nil {
		t.Fatal("releasing a retired stream cancelled the live one")
	}
	if _, ok = h.AdmitStream("", cancelThird); ok {
		t.Fatal("a stream with no installation was admitted")
	}
}

func TestOperationEventsRequireLiveOwnerAuthority(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO console_operations(id,kind,resource,actor,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision)
		 VALUES('op1','library.scan','library','actor','owner','queued','waiting',1,0,1,1,1,'','','',1)`); err != nil {
			return err
		}
		return operations.AppendOperationTx(tx, "op1")
	})
	if err != nil {
		t.Fatal(err)
	}
	h := New(db)
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "one"}}
	member, err := h.readAt(context.Background(), p, false, 0, true)
	if err != nil || len(member.Events) != 0 || member.NextAfter != "1" {
		t.Fatalf("member operation visibility: %+v %v", member, err)
	}
	owner, err := h.readAt(context.Background(), p, true, 0, true)
	if err != nil || len(owner.Events) != 1 || owner.Events[0].Type != "operation.updated" || owner.Events[0].Resource.ID != "op1" {
		t.Fatalf("owner operation visibility: %+v %v", owner, err)
	}
}

func appendLibraryEvent(t *testing.T, db *sql.DB, library, state string, found int64) {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		return apievents.Append(tx, apievents.LibraryAudience(library), "library.scan.updated", "library", library, strconv.FormatInt(time.Now().UnixMilli(), 10), map[string]any{"state": state, "found": found})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFeedDeliversLibraryEventsOnlyToViewersWhoSeeTheLibrary(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	appendLibraryEvent(t, db, "A", "started", 10)
	appendLibraryEvent(t, db, "B", "started", 20)
	h := New(db)
	h.LibraryVisible = func(tx *sql.Tx, p identity.Principal, library string) bool {
		if library == "A" {
			return true
		}
		if library == "B" {
			return p.ProfileID == "one"
		}
		return false
	}
	first := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "one"}}
	second := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "two"}}
	gotFirst, err := h.readAt(context.Background(), first, false, 0, true)
	if err != nil || len(gotFirst.Events) != 2 {
		t.Fatalf("first viewer library visibility: %+v %v", gotFirst, err)
	}
	gotSecond, err := h.readAt(context.Background(), second, false, 0, true)
	if err != nil || len(gotSecond.Events) != 1 {
		t.Fatalf("second viewer library visibility: %+v %v", gotSecond, err)
	}
	if gotSecond.Events[0].Resource.ID != "A" || gotSecond.Events[0].Type != "library.scan.updated" {
		t.Fatalf("second viewer received wrong event: %+v", gotSecond.Events[0])
	}
	for _, e := range gotSecond.Events {
		if e.Resource.ID == "B" {
			t.Fatalf("second viewer received hidden library B event: %+v", gotSecond)
		}
	}
}

func TestFeedPagingWithHiddenLibraryEvents(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 150; i++ {
		library := "A"
		if i%2 == 1 {
			library = "B"
		}
		appendLibraryEvent(t, db, library, "progress", int64(i))
	}
	h := New(db)
	h.LibraryVisible = func(tx *sql.Tx, p identity.Principal, library string) bool {
		return library == "A"
	}
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "one"}}
	var maximum int64
	if err = db.QueryRow(`SELECT max(id) FROM api_events`).Scan(&maximum); err != nil {
		t.Fatal(err)
	}
	// More than a page of raw rows behind: one resync at the newest id, and
	// nothing of either library.
	for _, behind := range []int64{0, maximum - PageLimit - 1} {
		page, err := h.readAt(context.Background(), p, false, behind, true)
		if err != nil || len(page.Events) != 1 || page.Events[0].Type != "stream.resync" || page.Events[0].Resource.ID != "all" || page.NextAfter != strconv.FormatInt(maximum, 10) || page.More {
			t.Fatalf("after %d: want one resync at %d, got %+v %v", behind, maximum, page, err)
		}
	}
	seen := map[string]bool{}
	var collected []string
	// Exactly a page of raw rows behind still pages, skipping the hidden half.
	after := maximum - PageLimit
	pages := 0
	for {
		page, err := h.readAt(context.Background(), p, false, after, true)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if pages > 10 {
			t.Fatalf("paging did not terminate: after=%d next=%s collected=%d", after, page.NextAfter, len(collected))
		}
		next, err := strconv.ParseInt(page.NextAfter, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if next <= after {
			t.Fatalf("NextAfter did not advance past hidden rows: after=%d next=%d", after, next)
		}
		for _, e := range page.Events {
			if seen[e.ID] {
				t.Fatalf("repeated visible event %s", e.ID)
			}
			seen[e.ID] = true
			collected = append(collected, e.ID)
			if e.Resource.ID != "A" {
				t.Fatalf("hidden library event leaked: %+v", e)
			}
		}
		after = next
		if after >= maximum {
			if page.NextAfter != strconv.FormatInt(maximum, 10) {
				t.Fatalf("last page must end at newest id %d, got %s", maximum, page.NextAfter)
			}
			break
		}
		if len(page.Events) == 0 && after >= maximum {
			break
		}
	}
	if len(collected) != PageLimit/2 || pages != 1 {
		t.Fatalf("visible count: got %d in %d pages, want %d in one", len(collected), pages, PageLimit/2)
	}
	for i := 1; i < len(collected); i++ {
		prev, _ := strconv.ParseInt(collected[i-1], 10, 64)
		cur, _ := strconv.ParseInt(collected[i], 10, 64)
		if cur <= prev {
			t.Fatalf("visible events out of order or skipped: %v", collected)
		}
	}
}

func TestEventBoundsUseIndexedEndpoints(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE api_events(id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatal(err)
	}
	// The actual production driver must keep both endpoints indexed, regardless
	// of ring size. The old combined aggregate produces SCAN api_events.
	rows, err := db.Query(`EXPLAIN QUERY PLAN ` + eventBoundsSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	searches := 0
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "SCAN api_events") {
			t.Fatalf("ring scan: %s", detail)
		}
		if strings.Contains(detail, "SEARCH api_events") {
			searches++
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if searches != 1 {
		t.Fatalf("want one indexed lower endpoint probe, got %d", searches)
	}
	var low, high int64
	if err = db.QueryRow(eventBoundsSQL).Scan(&low, &high); err != nil || low != 0 || high != 0 {
		t.Fatalf("empty bounds: %d %d %v", low, high, err)
	}
	if _, err = db.Exec(`INSERT INTO api_events VALUES(17),(19001)`); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(eventBoundsSQL).Scan(&low, &high); err != nil || low != 17 || high != 19001 {
		t.Fatalf("sparse bounds: %d %d %v", low, high, err)
	}
}

func TestLibraryVisibilityCheckedOncePerSnapshotPage(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		for i := 0; i < 80; i++ {
			for _, library := range []string{"visible", "hidden"} {
				if e := apievents.Append(tx, apievents.LibraryAudience(library), "library.scan.updated", "library", library, strconv.Itoa(i), nil); e != nil {
					return e
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := New(db)
	calls := map[string]int{}
	h.LibraryVisible = func(tx *sql.Tx, p identity.Principal, library string) bool {
		calls[library]++
		return library == "visible"
	}
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "member", ProfileID: "one"}}
	// A resync costs no visibility check at all.
	behind, err := h.readAt(context.Background(), p, false, 0, true)
	if err != nil || len(behind.Events) != 1 || behind.Events[0].Type != "stream.resync" || len(calls) != 0 {
		t.Fatalf("far-behind read: %+v calls=%v err=%v", behind, calls, err)
	}
	first, err := h.readAt(context.Background(), p, false, 160-PageLimit, true)
	if err != nil || len(first.Events) != PageLimit/2 || first.NextAfter != "160" || first.More {
		t.Fatalf("mixed page: events=%d cursor=%s more=%v err=%v", len(first.Events), first.NextAfter, first.More, err)
	}
	if calls["visible"] != 1 || calls["hidden"] != 1 {
		t.Fatalf("repeated checks within snapshot: %v", calls)
	}
	second, err := h.readAt(context.Background(), p, false, 160-PageLimit, true)
	if err != nil || len(second.Events) != PageLimit/2 || second.NextAfter != "160" {
		t.Fatalf("repeated mixed page: events=%d cursor=%s err=%v", len(second.Events), second.NextAfter, err)
	}
	if calls["visible"] != 2 || calls["hidden"] != 2 {
		t.Fatalf("verdict escaped snapshot boundary: %v", calls)
	}
}

func BenchmarkEventBounds(b *testing.B) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE api_events(id INTEGER PRIMARY KEY AUTOINCREMENT); WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<20000) INSERT INTO api_events SELECT i FROM n`); err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct{ name, query string }{
		{"combined_scan", `SELECT COALESCE(min(id),0),COALESCE(max(id),0) FROM api_events`},
		{"indexed_endpoints", eventBoundsSQL},
	} {
		b.Run(tc.name, func(b *testing.B) {
			var low, high int64
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := db.QueryRow(tc.query).Scan(&low, &high); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

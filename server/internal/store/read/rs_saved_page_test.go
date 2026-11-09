package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed rs_saved_page_test.go
var rsSavedMembersJournal string

func rsSavedMembersHash(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(browsePlanFixtureHash(t)+rsSavedMembersJournal)))
}

func rsSavedMembersConfig() governor.Config {
	c := readfixture.LiveConfig(time.Now)
	c.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}
	for i := range c.Partitions {
		c.Partitions[i][governor.MemoryBytes] = 128 << 20
	}
	return c
}

func newRSSavedMembersSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	f.exec(t, `INSERT INTO saved_resource_generation(id,revision)VALUES(1,1)`)
	f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at)VALUES('saved','collection','owner','local','a','p','Zulu','now'),('shared','collection','foreign','local','other','other','Alpha','now'),('server','collection','foreign','local','other','other','Beta','now'),('foreign','collection','foreign','local','x','x','Foreign','now'),('view','view','foreign','local','a','p','Forbidden view','now'),('deleted','collection','foreign','local','a','p','Forbidden deleted','now')`)
	f.exec(t, `UPDATE saved_resources SET visibility='server' WHERE id='server'`)
	f.exec(t, `UPDATE saved_resources SET deleted=1 WHERE id='deleted'`)
	f.exec(t, `INSERT INTO saved_resource_shares VALUES('shared','local','a','p','editor'),('saved','local','a','p','viewer'),('view','local','a','p','viewer')`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('e1','saved',1,'a'),('e2','saved',2,'a'),('e3','saved',3,'b'),('retired','saved',9,'c'),('noitem','saved',NULL,'d'),('shared1','shared',3,'a'),('wrong-lib','shared',1040,'b'),('server1','server',1,'a')`)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=8 WHERE id IN(1,3,1040)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=2`)
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries='["movies"]' WHERE id='p'`)
	return f
}

func newRSSavedMembersFixture(t *testing.T) (*browseFixture, *SavedCollectionReader) {
	t.Helper()
	f := composeReadFixture(t, newRSSavedMembersSource(t), SavedCollectionHarnessDefinitions(), rsSavedMembersConfig(), false, rsSavedMembersHash(t))
	r, e := NewSavedCollectionReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

func savedMembersComplete(t *testing.T, f *browseFixture, r *SavedCollectionReader, in SavedCollectionInput) SavedCollectionPage {
	t.Helper()
	p, ref, e := r.BeginMembers(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	limit := in.View.Limit
	for poll := 0; poll < 300; poll++ {
		fg, req, out := liveRequests("saved-member-poll")
		var page SavedCollectionPage
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			var e error
			page, _, e = r.Members(ctx, req, time.Second, out, in)
			return e
		})
		if e != nil {
			t.Fatalf("poll%d stage%d: %v", poll, p.stage, e)
		}
		if page.Pending {
			if len(page.Items) != 0 || len(page.Resources) != 0 || page.Next != nil || page.Total != 0 {
				t.Fatal("partial saved member page", page)
			}
			continue
		}
		if len(page.Entries) > limit || len(page.Items) > limit {
			t.Fatal("member page exceeded its cap", len(page.Entries), limit)
		}
		return page
	}
	t.Fatal("saved members did not finish")
	return SavedCollectionPage{}
}

func savedMembersError(t *testing.T, f *browseFixture, r *SavedCollectionReader, in SavedCollectionInput) error {
	t.Helper()
	fg, req, out := liveRequests("saved-member-error")
	var err error
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
		page, _, e := r.Members(ctx, req, time.Second, out, in)
		if e == nil {
			t.Fatal("expected saved member refusal, got", page)
		}
		if page.Pending || len(page.Entries) != 0 || len(page.Resources) != 0 || page.Total != 0 {
			t.Fatal("refused saved member page released data", page)
		}
		return e
	})
	return err
}

func TestRSSavedMembersPopulatedPages(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	in := SavedCollectionInput{View: f.input, Resource: "saved"}
	in.View.Limit = 1
	first := savedMembersComplete(t, f, r, in)
	if first.Total != 2 || len(first.Entries) != 1 || first.Entries[0].Item.ID != 1 || first.Next == nil || first.Exhausted || len(first.Resources) != 1 || first.Resources[0].Role != "owner" || first.Resources[0].Name != "Zulu" || first.Resources[0].EntryCount != 2 {
		t.Fatal(first)
	}
	in.After = first.Next
	second := savedMembersComplete(t, f, r, in)
	if second.Total != 2 || len(second.Entries) != 1 || second.Entries[0].Item.ID != 3 || second.Next != nil || !second.Exhausted {
		t.Fatal(second)
	}
	// Independent plain-SQL oracle over the same current subject tuple,
	// permitted library and authored order.
	rows, e := f.conn.QueryContext(t.Context(), `SELECT m.item_id FROM saved_resource_entries m JOIN catalog_entities e ON e.id=m.item_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id WHERE m.resource_id='saved' AND e.retired=0 AND e.admission_own_age IS NOT NULL AND e.admission_own_age<=12 AND l.name='Movies' ORDER BY m.order_key,m.id`)
	if e != nil {
		t.Fatal(e)
	}
	var want []int64
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		want = append(want, id)
	}
	rows.Close()
	got := []int64{first.Entries[0].Item.ID, second.Entries[0].Item.ID}
	if !slices.Equal(got, want) {
		t.Fatal("independent member oracle", got, want)
	}
	shared := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "shared"})
	if shared.Total != 1 || len(shared.Entries) != 1 || shared.Entries[0].Item.ID != 3 || shared.Resources[0].Role != "editor" {
		t.Fatal("share/library proof", shared)
	}
	server := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "server"})
	if server.Total != 1 || len(server.Entries) != 1 || server.Entries[0].Item.ID != 1 || server.Resources[0].Role != "viewer" {
		t.Fatal("server visibility proof", server)
	}
}

func TestRSSavedMembersWrongResourceKindDenied(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	for _, resource := range []string{"view", "deleted", "foreign"} {
		e := savedMembersError(t, f, r, SavedCollectionInput{View: f.input, Resource: resource})
		if !errors.Is(e, ErrDenied) {
			t.Fatalf("%s: %v", resource, e)
		}
	}
}

func TestRSSavedMembersSparseAllHidden(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	f.exec(t, `DELETE FROM saved_resource_entries WHERE resource_id='saved'`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('hidden','saved',2,'a'),('retired','saved',9,'b')`)
	page := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "saved"})
	if page.Total != 0 || len(page.Entries) != 0 || !page.Exhausted || page.Next != nil || len(page.Resources) != 1 || page.Resources[0].EntryCount != 0 {
		t.Fatal("all-hidden page", page)
	}
}

func TestRSSavedMembersChangedEntriesAndSource(t *testing.T) {
	t.Run("generation", func(t *testing.T) {
		f, r := newRSSavedMembersFixture(t)
		in := SavedCollectionInput{View: f.input, Resource: "saved"}
		in.View.Limit = 1
		first := savedMembersComplete(t, f, r, in)
		f.exec(t, `UPDATE saved_resource_generation SET revision=2 WHERE id=1`)
		in.After = first.Next
		if e := savedMembersError(t, f, r, in); !errors.Is(e, ErrStale) {
			t.Fatal("changed generation", e)
		}
	})
	t.Run("entries", func(t *testing.T) {
		f, r := newRSSavedMembersFixture(t)
		in := SavedCollectionInput{View: f.input, Resource: "saved"}
		in.View.Limit = 1
		first := savedMembersComplete(t, f, r, in)
		f.exec(t, `UPDATE saved_resource_entries SET item_id=4 WHERE id='e3'`)
		in.After = first.Next
		if e := savedMembersError(t, f, r, in); !errors.Is(e, ErrStale) {
			t.Fatal("changed member order", e)
		}
	})
}

func TestRSSavedMembersNoLibraryPrincipal(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries='[]' WHERE id='p'`)
	page := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "saved"})
	if page.Total != 0 || len(page.Entries) != 0 || !page.Exhausted || len(page.Resources) != 1 || page.Resources[0].Name != "Zulu" {
		t.Fatal("no-library member page", page)
	}
}

func TestRSSavedMembersMixedLibrariesReauthorize(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries='["movies","other"]' WHERE id='p'`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('other-lib','saved',1040,'e')`)
	page := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "saved"})
	if page.Total != 3 || len(page.Entries) != 3 {
		t.Fatal("mixed libraries", page.Total, len(page.Entries))
	}
	if page.Entries[0].Item.ID != 1 || page.Entries[1].Item.ID != 3 || page.Entries[2].Item.ID != 1040 || page.Entries[2].Library != "Other" {
		t.Fatal("mixed library order/names", page.Entries)
	}
}

func TestRSSavedMembersAutomaticCollectionPending(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening)VALUES(10,'collection',0,1,1,1,0)`)
	public := []byte{0xe3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 4, 0x4d}
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(1301,?,1,10)`, public)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,admission_own_age,admission_member_count)VALUES(1301,?,1,10,'Automatic','auto','a','auto','a',0,?,?,0,0)`, public, public, public)
	f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id)VALUES(1,'fixture','C',1301)`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('automatic','saved',1301,'f')`)
	// The complete automatic reducer is a separate task. Until it is composed
	// beside this file, the merged authored reducer refuses the automatic root
	// explicitly; it must never stand in as an authored empty collection.
	if e := savedMembersError(t, f, r, SavedCollectionInput{View: f.input, Resource: "saved"}); !errors.Is(e, ErrUnqualified) {
		t.Fatal("automatic collection must stay explicit", e)
	}
}

type rsSavedFinalSnapshots struct {
	*governor.Snapshots
	ready func() bool
	after func()
	fired bool
}

func (b *rsSavedFinalSnapshots) Read(ctx context.Context, r governor.Request, d time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s, e := b.Snapshots.Read(ctx, r, d, fn)
	if e == nil && b.ready() && !b.fired {
		b.fired = true
		b.after()
	}
	return s, e
}
func (b *rsSavedFinalSnapshots) ReadForecasted(ctx context.Context, r governor.Request, d time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s, e := b.Snapshots.ReadForecasted(ctx, r, d, f, fn)
	if e == nil && b.ready() && !b.fired {
		b.fired = true
		b.after()
	}
	return s, e
}

func TestRSSavedMembersFreshFinalDenial(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	in := SavedCollectionInput{View: f.input, Resource: "saved"}
	p, ref, e := r.BeginMembers(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	b := &rsSavedFinalSnapshots{Snapshots: f.snapshots, ready: func() bool { return p.ready }, after: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	r.snapshots = b
	for poll := 0; poll < 200; poll++ {
		fg, req, out := liveRequests("saved-final-denial")
		var page SavedCollectionPage
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			var e error
			page, _, e = r.Members(ctx, req, time.Second, out, in)
			return e
		})
		if e != nil {
			if !b.fired || !errors.Is(e, ErrDenied) || len(page.Items) != 0 || len(page.Resources) != 0 || page.Pending {
				t.Fatal("final denial", page, e, b.fired)
			}
			return
		}
		if !page.Pending {
			t.Fatal("revoked private draft escaped", page)
		}
	}
	t.Fatal("did not reach final boundary")
}

type rsSavedHeldSnapshots struct {
	*governor.Snapshots
	entered, release chan struct{}
	once             sync.Once
}

func (b *rsSavedHeldSnapshots) hold() { b.once.Do(func() { close(b.entered); <-b.release }) }
func (b *rsSavedHeldSnapshots) Read(ctx context.Context, r governor.Request, d time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	b.hold()
	return b.Snapshots.Read(ctx, r, d, fn)
}
func (b *rsSavedHeldSnapshots) ReadForecasted(ctx context.Context, r governor.Request, d time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	b.hold()
	return b.Snapshots.ReadForecasted(ctx, r, d, f, fn)
}

func TestRSSavedMembersCloseJoinsPoll(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	in := SavedCollectionInput{View: f.input, Resource: "saved"}
	p, ref, e := r.BeginMembers(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	held := &rsSavedHeldSnapshots{Snapshots: f.snapshots, entered: make(chan struct{}), release: make(chan struct{})}
	r.snapshots = held
	finished := make(chan error, 1)
	go func() {
		fg, req, out := liveRequests("saved-held")
		_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			_, _, e := r.Members(ctx, req, time.Second, out, in)
			return e
		})
		finished <- e
	}()
	<-held.entered
	started := make(chan struct{})
	closed := make(chan error, 1)
	go func() { close(started); closed <- p.Close() }()
	<-started
	select {
	case e := <-closed:
		close(held.release)
		<-finished
		t.Fatal("retired while poll still active", e)
	default:
	}
	close(held.release)
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	if !p.closed || p.records != nil || p.entries != nil || p.source != nil || p.resource != "" {
		t.Fatal("retained closed values")
	}
}

func TestRSSavedMembersBytePreflightRefusesPayload(t *testing.T) {
	f, _ := newRSSavedMembersFixture(t)
	for _, c := range []struct {
		name string
		args []any
	}{
		{"metadata", []any{"saved"}},
		{"entry", []any{int64(1), "saved"}},
		{"card", []any{"p", int64(1), int64(1), int64(1)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, err := f.snapshots.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2, GoCopyBytes: 64 << 10}, func(scope *governor.ReadScope) error {
				rows, _, e := queryReadScope(scope, "rs.saved.members."+c.name, c.args...)
				if !errors.Is(e, governor.ErrSnapshotBudget) || rows != nil {
					t.Fatal("wide payload decoded before byte admission", rows, e)
				}
				return e
			})
			if !errors.Is(err, governor.ErrSnapshotBudget) || result.Refusal != nil {
				t.Fatal(result, err)
			}
		})
	}
}

func TestRSSavedMembersWideText(t *testing.T) {
	f, r := newRSSavedMembersFixture(t)
	f.exec(t, `UPDATE catalog_entities SET title=?,overview=? WHERE id=1`, "雪\x00é", strings.Repeat("wide\x00界", 2000))
	f.exec(t, `UPDATE saved_resources SET summary=? WHERE id='saved'`, strings.Repeat("界", 4096))
	page := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "saved"})
	if page.Resources[0].Summary != strings.Repeat("界", 4096) {
		t.Fatal("wide metadata lost")
	}
	if page.Entries[0].Item.Title != "雪\x00é" || page.Entries[0].Item.Overview != strings.Repeat("wide\x00界", 2000) {
		t.Fatal("wide UTF8/NUL payload lost")
	}
}

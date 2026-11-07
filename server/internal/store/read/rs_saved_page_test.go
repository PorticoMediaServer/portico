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

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

//go:embed rs_saved_page_test.go
var rsSavedPageJournal string

func rsSavedPagesSource(t *testing.T) *browseFixture {
	f := newRSAutoSource(t)
	f.exec(t, `INSERT INTO saved_resource_generation(id,revision)VALUES(1,1)`)
	f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at)VALUES('saved','collection','ignored','local','a','p','Saved','now')`)
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p"})
	f.exec(t, `UPDATE saved_resources SET owner_key=?,name='Zulu' WHERE id='saved'`, owner)
	f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at,visibility,deleted)VALUES('shared','collection','foreign','local','other','other','Alpha','now','private',0),('server','collection','foreign','local','other','other','Beta','now','server',0),('view','view','foreign','local','other','other','Forbidden view','now','server',0),('deleted','collection','foreign','local','other','other','Forbidden deleted','now','server',1)`)
	f.exec(t, `INSERT INTO saved_resource_shares VALUES('shared','local','a','p','editor'),('saved','local','a','p','viewer'),('view','local','a','p','viewer')`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('e1','saved',1,'a'),('e2','saved',2,'a'),('e3','saved',3,'b'),('retired','saved',9,'c'),('shared1','shared',3,'a'),('wrong-lib','shared',1040,'b'),('server1','server',1,'a')`)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=8 WHERE id IN(1,3,1040)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=2`)
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries='["movies"]' WHERE id='p'`)
	return f
}
func rsSavedPageHash(t *testing.T) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(rsAutoHash(t)+rsSavedPageJournal)))
}
func newRSSavedPagesFixture(t *testing.T) (*browseFixture, *SavedCollectionReader) {
	f := composeReadFixture(t, rsSavedPagesSource(t), SavedCollectionHarnessDefinitions(), rsAutoConfig(), false, rsSavedPageHash(t))
	r, e := NewSavedCollectionReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func savedMembersComplete(t *testing.T, f *browseFixture, r *SavedCollectionReader, in SavedCollectionInput, mutate func(*SavedCollectionWork)) SavedCollectionPage {
	t.Helper()
	p, ref, e := r.BeginMembers(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	if mutate != nil {
		mutate(p)
	}
	for poll := 0; poll < 200; poll++ {
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
		if !page.Pending {
			return page
		}
		if len(page.Items) != 0 || len(page.Resources) != 0 || page.Next != nil || page.Total != 0 {
			t.Fatal("partial saved data", page)
		}
	}
	t.Fatal("saved members did not finish")
	return SavedCollectionPage{}
}
func savedDirectoryComplete(t *testing.T, f *browseFixture, r *SavedCollectionReader, in SavedCollectionDirectoryInput) SavedCollectionPage {
	t.Helper()
	p, ref, e := r.BeginDirectory(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	for poll := 0; poll < 300; poll++ {
		fg, req, out := liveRequests("saved-directory-poll")
		var page SavedCollectionPage
		_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			var e error
			page, _, e = r.DirectoryPage(ctx, req, time.Second, out, in)
			return e
		})
		if e != nil {
			t.Fatalf("directory poll%d stage%d stream%d resource%q: %v", poll, p.stage, p.stream, p.current.resource.ID, e)
		}
		if !page.Pending {
			return page
		}
		if len(page.Resources) != 0 || page.Next != nil || page.Total != 0 {
			t.Fatal("partial directory", page)
		}
	}
	t.Fatal("directory did not finish")
	return SavedCollectionPage{}
}
func TestRSSavedPagesPopulatedMembers(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	in := SavedCollectionInput{View: f.input, Resource: "saved"}
	in.View.Limit = 1
	first := savedMembersComplete(t, f, r, in, nil)
	if first.Total != 2 || len(first.Entries) != 1 || first.Entries[0].Item.ID != 1 || first.Next == nil || first.Exhausted || first.Resources[0].Role != "owner" {
		t.Fatal(first)
	}
	in.After = first.Next
	second := savedMembersComplete(t, f, r, in, nil)
	if second.Total != 2 || len(second.Entries) != 1 || second.Entries[0].Item.ID != 3 || second.Next != nil || !second.Exhausted {
		t.Fatal(second)
	}
	var want []int64
	rows, e := f.conn.QueryContext(t.Context(), `SELECT e.id FROM saved_resource_entries m JOIN catalog_entities e ON e.id=m.item_id WHERE m.resource_id='saved' AND e.retired=0 AND e.admission_own_age<=12 ORDER BY m.order_key,m.id`)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		want = append(want, id)
	}
	rows.Close()
	got := []int64{first.Items[0].ID, second.Items[0].ID}
	if !slices.Equal(got, want) {
		t.Fatal("independent member oracle", got, want)
	}
	shared := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "shared"}, nil)
	if shared.Total != 1 || len(shared.Entries) != 1 || shared.Entries[0].Item.ID != 3 || shared.Resources[0].Role != "editor" {
		t.Fatal("share/library proof", shared)
	}
}
func TestRSSavedPagesDirectoryUnion(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	in := SavedCollectionDirectoryInput{View: f.input}
	in.View.Limit = 2
	first := savedDirectoryComplete(t, f, r, in)
	var ids []string
	for _, v := range first.Resources {
		ids = append(ids, v.ID)
	}
	if !slices.Equal(ids, []string{"shared", "server"}) || first.Total != 3 || first.Posters != 3 || first.Next == nil || first.Exhausted {
		t.Fatal(first)
	}
	if first.Resources[0].EntryCount != 1 || len(first.Resources[0].Samples) != 1 || first.Resources[0].Samples[0].Item.ID != 3 {
		t.Fatal("exact shared sample/count", first.Resources[0])
	}
	in.After = first.Next
	second := savedDirectoryComplete(t, f, r, in)
	if len(second.Resources) != 1 || second.Resources[0].ID != "saved" || second.Total != 3 || second.Next != nil || !second.Exhausted {
		t.Fatal(second)
	}
	// Server-visible metadata is available even with no permitted media libraries;
	// samples and exact visible-member count become empty, never leaked.
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries='[]' WHERE id='p'`)
	empty := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	if len(empty.Resources) != 3 {
		t.Fatal("global directory requires an anchor library", empty)
	}
	for _, v := range empty.Resources {
		if v.EntryCount != 0 || len(v.Samples) != 0 {
			t.Fatal("forbidden library sample", v)
		}
	}
}
func TestRSSavedPagesNoUnadmittedGlobalScope(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	view := f.input
	view.Library = 0
	if _, _, e := f.reader.window(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, view); !errors.Is(e, ErrInput) {
		t.Fatal("default accepts global scope", e)
	}
	if _, _, e := r.Directory(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, f.input); !errors.Is(e, ErrInput) {
		t.Fatal("unadmitted directory", e)
	}
}

func TestRSSavedPagesPinnedAndSparseShape(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	owner := identity.PersonalKey(identity.Viewer{Authority: "local", AccountID: "a", ProfileID: "p"})
	f.exec(t, `INSERT INTO saved_pins(owner_key,kind,resource_id,pinned,revision)VALUES(?,'collection','saved',1,2),(?,'collection','server',1,3),(?,'collection','shared',1,4)`, owner, owner, owner)
	f.exec(t, `INSERT INTO saved_pin_order(owner_key,kind,resource_id,position)VALUES(?,'collection','saved',2),(?,'collection','server',1)`, owner, owner)
	all := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input, PinnedOnly: true})
	var ids []string
	for _, v := range all.Resources {
		ids = append(ids, v.ID)
	}
	if !slices.Equal(ids, []string{"server", "saved", "shared"}) {
		t.Fatal("viewer arrangement", ids)
	}
	in := SavedCollectionDirectoryInput{View: f.input, Shape: "squares"}
	in.View.Limit = 1
	sparse := savedDirectoryComplete(t, f, r, in)
	if len(sparse.Resources) != 0 || sparse.Next == nil || sparse.Exhausted || sparse.Posters != 3 || sparse.Total != 3 {
		t.Fatal("shape filtering invented exhaustion", sparse)
	}
	in.After = sparse.Next
	next := savedDirectoryComplete(t, f, r, in)
	if len(next.Resources) != 0 || next.Next == nil || next.Next.id == sparse.Next.id {
		t.Fatal("empty shape window did not advance", next)
	}
}

func TestRSSavedPagesDenialCapacityAndClock(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	for _, mode := range []string{"revoked", "share", "capacity", "clock", "footprint"} {
		t.Run(mode, func(t *testing.T) {
			resource := "saved"
			if mode == "share" {
				resource = "shared"
			}
			in := SavedCollectionInput{View: f.input, Resource: resource}
			p, ref, e := r.BeginMembers(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
			if e != nil || ref != nil {
				t.Fatal(e, ref)
			}
			defer p.Close()
			in.Work = p
			switch mode {
			case "revoked":
				f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
			case "share":
				f.exec(t, `DELETE FROM saved_resource_shares WHERE resource_id='shared'`)
			case "capacity":
				p.used = p.lease.PayloadBytes()
			case "clock":
				f.exec(t, `UPDATE authorization_family_tokens SET expires_at=?`, f.input.Now.Add(500*time.Millisecond).Format(time.RFC3339Nano))
				p.started = time.Now().Add(-time.Second)
			case "footprint":
				if e = f.authority.InterruptPreparations(governor.PublicationFootprint{{Library: 99999}}); e != nil {
					t.Fatal(e)
				}
			}
			fg, req, out := liveRequests("saved-negative")
			_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
				page, _, e := r.Members(ctx, req, time.Second, out, in)
				if e == nil || page.Pending || len(page.Items) > 0 || len(page.Resources) > 0 {
					t.Fatal("invalid work released data", page, e)
				}
				return e
			})
			if e == nil || !p.closed || p.records != nil || p.entries != nil || p.fence != nil || p.rawName != "" {
				t.Fatal("failed work custody", e, p.closed)
			}
		})
		f.exec(t, `UPDATE authorization_session_families SET revoked=0`)
		f.exec(t, `UPDATE authorization_family_tokens SET expires_at='2030-01-01T00:00:00Z'`)
		f.exec(t, `INSERT OR IGNORE INTO saved_resource_shares VALUES('shared','local','a','p','editor')`)
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
func TestRSSavedPagesFreshFinalDenial(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	in := SavedCollectionInput{View: f.input, Resource: "saved"}
	p, ref, e := r.BeginMembers(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	b := &rsSavedFinalSnapshots{Snapshots: f.snapshots, ready: func() bool { return p.ready }, after: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	r.snapshots = b
	for poll := 0; poll < 150; poll++ {
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

func TestRSSavedPagesActualWideTextAndNameOrder(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	names := []string{"Éclair\x00Z", "éclair\x00a", " 東京 "}
	for n, id := range []string{"shared", "server", "saved"} {
		f.exec(t, `UPDATE saved_resources SET name=?,summary=? WHERE id=?`, names[n], strings.Repeat("界", 4096), id)
	}
	f.exec(t, `UPDATE catalog_entities SET title=?,overview=? WHERE id=1`, "雪\x00é", strings.Repeat("wide\x00界", 2000))
	page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	var got []string
	for _, v := range page.Resources {
		got = append(got, v.ID)
		if v.Summary != strings.Repeat("界", 4096) {
			t.Fatal("wide metadata lost")
		}
	}
	rows, e := f.conn.QueryContext(t.Context(), `SELECT id FROM saved_resources WHERE kind='collection' AND deleted=0 ORDER BY lower(name),id`)
	if e != nil {
		t.Fatal(e)
	}
	var want []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		want = append(want, id)
	}
	rows.Close()
	if !slices.Equal(got, want) {
		t.Fatal("native lower/BINARY oracle", got, want)
	}
	members := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "saved"}, nil)
	if members.Entries[0].Item.Title != "雪\x00é" || members.Entries[0].Item.Overview != strings.Repeat("wide\x00界", 2000) {
		t.Fatal("wide UTF8/NUL payload lost")
	}
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
func TestRSSavedPagesCloseJoinsPoll(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
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
	closed := make(chan error, 1)
	go func() { closed <- p.Close() }()
	select {
	case e := <-closed:
		close(held.release)
		<-finished
		t.Fatal("retired while poll still active", e)
	case <-time.After(10 * time.Millisecond):
	}
	close(held.release)
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	if !p.closed || p.records != nil || p.entries != nil || p.source != nil || p.rawName != "" {
		t.Fatal("retained closed values")
	}
}

func TestRSSavedPagesMixedKinds(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries=NULL WHERE id='p'`)
	f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening)VALUES(5,'artist',0,1,1,1,1),(20,'ebook',0,0,1,1,0)`)
	for _, c := range []struct {
		id, library, kind int64
		title             string
	}{{13005, 1, 5, "Artist"}, {13020, 2, 20, "Reading"}} {
		public := []byte{0xe3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, byte(c.id >> 8), byte(c.id)}
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(?,?,?,?)`, c.id, public, c.library, c.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,admission_own_age,has_reading_attribution_members,reading_numbered_order,reading_number_value)VALUES(?,?,?,?,?,'typed','t','typed','t',0,?,?,8,?,1,0)`, c.id, public, c.library, c.kind, c.title, public, public, map[bool]int{true: 1, false: 0}[c.kind == 20])
	}
	f.exec(t, `UPDATE libraries SET kind='book' WHERE id='other'`)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available,library_id)VALUES(13020,'book-asset','/books/title.epub',100,1,'epub','','',0,0,0,0,2)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(13020,13020,2)`)
	f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format)VALUES(13020,13020,'epub')`)
	f.exec(t, `DELETE FROM saved_resource_entries WHERE resource_id='saved'`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('artist','saved',13005,'a'),('movie','saved',1,'b'),('reading','saved',13020,'c'),('automatic','saved',1101,'d')`)
	page := savedMembersComplete(t, f, r, SavedCollectionInput{View: f.input, Resource: "saved"}, nil)
	var kinds []int64
	for _, e := range page.Entries {
		kinds = append(kinds, e.Item.Kind)
	}
	if !slices.Equal(kinds, []int64{5, 1, 20, 10}) || page.Total != 4 || page.Resources[0].Shape != "squares" {
		t.Fatal("mixed catalogue eligibility", kinds, page.Total)
	}
	if page.Entries[2].Reading == nil || page.Entries[2].Reading.Available || page.Entries[2].Library != "other" {
		t.Fatal("accepted unavailable Reading facts", page.Entries[2])
	}
}

func TestRSSavedPagesCountsOnlyAutomaticEligibility(t *testing.T) {
	f, r := newRSSavedPagesFixture(t)
	f.exec(t, `DELETE FROM saved_resource_entries WHERE resource_id='saved'`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=8 WHERE id IN(3,5,6,7)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id IN(1,4)`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('a','saved',3,'a'),('b','saved',5,'b'),('c','saved',6,'c'),('d','saved',7,'d'),('e','saved',1101,'e')`)
	page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	for _, card := range page.Resources {
		if card.ID == "saved" {
			if card.EntryCount != 4 || len(card.Samples) != 4 {
				t.Fatal("all-hidden automatic root changed the exact count", card.EntryCount, len(card.Samples))
			}
			return
		}
	}
	t.Fatal("saved collection absent")
}

func TestRSSavedPagesBytePreflightRefusesPayload(t *testing.T) {
	f, _ := newRSSavedPagesFixture(t)
	for _, c := range []struct {
		name string
		args []any
	}{{"metadata", []any{"saved"}}, {"resource", []any{int64(1)}}, {"entry", []any{int64(1), "saved"}}, {"card", []any{"p", int64(1), int64(1), int64(1)}}} {
		t.Run(c.name, func(t *testing.T) {
			result, err := f.snapshots.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2}, func(scope *governor.ReadScope) error {
				rows, _, e := queryReadScope(scope, "rs.saved_page."+c.name, c.args...)
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

package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed rs_saved_directory_test.go
var rsSavedDirectoryJournal string

func rsSavedDirectoryHash(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%x", sha256.Sum256([]byte(browsePlanFixtureHash(t)+rsSavedDirectoryJournal)))
}

func rsSavedDirectoryConfig() governor.Config {
	c := readfixture.LiveConfig(time.Now)
	c.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}
	for i := range c.Partitions {
		c.Partitions[i][governor.MemoryBytes] = 128 << 20
	}
	return c
}

func rsSavedDirectoryViewerKey() string {
	return rsSavedDirectoryPersonalKey("local", "a", "p")
}

// rsSavedDirectoryDecl finds one receiver-less function declaration.
func rsSavedDirectoryDecl(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FuncDecl); ok && f.Recv == nil && f.Name.Name == name {
			return f
		}
	}
	t.Fatalf("%s is gone", name)
	return nil
}

// TestRSSavedDirectoryPersonalKeyDrift proves the local personal-key encoding
// still matches the identity -> persistence source it mirrors. The read package
// cannot import either package (persistence pulls in dbwork/worker, whose init()
// registers legacy SQLite scalar functions and changes the function hash the
// plan goldens bind), so the guard compares source text instead: the delegation
// order in identity.PersonalKey and the tuple and prefix in
// persistence.PersonalOwnerKey.
func TestRSSavedDirectoryPersonalKeyDrift(t *testing.T) {
	fset := token.NewFileSet()
	identityFile, err := parser.ParseFile(fset, "../../identity/personal_key.go", nil, 0)
	if err != nil {
		t.Fatalf("parse identity/personal_key.go: %v", err)
	}
	key := rsSavedDirectoryDecl(t, identityFile, "PersonalKey")
	var result ast.Expr
	for _, s := range key.Body.List {
		if r, ok := s.(*ast.ReturnStmt); ok && len(r.Results) == 1 {
			result = r.Results[0]
		}
	}
	if result == nil {
		t.Fatal("identity.PersonalKey no longer returns a single expression")
	}
	var call strings.Builder
	if err = printer.Fprint(&call, fset, result); err != nil {
		t.Fatalf("print identity.PersonalKey result: %v", err)
	}
	if want := "persistence.PersonalOwnerKey(v.Authority, v.AccountID, v.ProfileID)"; call.String() != want {
		t.Fatalf("identity.PersonalKey returns %s, want %s; rsSavedDirectoryPersonalKey encodes [authority,account,profile]", call.String(), want)
	}
	ownerFile, err := parser.ParseFile(fset, "../../persistence/personal_key.go", nil, 0)
	if err != nil {
		t.Fatalf("parse persistence/personal_key.go: %v", err)
	}
	owner := rsSavedDirectoryDecl(t, ownerFile, "PersonalOwnerKey")
	var body strings.Builder
	for _, s := range owner.Body.List {
		if err = printer.Fprint(&body, fset, s); err != nil {
			t.Fatalf("print persistence.PersonalOwnerKey body: %v", err)
		}
	}
	for _, want := range []string{`[3]string{authority, account, profile}`, `"viewer:" + base64.RawURLEncoding.EncodeToString(`} {
		if !strings.Contains(body.String(), want) {
			t.Fatalf("persistence.PersonalOwnerKey no longer contains %s; rsSavedDirectoryPersonalKey must change with it", want)
		}
	}
}

// newRSSavedDirectorySource seeds the complete union: an owned collection, a
// shared collection, a server-visible collection, and foreign/view/deleted rows
// that must never reach any stream. Each resource's first visible member drives
// its shape and sample set.
func newRSSavedDirectorySource(t *testing.T) *browseFixture {
	t.Helper()
	f := newBrowseSource(t)
	owner := rsSavedDirectoryViewerKey()
	f.exec(t, `INSERT INTO saved_resource_generation(id,revision)VALUES(1,1)`)
	f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at)VALUES
		('saved','collection',?,'local','a','p','Zulu','now'),
		('shared','collection','foreign','local','other','other','Alpha','now'),
		('server','collection','foreign','local','other','other','Beta','now'),
		('foreign','collection','foreign','local','x','x','Foreign Secret','now')`, owner)
	f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at)VALUES
		('view','view',?,'local','a','p','Forbidden view','now'),
		('deleted','collection',?,'local','a','p','Deleted Secret','now')`, owner, owner)
	f.exec(t, `UPDATE saved_resources SET visibility='server' WHERE id='server'`)
	f.exec(t, `UPDATE saved_resources SET deleted=1 WHERE id='deleted'`)
	f.exec(t, `INSERT INTO saved_resource_shares VALUES('shared','local','a','p','editor'),('saved','local','a','p','viewer'),('view','local','a','p','viewer')`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES
		('e1','saved',1,'a'),('e2','saved',2,'b'),('e3','saved',3,'c'),('e4','saved',9,'d'),('e5','saved',NULL,'e'),
		('s1','shared',1,'a'),('srv1','server',3,'a')`)
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated)VALUES('p',12,0)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=8 WHERE id IN(1,3)`)
	f.exec(t, `UPDATE catalog_entities SET admission_own_age=18 WHERE id=2`)
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries='["movies"]' WHERE id='p'`)
	return f
}

func newRSSavedDirectoryFixture(t *testing.T) (*browseFixture, *SavedCollectionReader) {
	t.Helper()
	f := composeReadFixture(t, newRSSavedDirectorySource(t), SavedCollectionDirectoryHarnessDefinitions(), rsSavedDirectoryConfig(), false, rsSavedDirectoryHash(t))
	r, e := NewSavedCollectionReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

// savedDirectoryPoll runs one bounded directory poll under the live public
// admission path and returns the whole page or the first error.
func savedDirectoryPoll(t *testing.T, f *browseFixture, r *SavedCollectionReader, in SavedCollectionDirectoryInput) (SavedCollectionDirectoryPage, error) {
	t.Helper()
	fg, req, out := liveRequests("saved-directory-poll")
	var page SavedCollectionDirectoryPage
	_, err := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, _, e = r.DirectoryPage(ctx, req, time.Second, out, in)
		return e
	})
	return page, err
}

func savedDirectoryComplete(t *testing.T, f *browseFixture, r *SavedCollectionReader, in SavedCollectionDirectoryInput) SavedCollectionDirectoryPage {
	t.Helper()
	p, ref, e := r.BeginDirectory(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	for poll := 0; poll < 2000; poll++ {
		page, e := savedDirectoryPoll(t, f, r, in)
		if e != nil {
			t.Fatalf("poll%d stage%d stream%d resource%q: %v", poll, p.stage, p.stream, p.current.resource.ID, e)
		}
		if page.Pending {
			if len(page.Resources) != 0 || page.Next != nil || page.Total != 0 || page.Members != 0 || page.Posters != 0 || page.Squares != 0 {
				t.Fatal("partial saved directory page", page)
			}
			continue
		}
		return page
	}
	t.Fatal("saved directory did not finish")
	return SavedCollectionDirectoryPage{}
}

func savedDirectoryError(t *testing.T, f *browseFixture, r *SavedCollectionReader, in SavedCollectionDirectoryInput) error {
	t.Helper()
	fg, req, out := liveRequests("saved-directory-error")
	var err error
	_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
		page, _, e := r.DirectoryPage(ctx, req, time.Second, out, in)
		if e == nil {
			t.Fatal("expected saved directory refusal, got", page)
		}
		if page.Pending || len(page.Resources) != 0 || page.Total != 0 || page.Members != 0 || page.Next != nil {
			t.Fatal("refused saved directory page released data", page)
		}
		return e
	})
	return err
}

// savedDirectoryResumeError follows an explicit work until it returns the
// expected refusal, proving that no earlier poll released partial data.
func savedDirectoryResumeError(t *testing.T, f *browseFixture, r *SavedCollectionReader, in SavedCollectionDirectoryInput) error {
	t.Helper()
	p, ref, e := r.BeginDirectory(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	for poll := 0; poll < 2000; poll++ {
		page, e := savedDirectoryPoll(t, f, r, in)
		if e != nil {
			if page.Pending || len(page.Resources) != 0 || page.Total != 0 || page.Members != 0 || page.Next != nil {
				t.Fatal("failed directory work released data", page)
			}
			return e
		}
		if !page.Pending {
			return nil
		}
	}
	t.Fatal("resume did not terminate")
	return nil
}

func savedDirectoryIDs(page SavedCollectionDirectoryPage) []string {
	var ids []string
	for _, v := range page.Resources {
		ids = append(ids, v.ID)
	}
	return ids
}

// rsSavedDirectoryOrderOracle is the independent plain-SQL union and ordering:
// the legacy directory oracle over the same fixture, never a copy of the Go
// sort. A pinned listing uses the persisted printf key; otherwise lower(name).
func rsSavedDirectoryOrderOracle(t *testing.T, f *browseFixture, pinned bool) []string {
	t.Helper()
	owner := rsSavedDirectoryViewerKey()
	visible := `r.kind='collection' AND r.deleted=0 AND (r.owner_key=? OR r.visibility='server' OR EXISTS(SELECT 1 FROM saved_resource_shares s WHERE s.resource_id=r.id AND s.authority='local' AND s.account_id='a' AND s.profile_id='p'))`
	var query string
	var args []any
	if pinned {
		query = `SELECT r.id FROM saved_resources AS r WHERE ` + visible + ` AND EXISTS(SELECT 1 FROM saved_pins p WHERE p.owner_key=? AND p.kind='collection' AND p.resource_id=r.id AND p.pinned=1) ORDER BY printf('%08d',COALESCE((SELECT o.position FROM saved_pin_order o WHERE o.owner_key=? AND o.kind=r.kind AND o.resource_id=r.id),99999999))||char(31)||lower(r.name),r.id`
		args = []any{owner, owner, owner}
	} else {
		query = `SELECT r.id FROM saved_resources AS r WHERE ` + visible + ` ORDER BY lower(r.name),r.id`
		args = []any{owner}
	}
	rows, e := f.conn.QueryContext(t.Context(), query, args...)
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

// rsSavedDirectoryMemberOracle returns the complete visible member order for one
// resource using only current entity/library/admission facts.
func rsSavedDirectoryMemberOracle(t *testing.T, f *browseFixture, resource string) []int64 {
	t.Helper()
	rows, e := f.conn.QueryContext(t.Context(), `SELECT m.item_id FROM saved_resource_entries m JOIN catalog_entities e ON e.id=m.item_id JOIN catalog_libraries cl ON cl.id=e.library_id JOIN libraries l ON l.id=cl.library_id WHERE m.resource_id=? AND e.retired=0 AND e.admission_own_age IS NOT NULL AND e.admission_own_age<=12 AND l.name='Movies' ORDER BY m.order_key,m.id`, resource)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
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

func TestRSSavedDirectoryUnionDeduplicatesGrants(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	// The owned collection is also shared to the same viewer; the server-visible
	// collection is also shared. Neither may be counted or sampled twice.
	f.exec(t, `INSERT OR IGNORE INTO saved_resource_shares VALUES('server','local','a','p','editor')`)
	page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	if page.Total != 3 || page.Members != 4 || page.Posters != 3 || page.Squares != 0 || !page.Exhausted || page.Next != nil || page.Pending {
		t.Fatal("union totals", page.Total, page.Members, page.Posters, page.Squares, page.Exhausted)
	}
	got := savedDirectoryIDs(page)
	want := rsSavedDirectoryOrderOracle(t, f, false)
	if !slices.Equal(got, want) || !slices.Equal(got, []string{"shared", "server", "saved"}) {
		t.Fatal("independent directory order", got, want)
	}
	byID := map[string]SavedCollectionDirectoryItem{}
	for _, v := range page.Resources {
		if _, exists := byID[v.ID]; exists {
			t.Fatal("duplicate authorized resource", v.ID)
		}
		byID[v.ID] = v
	}
	if v := byID["saved"]; v.Role != "owner" || v.EntryCount != 2 || len(v.Samples) != 2 || v.Samples[0].Item.ID != 1 || v.Samples[1].Item.ID != 3 || v.Samples[0].Library != "Movies" {
		t.Fatal("owner duplicate grant", v)
	}
	if v := byID["shared"]; v.Role != "editor" || v.EntryCount != 1 || len(v.Samples) != 1 || v.Samples[0].Item.ID != 1 {
		t.Fatal("share grant", v)
	}
	if v := byID["server"]; v.Role != "editor" || v.EntryCount != 1 || len(v.Samples) != 1 || v.Samples[0].Item.ID != 3 {
		t.Fatal("server plus share grant", v)
	}
	for _, v := range page.Resources {
		wantMembers := rsSavedDirectoryMemberOracle(t, f, v.ID)
		if int64(len(wantMembers)) != v.EntryCount {
			t.Fatal("independent member count", v.ID, v.EntryCount, wantMembers)
		}
		var gotSamples []int64
		for _, e := range v.Samples {
			gotSamples = append(gotSamples, e.Item.ID)
		}
		if len(gotSamples) > 4 || len(gotSamples) != min(4, len(wantMembers)) || !slices.Equal(gotSamples, wantMembers[:len(gotSamples)]) {
			t.Fatal("four sample oracle", v.ID, gotSamples, wantMembers)
		}
	}
}

func TestRSSavedDirectorySeventeenSetsPaginates(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	owner := rsSavedDirectoryViewerKey()
	for n := 0; n < 20; n++ {
		f.exec(t, `INSERT INTO saved_resources(id,kind,owner_key,owner_authority,owner_account,owner_profile,name,created_at)VALUES(?, 'collection',?,'local','a','p',?,'now')`, fmt.Sprintf("set-%02d", n), owner, fmt.Sprintf("Set %02d", n))
	}
	// Above RB's bounded 16-row PK fallback the exact union must still answer.
	want := rsSavedDirectoryOrderOracle(t, f, false)
	if len(want) != 23 {
		t.Fatal("fixture union", want)
	}
	in := SavedCollectionDirectoryInput{View: f.input}
	in.View.Limit = 5
	var got []string
	for page := 0; page < 20; page++ {
		value := savedDirectoryComplete(t, f, r, in)
		if value.Pending || value.Total != 23 {
			t.Fatal("exact directory total", value.Total, value.Pending)
		}
		got = append(got, savedDirectoryIDs(value)...)
		if value.Exhausted {
			if value.Next != nil {
				t.Fatal("exhausted page kept a raw continuation")
			}
			break
		}
		if value.Next == nil {
			t.Fatal("non-exhausted page lost its continuation")
		}
		in.After = value.Next
	}
	if !slices.Equal(got, want) {
		t.Fatal("paged exact order", got, want)
	}
	for n := 0; n < 20; n++ {
		if !slices.Contains(got, fmt.Sprintf("set-%02d", n)) {
			t.Fatal("missing large-set resource", n)
		}
	}
}

func TestRSSavedDirectoryForeignPrivateNeverHydrated(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	f.exec(t, `UPDATE saved_resources SET name='Foreign Secret Marker' WHERE id='foreign'`)
	f.exec(t, `UPDATE saved_resources SET name='Deleted Secret Marker' WHERE id='deleted'`)
	f.exec(t, `UPDATE saved_resources SET name='Forbidden View Marker' WHERE id='view'`)
	page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	want := rsSavedDirectoryOrderOracle(t, f, false)
	if !slices.Equal(savedDirectoryIDs(page), want) {
		t.Fatal("foreign union", savedDirectoryIDs(page), want)
	}
	for _, v := range page.Resources {
		if v.ID == "foreign" || v.ID == "view" || v.ID == "deleted" {
			t.Fatal("unauthorized resource reached the directory", v.ID)
		}
		if strings.Contains(v.Name, "Marker") || strings.Contains(v.Summary, "Marker") {
			t.Fatal("foreign private name hydrated", v)
		}
		for _, e := range v.Samples {
			if strings.Contains(e.Item.Title, "Marker") {
				t.Fatal("foreign sample hydrated", e)
			}
		}
	}
	if page.Next != nil && (page.Next.id == "foreign" || page.Next.id == "view" || page.Next.id == "deleted") {
		t.Fatal("unauthorized raw continuation", page.Next.id)
	}
	// A caller-supplied resource is not part of this family: no direct name
	// request can bypass the union and leak a foreign private collection.
	if e := savedDirectoryError(t, f, r, SavedCollectionDirectoryInput{View: f.input, Shape: "bad-shape"}); !errors.Is(e, ErrInput) {
		t.Fatal("directory accepted an unknown shape", e)
	}
}

func TestRSSavedDirectoryPinnedPositions(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	owner := rsSavedDirectoryViewerKey()
	// Negative and wide positions exercise the printf('%08d') oracle exactly.
	f.exec(t, `INSERT INTO saved_pins(owner_key,kind,resource_id,pinned,revision)VALUES(?,'collection','saved',1,2),(?,'collection','server',1,-3),(?,'collection','shared',1,4)`, owner, owner, owner)
	f.exec(t, `INSERT INTO saved_pin_order(owner_key,kind,resource_id,position)VALUES(?,'collection','saved',123456789),(?,'collection','server',-5)`, owner, owner)
	page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input, PinnedOnly: true})
	want := rsSavedDirectoryOrderOracle(t, f, true)
	if !slices.Equal(savedDirectoryIDs(page), want) {
		t.Fatal("independent pinned order", savedDirectoryIDs(page), want)
	}
	if got := savedDirectoryIDs(page); !slices.Equal(got, []string{"server", "saved", "shared"}) {
		t.Fatal("printf position order", got)
	}
	byID := map[string]SavedCollectionDirectoryItem{}
	for _, v := range page.Resources {
		byID[v.ID] = v
	}
	if v := byID["server"]; !v.Pinned || v.Position != -5 || v.PinRevision != -3 {
		t.Fatal("negative position", v)
	}
	if v := byID["saved"]; !v.Pinned || v.Position != 123456789 || v.PinRevision != 2 {
		t.Fatal("wide position", v)
	}
	if v := byID["shared"]; !v.Pinned || v.Position != 99999999 {
		t.Fatal("unarranged pinned position", v)
	}
	// A non-pinned directory keeps the plain lower(name) arrangement.
	f.exec(t, `DELETE FROM saved_pins`)
	plain := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	if !slices.Equal(savedDirectoryIDs(plain), rsSavedDirectoryOrderOracle(t, f, false)) {
		t.Fatal("plain arrangement after pins removed", savedDirectoryIDs(plain))
	}
}

func TestRSSavedDirectoryUTF8NULOrder(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	for id, name := range map[string]string{"shared": "Éclair\x00Z", "server": "éclair\x00a", "saved": " 東京 "} {
		f.exec(t, `UPDATE saved_resources SET name=? WHERE id=?`, name, id)
	}
	page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	got := savedDirectoryIDs(page)
	want := rsSavedDirectoryOrderOracle(t, f, false)
	if !slices.Equal(got, want) {
		t.Fatal("native lower/BINARY order", got, want)
	}
	byID := map[string]SavedCollectionDirectoryItem{}
	for _, v := range page.Resources {
		byID[v.ID] = v
	}
	if byID["shared"].Name != "Éclair\x00Z" || byID["server"].Name != "éclair\x00a" || byID["saved"].Name != " 東京 " {
		t.Fatal("wide/NUL name lost")
	}
}

func TestRSSavedDirectoryShapeFilterKeepsRawContinuation(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	// Make the middle raw resource (Beta) a squares-first collection.
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening)VALUES(5,'artist',0,1,1,1,1)`)
	public := make([]byte, 16)
	public[0], public[14], public[15] = 0xe3, 0x32, 0xcd
	f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(13005,?,1,5)`, public)
	f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,admission_own_age)VALUES(13005,?,1,5,'Artist','typed','t','typed','t',0,?,?,8)`, public, public, public)
	f.exec(t, `DELETE FROM saved_resource_entries WHERE resource_id='server'`)
	f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('srv-artist','server',13005,'a')`)
	in := SavedCollectionDirectoryInput{View: f.input, Shape: "squares"}
	in.View.Limit = 1
	first := savedDirectoryComplete(t, f, r, in)
	if len(first.Resources) != 0 || first.Next == nil || first.Exhausted || first.Total != 3 || first.Posters != 2 || first.Squares != 1 {
		t.Fatal("shape filter invented a page or exhaustion", first)
	}
	in.After = first.Next
	second := savedDirectoryComplete(t, f, r, in)
	if ids := savedDirectoryIDs(second); !slices.Equal(ids, []string{"server"}) || second.Exhausted || second.Next == nil {
		t.Fatal("filtered raw window", ids, second.Exhausted)
	}
	if second.Next.id == first.Next.id {
		t.Fatal("empty shape window did not advance the raw continuation")
	}
	in.After = second.Next
	last := savedDirectoryComplete(t, f, r, in)
	if len(last.Resources) != 0 || !last.Exhausted || last.Next != nil {
		t.Fatal("shape-filtered tail", last)
	}
}

func TestRSSavedDirectoryMixedReadingAndAutomaticSamples(t *testing.T) {
	t.Run("reading", func(t *testing.T) {
		f, r := newRSSavedDirectoryFixture(t)
		f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening)VALUES(20,'ebook',0,0,1,1,0)`)
		public := make([]byte, 16)
		public[0], public[14], public[15] = 0xe3, 0x32, 0xdc
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(13020,?,2,20)`, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,admission_own_age,has_reading_attribution_members,reading_numbered_order,reading_number_value)VALUES(13020,?,2,20,'Reading','typed','t','typed','t',0,?,?,8,1,1,0)`, public, public, public)
		f.exec(t, `UPDATE libraries SET kind='book' WHERE id='other'`)
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available,library_id)VALUES(13020,'book-asset','/books/title.epub',100,1,'epub','','',0,0,0,0,2)`)
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(13020,13020,2)`)
		f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format)VALUES(13020,13020,'epub')`)
		f.exec(t, `UPDATE direct_memberships SET allowed_libraries='["movies","other"]' WHERE account_id='a'`)
		f.exec(t, `UPDATE direct_profiles SET allowed_libraries='["movies","other"]' WHERE id='p'`)
		f.exec(t, `DELETE FROM saved_resource_entries WHERE resource_id='saved'`)
		f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('reading','saved',13020,'a'),('movie','saved',1,'b')`)
		page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
		byID := map[string]SavedCollectionDirectoryItem{}
		for _, v := range page.Resources {
			byID[v.ID] = v
		}
		v := byID["saved"]
		if v.EntryCount != 2 || len(v.Samples) != 2 || v.Samples[0].Item.Kind != 20 || v.Samples[0].Reading == nil || v.Samples[0].Library != "Other" {
			t.Fatal("Reading sample facts", v)
		}
		if v.Samples[0].Reading.Available {
			t.Fatal("accepted unavailable Reading sample invented availability")
		}
		if byID["saved"].Shape != "posters" || len(byID["shared"].Samples) != 1 {
			t.Fatal("mixed sample shapes", byID["saved"].Shape)
		}
	})
	t.Run("automatic", func(t *testing.T) {
		f, r := newRSSavedDirectoryFixture(t)
		f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening)VALUES(10,'collection',0,1,1,1,0)`)
		public := make([]byte, 16)
		public[0], public[14], public[15] = 0xe3, 0x05, 0x15
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind)VALUES(1301,?,1,10)`, public)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,admission_own_age,admission_member_count)VALUES(1301,?,1,10,'Automatic','auto','a','auto','a',0,?,?,0,0)`, public, public, public)
		f.exec(t, `INSERT INTO catalog_automatic_collections(library_id,provider,provider_id,entity_id)VALUES(1,'fixture','C',1301)`)
		f.exec(t, `INSERT INTO saved_resource_entries(id,resource_id,item_id,order_key)VALUES('automatic','saved',1301,'f')`)
		// The complete automatic reducer is task 05. Until it is composed beside
		// this family the directory refuses the automatic sample explicitly; it
		// never invents an authored empty collection or a partial directory.
		if e := savedDirectoryError(t, f, r, SavedCollectionDirectoryInput{View: f.input}); !errors.Is(e, ErrUnqualified) {
			t.Fatal("automatic sample must stay explicit", e)
		}
	})
}

func TestRSSavedDirectoryNoLibraryPrincipal(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	f.exec(t, `UPDATE direct_profiles SET allowed_libraries='[]' WHERE id='p'`)
	page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	want := rsSavedDirectoryOrderOracle(t, f, false)
	if !slices.Equal(savedDirectoryIDs(page), want) {
		t.Fatal("no-library directory names", savedDirectoryIDs(page), want)
	}
	for _, v := range page.Resources {
		if v.Name == "" || v.EntryCount != 0 || len(v.Samples) != 0 {
			t.Fatal("no-library resource leaked members", v)
		}
	}
	if page.Members != 0 {
		t.Fatal("no-library member total", page.Members)
	}
}

func TestRSSavedDirectoryChangedSourceStale(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*browseFixture)
	}{
		{"generation", func(f *browseFixture) {
			f.exec(t, `UPDATE saved_resource_generation SET revision=2 WHERE id=1`)
		}},
		{"entries", func(f *browseFixture) {
			f.exec(t, `UPDATE saved_resource_entries SET order_key='z' WHERE id='e1'`)
		}},
		{"pin-revision", func(f *browseFixture) {
			owner := rsSavedDirectoryViewerKey()
			f.exec(t, `INSERT INTO saved_pins(owner_key,kind,resource_id,pinned,revision)VALUES(?,'collection','saved',1,1)`, owner)
		}},
		{"pin-position", func(f *browseFixture) {
			owner := rsSavedDirectoryViewerKey()
			f.exec(t, `INSERT INTO saved_pin_order(owner_key,kind,resource_id,position)VALUES(?,'collection','saved',1)`, owner)
		}},
		{"resource-name", func(f *browseFixture) {
			f.exec(t, `UPDATE saved_resources SET name='Aardvark' WHERE id='saved'`)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, r := newRSSavedDirectoryFixture(t)
			in := SavedCollectionDirectoryInput{View: f.input}
			in.View.Limit = 1
			first := savedDirectoryComplete(t, f, r, in)
			if first.Next == nil || first.Exhausted {
				t.Fatal("fixture needs a continuation", first)
			}
			c.mutate(f)
			in.After = first.Next
			if e := savedDirectoryResumeError(t, f, r, in); !errors.Is(e, ErrStale) {
				t.Fatal("changed source", e)
			}
		})
	}
}

func TestRSSavedDirectoryCursorBindsQuery(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	in := SavedCollectionDirectoryInput{View: f.input}
	in.View.Limit = 1
	first := savedDirectoryComplete(t, f, r, in)
	if first.Next == nil {
		t.Fatal("fixture needs a continuation")
	}
	for _, other := range []SavedCollectionDirectoryInput{
		{View: f.input, PinnedOnly: true},
		{View: func() BrowseInput { v := f.input; v.Limit = 2; return v }()},
		{View: f.input, Shape: "squares"},
	} {
		other.After = first.Next
		if e := savedDirectoryError(t, f, r, other); !errors.Is(e, ErrStale) {
			t.Fatal("cursor accepted another query", e)
		}
	}
}

func TestRSSavedDirectoryBytePreflightRefusesPayload(t *testing.T) {
	f, _ := newRSSavedDirectoryFixture(t)
	for _, c := range []struct {
		name string
		args []any
	}{
		{"resource", []any{int64(1)}},
		{"share_resource", []any{int64(1)}},
		{"metadata", []any{"saved"}},
		{"entry", []any{int64(1), "saved"}},
		{"card", []any{"p", int64(1), int64(1), int64(1)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, err := f.snapshots.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2, GoCopyBytes: 64 << 10}, func(scope *governor.ReadScope) error {
				rows, _, e := queryReadScope(scope, "rs.saved.directory."+c.name, c.args...)
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

func TestRSSavedDirectoryBeginGuards(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	deadline := time.Now().Add(12 * time.Second)
	for _, c := range []struct {
		name string
		in   SavedCollectionDirectoryInput
	}{
		{"zero-limit", SavedCollectionDirectoryInput{View: func() BrowseInput { v := f.input; v.Limit = 0; return v }()}},
		{"large-limit", SavedCollectionDirectoryInput{View: func() BrowseInput { v := f.input; v.Limit = MaxPage + 1; return v }()}},
		{"descending", SavedCollectionDirectoryInput{View: func() BrowseInput { v := f.input; v.Descending = true; return v }()}},
		{"keep-articles", SavedCollectionDirectoryInput{View: func() BrowseInput { v := f.input; v.KeepArticles = true; return v }()}},
		{"view-cursor", SavedCollectionDirectoryInput{View: func() BrowseInput { v := f.input; v.After = &BrowseCursor{}; return v }()}},
		{"bad-shape", SavedCollectionDirectoryInput{View: f.input, Shape: "triangles"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, _, e := r.BeginDirectory(t.Context(), f.authority, 1, deadline, 8<<20, c.in)
			if !errors.Is(e, ErrInput) || p != nil {
				t.Fatalf("guard %s: %v", c.name, e)
			}
		})
	}
	p, _, e := r.BeginDirectory(t.Context(), f.authority, 1, deadline, 1024, SavedCollectionDirectoryInput{View: f.input})
	if !errors.Is(e, ErrInput) || p != nil {
		t.Fatal("short lease", e)
	}
	p, ref, e := r.BeginDirectory(t.Context(), f.authority, 1, deadline, 8<<20, SavedCollectionDirectoryInput{View: f.input})
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	if e = p.Close(); e != nil {
		t.Fatal(e)
	}
	if e = p.Close(); e != nil {
		t.Fatal("close is not idempotent", e)
	}
	if e = savedDirectoryError(t, f, r, SavedCollectionDirectoryInput{View: f.input, After: &SavedCollectionDirectoryCursor{}}); !errors.Is(e, ErrStale) {
		t.Fatal("DirectoryPage accepted a foreign cursor", e)
	}
}

type rsSavedDirectoryFinalSnapshots struct {
	*governor.Snapshots
	ready func() bool
	after func()
	fired bool
}

func (b *rsSavedDirectoryFinalSnapshots) Read(ctx context.Context, r governor.Request, d time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s, e := b.Snapshots.Read(ctx, r, d, fn)
	if e == nil && b.ready() && !b.fired {
		b.fired = true
		b.after()
	}
	return s, e
}
func (b *rsSavedDirectoryFinalSnapshots) ReadForecasted(ctx context.Context, r governor.Request, d time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	s, e := b.Snapshots.ReadForecasted(ctx, r, d, f, fn)
	if e == nil && b.ready() && !b.fired {
		b.fired = true
		b.after()
	}
	return s, e
}

func TestRSSavedDirectoryFreshFinalDenial(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	in := SavedCollectionDirectoryInput{View: f.input}
	p, ref, e := r.BeginDirectory(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	b := &rsSavedDirectoryFinalSnapshots{Snapshots: f.snapshots, ready: func() bool { return p.ready }, after: func() { f.exec(t, `UPDATE authorization_session_families SET revoked=1`) }}
	r.snapshots = b
	for poll := 0; poll < 400; poll++ {
		page, e := savedDirectoryPoll(t, f, r, in)
		if e != nil {
			if !b.fired || !errors.Is(e, ErrDenied) || len(page.Resources) != 0 || page.Total != 0 || page.Pending {
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

type rsSavedDirectoryHeldSnapshots struct {
	*governor.Snapshots
	entered, release chan struct{}
	once             sync.Once
}

func (b *rsSavedDirectoryHeldSnapshots) hold() { b.once.Do(func() { close(b.entered); <-b.release }) }
func (b *rsSavedDirectoryHeldSnapshots) Read(ctx context.Context, r governor.Request, d time.Duration, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	b.hold()
	return b.Snapshots.Read(ctx, r, d, fn)
}
func (b *rsSavedDirectoryHeldSnapshots) ReadForecasted(ctx context.Context, r governor.Request, d time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	b.hold()
	return b.Snapshots.ReadForecasted(ctx, r, d, f, fn)
}

func TestRSSavedDirectoryCloseJoinsPoll(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	in := SavedCollectionDirectoryInput{View: f.input}
	p, ref, e := r.BeginDirectory(t.Context(), f.authority, 1, time.Now().Add(12*time.Second), 8<<20, in)
	if e != nil || ref != nil {
		t.Fatal(e, ref)
	}
	defer p.Close()
	in.Work = p
	held := &rsSavedDirectoryHeldSnapshots{Snapshots: f.snapshots, entered: make(chan struct{}), release: make(chan struct{})}
	r.snapshots = held
	finished := make(chan error, 1)
	go func() {
		fg, req, out := liveRequests("saved-directory-held")
		_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			_, _, e := r.DirectoryPage(ctx, req, time.Second, out, in)
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
	if !p.closed || p.records != nil || p.seen != nil || p.sortKeys != nil || p.fence != nil || p.rawName != "" || p.shape != "" {
		t.Fatal("retained closed values")
	}
}

func TestRSSavedDirectoryWideText(t *testing.T) {
	f, r := newRSSavedDirectoryFixture(t)
	f.exec(t, `UPDATE saved_resources SET summary=? WHERE id='saved'`, strings.Repeat("界", 4096))
	f.exec(t, `UPDATE catalog_entities SET title=?,overview=? WHERE id=1`, "雪\x00é", strings.Repeat("wide\x00界", 2000))
	page := savedDirectoryComplete(t, f, r, SavedCollectionDirectoryInput{View: f.input})
	byID := map[string]SavedCollectionDirectoryItem{}
	for _, v := range page.Resources {
		byID[v.ID] = v
	}
	if byID["saved"].Summary != strings.Repeat("界", 4096) {
		t.Fatal("wide summary lost")
	}
	if len(byID["saved"].Samples) != 2 || byID["saved"].Samples[0].Item.Title != "雪\x00é" || byID["saved"].Samples[0].Item.Overview != strings.Repeat("wide\x00界", 2000) {
		t.Fatal("wide UTF8/NUL sample lost")
	}
}

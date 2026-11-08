package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed ia_extra_test.go
var iaExtraJournal string

func iaExtraHash(t *testing.T) string {
	h := sha256.Sum256([]byte(readfixture.ForegroundHash(t) + iaExtraJournal))
	return hex.EncodeToString(h[:])
}

// newIAExtraSource seeds video extras with an exact owner, an explicit
// unresolved owner, retired and foreign rows, separate editions/parts/CUEs and
// episode asset boundaries. No audio or reading folder becomes a video extra.
func newIAExtraSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(4,'episode',1,0,1,1,0)`)
	for _, v := range []struct {
		id, library, kind int64
		retired           bool
	}{{3001, 1, 1, false}, {3002, 1, 1, true}, {3003, 1, 1, false}, {3004, 1, 1, false}, {3005, 2, 1, false}, {3006, 1, 1, false}, {3007, 1, 1, false}, {3008, 2, 1, false}, {3009, 1, 1, false}} {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(v.id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,?)`, v.id, public, v.library, v.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,retired,admission_own_age) VALUES(?,?,?,?,'Extra','extra','extra','extra','extra',0,?,?,?,12)`, v.id, public, v.library, v.kind, public, public, v.retired)
	}
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(30,'extra30','/media/extras/trailer.mkv',10,1,'mkv','h264','aac',1920,1080,10.0,1),(31,'extra31','/media/extras/second.mkv',10,1,'mkv','h264','aac',1920,1080,10.0,1),(32,'extra32','/media/other/other.mkv',10,1,'mkv','h264','aac',1920,1080,10.0,2)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,edition,library_id,cue_ordinal,start_seconds,end_seconds) VALUES(3002,30,1,'',1,NULL,0.0,NULL),(3003,30,1,'',1,NULL,0.0,NULL),(3003,31,2,'director',1,1,1.0,2.0),(3004,31,1,'',1,NULL,0.0,NULL),(3005,32,1,'',2,NULL,0.0,NULL),(3006,31,1,'',1,NULL,0.0,NULL),(3007,30,1,'',1,NULL,0.0,NULL),(3009,30,1,'',1,NULL,0.0,NULL)`)
	f.exec(t, `INSERT INTO item_extras(item_id,library_id,parent_path,owner_id,kind,title,asset_id,relative_path) VALUES(3002,'movies','Movies',3001,'trailer','Retired','extra30','Movies/Trailers/Retired.mkv'),(3003,'movies','Movies',3001,'trailer','Trailer','extra30','Movies/Trailers/Trailer.mkv'),(3004,'movies','Movies',3001,'trailer','Second','extra31','Movies/Trailers/Second.mkv'),(3005,'other','Other',NULL,'other','Unresolved','extra32','Other/Other.mkv'),(3006,'movies','Movies',NULL,'other','Unresolved','extra31','Movies/Other/Unresolved.mkv'),(3007,'movies','Movies',3008,'other','Cross','extra30','Movies/Other/Cross.mkv'),(3009,'other','Other',NULL,'other','Mismatch','extra30','Other/Mismatch.mkv')`)
	f.exec(t, `INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(3003,'extra30','whole_source'),(3004,'extra31','unknown_multi_episode'),(3006,'extra31','whole_source')`)
	return f
}

func newIAExtraFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := newIAExtraSource(t)
	f = composeReadFixture(t, f, append(browseDefinitions(), IAExtraHarnessDefinitions()...), readfixture.LiveConfig(time.Now), false, iaExtraHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}

func iaExtraRead(t *testing.T, f *browseFixture, r *ForegroundReader, in IAExtraInput) (IAExtraPage, BrowseReadResult, error) {
	t.Helper()
	q, request, output := liveRequests("ia-extra")
	var page IAExtraPage
	var result BrowseReadResult
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, result, e = r.IAExtraMembership(ctx, request, time.Second, out, in)
		return e
	})
	return page, result, e
}

func iaExtraOwnerInput(f *browseFixture) IAExtraInput {
	return IAExtraInput{View: f.input, Mode: IAExtraOwnerMode, Entity: 3001, ExtraKind: "trailer"}
}

func TestIAExtraOwnerCohort(t *testing.T) {
	f, r := newIAExtraFixture(t)
	page, result, e := iaExtraRead(t, f, r, iaExtraOwnerInput(f))
	if e != nil {
		t.Fatal(e)
	}
	// The retired member consumes its raw position but is never published.
	if page.Entity.ID != 3001 || len(page.Members) != 2 || page.Members[0].Item != 3003 || page.Members[1].Item != 3004 {
		t.Fatalf("owner cohort=%+v", page)
	}
	if page.RawRows != 3 || page.RawPositions != 3 || !page.Exhausted || page.Next != nil {
		t.Fatalf("raw accounting=%+v", page)
	}
	m := page.Members[0]
	if m.Kind != 1 || m.Revision != 1 || m.Owner == nil || *m.Owner != 3001 || m.ParentPath != "Movies" || m.Type != "trailer" || m.Title != "Trailer" || m.AssetToken != "extra30" || m.RelativePath != "Movies/Trailers/Trailer.mkv" || m.Boundary != "whole_source" || m.AssetMembers != 4 {
		t.Fatalf("member=%+v", m)
	}
	// Two separate editions/parts stay separate; the CUE range is not collapsed.
	if len(m.Links) != 2 || m.Links[0].Asset != 30 || m.Links[0].Part != 1 || m.Links[0].CueOrdinal != nil || m.Links[1].Asset != 31 || m.Links[1].Part != 2 || m.Links[1].Edition != "director" || m.Links[1].CueOrdinal == nil || *m.Links[1].CueOrdinal != 1 {
		t.Fatalf("links=%+v", m.Links)
	}
	if page.Members[1].Boundary != "unknown_multi_episode" || page.Members[1].Owner == nil || *page.Members[1].Owner != 3001 {
		t.Fatalf("second member=%+v", page.Members[1])
	}
	if result.LookupStatements == 0 || result.PublicationStatements == 0 || result.Attempts != 1 {
		t.Fatalf("statement accounting=%+v", result)
	}
}

func TestIAExtraPathExact(t *testing.T) {
	f, r := newIAExtraFixture(t)
	in := IAExtraInput{View: f.input, Mode: IAExtraPathMode, Entity: 3001, ExtraKind: "trailer", ParentPath: "Movies", Title: "Trailer"}
	page, _, e := iaExtraRead(t, f, r, in)
	if e != nil || len(page.Members) != 1 || page.Members[0].Item != 3003 || !page.Exhausted {
		t.Fatalf("path member=%+v err=%v", page, e)
	}
	in.Title = "Missing"
	page, _, e = iaExtraRead(t, f, r, in)
	if e != nil || len(page.Members) != 0 || page.RawRows != 0 || !page.Exhausted || page.Entity.ID != 3001 {
		t.Fatalf("path empty=%+v err=%v", page, e)
	}
}

func TestIAExtraMemberUnresolvedOwner(t *testing.T) {
	f, r := newIAExtraFixture(t)
	in := IAExtraInput{View: f.input, Mode: IAExtraMemberMode, Entity: 3006}
	page, _, e := iaExtraRead(t, f, r, in)
	if e != nil || len(page.Members) != 1 {
		t.Fatalf("member=%+v err=%v", page, e)
	}
	m := page.Members[0]
	// An unresolved owner is explicit: no parent path, title or item is promoted
	// into an owner id.
	if m.Owner != nil || m.ParentPath != "Movies" || m.Type != "other" || m.Title != "Unresolved" || m.Boundary != "whole_source" || m.AssetMembers != 3 || len(m.Links) != 1 {
		t.Fatalf("unresolved member=%+v", m)
	}
	if page.Entity.ID != 3006 || !page.Exhausted || page.RawRows != 1 || page.RawPositions != 1 {
		t.Fatalf("member page=%+v", page)
	}
}

func TestIAExtraCrossLibraryRefusal(t *testing.T) {
	f, r := newIAExtraFixture(t)
	for _, in := range []IAExtraInput{
		{View: f.input, Mode: IAExtraMemberMode, Entity: 3005},
		{View: f.input, Mode: IAExtraMemberMode, Entity: 3007},
		{View: f.input, Mode: IAExtraMemberMode, Entity: 3009},
	} {
		page, _, e := iaExtraRead(t, f, r, in)
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAExtraPage{}) {
			t.Fatalf("foreign member %d=%+v err=%v", in.Entity, page, e)
		}
	}
	in := iaExtraOwnerInput(f)
	in.View.Library = 2
	page, _, e := iaExtraRead(t, f, r, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAExtraPage{}) {
		t.Fatalf("foreign owner library=%+v err=%v", page, e)
	}
}

func TestIAExtraLinkCohortCap(t *testing.T) {
	f, r := newIAExtraFixture(t)
	for id := int64(40); id < 57; id++ {
		token := fmt.Sprint("cap", id)
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,1,1,'mkv','','',0,0,1,1)`, id, token, token)
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,edition,library_id,cue_ordinal) VALUES(3004,?,1,'',1,?)`, id, id-39)
	}
	page, _, e := iaExtraRead(t, f, r, iaExtraOwnerInput(f))
	// 17 links exceed the 16-link edition/part/CUE cohort; the lookahead row
	// refuses the page instead of silently dropping the extra link.
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(page, IAExtraPage{}) {
		t.Fatalf("over-cap cohort=%+v err=%v", page, e)
	}
}

func TestIAExtraCursorAndAuthorityDrift(t *testing.T) {
	f, r := newIAExtraFixture(t)
	in := iaExtraOwnerInput(f)
	in.View.Limit = 1
	page, _, e := iaExtraRead(t, f, r, in)
	if e != nil || len(page.Members) != 1 || page.Next == nil || page.Exhausted || page.Next.id != 3003 {
		t.Fatalf("first window=%+v err=%v", page, e)
	}
	in.After = page.Next
	f.exec(t, `UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=3001`)
	page, _, e = iaExtraRead(t, f, r, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(page, IAExtraPage{}) {
		t.Fatalf("cursor source drift=%+v err=%v", page, e)
	}
	in.After = nil
	f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	page, _, e = iaExtraRead(t, f, r, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAExtraPage{}) {
		t.Fatalf("revoked authority=%+v err=%v", page, e)
	}
}

func TestIAExtraPolicyAndInput(t *testing.T) {
	f, r := newIAExtraFixture(t)
	for _, in := range []IAExtraInput{
		{View: f.input, Mode: 0, Entity: 3001},
		{View: f.input, Mode: IAExtraOwnerMode, Entity: 0, ExtraKind: "trailer"},
		{View: f.input, Mode: IAExtraOwnerMode, Entity: 3001, ExtraKind: "bonus"},
		{View: f.input, Mode: IAExtraOwnerMode, Entity: 3001, ExtraKind: "trailer", Title: "x"},
		{View: f.input, Mode: IAExtraPathMode, Entity: 3001, ExtraKind: "trailer", ParentPath: "../escape", Title: "Trailer"},
		{View: f.input, Mode: IAExtraPathMode, Entity: 3001, ExtraKind: "trailer", ParentPath: "Movies", Title: ""},
		{View: f.input, Mode: IAExtraMemberMode, Entity: 3003, ExtraKind: "trailer"},
	} {
		page, _, e := iaExtraRead(t, f, r, in)
		if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IAExtraPage{}) {
			t.Fatalf("input %+v=%+v err=%v", in, page, e)
		}
	}
	page, _, e := r.IAExtraMembership(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, iaExtraOwnerInput(f))
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IAExtraPage{}) {
		t.Fatal("missing foreground custody", e)
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',20,1)`)
	page, _, e = iaExtraRead(t, f, r, iaExtraOwnerInput(f))
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(page, IAExtraPage{}) {
		t.Fatalf("restricted profile=%+v err=%v", page, e)
	}
}

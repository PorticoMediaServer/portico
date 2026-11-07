package read

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

//go:embed ia_extra_test.go
var iaExtraJournal string

func iaExtraHash(t *testing.T) string {
	h := sha256.Sum256([]byte(iaAudioHash(t) + iaExtraJournal))
	return hex.EncodeToString(h[:])
}
func newIAExtraSource(t *testing.T) *browseFixture {
	f := newIAAudioSource(t)
	f.exec(t, `INSERT INTO item_extras(item_id,library_id,parent_path,owner_id,kind,title,asset_id,relative_path) VALUES(2003,'movies','/media/extras',2002,'trailer','é trailer','asset','extras/trailer.mkv'),(2006,'movies','/media/extras',NULL,'other','Unresolved','audio2','extras/other.mkv')`)
	return f
}
func extraFixture(t *testing.T, defs []stmt.Definition) (*browseFixture, *ForegroundReader) {
	f := newIAExtraSource(t)
	if defs == nil {
		defs = IAExtraHarnessDefinitions()
	}
	f = composeReadFixture(t, f, append(browseDefinitions(), defs...), readfixture.LiveConfig(time.Now), false, iaExtraHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func extraRead(t *testing.T, f *browseFixture, r *ForegroundReader, in IAExtraInput) (IAExtraPage, BrowseReadResult, error) {
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
func extraInput(f *browseFixture) IAExtraInput {
	in := IAExtraInput{View: f.input, Entity: 2002, ExtraKind: "trailer"}
	in.View.Kind = 6
	return in
}
func TestIAExtraMembership(t *testing.T) {
	f, r := extraFixture(t, nil)
	in := extraInput(f)
	p, _, e := extraRead(t, f, r, in)
	if e != nil || len(p.Members) != 1 || !p.Exhausted {
		t.Fatal(p, e)
	}
	m := p.Members[0]
	if m.Item != 2003 || m.Kind != 7 || m.Owner == nil || *m.Owner != 2002 || m.AssetToken != "asset" || m.Type != "trailer" || m.RelativePath != "extras/trailer.mkv" || len(m.Links) != 2 || m.Album == nil || m.Links[1].CueOrdinal == nil {
		t.Fatal(m)
	}
	in.Entity = 2006
	in.View.Kind = 9
	in.ExtraKind = ""
	p, _, e = extraRead(t, f, r, in)
	if e != nil || len(p.Members) != 1 || p.Members[0].Owner != nil || len(p.Links) != 1 {
		t.Fatal(p, e)
	}
}
func TestIAExtraAuthorityAndCursor(t *testing.T) {
	f, r := extraFixture(t, nil)
	in := extraInput(f)
	in.View.Limit = 1
	f.exec(t, `INSERT INTO item_extras(item_id,library_id,parent_path,owner_id,kind,title,asset_id,relative_path) VALUES(2004,'movies','/media/extras',2002,'trailer','second','asset','second.mkv')`)
	p, _, e := extraRead(t, f, r, in)
	if e != nil || p.Next == nil {
		t.Fatal(p, e)
	}
	in.After = p.Next
	f.exec(t, `UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=2002`)
	p, _, e = extraRead(t, f, r, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(p, IAExtraPage{}) {
		t.Fatal(p, e)
	}
	in.After = nil
	f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	p, _, e = extraRead(t, f, r, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, IAExtraPage{}) {
		t.Fatal(p, e)
	}
}
func TestIAExtraPublication(t *testing.T) {
	f, r := extraFixture(t, nil)
	in := extraInput(f)
	r.snapshots = &iaAudioBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
		if phase == 1 {
			f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
		}
	}}
	p, _, e := extraRead(t, f, r, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, IAExtraPage{}) {
		t.Fatal(p, e)
	}
}
func TestIAExtraCompleteCohort(t *testing.T) {
	f, r := extraFixture(t, nil)
	for id := int64(3); id <= 18; id++ {
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,1,1,'','','',0,0,1,1)`, id, fmt.Sprint(id), fmt.Sprint(id))
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,library_id) VALUES(2003,?,?,1)`, id, id)
	}
	p, _, e := extraRead(t, f, r, extraInput(f))
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(p, IAExtraPage{}) {
		t.Fatal(p, e)
	}
}
func TestIAExtraSQLMutants(t *testing.T) {
	for _, mode := range []string{"library", "cohort"} {
		t.Run(mode, func(t *testing.T) {
			defs := IAExtraHarnessDefinitions()
			for i := range defs {
				if mode == "library" && defs[i].ID == readStatementID("foreground.ia.extra.extra") {
					defs[i].SQL = strings.Replace(defs[i].SQL, "relative_path,l.id", "relative_path,1", 1)
				}
				if mode == "cohort" && defs[i].ID == readStatementID("foreground.ia.extra.links") {
					defs[i].SQL = strings.Replace(defs[i].SQL, "LIMIT 17", "LIMIT 2", 1)
				}
			}
			f, r := extraFixture(t, defs)
			if mode == "cohort" {
				for id := int64(3); id <= 18; id++ {
					f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,1,1,'','','',0,0,1,1)`, id, fmt.Sprint(id), fmt.Sprint(id))
					f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,library_id) VALUES(2003,?,?,1)`, id, id)
				}
			}
			if mode == "library" {
				f.exec(t, `UPDATE item_extras SET library_id='other' WHERE item_id=2003`)
			}
			p, _, e := extraRead(t, f, r, extraInput(f))
			if e != nil || len(p.Members) != 1 {
				t.Fatal("mutant did not falsify safeguard", p, e)
			}
			if mode == "cohort" && len(p.Members[0].Links) != 2 {
				t.Fatal("cohort mutant ineffective")
			}
		})
	}
}
func TestIAExtraForeignAndRejected(t *testing.T) {
	for _, mode := range []string{"library", "owner", "retired"} {
		t.Run(mode, func(t *testing.T) {
			f, r := extraFixture(t, nil)
			in := extraInput(f)
			switch mode {
			case "library":
				f.exec(t, `UPDATE item_extras SET library_id='other' WHERE item_id=2003`)
			case "owner":
				f.exec(t, `PRAGMA foreign_keys=OFF`)
				f.exec(t, `UPDATE catalog_entities SET library_id=2 WHERE id=2002`)
				f.exec(t, `PRAGMA foreign_keys=ON`)
				in.Entity = 2003
				in.View.Kind = 7
				in.ExtraKind = ""
			case "retired":
				f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2003`)
			}
			p, _, e := extraRead(t, f, r, in)
			if mode == "retired" {
				if e != nil || len(p.Members) != 0 || p.RawPositions != 1 || !p.Exhausted {
					t.Fatal(p, e)
				}
			} else if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(p, IAExtraPage{}) {
				t.Fatal(p, e)
			}
		})
	}
}

func TestIAExtraPolicyAndCustody(t *testing.T) {
	f, r := extraFixture(t, nil)
	in := extraInput(f)
	p, _, e := r.IAExtraMembership(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, in)
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(p, IAExtraPage{}) {
		t.Fatal(p, e)
	}
	f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',20,1)`)
	p, _, e = extraRead(t, f, r, in)
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(p, IAExtraPage{}) {
		t.Fatal(p, e)
	}
}

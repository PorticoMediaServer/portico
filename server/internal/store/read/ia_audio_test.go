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

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
)

//go:embed ia_audio_test.go
var iaAudioJournal string

func iaAudioHash(t *testing.T) string {
	h := sha256.Sum256([]byte(readfixture.ForegroundHash(t) + iaAudioJournal))
	return hex.EncodeToString(h[:])
}
func newIAAudioSource(t *testing.T) *browseFixture {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,1),(9,'audiobook_file',1,0,1,1,1)`)
	for _, v := range []struct {
		id, kind                               int64
		artist, album, book, disc, track, part any
	}{{2001, 5, nil, nil, nil, nil, nil, nil}, {2002, 6, int64(2001), nil, nil, nil, nil, nil}, {2003, 7, nil, int64(2002), nil, int64(1), int64(2), nil}, {2004, 7, nil, int64(2002), nil, int64(2), int64(1), nil}, {2005, 8, nil, nil, nil, nil, nil, nil}, {2006, 9, nil, nil, int64(2005), int64(1), nil, int64(2)}, {2007, 9, nil, nil, int64(2005), int64(1), nil, int64(1)}, {2008, 7, nil, int64(2002), nil, int64(1), int64(1), nil}, {2009, 7, nil, int64(2002), nil, int64(1), int64(1), nil}} {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(v.id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, v.id, public, v.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,artist_id,album_id,book_id,disc_number,track_number,part_number,admission_own_age) VALUES(?,?,1,?,'é Album','audio','audio','audio','audio',0,?,?,?,?,?,?,?,?,12)`, v.id, public, v.kind, public, public, v.artist, v.album, v.book, v.disc, v.track, v.part)
	}
	f.exec(t, `INSERT INTO catalog_song_artists(song_id,artist_id) VALUES(2003,2001)`)
	f.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=2008`)
	f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(2,'audio2','/media/audio2',10,1,'flac','','flac',0,0,10.0,1)`)
	f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,edition,library_id,cue_ordinal,start_seconds,end_seconds) VALUES(2003,1,2,'é edition',1,1,1.0,2.0),(2003,2,1,'alternate',1,NULL,0.0,NULL),(2006,2,2,'book edition',1,NULL,0.0,NULL)`)
	return f
}
func newIAAudioFixture(t *testing.T) (*browseFixture, *ForegroundReader) {
	t.Helper()
	f := newIAAudioSource(t)
	f = composeReadFixture(t, f, append(browseDefinitions(), IAAudioHarnessDefinitions()...), readfixture.LiveConfig(time.Now), false, iaAudioHash(t))
	r, e := NewForegroundReader(f.snapshots)
	if e != nil {
		t.Fatal(e)
	}
	return f, r
}
func iaAudioRead(t *testing.T, f *browseFixture, r *ForegroundReader, in IAAudioInput) (IAAudioPage, BrowseReadResult, error) {
	t.Helper()
	q, request, output := liveRequests("ia-audio")
	var page IAAudioPage
	var result BrowseReadResult
	_, e := ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, output, func(ctx context.Context, out BrowseOutput) error {
		var e error
		page, result, e = r.IAAudioMembership(ctx, request, time.Second, out, in)
		return e
	})
	return page, result, e
}
func TestIAAudioMembershipOrderAndVersions(t *testing.T) {
	f, r := newIAAudioFixture(t)
	for _, v := range []struct {
		kind, entity int64
		want         []int64
	}{{5, 2001, []int64{2002}}, {6, 2002, []int64{2009, 2003, 2004}}, {8, 2005, []int64{2007, 2006}}} {
		in := IAAudioInput{View: f.input, Entity: v.entity}
		in.View.Kind = v.kind
		in.View.Limit = 1
		var got []int64
		for n := 0; n < 8; n++ {
			page, _, e := iaAudioRead(t, f, r, in)
			if e != nil {
				t.Fatal(e)
			}
			for _, m := range page.Members {
				got = append(got, m.ID)
				if m.ID == 2003 && (len(m.Links) != 2 || m.Links[0].Asset != 2 || m.Links[1].Asset != 1 || m.Links[1].CueOrdinal == nil || m.Links[1].Edition != "é edition") {
					t.Fatal("version/CUE collapse", m)
				}
			}
			if page.Exhausted {
				break
			}
			if page.Next == nil {
				t.Fatal("missing raw continuation")
			}
			in.After = page.Next
		}
		if !reflect.DeepEqual(got, v.want) {
			t.Fatal("canonical membership order", got, v.want)
		}
	}
	for _, v := range []struct {
		kind, entity int64
		links        int
	}{{7, 2003, 2}, {9, 2006, 1}} {
		in := IAAudioInput{View: f.input, Entity: v.entity}
		in.View.Kind = v.kind
		page, result, e := iaAudioRead(t, f, r, in)
		if e != nil || len(page.Links) != v.links || !page.Exhausted || result.LookupStatements < 9 {
			t.Fatal("leaf membership", page, result, e)
		}
	}
}
func TestIAAudioRawCursorAndDrift(t *testing.T) {
	f, r := newIAAudioFixture(t)
	in := IAAudioInput{View: f.input, Entity: 2002}
	in.View.Kind = 6
	in.View.Limit = 1
	page, _, e := iaAudioRead(t, f, r, in)
	if e != nil || page.RawPositions != 2 || page.Next == nil || page.Next.id != 2009 {
		t.Fatal("rejected raw row lost", page, e)
	}
	in.After = page.Next
	f.exec(t, `UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=2002`)
	page, _, e = iaAudioRead(t, f, r, in)
	if !errors.Is(e, ErrStale) || !reflect.DeepEqual(page, IAAudioPage{}) {
		t.Fatal("cursor source drift", page, e)
	}
}
func TestIAAudioAuthorityKindsAndLibrary(t *testing.T) {
	f, r := newIAAudioFixture(t)
	in := IAAudioInput{View: f.input, Entity: 2002}
	in.View.Kind = 8
	page, _, e := iaAudioRead(t, f, r, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAAudioPage{}) {
		t.Fatal("album treated as book", e)
	}
	in.View.Kind = 6
	in.View.Library = 2
	page, _, e = iaAudioRead(t, f, r, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAAudioPage{}) {
		t.Fatal("cross-library root", e)
	}
	in.View.Library = 1
	f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
	page, _, e = iaAudioRead(t, f, r, in)
	if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAAudioPage{}) {
		t.Fatal("revoked authority", e)
	}
}
func TestIAAudioOverCapRefusesCompleteLinks(t *testing.T) {
	f, r := newIAAudioFixture(t)
	for id := int64(3); id <= 18; id++ {
		f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,10,1,'flac','','flac',0,0,10.0,1)`, id, fmt.Sprint(id), fmt.Sprintf("/audio/%d", id))
		f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,library_id) VALUES(2003,?,?,1)`, id, id)
	}
	in := IAAudioInput{View: f.input, Entity: 2003}
	in.View.Kind = 7
	page, _, e := iaAudioRead(t, f, r, in)
	if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(page, IAAudioPage{}) {
		t.Fatal("truncated accepted cohort", e)
	}
}
func TestIAAudioPublicRequiresCustody(t *testing.T) {
	f, r := newIAAudioFixture(t)
	in := IAAudioInput{View: f.input, Entity: 2003}
	in.View.Kind = 7
	page, _, e := r.IAAudioMembership(t.Context(), foregroundRequest(), time.Second, BrowseOutput{}, in)
	if !errors.Is(e, ErrInput) || !reflect.DeepEqual(page, IAAudioPage{}) {
		t.Fatal("missing foreground custody", e)
	}
}

// This wrapper keeps the native forecast path while injecting a change only
// after SQLite has joined and retired the previous physical snapshot.
type iaAudioBoundary struct {
	*governor.Snapshots
	phase  int
	mutate func(int)
}

func (b *iaAudioBoundary) ReadForecasted(ctx context.Context, r governor.Request, budget time.Duration, f governor.SnapshotForecast, fn func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	result, e := b.Snapshots.ReadForecasted(ctx, r, budget, f, fn)
	b.phase++
	b.mutate(b.phase)
	return result, e
}
func TestIAAudioPublicationFences(t *testing.T) {
	for _, mode := range []string{"revoke", "source"} {
		t.Run(mode, func(t *testing.T) {
			f, r := newIAAudioFixture(t)
			in := IAAudioInput{View: f.input, Entity: 2003}
			in.View.Kind = 7
			r.snapshots = &iaAudioBoundary{Snapshots: f.snapshots, mutate: func(phase int) {
				if phase == 1 {
					if mode == "revoke" {
						f.exec(t, `UPDATE authorization_session_families SET revoked=1`)
					} else {
						f.exec(t, `UPDATE catalog_entities SET source_revision=source_revision+1 WHERE id=2003`)
					}
				}
			}}
			page, result, e := iaAudioRead(t, f, r, in)
			if mode == "revoke" {
				if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAAudioPage{}) {
					t.Fatal("late authority leaked", e)
				}
			} else if e != nil || result.Attempts != 2 || len(page.Links) != 2 {
				t.Fatal("source retry", result, e)
			}
		})
	}
}
func TestIAAudioHiddenAndForeignMembers(t *testing.T) {
	t.Run("hidden_root", func(t *testing.T) {
		f, r := newIAAudioFixture(t)
		f.exec(t, `INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated) VALUES('p',10,0)`)
		in := IAAudioInput{View: f.input, Entity: 2003}
		in.View.Kind = 7
		page, _, e := iaAudioRead(t, f, r, in)
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAAudioPage{}) {
			t.Fatal("hidden root disclosed", e)
		}
	})
	t.Run("foreign_member", func(t *testing.T) {
		f, r := newIAAudioFixture(t)
		f.exec(t, `PRAGMA foreign_keys=OFF`)
		f.exec(t, `UPDATE catalog_entities SET library_id=2 WHERE id=2003`)
		f.exec(t, `PRAGMA foreign_keys=ON`)
		in := IAAudioInput{View: f.input, Entity: 2002}
		in.View.Kind = 6
		page, _, e := iaAudioRead(t, f, r, in)
		if !errors.Is(e, ErrDenied) || !reflect.DeepEqual(page, IAAudioPage{}) {
			t.Fatal("foreign member disclosed", e)
		}
	})
}

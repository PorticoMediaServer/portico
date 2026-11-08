package read

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

// TestRBGroupTVBounds pins the explicit complete-small native TV group limits:
// 256 raw rows scanned and 16 distinct referenced seasons. The show episode
// scan also returns same-library entities carrying show_id (the fixture's four
// kind=3 season rows), so those consume raw positions without becoming items.
// Retired episodes likewise consume raw positions even when ineligible.
func TestRBGroupTVBounds(t *testing.T) {
	t.Run("raw-256", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			source := rbGroupSource(t)
			source.exec(t, `UPDATE catalog_entities SET retired=0 WHERE kind=4 AND show_id=2001`)
			source.exec(t, "BEGIN")
			for id := int64(6000); id <= 6171; id++ {
				public := make([]byte, 16)
				binary.LittleEndian.PutUint64(public, uint64(id))
				source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,4)`, id, public)
				source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,show_id,season_id,numbering,episode_number) VALUES(?,?,1,4,?,?,'a',?,'a',0,?,?,2001,2002,'seasonal',?)`, id, public, fmt.Sprint("E", id), fmt.Sprint("E", id), fmt.Sprint("E", id), public, public, id-5000)
			}
			source.exec(t, "COMMIT")

			defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBTVDefinitions()...)
			f := composeBrowseFixture(t, source, defs)
			in := RBTVInput{View: f.input, Subject: 2001, Scope: RBTVShow, Group: RBTVAll}
			in.View.Kind = 4
			p, _, e := f.reader.rbTVGroupExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
			// 80 live fixture episodes + 172 added episodes + 4 season rows = 256 raw.
			if e != nil || p.RawRows != 256 || len(p.Items) != 252 {
				t.Fatalf("raw 256: raw=%d items=%d err=%v", p.RawRows, len(p.Items), e)
			}
		})
	})

	t.Run("raw-257", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			source := rbGroupSource(t)
			source.exec(t, `UPDATE catalog_entities SET retired=0 WHERE kind=4 AND show_id=2001`)
			source.exec(t, "BEGIN")
			for id := int64(6000); id <= 6172; id++ {
				public := make([]byte, 16)
				binary.LittleEndian.PutUint64(public, uint64(id))
				source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,4)`, id, public)
				source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,show_id,season_id,numbering,episode_number) VALUES(?,?,1,4,?,?,'a',?,'a',0,?,?,2001,2002,'seasonal',?)`, id, public, fmt.Sprint("E", id), fmt.Sprint("E", id), fmt.Sprint("E", id), public, public, id-5000)
			}
			source.exec(t, "COMMIT")
			// The tail is retired but still consumes the raw row bound.
			source.exec(t, `UPDATE catalog_entities SET retired=1 WHERE id=6172`)

			defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBTVDefinitions()...)
			f := composeBrowseFixture(t, source, defs)
			in := RBTVInput{View: f.input, Subject: 2001, Scope: RBTVShow, Group: RBTVAll}
			in.View.Kind = 4
			p, _, e := f.reader.rbTVGroupExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
			if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(p, RBTVResult{}) {
				t.Fatalf("raw 257: raw=%d items=%d err=%v", p.RawRows, len(p.Items), e)
			}
		})
	})

	t.Run("seasons-16", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			source := rbGroupSource(t)
			source.exec(t, `UPDATE catalog_entities SET retired=0 WHERE kind=4 AND show_id=2001`)
			seasons := []int64{2002, 2009, 2011, 2012}
			source.exec(t, "BEGIN")
			for n := int64(0); n < 12; n++ {
				id := int64(2100) + n
				public := make([]byte, 16)
				binary.LittleEndian.PutUint64(public, uint64(id))
				source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,3)`, id, public)
				source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,show_id,season_number) VALUES(?,?,1,3,?,?,'a',?,'a',0,?,?,2001,?)`, id, public, fmt.Sprint("S", id), fmt.Sprint("S", id), fmt.Sprint("S", id), public, public, int64(20)+n)
				seasons = append(seasons, id)
			}
			for n := int64(0); n < 80; n++ {
				source.exec(t, `UPDATE catalog_entities SET season_id=? WHERE id=?`, seasons[n%16], 5000+n)
			}
			source.exec(t, "COMMIT")

			defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBTVDefinitions()...)
			f := composeBrowseFixture(t, source, defs)
			in := RBTVInput{View: f.input, Subject: 2001, Scope: RBTVShow, Group: RBTVAll}
			in.View.Kind = 4
			p, _, e := f.reader.rbTVGroupExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
			// 80 live episodes + 16 referenced season rows = 96 raw.
			if e != nil || p.RawRows != 96 || len(p.Items) != 80 {
				t.Fatalf("seasons 16: raw=%d items=%d err=%v", p.RawRows, len(p.Items), e)
			}
			hydrated := map[int64]bool{}
			for _, item := range p.Items {
				hydrated[item.SeasonNumber] = true
			}
			if len(hydrated) != 16 {
				t.Fatalf("hydrated seasons=%d", len(hydrated))
			}
		})
	})

	t.Run("seasons-17", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			source := rbGroupSource(t)
			source.exec(t, `UPDATE catalog_entities SET retired=0 WHERE kind=4 AND show_id=2001`)
			seasons := []int64{2002, 2009, 2011, 2012}
			source.exec(t, "BEGIN")
			for n := int64(0); n < 13; n++ {
				id := int64(2100) + n
				public := make([]byte, 16)
				binary.LittleEndian.PutUint64(public, uint64(id))
				source.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,3)`, id, public)
				source.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,show_id,season_number) VALUES(?,?,1,3,?,?,'a',?,'a',0,?,?,2001,?)`, id, public, fmt.Sprint("S", id), fmt.Sprint("S", id), fmt.Sprint("S", id), public, public, int64(20)+n)
				seasons = append(seasons, id)
			}
			for n := int64(0); n < 80; n++ {
				source.exec(t, `UPDATE catalog_entities SET season_id=? WHERE id=?`, seasons[n%17], 5000+n)
			}
			source.exec(t, "COMMIT")

			defs := append(append(browseDefinitions(), RBGroupDefinitions()...), RBTVDefinitions()...)
			f := composeBrowseFixture(t, source, defs)
			in := RBTVInput{View: f.input, Subject: 2001, Scope: RBTVShow, Group: RBTVAll}
			in.View.Kind = 4
			p, _, e := f.reader.rbTVGroupExact(t.Context(), rbGroupRequest(), time.Second, f.output, in)
			if !errors.Is(e, ErrNotReady) || !reflect.DeepEqual(p, RBTVResult{}) {
				t.Fatalf("seasons 17: raw=%d items=%d err=%v", p.RawRows, len(p.Items), e)
			}
		})
	})
}

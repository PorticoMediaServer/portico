package read

import (
	"encoding/binary"
	"testing"
)

func TestIAAudioNullableOrderingFacts(t *testing.T) {
	f, r := newIAAudioFixture(t)
	for _, v := range []struct {
		id, kind, parent int64
		column           string
	}{{2010, 7, 2002, "album_id"}, {2011, 9, 2005, "book_id"}} {
		public := make([]byte, 16)
		binary.LittleEndian.PutUint64(public, uint64(v.id))
		f.exec(t, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,1,?)`, v.id, public, v.kind)
		f.exec(t, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,`+v.column+`,admission_own_age) VALUES(?,?,1,?,'Null order','audio','audio','audio','audio',0,?,?,?,12)`, v.id, public, v.kind, public, public, v.parent)
	}
	for _, v := range []struct{ kind, entity, want int64 }{{6, 2002, 2010}, {8, 2005, 2011}} {
		in := IAAudioInput{View: f.input, Entity: v.entity}
		in.View.Kind = v.kind
		in.View.Limit = 1
		page, _, e := iaAudioRead(t, f, r, in)
		if e != nil || len(page.Members) != 1 || page.Members[0].ID != v.want {
			t.Fatal("NULL order placement", page, e)
		}
		m := page.Members[0]
		if m.Disc != nil || m.Track != nil || m.Part != nil {
			t.Fatal("normalized order fabricated metadata", m)
		}
	}
}

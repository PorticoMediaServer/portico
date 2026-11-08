package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"testing"
)

func TestRBPersonalFilterPublicCompilerParity(t *testing.T) {
	data, err := os.ReadFile("testdata/rb-personal-filter-parity.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Query              json.RawMessage
		Where              string
		Args               []any
		Profile            string
		ItemPivot, Bounded bool
	}
	if err = json.Unmarshal(plain, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 95 {
		t.Fatal("incomplete public compiler fixture", len(vectors))
	}
	f := newBrowseSource(t)
	f.exec(t, "DELETE FROM personal_items")
	zero, five, ten, three := int64(0), int64(5), int64(10), int64(3)
	facts := []rbPersonalFilterFacts{
		{},
		{present: true},
		{present: true, favorite: true, rating: &zero},
		{present: true, watchlisted: true, rating: &five},
		{present: true, favorite: true, watchlisted: true, rating: &ten},
		{present: true, rating: &three},
		{}, // a different profile's personal row is not this viewer's state
		{present: true, favorite: true},
	}
	for i, fact := range facts {
		if !fact.present {
			continue
		}
		var rating any
		if fact.rating != nil {
			rating = *fact.rating
		}
		f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,favorite,watchlisted,rating) SELECT 'p',public_id,id,library_id,?,?,? FROM catalog_entities WHERE id=?`, fact.favorite, fact.watchlisted, rating, int64(i+1))
	}
	f.exec(t, `INSERT INTO personal_items(profile_id,item_public_id,item_id,library_id,favorite,watchlisted,rating) SELECT 'q',public_id,id,library_id,1,1,10 FROM catalog_entities WHERE id=7`)
	for _, v := range vectors {
		if v.Profile != "p" || !v.ItemPivot || !v.Bounded {
			t.Fatal("fixture changed source scope")
		}
		filter, err := compileRBPersonalFilter(v.Query)
		if err != nil {
			t.Fatal(string(v.Query), err)
		}
		var got, want []int64
		for i, fact := range facts {
			if filter.matches(fact) {
				got = append(got, int64(i+1))
			}
		}
		// Only the test oracle executes the frozen public SQL. This is not a
		// registered source read or a new native membership/authority recipe.
		rows, err := f.conn.QueryContext(t.Context(), "SELECT e.entity_id FROM (SELECT id AS entity_id FROM catalog_entities WHERE id<=8) AS e WHERE "+v.Where+" ORDER BY e.entity_id", v.Args...)
		if err != nil {
			t.Fatal(string(v.Query), v.Where, err)
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			want = append(want, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || !slices.Equal(got, want) {
			t.Fatal(string(v.Query), "native", got, "public", want, err)
		}
	}
}

func TestRBPersonalFilterClosedVocabulary(t *testing.T) {
	personal := json.RawMessage(`{"variant":"predicate","field":"favorite","operator":"equals","value":true}`)
	for _, raw := range []json.RawMessage{personal, append(append([]byte(`{"variant":"group","not":`), personal...), '}')} {
		if _, err := compileRBScalarFilter(raw); !errors.Is(err, ErrUnqualified) {
			t.Fatal("personal field entered shared scalar/LC grammar", err)
		}
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"variant":"predicate","field":"year","operator":"equals","value":2000}`),
		json.RawMessage(`{"variant":"group","not":{"variant":"predicate","field":"savedCollection","operator":"equals","value":"private"}}`),
	} {
		if _, err := compileRBPersonalFilter(raw); !errors.Is(err, ErrUnqualified) {
			t.Fatal("unsupported personal source compiled", err)
		}
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"variant":"predicate","field":"favorite","operator":"equals","value":1}`),
		json.RawMessage(`{"variant":"predicate","field":"watchlisted","operator":"in","value":[true]}`),
	} {
		if _, err := compileRBPersonalFilter(raw); err == nil {
			t.Fatal("invalid personal atom accepted")
		}
	}
}

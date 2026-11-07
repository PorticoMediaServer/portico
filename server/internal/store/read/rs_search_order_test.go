package read

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestRSSearchOrderNativeOracle(t *testing.T) {
	f := newBrowseSource(t)
	f.exec(t, `CREATE TEMP TABLE rs_search_order_oracle(id INTEGER PRIMARY KEY,sort_key TEXT COLLATE NOCASE,sort_name TEXT,year INTEGER,added TEXT)`)
	keys := []string{"École", "école", "eCole", "Ecole", "東京", "\x00x", "\x00yy", "e\u0301cole", "Α", "α", "Z"}
	var entities, people []FullSearchHit
	for i, key := range keys {
		id := int64(i + 1)
		year := int64(i % 3)
		added := ""
		var stored any
		if i%3 != 0 {
			added = fmt.Sprintf("2026-11-%02dT00:00:00.000000000Z", i%2+1)
			stored = added
		}
		f.exec(t, `INSERT INTO rs_search_order_oracle VALUES(?,?,?,?,?)`, id, key, key, year, stored)
		e := rsSearchTestHit(1, id, 1)
		e.SortKey = key
		e.Year = year
		e.AddedAt = added
		entities = append(entities, e)
		e = rsSearchTestHit(RSSearchPeople, id, 1)
		e.SortKey = key
		e.Year = year
		e.AddedAt = added
		people = append(people, e)
	}
	for _, family := range []struct {
		name string
		hits []FullSearchHit
	}{{"entity", entities}, {"people", people}} {
		for _, order := range []string{"title", "releaseYear", "dateAdded"} {
			for _, direction := range []string{"asc", "desc"} {
				t.Run(family.name+"/"+order+"/"+direction, func(t *testing.T) {
					expression := map[string]string{"title": "sort_key", "releaseYear": "year", "dateAdded": "COALESCE(added,'')"}[order]
					if family.hits[0].Subject == search.ManifestPerson && order == "title" {
						expression = "sort_name"
					}
					rows, e := f.conn.QueryContext(t.Context(), `SELECT id FROM rs_search_order_oracle ORDER BY `+expression+` `+direction+`,id `+direction)
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
					if e = rows.Err(); e != nil {
						t.Fatal(e)
					}
					rows.Close()
					hits := append([]FullSearchHit(nil), family.hits...)
					sort.Slice(hits, func(i, j int) bool { return rsSearchCompare(hits[i], hits[j], order, direction) < 0 })
					var got []int64
					for _, h := range hits {
						got = append(got, h.ID)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatal("canonical source ordering differs from native SQL", got, want)
					}
				})
			}
		}
	}
	if rsSearchOrderFor(RSSearchPeople, "releaseYear") != "title" || rsSearchOrderFor(13, "dateAdded") != "title" {
		t.Fatal("unsupported group sort fallback changed")
	}
}

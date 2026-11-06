package read

import (
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const maxOwnerPositions = 64

type ownerRelation uint8

const (
	personWorks ownerRelation = iota + 1
	artistAlbums
	artistSongs
	showChildren
	authoredMembers
)

// A raw position is never a match or a permission proof. The accepted-head,
// source/publication, owner/work visibility and complete-term verifier follows
// this pager. Hidden/off-head/duplicate rows consume the SAME raw budget.
type ownerPosition struct{ Work, Ordinal, Image int64 }
type ownerCursor struct {
	relation ownerRelation
	owner    int64
	position ownerPosition
}
type ownerPage struct {
	positions []ownerPosition
	after     *ownerCursor
	exhausted bool
}

func readOwnerPage(query browseQuery, relation ownerRelation, owner int64, after *ownerCursor, limit int) (ownerPage, error) {
	if owner <= 0 || limit < 1 || limit > maxOwnerPositions || relation < personWorks || relation > authoredMembers ||
		after != nil && (after.relation != relation || after.owner != owner || after.position.Work <= 0) {
		return ownerPage{}, ErrInput
	}
	names := map[ownerRelation]string{personWorks: "person", artistAlbums: "artist", artistSongs: "song_artist", showChildren: "show", authoredMembers: "collection"}
	prefix := "full.owner." + names[relation] + "."
	type seek struct {
		name string
		args []any
	}
	seeks := []seek{{"first", []any{owner}}}
	if after != nil {
		p := after.position
		seeks = []seek{{"next", []any{owner, p.Work}}}
		if relation == personWorks {
			if p.Ordinal < 0 || p.Image <= 0 {
				return ownerPage{}, ErrInput
			}
			seeks = []seek{{"image_tail", []any{owner, p.Work, p.Ordinal, p.Image}}, {"ordinal_tail", []any{owner, p.Work, p.Ordinal}}, {"next", []any{owner, p.Work}}}
		}
	}
	var page ownerPage
	for _, seek := range seeks {
		rows, err := query(prefix+seek.name, append(seek.args, int64(limit-len(page.positions)))...)
		if err != nil {
			return ownerPage{}, err
		}
		if len(rows) > limit-len(page.positions) {
			return ownerPage{}, ErrUnqualified
		}
		for _, row := range rows {
			p := ownerPosition{Work: row[0].(int64)}
			if relation == personWorks {
				p.Ordinal, p.Image = row[1].(int64), row[2].(int64)
			}
			if p.Work <= 0 || p.Ordinal < 0 || relation == personWorks && p.Image <= 0 {
				return ownerPage{}, ErrUnqualified
			}
			previous := after
			if page.after != nil {
				previous = page.after
			}
			if previous != nil && compareOwnerPosition(p, previous.position) >= 0 {
				return ownerPage{}, ErrUnqualified
			}
			page.positions = append(page.positions, p)
			page.after = &ownerCursor{relation, owner, p}
		}
		if len(page.positions) > limit {
			return ownerPage{}, ErrUnqualified
		}
		if len(page.positions) == limit {
			return page, nil
		}
	}
	page.exhausted = true
	return page, nil
}

func compareOwnerPosition(a, b ownerPosition) int {
	if a.Work < b.Work {
		return -1
	}
	if a.Work > b.Work {
		return 1
	}
	if a.Ordinal < b.Ordinal {
		return -1
	}
	if a.Ordinal > b.Ordinal {
		return 1
	}
	if a.Image < b.Image {
		return -1
	}
	if a.Image > b.Image {
		return 1
	}
	return 0
}

// FullSearchDefinitions is inert metadata for the merged owner-reverse paths.
// Album ID-order and automatic membership are deliberately not represented by
// a different relation: their actual draft/index/accepted-evidence paths must
// land before an adapter can execute them. No bank/FTS authority is minted.
func FullSearchDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, plan stmt.Plan) {
		empty := append([]any(nil), bindings...)
		empty[0] = int64(99999)
		out = append(out, stmt.Definition{ID: stmt.ID("read.full.owner." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/owner_reverse.go:readOwnerPage"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: plan})
	}
	person := "SELECT entity_id,ord,image_id FROM catalog_credits INDEXED BY catalog_credits_person WHERE person_id=?"
	p := func() stmt.Plan { return stmt.Plan{Hot: true, Indexes: []string{"catalog_credits_person"}} }
	add("person.first", person+" ORDER BY entity_id DESC,ord DESC,image_id DESC LIMIT ?", []stmt.Param{i("owner"), i("limit")}, []any{int64(1), int64(2)}, p())
	add("person.image_tail", person+" AND entity_id=? AND ord=? AND image_id<? ORDER BY image_id DESC LIMIT ?", []stmt.Param{i("owner"), i("work"), i("ordinal"), i("image"), i("limit")}, []any{int64(1), int64(1), int64(1), int64(3), int64(2)}, p())
	add("person.ordinal_tail", person+" AND entity_id=? AND ord<? ORDER BY ord DESC,image_id DESC LIMIT ?", []stmt.Param{i("owner"), i("work"), i("ordinal"), i("limit")}, []any{int64(1), int64(1), int64(2), int64(2)}, p())
	add("person.next", person+" AND entity_id<? ORDER BY entity_id DESC,ord DESC,image_id DESC LIMIT ?", []stmt.Param{i("owner"), i("work"), i("limit")}, []any{int64(1), int64(2), int64(2)}, p())
	for _, route := range []struct{ name, table, index, owner, id, predicate string }{
		{"artist", "catalog_entities", "catalog_entities_artist", "artist_id", "id", "artist_id IS NOT NULL AND "},
		{"song_artist", "catalog_song_artists", "catalog_song_artists_artist", "artist_id", "song_id", ""},
		{"show", "catalog_entities", "catalog_entities_show_fk", "show_id", "id", "show_id IS NOT NULL AND "},
		{"collection", "catalog_collection_members", "sqlite_autoindex_catalog_collection_members_1", "collection_id", "item_id", ""},
	} {
		base := fmt.Sprintf("SELECT %s FROM %s INDEXED BY %s WHERE %s%s=?", route.id, route.table, route.index, route.predicate, route.owner)
		plan := stmt.Plan{Hot: true, Indexes: []string{route.index}}
		if route.name == "collection" {
			plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: route.table, Name: route.index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: route.owner, Collation: "BINARY"}, {Name: route.id, Collation: "BINARY"}}}}
		}
		add(route.name+".first", base+" ORDER BY "+route.id+" DESC LIMIT ?", []stmt.Param{i("owner"), i("limit")}, []any{int64(1), int64(2)}, plan)
		add(route.name+".next", base+" AND "+route.id+"<? ORDER BY "+route.id+" DESC LIMIT ?", []stmt.Param{i("owner"), i("work"), i("limit")}, []any{int64(1), int64(3), int64(2)}, plan)
	}
	field := stmt.Plan{Hot: true, Indexes: []string{"search_fragment_publication"}}
	base := "SELECT ordinal,prefix_tokens,lexical_tokens,normalized,source_revision,generation,owner_entity,owner_person,search_generation_id FROM search_fragments INDEXED BY search_fragment_publication WHERE publication_id=? AND field=?"
	add("field.first", base+" ORDER BY ordinal ASC LIMIT ?", []stmt.Param{i("publication"), i("field"), i("limit")}, []any{int64(1), int64(4), int64(64)}, field)
	add("field.next", base+" AND ordinal>? ORDER BY ordinal ASC LIMIT ?", []stmt.Param{i("publication"), i("field"), i("after"), i("limit")}, []any{int64(1), int64(4), int64(0), int64(64)}, field)
	return out
}

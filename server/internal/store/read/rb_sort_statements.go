package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

type rbScalarOrder struct {
	id                       ScalarSort
	column, index, predicate string
	kind                     int64 // capture cohort, not request-selected SQL
	typ                      stmt.Type
	kinds                    []int64
}

func rbScalarOrders() []rbScalarOrder {
	return []rbScalarOrder{
		{SortYear, "year", "catalog_entities_year", "", 1, stmt.Integer, []int64{1, 2, 3, 4, 6, 7, 8, 9}},
		{SortDuration, "duration_key", "catalog_entities_duration", "kind IN(1,2,3,4,5,6,7,8,9,10)", 1, stmt.Real, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{SortCommunityRating, "community_rating_key", "catalog_entities_rating", "kind IN(1,2,3,4,5,6,7,8,9,10,13)", 1, stmt.Real, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{SortCriticRating, "critic_rating_key", "catalog_entities_critic", "kind IN(1,2,3,4,10)", 1, stmt.Real, []int64{1, 2, 3, 4, 10}},
		{SortEpisodeCount, "episode_count", "catalog_entities_episode_count", "kind IN(2,3)", 2, stmt.Integer, []int64{2, 3}},
		{SortLatestAired, "latest_aired_key", "catalog_entities_latest_aired", "kind IN(2,3)", 2, stmt.Text, []int64{2}},
		{SortReleaseDate, "release_key", "catalog_entities_release", "kind IN(1,2,3,4,6,8)", 1, stmt.Text, []int64{1, 2, 3, 4, 6, 8}},
		{SortAired, "release_key", "catalog_entities_release", "kind IN(1,2,3,4,6,8)", 4, stmt.Text, []int64{4}},
		{SortTrackNumber, "track_key", "catalog_entities_track", "kind=7", 7, stmt.Integer, []int64{7}},
		{SortFileSize, "file_size_key", "catalog_entities_file_size", "kind IN(1,2,3,4,5,6,7,8,9,10)", 1, stmt.Integer, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
	}
}
func (s rbScalarOrder) supports(kind int64) bool {
	for _, k := range s.kinds {
		if kind == k {
			return true
		}
	}
	return false
}
func rbScalarSpec(id ScalarSort) (rbScalarOrder, bool) {
	for _, s := range rbScalarOrders() {
		if s.id == id {
			return s, true
		}
	}
	return rbScalarOrder{}, false
}

// RBBrowseDefinitions is an inert closed composition. Each harness obtains its
// own actual Reader Capture receipt before registration; no shared-list append.
func RBBrowseDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbScalarDefinitions()...)
}
func rbScalarDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	for _, s := range rbScalarOrders() {
		for _, desc := range []bool{false, true} {
			direction, order, op := "asc.", "ASC", ">"
			if desc {
				direction, order, op = "desc.", "DESC", "<"
			}
			base := "SELECT id," + s.column + ",public_id,title,retired,revision,admission_own_age FROM catalog_entities INDEXED BY " + s.index + " WHERE library_id=? AND kind=?"
			if s.predicate != "" {
				base += " AND " + s.predicate
			}
			end := " ORDER BY " + s.column + " " + order + ",id " + order + " LIMIT ?"
			key := func(v int64) any {
				switch s.typ {
				case stmt.Real:
					return float64(v)
				case stmt.Text:
					return string(rune('0' + v))
				default:
					return v
				}
			}
			add := func(phase, sql, seek string, params []stmt.Param, bindings []any) {
				empty := append([]any(nil), bindings...)
				empty[0] = int64(3)
				out = append(out, stmt.Definition{ID: statementID("rb.scalar." + string(s.id) + "." + direction + phase), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_sort.go:BrowseReader.SortedWindow"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{s.index}, Seeks: []string{seek}}})
			}
			add("first", base+end, "library_id=? AND kind=?", []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), s.kind, int64(MaxRaw)})
			after := int64(0)
			nextKey := int64(0)
			if desc {
				after = 1 << 62
				nextKey = 2
			}
			add("tail", base+" AND "+s.column+"=? AND id"+op+"?"+end, s.column+"=? AND id"+op+"?", []stmt.Param{i("library"), i("kind"), {Name: "key", Type: s.typ}, i("after"), i("limit")}, []any{int64(1), s.kind, key(1), after, int64(MaxRaw)})
			add("next", base+" AND "+s.column+op+"?"+end, s.column+op+"?", []stmt.Param{i("library"), i("kind"), {Name: "key", Type: s.typ}, i("limit")}, []any{int64(1), s.kind, key(nextKey), int64(MaxRaw)})
		}
	}
	return out
}

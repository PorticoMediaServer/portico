package catalog

import (
	"errors"
	"reflect"
	"testing"
)

func TestShowWorkspaceEpisodeDirectionPagesAndBindsCursor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, grouped := range []bool{false, true} {
		t.Run(map[bool]string{false: "season", true: "group"}[grouped], func(t *testing.T) {
			s, db, names := workspaceFixture(t)
			r := workspaceRequest(names)
			r.SelectedSeasonID = names["season1"].Public
			if grouped {
				if _, err := db.Exec(`INSERT INTO catalog_show_settings(entity_id,hide_seasons,revision) VALUES(?,1,1)`, names["show"].ID); err != nil {
					t.Fatal(err)
				}
				r.SelectedSeasonID = ""
				r.Group = "all"
			}
			collect := func(direction string) []string {
				t.Helper()
				q := r
				q.EpisodeDirection = direction
				var ids []string
				for {
					w, err := s.ShowWorkspace(q)
					if err != nil {
						t.Fatal(err)
					}
					if w.Episodes.Query.Direction != direction || w.Episodes.Query.Sort != "episode" {
						t.Fatalf("actual order missing: %+v", w.Episodes.Query)
					}
					section := w.Episodes.Sections[0]
					for _, entry := range section.Entries {
						ids = append(ids, entry.ID)
					}
					if section.NextCursor == "" {
						break
					}
					q.Cursor = section.NextCursor
				}
				return ids
			}
			asc, desc := collect("asc"), collect("desc")
			for i, j := 0, len(desc)-1; i < j; i, j = i+1, j-1 {
				desc[i], desc[j] = desc[j], desc[i]
			}
			if len(asc) < 200 || !reflect.DeepEqual(asc, desc) {
				t.Fatalf("orders do not cover the same whole source: %d/%d", len(asc), len(desc))
			}
			first, err := s.ShowWorkspace(r)
			if err != nil {
				t.Fatal(err)
			}
			r.Cursor = first.Episodes.Sections[0].NextCursor
			for _, mutate := range []func(*ShowWorkspaceRequest){func(q *ShowWorkspaceRequest) { q.EpisodeDirection = "desc" }, func(q *ShowWorkspaceRequest) { q.PlayState = "unplayed" }, func(q *ShowWorkspaceRequest) { q.Viewer.Fence = "another"; q.ViewerFence = "another" }} {
				changed := r
				mutate(&changed)
				if _, err = s.ShowWorkspace(changed); !errors.Is(err, ErrCursor) {
					t.Fatalf("cursor accepted changed query: %v", err)
				}
			}
			r.Cursor = ""
			r.EpisodeDirection = "sideways"
			if _, err = s.ShowWorkspace(r); !errors.Is(err, ErrShowContext) {
				t.Fatalf("invalid order accepted: %v", err)
			}
		})
	}
}

// Equal episode numbers can coexist under different numbering schemes. Keep
// four real fixture entities/files so the two equal numbers straddle a page
// boundary in BOTH directions, without dropping the production unique index.
func TestShowWorkspaceEpisodeDirectionTiedBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, grouped := range []bool{false, true} {
		t.Run(map[bool]string{false: "season", true: "group"}[grouped], func(t *testing.T) {
			s, db, names := workspaceFixture(t)
			if _, err := db.Exec(`DELETE FROM catalog_asset_links WHERE entity_id IN (SELECT entity_id FROM catalog_episodes WHERE show_id=?) AND entity_id NOT IN (?,?,?,?)`, names["show"].ID, names["ep-001"].ID, names["ep-002"].ID, names["ep-003"].ID, names["ep-004"].ID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE catalog_episodes SET numbering='absolute',number=2 WHERE entity_id=?`, names["ep-003"].ID); err != nil {
				t.Fatal(err)
			}
			r := workspaceRequest(names)
			r.Limit = 2
			r.SelectedSeasonID = names["season1"].Public
			if grouped {
				if _, err := db.Exec(`INSERT INTO catalog_show_settings(entity_id,hide_seasons,revision) VALUES(?,1,1)`, names["show"].ID); err != nil {
					t.Fatal(err)
				}
				r.SelectedSeasonID = ""
				r.Group = "all"
			}
			orders := map[string][]string{}
			for _, direction := range []string{"asc", "desc"} {
				q := r
				q.EpisodeDirection = direction
				first, err := s.ShowWorkspace(q)
				if err != nil {
					t.Fatal(err)
				}
				page := first.Episodes.Sections[0]
				if len(page.Entries) != 2 || page.NextCursor == "" {
					t.Fatalf("missing first boundary: %+v", page)
				}
				q.Cursor = page.NextCursor
				second, err := s.ShowWorkspace(q)
				if err != nil {
					t.Fatal(err)
				}
				tail := second.Episodes.Sections[0]
				if len(tail.Entries) != 2 || tail.NextCursor != "" {
					t.Fatalf("missing final boundary: %+v", tail)
				}
				if page.Entries[1].EpisodeNumber == nil || tail.Entries[0].EpisodeNumber == nil || *page.Entries[1].EpisodeNumber != 2 || *tail.Entries[0].EpisodeNumber != 2 {
					t.Fatal("fixture did not cross the tied-number boundary")
				}
				seen := map[string]bool{}
				for _, entry := range append(page.Entries, tail.Entries...) {
					if seen[entry.ID] {
						t.Fatal("duplicate boundary entity", entry.ID)
					}
					seen[entry.ID] = true
					orders[direction] = append(orders[direction], entry.ID)
				}
			}
			for i, id := range orders["asc"] {
				if id != orders["desc"][3-i] {
					t.Fatal("descending tuple is not exact reverse", orders)
				}
			}
		})
	}
}

func TestShowWorkspaceDefaultAscendingDoesNotWriteNewestFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, names := workspaceFixture(t)
	if _, err := db.Exec(`INSERT INTO catalog_show_settings(entity_id,newest_first,revision) VALUES(?,1,9)`, names["show"].ID); err != nil {
		t.Fatal(err)
	}
	r := workspaceRequest(names)
	r.SelectedSeasonID = names["season1"].Public
	before, err := readShowSettings(db, names["show"].Public)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.ShowWorkspace(r)
	if err != nil {
		t.Fatal(err)
	}
	if w.Episodes.Query.Direction != "asc" || w.Episodes.Sections[0].Entries[0].ID != names["ep-001"].Public {
		t.Fatal("omitted workspace direction did not override newestFirst", w.Episodes.Query)
	}
	after, err := readShowSettings(db, names["show"].Public)
	if err != nil {
		t.Fatal(err)
	}
	if !before.NewestFirst || before.Revision != 9 || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(before, w.Settings) {
		t.Fatal("workspace read changed owner settings", before, after, w.Settings)
	}
}

package httpapi

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
)

// Listening facts are additive to the recorded read-path answers. Expectations
// use the original expected entry IDs and Tiny's raw facts, never an actual
// response or the listening/browse enrichment implementation.
func goldenListeningExtension(t *testing.T, f *goldenFixture, c goldenCase, want string) string {
	t.Helper()
	if c.Status != 200 || !(strings.HasPrefix(c.Name, "browse:") || strings.HasPrefix(c.Name, "content:")) {
		return want
	}
	var doc any
	if err := json.Unmarshal([]byte(want), &doc); err != nil {
		t.Fatal(err)
	}
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case []any:
			for _, item := range v {
				visit(item)
			}
		case map[string]any:
			id, hasID := v["id"].(string)
			_, hasTitle := v["title"].(string)
			if hasID && hasTitle && f.public[id] != "" {
				switch v["kind"] {
				case "song":
					// These two newly issued golden profiles have no favorite events.
					v["favorite"] = false
				case "album":
					// Tiny has only seeded local genre terms, three songs and
					// two distinct terms per song. Read those source facts directly.
					rows, err := f.db.Query(`SELECT DISTINCT g.source_name FROM catalog_term_sources g JOIN catalog_terms term ON term.id=g.term_id JOIN catalog_songs song ON song.entity_id=g.entity_id JOIN catalog_entities album ON album.id=song.album_id WHERE album.public_id=pid_blob(?) AND term.vocab=1 ORDER BY g.source_name COLLATE NOCASE`, f.public[id])
					if err != nil {
						t.Fatal(err)
					}
					genres := []string{}
					for rows.Next() {
						var genre string
						if err = rows.Scan(&genre); err != nil {
							t.Fatal(err)
						}
						genres = append(genres, genre)
					}
					if err = rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					if len(genres) != fixture.Tiny().SongsPerAlbum*fixture.Tiny().GenresPerItem {
						t.Fatal("Tiny album genre facts", id, genres)
					}
					v["genres"] = genres
				case "artist":
					v["albumCount"] = fixture.Tiny().AlbumsPerArtist
				case "book":
					v["narrators"] = []string{"Narrator"}
					rows, err := f.db.Query(`SELECT max(a.duration) FROM catalog_entities b JOIN catalog_book_files p ON p.book_id=b.id JOIN catalog_asset_links l ON l.entity_id=p.entity_id JOIN catalog_assets a ON a.id=l.asset_id WHERE b.public_id=pid_blob(?) GROUP BY p.entity_id`, f.public[id])
					if err != nil {
						t.Fatal(err)
					}
					parts, seconds := 0, 0.0
					for rows.Next() {
						var n float64
						if err = rows.Scan(&n); err != nil {
							t.Fatal(err)
						}
						if n <= 0 {
							t.Fatal("Tiny book has no known duration", id)
						}
						parts++
						seconds += n
					}
					if err = rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					if parts != fixture.Tiny().FilesPerBook {
						t.Fatal("Tiny book part count", id, parts)
					}
					v["durationMs"] = int64(seconds * 1000)
				}
			}
			for _, child := range v {
				visit(child)
			}
		}
	}
	if root, ok := doc.(map[string]any); ok && strings.HasPrefix(c.Name, "content:music:") {
		// MA removes unsupported Instant mix; every other offered action remains exact.
		root["listening"].(map[string]any)["actions"] = []string{"play", "shuffle", "enqueue"}
		if c.Name == "content:music:discover/open" {
			// Tiny has no recent seven-day albums, personal plays or accessible playlists.
			// The new Discover policy therefore contains only Artists and Genres, using
			// the already recorded canonical memberships/orders/counts at the same limit8.
			raw, err := os.ReadFile("testdata/readpath_golden.json")
			if err != nil {
				t.Fatal(err)
			}
			var recorded []goldenCase
			if err = json.Unmarshal(raw, &recorded); err != nil {
				t.Fatal(err)
			}
			sections := []any{}
			for _, spec := range []struct{ id, title string }{{"artists", "Artists"}, {"genres", "Genres"}} {
				found := false
				for _, old := range recorded {
					if old.Name != "browse:fixture-music:"+spec.id+":title:asc/open" {
						continue
					}
					found = true
					response := goldenHomeCoverDecoration(t, f, old, old.Response)
					response = goldenListeningExtension(t, f, old, response)
					var page map[string]any
					if err = json.Unmarshal([]byte(response), &page); err != nil {
						t.Fatal(err)
					}
					info := page["pageInfo"].(map[string]any)
					sections = append(sections, map[string]any{"id": spec.id, "type": "rail", "heading": map[string]any{"key": "section." + spec.id, "fallback": spec.title}, "entries": page["entries"], "totalCount": info["total"], "nextCursor": info["nextCursor"], "start": 0})
				}
				if !found {
					t.Fatal("missing recorded canonical membership", spec.id)
				}
			}
			root["sections"] = sections
		}
	}
	visit(doc)
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return canonical(string(b))
}

package catalog

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A class build reads its library in 100-row chunks. Each chunk must be a
// walk forward from the cursor, never a sort of the whole library (which made
// a class build quadratic in the library: a 588,000-song music class did not
// finish in 16 minutes). Planned both before statistics exist and after.
func TestVisibilityClassChunksWalkForward(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 40; i++ {
		c.Movie(movies, "/m/"+string(rune('a'+i%26))+string(rune('a'+i/26))+".mkv", "Film", 2000+i)
	}
	c.Drain()
	for _, stage := range []string{"no statistics", "analyzed"} {
		if stage == "analyzed" {
			if _, err := c.DB.Exec(`ANALYZE`); err != nil {
				t.Fatal(err)
			}
		}
		rows, err := c.DB.Query(`EXPLAIN QUERY PLAN `+visibilityChunkSQL, 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, " | ")
		if strings.Contains(joined, "TEMP B-TREE") || !strings.Contains(joined, "INTEGER PRIMARY KEY") {
			t.Fatalf("%s: class chunk is not a forward walk: %s", stage, joined)
		}
	}
}

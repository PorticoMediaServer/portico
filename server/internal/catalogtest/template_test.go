package catalogtest

import (
	"fmt"
	"testing"
)

func TestTemplatesIndependentCopiesAndWorkers(t *testing.T) {
	if testing.Short() {
		t.Skip("fixture snapshot isolation runs in the full merge tier")
	}
	suite := NewTemplates()
	t.Cleanup(suite.Close)
	builds := 0
	var source *Catalog
	build := func(t *testing.T) (*Catalog, Names) {
		builds++
		c := Open(t)
		source = c
		lib := c.Library("movies", "Movies", "movie", "/movies")
		item := c.Movie(lib, "/movies/one.mkv", "Seed", 2000)
		c.Drain()
		return c, Names{"seed": item}
	}
	first, names := suite.Open(t, "seed", build)
	if err := source.DB.Ping(); err == nil {
		t.Fatal("builder database still live")
	}
	second, other := suite.Open(t, "seed", build)
	if builds != 1 {
		t.Fatalf("built %d times", builds)
	}
	if first.DB == second.DB || first.worker != nil || second.worker != nil {
		t.Fatal("shared handle or inherited worker")
	}
	if names["seed"] != other["seed"] {
		t.Fatal("fixture identifiers changed")
	}
	seed := names["seed"]
	delete(names, "seed")
	if other["seed"] != seed {
		t.Fatal("shared names map")
	}
	for _, key := range []string{"chapter_cursor_key", "activity_cursor_key", "catalog_cursor_key"} {
		var a, b string
		if err := first.DB.QueryRow(`SELECT value FROM configuration WHERE key=?`, key).Scan(&a); err != nil {
			t.Fatal(err)
		}
		if err := second.DB.QueryRow(`SELECT value FROM configuration WHERE key=?`, key).Scan(&b); err != nil {
			t.Fatal(err)
		}
		if a == "" || a == b {
			t.Fatal("shared signing key", key)
		}
	}
	first.Fields(seed.ID, map[string]any{"title": "Changed"})
	first.Drain()
	second.Drain()
	if first.worker == second.worker {
		t.Fatal("shared worker")
	}
	var title string
	if err := second.DB.QueryRow(`SELECT title FROM catalog_browse_rows WHERE entity_id=?`, seed.ID).Scan(&title); err != nil || title != "Seed" {
		t.Fatalf("copy mutated: %q %v", title, err)
	}
	next := first.Movie(first.Handle("movies"), "/movies/two.mkv", "Next", 2001)
	another := second.Movie(second.Handle("movies"), "/movies/two.mkv", "Next", 2001)
	if next.ID != another.ID || next.ID <= seed.ID {
		t.Fatal("counters not isolated", next, another)
	}
	// Closing a consumer must not close another consumer or invalidate the cache.
	if err := first.DB.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			c, n := suite.Open(t, "seed", build)
			c.Fields(n["seed"].ID, map[string]any{"title": t.Name()})
			c.Drain()
		})
	}
	t.Cleanup(func() {
		if err := second.DB.Ping(); err != nil {
			t.Fatal("consumer closed by sibling", err)
		}
	})
}

func TestTemplatesCloseKeepsConsumerAlive(t *testing.T) {
	if testing.Short() {
		t.Skip("fixture snapshot isolation runs in the full merge tier")
	}
	suite := NewTemplates()
	c, n := suite.Open(t, "one", func(t *testing.T) (*Catalog, Names) {
		c := Open(t)
		lib := c.Library("movies", "Movies", "movie", "/movies")
		item := c.Movie(lib, "/movies/one.mkv", "One", 2000)
		c.Drain()
		return c, Names{"one": item}
	})
	suite.Close()
	suite.Close()
	c.Fields(n["one"].ID, map[string]any{"title": "Alive"})
	c.Drain()
	var title string
	if err := c.DB.QueryRow(`SELECT title FROM catalog_browse_rows WHERE entity_id=?`, n["one"].ID).Scan(&title); err != nil || title != "Alive" {
		t.Fatal(title, err)
	}
}

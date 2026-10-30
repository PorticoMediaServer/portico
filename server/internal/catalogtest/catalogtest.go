// Package catalogtest builds catalogue fixtures for tests through the real
// write API (compactcatalog), so a test's catalogue is exactly what a scan
// would have written. Every helper fails the test on error.
//
//	c := catalogtest.Open(t)
//	films := c.Library("films", "Films", "movie", "/films")
//	heat := c.Movie(films, "/films/Heat.mkv", "Heat", 1995)
//	c.Drain() // derived data (search, browse, availability, …)
//	get(t, "/v1/items/"+heat.Public)
package catalogtest

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Catalog is a test database and the helpers that fill it.
type Catalog struct {
	T  testing.TB
	DB *sql.DB

	worker   *compactcatalog.Worker
	workerDB *sql.DB
}

// Item is a created entity: its integer id, its public id and, for a playable
// item, its file's integer id and public token.
type Item struct {
	ID     int64
	Public string
	Asset  int64
	Token  string
}

// Open opens a fresh database in the test's temporary directory.
func Open(t testing.TB) *Catalog {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Catalog{T: t, DB: db}
}

// New wraps a database the test opened itself.
func New(t testing.TB, db *sql.DB) *Catalog { return &Catalog{T: t, DB: db} }

// Write runs fn in one catalogue write transaction.
func (c *Catalog) Write(fn func(ctx context.Context, tx *sql.Tx) error) {
	c.T.Helper()
	if err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		return fn(context.Background(), tx)
	}); err != nil {
		c.T.Fatal(err)
	}
}

// Exec runs a statement (for non-catalogue fixture rows).
func (c *Catalog) Exec(query string, args ...any) {
	c.T.Helper()
	if _, err := c.DB.Exec(query, args...); err != nil {
		c.T.Fatalf("%v: %s", err, query)
	}
}

// Drain runs the derived-data worker until nothing is queued.
func (c *Catalog) Drain() {
	c.T.Helper()
	// Production keeps one derived worker for the open database. Recreating it
	// for every fixture drain repeats startup read sampling, which cannot run
	// while a deliberately retained queue capture owns the background connection.
	if c.worker == nil || c.workerDB != c.DB {
		c.worker, c.workerDB = compactcatalog.NewWorker(c.DB), c.DB
	}
	w := c.worker
	// Facet staging spends at most eight raw rows per transaction and can
	// revisit a holder after an upstream fence changes. A fixed 10,000-turn
	// guard predates that protocol and is smaller than legitimate fixture work.
	// Keep a finite guard proportional to the fixture, without changing any
	// production quantum, writer plan, or requirement to reach an empty queue.
	var entities int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_entities`).Scan(&entities); err != nil {
		c.T.Fatal(err)
	}
	maxSteps := 10000 + 32*entities
	turns := make(map[int]int)
	for i := 0; i < maxSteps; i++ {
		n, err := w.Step(context.Background(), 500)
		if err != nil {
			c.T.Fatal(err)
		}
		domain, _, _ := w.LastBatch()
		turns[domain]++
		if n == 0 {
			if i >= 10000 {
				c.T.Logf("catalogue fixture drained in %d bounded turns (%d entities)", i+1, entities)
			}
			return
		}
	}
	c.T.Logf("drain turns by domain: %v", turns)
	// Failure-only diagnostics preserve the original finite guard and terminal
	// requirement while identifying which bounded owner failed to advance.
	for _, query := range []string{
		`SELECT domain,count(*) FROM catalog_dirty GROUP BY domain ORDER BY domain`,
		`SELECT domain,rebuilding,first_build,cleaning,active_slot FROM catalog_derivations WHERE rebuilding=1 OR first_build=1 OR cleaning=1`,
		`SELECT domain,count(*) FROM catalog_derivation_failures GROUP BY domain ORDER BY domain`,
	} {
		rows, err := c.DB.Query(query)
		if err != nil {
			c.T.Logf("drain residue: %v", err)
			continue
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			c.T.Log(err)
			continue
		}
		for rows.Next() {
			values := make([]any, len(cols))
			targets := make([]any, len(cols))
			for i := range values {
				targets[i] = &values[i]
			}
			if err = rows.Scan(targets...); err != nil {
				c.T.Log(err)
				break
			}
			c.T.Logf("drain residue %v: %v", cols, values)
		}
		if err = rows.Err(); err != nil {
			c.T.Log(err)
		}
		rows.Close()
	}
	c.T.Fatal("catalogue worker did not drain")
}

// Library creates a library (kind: movie, tv, anime, music, audiobook) and
// returns its catalogue handle.
func (c *Catalog) Library(id, name, kind, root string) int64 {
	c.T.Helper()
	c.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, id, name, kind, root)
	return c.Handle(id)
}

// Handle is a library's catalogue handle.
func (c *Catalog) Handle(library string) int64 {
	c.T.Helper()
	var id int64
	if err := c.DB.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, library).Scan(&id); err != nil {
		c.T.Fatalf("library %q: %v", library, err)
	}
	return id
}

// Public is an entity's public id.
func (c *Catalog) Public(id int64) string {
	c.T.Helper()
	p, err := entityid.Public(context.Background(), c.DB, id)
	if err != nil {
		c.T.Fatal(err)
	}
	return p
}

// ID is the integer id of a public id.
func (c *Catalog) ID(public string) int64 {
	c.T.Helper()
	id, err := entityid.Resolve(context.Background(), c.DB, public)
	if err != nil {
		c.T.Fatalf("entity %q: %v", public, err)
	}
	return id
}

func (c *Catalog) root(library int64) string {
	c.T.Helper()
	var root string
	if err := c.DB.QueryRow(`SELECT root FROM catalog_libraries WHERE id=?`, library).Scan(&root); err != nil {
		c.T.Fatal(err)
	}
	return root
}

// Entity creates (or finds) any entity with optional side facts.
func (c *Catalog) Entity(e compactcatalog.Entity, facts map[string]any) Item {
	c.T.Helper()
	var it Item
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		if it.ID, _, err = compactcatalog.UpsertEntityTx(ctx, tx, e); err != nil {
			return err
		}
		if len(facts) > 0 {
			return compactcatalog.SetFactsTx(ctx, tx, it.ID, facts)
		}
		return nil
	})
	it.Public = c.Public(it.ID)
	return it
}

// File records a file and links it to item (duration in seconds).
func (c *Catalog) File(item int64, path string, duration float64) (int64, string) {
	c.T.Helper()
	var asset int64
	var token string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		asset, token, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: strings.TrimPrefix(filepath.Ext(path), "."), VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: duration})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{})
	})
	return asset, token
}

func (c *Catalog) playable(library int64, kind compactcatalog.Kind, parent int64, path, title string, year int, facts map[string]any) Item {
	c.T.Helper()
	it := c.Entity(compactcatalog.Entity{Library: library, Kind: kind, Parent: parent, Key: compactcatalog.ItemKey(c.root(library), path, 0), Title: title, Year: year, Added: "2026-01-01T00:00:00.000Z"}, facts)
	it.Asset, it.Token = c.File(it.ID, path, 5400)
	return it
}

// Movie creates a movie with one file.
func (c *Catalog) Movie(library int64, path, title string, year int) Item {
	c.T.Helper()
	return c.playable(library, compactcatalog.Movie, 0, path, title, year, nil)
}

// Show creates a show.
func (c *Catalog) Show(library int64, title string, year int) Item {
	c.T.Helper()
	key := strings.ToLower(title) + ":" + fmt.Sprint(year)
	return c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey(key), Title: title, Year: year}, map[string]any{"local_key": key})
}

// Season creates a show's season.
func (c *Catalog) Season(show Item, number int) Item {
	c.T.Helper()
	var library int64
	var key string
	if err := c.DB.QueryRow(`SELECT e.library_id,s.local_key FROM catalog_entities e JOIN catalog_shows s ON s.entity_id=e.id WHERE e.id=?`, show.ID).Scan(&library, &key); err != nil {
		c.T.Fatal(err)
	}
	title := fmt.Sprintf("Season %d", number)
	if number == 0 {
		title = "Specials"
	}
	return c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Season, Parent: show.ID, Key: compactcatalog.SeasonKey(key, number), Title: title}, map[string]any{"show_id": show.ID, "number": number})
}

// Episode creates a seasonal episode with one file.
func (c *Catalog) Episode(show, season Item, number int, path string) Item {
	c.T.Helper()
	var library int64
	if err := c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, show.ID).Scan(&library); err != nil {
		c.T.Fatal(err)
	}
	return c.playable(library, compactcatalog.Episode, season.ID, path, fmt.Sprintf("Episode %d", number), 0, map[string]any{"show_id": show.ID, "season_id": season.ID, "numbering": "seasonal", "number": number})
}

// Artist creates an artist.
func (c *Catalog) Artist(library int64, name string) Item {
	c.T.Helper()
	key := strings.ToLower(name)
	return c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Artist, Key: compactcatalog.ArtistKey(key), Title: name}, map[string]any{"local_key": key})
}

// Album creates an album of an artist.
func (c *Catalog) Album(artist Item, title string, year int) Item {
	c.T.Helper()
	var library int64
	if err := c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, artist.ID).Scan(&library); err != nil {
		c.T.Fatal(err)
	}
	key := strings.ToLower(title) + "|" + fmt.Sprint(artist.ID)
	return c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Album, Parent: artist.ID, Key: compactcatalog.AlbumKey(key), Title: title, Year: year}, map[string]any{"artist_id": artist.ID, "local_key": key})
}

// Song creates a track on an album with one file, credited to the album artist.
func (c *Catalog) Song(album Item, track int, path, title string) Item {
	c.T.Helper()
	var library, artist int64
	if err := c.DB.QueryRow(`SELECT e.library_id,a.artist_id FROM catalog_entities e JOIN catalog_albums a ON a.entity_id=e.id WHERE e.id=?`, album.ID).Scan(&library, &artist); err != nil {
		c.T.Fatal(err)
	}
	it := c.playable(library, compactcatalog.Track, album.ID, path, title, 0, map[string]any{"album_id": album.ID, "track_number": track})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetSongArtistsTx(ctx, tx, it.ID, []int64{artist})
	})
	return it
}

// Book creates an audiobook by an author (and the author entity).
func (c *Catalog) Book(library int64, title, author string) Item {
	c.T.Helper()
	key := strings.ToLower(title + "|" + author)
	b := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Book, Key: compactcatalog.BookKey(key), Title: title}, map[string]any{"library_id": library, "local_key": key, "author": author})
	if author != "" {
		c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Author, Key: compactcatalog.AuthorKey(author), Title: author}, nil)
	}
	return b
}

// BookFile creates a part of a book with one file.
func (c *Catalog) BookFile(book Item, part int, path string) Item {
	c.T.Helper()
	var library int64
	if err := c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, book.ID).Scan(&library); err != nil {
		c.T.Fatal(err)
	}
	return c.playable(library, compactcatalog.Part, book.ID, path, fmt.Sprintf("Part %d", part), 0, map[string]any{"book_id": book.ID, "part_number": part})
}

// Fields sets named fields (as a provider would; owner locks apply).
func (c *Catalog) Fields(id int64, fields map[string]any) {
	c.T.Helper()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFieldsTx(ctx, tx, id, compactcatalog.Automatic, fields)
	})
}

// Genres replaces an entity's genres from provider.
func (c *Catalog) Genres(id int64, provider string, names ...string) {
	c.T.Helper()
	terms := make([]compactcatalog.Term, len(names))
	for i, n := range names {
		terms[i] = compactcatalog.Term{Name: n}
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetTermsTx(ctx, tx, id, compactcatalog.VocabGenre, provider, terms)
	})
}

// Attributes replaces an item's values of one attribute field.
func (c *Catalog) Attributes(id int64, field string, values ...string) {
	c.T.Helper()
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAttributesTx(ctx, tx, id, field, values)
	})
}

// Collection creates a movie collection with members.
func (c *Catalog) Collection(library int64, name string, members ...Item) Item {
	c.T.Helper()
	col := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Collection, Key: compactcatalog.CollectionKey(name), Title: name}, map[string]any{"library_id": library, "name_key": strings.ToLower(name), "created_at": "2026-01-01T00:00:00.000Z"})
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, m := range members {
			if err := compactcatalog.SetCollectionMemberTx(ctx, tx, col.ID, m.ID, "", true); err != nil {
				return err
			}
		}
		return nil
	})
	return col
}

// Delete deletes an entity.
func (c *Catalog) Delete(id int64) {
	c.T.Helper()
	c.Write(func(ctx context.Context, tx *sql.Tx) error { return compactcatalog.DeleteEntityTx(ctx, tx, id) })
}

// Names keeps a test's readable fixture names ('kids', 'teen') for the items it
// created, so assertions stay readable: build with names[name] = c.Movie(…),
// compare results with names.Of(publicID) or names.Publics("kids", "teen").
type Names map[string]Item

// Of is the fixture name of a public id ("" when it is none of them).
func (n Names) Of(public string) string {
	for name, it := range n {
		if it.Public == public {
			return name
		}
	}
	return ""
}

// All maps public ids to fixture names, in order.
func (n Names) All(publics []string) []string {
	out := make([]string, len(publics))
	for i, p := range publics {
		out[i] = n.Of(p)
	}
	return out
}

// Publics are the public ids of the named items, in order.
func (n Names) Publics(names ...string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		it, ok := n[name]
		if !ok {
			panic("catalogtest: no fixture named " + name)
		}
		out[i] = it.Public
	}
	return out
}

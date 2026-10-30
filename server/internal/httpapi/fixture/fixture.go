// Package fixture builds a catalogue that exercises the read paths the way a
// real library does.
//
// The load test used to seed bare `items` rows and nothing else — no assets, no
// `item_assets`, no attributes, no genres, no credits, no personal state, no
// shows, albums or books, and no profile carrying a restriction. The consequence
// was that every visibility predicate matched nothing, so every home row was
// empty and free, the restriction predicate had nothing to test, the album and
// book branches of the browse projection were empty, and facets returned nothing.
// The test measured the parts of the read path that are cheap and skipped every
// part that is not, which is why it could report a 2.96 s p95 with twelve seconds
// of SQL hiding inside it.
//
// This package is deliberately a package and not a test file: the load test, a
// bench binary and a manual harness all need the same catalogue, and a fixture
// that only one of them can build is a fixture that drifts.
package fixture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Shape is what to build. Every count is exact, so a shape hashes to a cache
// key and two runs of the same tier get byte-identical catalogues.
type Shape struct {
	Movies, Shows, SeasonsPerShow, EpisodesPerSeason int
	Artists, AlbumsPerArtist, SongsPerAlbum          int
	Books, FilesPerBook                              int
	Collections, ItemsPerCollection                  int
	GenresPerItem, CreditsPerItem, AttributesPerItem int
	// WatchedPercent and ProgressPercent are percentages of the catalogue that
	// carry personal state. Real libraries are mostly untouched; a fixture where
	// everything is watched measures a case that does not occur.
	Profiles, WatchedPercent, ProgressPercent int
	// RestrictedProfiles carry a rating ceiling, blocked labels and a narrowed
	// library list, so the restriction predicate and the per-viewer cache keying
	// are both exercised rather than assumed.
	RestrictedProfiles int
	Seed               int64
}

// Config selects representative population options without changing the
// historical fixture used by immutable read-path equivalence checks.
type Config struct {
	CanonicalCreditDepartments bool
}

// Libraries is what Generate created, so a caller can address them.
type Libraries struct {
	Movies, Shows, Music, Books string
}

// Key is the cache identity of a shape.
func (s Shape) Key() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%#v", s)))
	return hex.EncodeToString(sum[:8])
}

// Items is how many catalogue rows this shape produces.
func (s Shape) Items() int {
	return s.Movies +
		s.Shows*s.SeasonsPerShow*s.EpisodesPerSeason +
		s.Artists*s.AlbumsPerArtist*s.SongsPerAlbum +
		s.Books*s.FilesPerBook
}

// Smoke, Release and Deep are the audit's tiers. Smoke runs on every pull
// request; release before a release; deep before a capacity claim.
func Smoke() Shape {
	return Shape{
		Movies: 900, Shows: 20, SeasonsPerShow: 2, EpisodesPerSeason: 6,
		Artists: 12, AlbumsPerArtist: 2, SongsPerAlbum: 6, Books: 8, FilesPerBook: 3,
		Collections: 6, ItemsPerCollection: 20,
		GenresPerItem: 3, CreditsPerItem: 6, AttributesPerItem: 4,
		Profiles: 4, WatchedPercent: 2, ProgressPercent: 5, RestrictedProfiles: 1, Seed: 1,
	}
}

func Release() Shape {
	return Shape{
		Movies: 60000, Shows: 900, SeasonsPerShow: 4, EpisodesPerSeason: 8,
		Artists: 700, AlbumsPerArtist: 3, SongsPerAlbum: 10, Books: 400, FilesPerBook: 6,
		Collections: 120, ItemsPerCollection: 40,
		GenresPerItem: 3, CreditsPerItem: 6, AttributesPerItem: 5,
		Profiles: 40, WatchedPercent: 2, ProgressPercent: 5, RestrictedProfiles: 10, Seed: 2,
	}
}

// Deep is the owner's bar: two hundred viewers over a million items. It is a
// large fixture — tens of minutes to build and gigabytes on disk — so it is
// built deliberately, before a capacity claim, and deleted afterwards.
func Deep() Shape {
	return Shape{
		Movies: 620000, Shows: 9000, SeasonsPerShow: 5, EpisodesPerSeason: 8,
		Artists: 7000, AlbumsPerArtist: 4, SongsPerAlbum: 10, Books: 4000, FilesPerBook: 8,
		Collections: 400, ItemsPerCollection: 60,
		GenresPerItem: 3, CreditsPerItem: 6, AttributesPerItem: 5,
		Profiles: 200, WatchedPercent: 1, ProgressPercent: 2, RestrictedProfiles: 50, Seed: 3,
	}
}

// generationBatch is how many catalogue rows one transaction writes. It is
// larger than the scanner's ten because this is not competing with anyone: the
// fixture builds against a database nobody is reading.
const generationBatch = 500

// ratings and labels are the restriction inputs. Five bands, because a fixture
// where everything is one rating never exercises a ceiling.
var ratings = []string{"G", "PG", "PG-13", "R", "NC-17"}
var labels = []string{"violence", "language", "nudity", "drugs", "frightening"}
var genreNames = []string{"Action", "Drama", "Comedy", "Thriller", "Documentary", "Horror", "Science Fiction", "Romance", "Animation", "Crime", "Fantasy", "Mystery"}

// Generate builds the catalogue. It reports progress so a deep tier does not
// look hung, and it yields at every batch boundary so it can run against a
// server that is also serving.
func Generate(ctx context.Context, db *sql.DB, shape Shape, report func(done, total int)) (Libraries, error) {
	return GenerateWithConfig(ctx, db, shape, Config{}, report)
}

// GenerateWithConfig uses the same population APIs with explicit options.
func GenerateWithConfig(ctx context.Context, db *sql.DB, shape Shape, config Config, report func(done, total int)) (Libraries, error) {
	out := Libraries{}
	random := rand.New(rand.NewSource(shape.Seed))
	zipf := rand.NewZipf(random, 1.2, 1, 400)
	total := shape.Items()
	done := 0
	progress := func(n int) {
		done += n
		if report != nil {
			report(done, total)
		}
	}

	libraries := []struct {
		id, name, kind, root string
		target               *string
	}{
		{"fixture-movies", "Films", "movie", "/fixture/films", &out.Movies},
		{"fixture-tv", "Shows", "tv", "/fixture/shows", &out.Shows},
		{"fixture-music", "Music", "music", "/fixture/music", &out.Music},
		{"fixture-books", "Books", "audiobook", "/fixture/books", &out.Books},
	}
	// handles are the fixture libraries' catalogue handles for the write API;
	// entities maps a fixture id ("movie-0000001", "show-000002-s01", ...) to
	// the integer entity the step that created it recorded at execution time.
	// Steps execute strictly in add order, so a step only looks up ids earlier
	// steps recorded.
	handles := map[string]int64{}
	roots := map[string]string{}
	entities := map[string]int64{}
	err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for _, library := range libraries {
			if _, e := tx.Exec(`INSERT OR IGNORE INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, library.id, library.name, library.kind, library.root); e != nil {
				return e
			}
			// The write API takes the library's integer handle; the trigger on
			// libraries created its library_sources row, which the UPDATE below
			// keeps healthy so availability takes the real-installation branch.
			handle, e := compactcatalog.LibraryTx(ctx, tx, library.id)
			if e != nil {
				return e
			}
			handles[library.id] = handle
			roots[library.id] = library.root
			*library.target = library.id
		}
		// A library source per library, healthy, so the availability projection
		// takes the branch a real installation takes.
		_, e := tx.Exec(`UPDATE library_sources SET enabled=1,health='ready' WHERE id IN('fixture-movies','fixture-tv','fixture-music','fixture-books')`)
		return e
	})
	if err != nil {
		return out, err
	}

	// A writer that accumulates statements and commits at the batch boundary.
	pending := 0
	var batch []func(*sql.Tx) error
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		work := batch
		batch = nil
		pending = 0
		if !dbwork.Yield(ctx) {
			return ctx.Err()
		}
		return dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			for _, step := range work {
				if e := step(tx); e != nil {
					return e
				}
			}
			return nil
		})
	}
	add := func(step func(*sql.Tx) error) error {
		batch = append(batch, step)
		pending++
		if pending < generationBatch {
			return nil
		}
		return flush()
	}

	stamp := func(n int) string {
		return time.Unix(1600000000+int64(n)*37, 0).UTC().Format("2006-01-02T15:04:05.000Z")
	}
	// item writes one catalogue row with its asset, link, attributes, genres and
	// credits, and (for a slice of the catalogue) personal state. Everything
	// catalogue-shaped goes through the write API; personal state and progress
	// are non-catalogue tables and stay direct INSERTs with the integer id the
	// entity step recorded.
	counter := 0
	item := func(library, id, kindName, title string, year int, parentKey string, extra func(*sql.Tx) error) error {
		counter++
		n := counter
		watched := random.Intn(100) < shape.WatchedPercent
		progressed := random.Intn(100) < shape.ProgressPercent
		rating := ratings[zipf.Uint64()%uint64(len(ratings))]
		label := labels[n%len(labels)]
		genres := make([]string, 0, shape.GenresPerItem)
		for g := 0; g < shape.GenresPerItem; g++ {
			genres = append(genres, genreNames[(n+g*7)%len(genreNames)])
		}
		credits := shape.CreditsPerItem
		return add(func(tx *sql.Tx) error {
			kind, e := compactcatalog.ParseKind(kindName)
			if e != nil {
				return e
			}
			path := "/fixture/" + id + ".mp4"
			entity, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: handles[library],
				Kind:    kind,
				Parent:  entities[parentKey],
				Key:     compactcatalog.ItemKey(roots[library], path, 0),
				Title:   title,
				Year:    year,
				Added:   stamp(n),
			})
			if e != nil {
				return e
			}
			entities[id] = entity
			if e := compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{
				"overview":     strings.Repeat("A synopsis sentence that is long enough to matter on the wire. ", 6),
				"poster_url":   "local:poster/" + id,
				"backdrop_url": "local:backdrop/" + id,
			}); e != nil {
				return e
			}
			asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{
				Path: path, Size: 1 << 20, ModifiedNS: int64(n),
				Container: "mp4", VideoCodec: "h264", AudioCodec: "aac",
				Width: 1920, Height: 1080, Duration: float64(600 + n%4200),
			})
			if e != nil {
				return e
			}
			if e := compactcatalog.LinkAssetTx(ctx, tx, entity, asset, compactcatalog.Link{}); e != nil {
				return e
			}
			if e := compactcatalog.SetAttributesTx(ctx, tx, entity, "contentRating", []string{rating}); e != nil {
				return e
			}
			if e := compactcatalog.SetAttributesTx(ctx, tx, entity, "label", []string{label}); e != nil {
				return e
			}
			for a := 2; a < shape.AttributesPerItem; a++ {
				field := []string{"studio", "tag", "audioLanguage", "series"}[(n+a)%4]
				value := fmt.Sprintf("%s-%02d", field, (n+a)%17)
				if e := compactcatalog.SetAttributesTx(ctx, tx, entity, field, []string{value}); e != nil {
					return e
				}
			}
			if len(genres) > 0 {
				terms := make([]compactcatalog.Term, 0, len(genres))
				for _, name := range genres {
					terms = append(terms, compactcatalog.Term{SourceID: strings.ToLower(name), Name: name, Key: strings.ToLower(name)})
				}
				if e := compactcatalog.SetTermsTx(ctx, tx, entity, compactcatalog.VocabGenre, "fixture", terms); e != nil {
					return e
				}
			}
			if credits > 0 {
				list := make([]compactcatalog.Credit, 0, credits)
				for c := 0; c < credits; c++ {
					person := fmt.Sprintf("person-%04d", (n*3+c*11)%(1+shape.Movies/4+64))
					role := []string{"Actor", "Director", "Writer"}[c%3]
					department := []string{"cast", "directing", "writing"}[c%3]
					if config.CanonicalCreditDepartments {
						department = []string{"Acting", "Directing", "Writing"}[c%3]
					}
					list = append(list, compactcatalog.Credit{
						PersonKey: "fixture:" + person, PersonName: "Person " + person,
						ProviderPersonID: person, CreditID: person, CreditedName: "Person " + person,
						Role: role, Department: department, Ordinal: c,
					})
				}
				if e := compactcatalog.SetCreditsTx(ctx, tx, entity, "fixture", list); e != nil {
					return e
				}
			}
			if watched || progressed {
				profile := fmt.Sprintf("fixture-profile-%03d", n%max(1, shape.Profiles))
				if _, e := tx.Exec(`INSERT INTO personal_items(profile_id,item_id,watched,favorite,watchlisted,last_played_at,revision) VALUES(?,?,?,?,?,?,1)`,
					profile, entity, boolInt(watched), boolInt(n%7 == 0), boolInt(n%11 == 0), stamp(n)); e != nil {
					return e
				}
			}
			if progressed {
				profile := fmt.Sprintf("fixture-profile-%03d", n%max(1, shape.Profiles))
				if _, e := tx.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES(?,?,?,0,'fixture')`, profile, entity, int64(30+n%500)*1000); e != nil {
					return e
				}
				if _, e := tx.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES(?,?,?,?,'playing')`, profile, library, entity, stamp(n)); e != nil {
					return e
				}
			}
			if extra != nil {
				return extra(tx)
			}
			return nil
		})
	}

	title := func(n int) string {
		return fmt.Sprintf("%s %s %06d", titleFrom(zipf), titleFrom(zipf), n)
	}

	for i := 0; i < shape.Movies; i++ {
		if err = item(out.Movies, fmt.Sprintf("movie-%07d", i), "movie", title(i), 1950+i%75, "", nil); err != nil {
			return out, err
		}
		progress(1)
	}
	for s := 0; s < shape.Shows; s++ {
		show := fmt.Sprintf("show-%06d", s)
		showTitle := title(s)
		year := 1990 + s%35
		if err = add(func(tx *sql.Tx) error {
			entity, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: handles[out.Shows], Kind: compactcatalog.Show,
				Key: compactcatalog.ShowKey(show), Title: showTitle, Year: year,
			})
			if e != nil {
				return e
			}
			entities[show] = entity
			return compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{"local_key": show})
		}); err != nil {
			return out, err
		}
		for season := 1; season <= shape.SeasonsPerShow; season++ {
			seasonID := fmt.Sprintf("%s-s%02d", show, season)
			number := season
			if err = add(func(tx *sql.Tx) error {
				entity, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
					Library: handles[out.Shows], Kind: compactcatalog.Season, Parent: entities[show],
					Key: compactcatalog.SeasonKey(show, number), Title: fmt.Sprintf("Season %d", number),
				})
				if e != nil {
					return e
				}
				entities[seasonID] = entity
				return compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{"show_id": entities[show], "number": number})
			}); err != nil {
				return out, err
			}
			for e := 1; e <= shape.EpisodesPerSeason; e++ {
				episode := fmt.Sprintf("%s-e%03d", seasonID, e)
				number := e
				if err = item(out.Shows, episode, "episode", fmt.Sprintf("%s %d×%02d", showTitle, season, e), 1990+s%35, seasonID, func(tx *sql.Tx) error {
					return compactcatalog.SetFactsTx(ctx, tx, entities[episode], map[string]any{
						"show_id": entities[show], "season_id": entities[seasonID],
						"numbering": "seasonal", "number": number,
					})
				}); err != nil {
					return out, err
				}
				progress(1)
			}
		}
	}
	for a := 0; a < shape.Artists; a++ {
		artist := fmt.Sprintf("artist-%06d", a)
		artistName := titleFrom(zipf) + fmt.Sprintf(" Band %04d", a)
		if err = add(func(tx *sql.Tx) error {
			entity, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: handles[out.Music], Kind: compactcatalog.Artist,
				Key: compactcatalog.ArtistKey(artist), Title: artistName,
			})
			if e != nil {
				return e
			}
			entities[artist] = entity
			return compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{"local_key": artist})
		}); err != nil {
			return out, err
		}
		for al := 0; al < shape.AlbumsPerArtist; al++ {
			album := fmt.Sprintf("%s-al%03d", artist, al)
			albumTitle := titleFrom(zipf) + fmt.Sprintf(" Release %03d", al)
			year := 1970 + (a+al)%55
			if err = add(func(tx *sql.Tx) error {
				entity, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
					Library: handles[out.Music], Kind: compactcatalog.Album, Parent: entities[artist],
					Key: compactcatalog.AlbumKey(album), Title: albumTitle, Year: year,
				})
				if e != nil {
					return e
				}
				entities[album] = entity
				return compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{"artist_id": entities[artist], "local_key": album})
			}); err != nil {
				return out, err
			}
			for s := 0; s < shape.SongsPerAlbum; s++ {
				song := fmt.Sprintf("%s-t%03d", album, s)
				track := s + 1
				if err = item(out.Music, song, "song", fmt.Sprintf("%s %02d", albumTitle, track), year, album, func(tx *sql.Tx) error {
					if e := compactcatalog.SetFactsTx(ctx, tx, entities[song], map[string]any{
						"album_id": entities[album], "disc_number": 1, "track_number": track,
					}); e != nil {
						return e
					}
					return compactcatalog.SetSongArtistsTx(ctx, tx, entities[song], []int64{entities[artist]})
				}); err != nil {
					return out, err
				}
				progress(1)
			}
		}
	}
	for b := 0; b < shape.Books; b++ {
		book := fmt.Sprintf("book-%06d", b)
		bookTitle := titleFrom(zipf) + fmt.Sprintf(" Volume %04d", b)
		author := fmt.Sprintf("Author %03d", b%64)
		if err = add(func(tx *sql.Tx) error {
			entity, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: handles[out.Books], Kind: compactcatalog.Book,
				Key: compactcatalog.BookKey(book), Title: bookTitle,
			})
			if e != nil {
				return e
			}
			entities[book] = entity
			if e = compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{
				"library_id": handles[out.Books], "local_key": book,
				"author": author, "narrator": "Narrator",
			}); e != nil {
				return e
			}
			// As the scanner does: an author is a browse entity of its own.
			_, _, e = compactcatalog.EnsureEntityTx(ctx, tx, compactcatalog.Entity{
				Library: handles[out.Books], Kind: compactcatalog.Author,
				Key: compactcatalog.AuthorKey(author), Title: author,
			})
			return e
		}); err != nil {
			return out, err
		}
		for f := 0; f < shape.FilesPerBook; f++ {
			file := fmt.Sprintf("%s-p%03d", book, f)
			part := f + 1
			if err = item(out.Books, file, "audiobook_file", fmt.Sprintf("%s part %02d", bookTitle, part), 2000+b%25, book, func(tx *sql.Tx) error {
				return compactcatalog.SetFactsTx(ctx, tx, entities[file], map[string]any{
					"book_id": entities[book], "disc_number": 1, "part_number": part,
				})
			}); err != nil {
				return out, err
			}
			progress(1)
		}
	}
	for c := 0; c < shape.Collections; c++ {
		collection := fmt.Sprintf("collection-%05d", c)
		name := fmt.Sprintf("Collection %05d", c)
		nameKey := strings.ToLower(name)
		index := c
		if err = add(func(tx *sql.Tx) error {
			entity, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: handles[out.Movies], Kind: compactcatalog.Collection,
				Key: compactcatalog.CollectionKey(nameKey), Title: name,
			})
			if e != nil {
				return e
			}
			entities[collection] = entity
			if e := compactcatalog.SetFactsTx(ctx, tx, entity, map[string]any{
				"library_id": handles[out.Movies], "name_key": nameKey, "created_at": stamp(index),
			}); e != nil {
				return e
			}
			for m := 0; m < shape.ItemsPerCollection && m < shape.Movies; m++ {
				member := fmt.Sprintf("movie-%07d", (index*shape.ItemsPerCollection+m)%shape.Movies)
				if e := compactcatalog.SetCollectionMemberTx(ctx, tx, entity, entities[member], fmt.Sprintf("%08d", m), true); e != nil {
					return e
				}
			}
			return nil
		}); err != nil {
			return out, err
		}
	}
	if err = flush(); err != nil {
		return out, err
	}
	// Derived data (search, browse rows, availability, counts) is built by the
	// catalogue worker; run it to completion so the fixture is the state a real
	// server reaches once its worker is idle.
	if err = compactcatalog.Drain(ctx, db); err != nil {
		return out, err
	}
	// Content ratings are classified by the server's background worker; a
	// restricted viewer treats an unclassified rating as unrated.
	for {
		more, err := compactcatalog.ClassifyPendingRatings(ctx, db, identity.RatingAge)
		if err != nil {
			return out, err
		}
		if !more {
			break
		}
	}
	return out, nil
}

// titleFrom draws a word from a Zipfian vocabulary, so full-text matches are
// skewed the way real searches are: a handful of very common words and a long
// tail. A uniform vocabulary makes every search term match the same number of
// documents, which is the one distribution that never occurs.
func titleFrom(zipf *rand.Zipf) string {
	return fmt.Sprintf("Word%03d", zipf.Uint64())
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// Verify asserts that the fact tables the fixture writes are populated. A
// fixture that silently skips one measures a read path that will not exist in
// production, so this fails the build rather than the benchmark. Derived data
// (search documents, browse rows, availability) is built asynchronously by the
// catalogue worker, so Verify checks the live availability view and that every
// derived domain the fixture feeds was queued instead.
func Verify(ctx context.Context, db *sql.DB) error {
	for _, projection := range []struct {
		table, why string
	}{
		{"catalog_entities", "the catalogue itself"},
		{"catalog_assets", "playback has nothing to resolve without them"},
		{"catalog_asset_links", "every availability predicate reads this"},
		{"catalog_item_attribute_edges", "the restriction predicate has nothing to filter on"},
		{"catalog_entity_terms", "facets and recommendations have nothing to count"},
		{"catalog_credits", "the recommendation row has no people facets"},
		{"personal_items", "continue-watching and watchlist rows are empty"},
		{"progress", "resume has nothing to resume"},
	} {
		var rows int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+projection.table).Scan(&rows); err != nil {
			return fmt.Errorf("fixture: reading %s: %w", projection.table, err)
		}
		if rows == 0 {
			return fmt.Errorf("fixture: %s is empty, so %s", projection.table, projection.why)
		}
	}
	var visible int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_item_availability WHERE available=1`).Scan(&visible); err != nil {
		return err
	}
	if visible == 0 {
		return fmt.Errorf("fixture: no item is visible, so every home row would be empty and free")
	}
	// Generate drains the catalogue worker, so the derived read models are
	// built and nothing is left queued.
	for _, derived := range []struct {
		table, why string
	}{
		{"catalog_search_documents", "search has nothing to match"},
		{"catalog_browse_rows", "browse and Home have no rows"},
		{"catalog_browse_memberships", "containers have no members"},
		{"catalog_item_availability", "no item has an availability"},
	} {
		var rows int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM `+derived.table+` LIMIT 1)`).Scan(&rows); err != nil {
			return fmt.Errorf("fixture: reading %s: %w", derived.table, err)
		}
		if rows == 0 {
			return fmt.Errorf("fixture: %s is empty, so %s", derived.table, derived.why)
		}
	}
	var queued int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM catalog_dirty LIMIT 1)`).Scan(&queued); err != nil {
		return fmt.Errorf("fixture: reading the derived queue: %w", err)
	}
	if queued != 0 {
		return fmt.Errorf("fixture: derived work is still queued after the build")
	}
	// The title blocks deep browse positions come from must count every row.
	if err := compactcatalog.CheckBrowseBlocks(ctx, db); err != nil {
		return err
	}
	if err := compactcatalog.CheckFacetCounts(ctx, db); err != nil {
		return err
	}
	return nil
}

// schemaKey identifies the schema a fixture was built against, so a cached
// fixture cannot outlive the installer that produced it. It is resolved once per
// process by opening an empty database and hashing its `sqlite_master` and its
// migration ledger digests, which costs about twenty milliseconds and removes a whole class of confusing stale
// results.
var schemaOnce sync.Once
var schemaDigest string

func schemaKey() string {
	schemaOnce.Do(func() {
		directory, err := os.MkdirTemp("", "portico-schema-")
		if err != nil {
			return
		}
		defer os.RemoveAll(directory)
		db, err := persistence.Open(filepath.Join(directory, "schema.sqlite"))
		if err != nil {
			return
		}
		defer db.Close()
		rows, err := db.Query(`SELECT type,name,COALESCE(sql,'') FROM sqlite_master ORDER BY type,name`)
		if err != nil {
			return
		}
		defer rows.Close()
		sum := sha256.New()
		for rows.Next() {
			var kind, name, statement string
			if rows.Scan(&kind, &name, &statement) != nil {
				return
			}
			sum.Write([]byte(kind + "\x00" + name + "\x00" + statement + "\n"))
		}
		// The ledger digest covers what sqlite_master cannot: a migration that
		// only changes data (a default, a backfill) leaves the schema text alone
		// but must still retire every cached fixture built before it.
		var version string
		if err := db.QueryRow(`SELECT value FROM configuration WHERE key='schema_version'`).Scan(&version); err != nil {
			return
		}
		fmt.Fprintf(sum, "schema_version\x00%s\n", version)
		// What the fixture writes is also a function of code: this file and the
		// catalogue writers it calls. The running binary covers both, so a code
		// change never reuses a fixture an older build wrote.
		if !hashExecutable(sum) {
			return
		}
		schemaDigest = hex.EncodeToString(sum.Sum(nil)[:8])
	})
	return schemaDigest
}

// Build returns a path to a database holding this shape, building it once and
// copying it afterwards. A deep fixture takes minutes to build and seconds to
// copy, and a load test that rebuilds it every run is a load test nobody runs.
func Build(ctx context.Context, shape Shape, destination string, report func(done, total int)) (Libraries, error) {
	return BuildWithConfig(ctx, shape, Config{}, destination, report)
}

// BuildWithConfig keeps distinct cache entries for opt-in population variants.
func BuildWithConfig(ctx context.Context, shape Shape, config Config, destination string, report func(done, total int)) (Libraries, error) {
	variant := ""
	if config.CanonicalCreditDepartments {
		variant = "-canonical-credits"
	}
	cached := filepath.Join(os.TempDir(), "portico-fixture-"+shape.Key()+"-"+schemaKey()+variant+".sqlite")
	libraries := Libraries{Movies: "fixture-movies", Shows: "fixture-tv", Music: "fixture-music", Books: "fixture-books"}
	if _, err := os.Stat(cached); err != nil {
		staging := cached + ".building"
		_ = os.Remove(staging)
		db, openErr := persistence.Open(staging)
		if openErr != nil {
			return libraries, openErr
		}
		if libraries, err = GenerateWithConfig(ctx, db, shape, config, report); err != nil {
			db.Close()
			_ = os.Remove(staging)
			return libraries, err
		}
		if err = Verify(ctx, db); err != nil {
			db.Close()
			_ = os.Remove(staging)
			return libraries, err
		}
		// A checkpointed database copies as one file.
		if _, err = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			db.Close()
			return libraries, err
		}
		db.Close()
		if err = os.Rename(staging, cached); err != nil {
			return libraries, err
		}
	}
	return libraries, copyFile(cached, destination)
}

// copyFile duplicates the cached fixture for one run.
//
// A deep fixture is twelve gigabytes. Copying it byte by byte costs that much
// disk twice over and minutes of wall time before the measurement starts, which
// on a developer machine is the difference between the deep tier being runnable
// and not. Where the filesystem can clone a file — APFS and modern Linux
// filesystems both can — the copy is a copy-on-write clone: instant, and it
// occupies only the blocks the run actually writes. The byte copy is the
// fallback and the behaviour is identical either way.
func copyFile(from, to string) error {
	if cloneFile(from, to) == nil {
		return nil
	}
	return copyBytes(from, to)
}

// cloneFile asks the platform's own tool for a copy-on-write clone. It is the
// system `cp` rather than a syscall so that this stays portable and
// dependency-free; a platform without the flag fails and the caller falls back.
func cloneFile(from, to string) error {
	flag := ""
	switch runtime.GOOS {
	case "darwin":
		flag = "-c"
	case "linux":
		flag = "--reflink=always"
	default:
		return errors.New("fixture: no clone on this platform")
	}
	if err := os.Remove(to); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := exec.Command("cp", flag, from, to).Run(); err != nil {
		_ = os.Remove(to)
		return err
	}
	// A clone that produced nothing is not a clone.
	info, err := os.Stat(to)
	if err != nil {
		return err
	}
	source, err := os.Stat(from)
	if err != nil {
		return err
	}
	if info.Size() != source.Size() {
		_ = os.Remove(to)
		return errors.New("fixture: the clone is a different size")
	}
	return os.Chmod(to, 0600)
}

func copyBytes(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer target.Close()
	_, err = io.Copy(target, source)
	return err
}

// RestrictedProfile is the restriction a fixture profile carries. It is a
// rating ceiling *and* blocked labels *and* a narrowed library list, because a
// profile with only one of the three exercises only one of the three clauses.
type RestrictedProfile struct {
	MaximumAge    int
	BlockedLabels []string
	Libraries     []string
}

// Restriction is the restriction the fixture's restricted profiles carry.
func Restriction(libraries Libraries) RestrictedProfile {
	return RestrictedProfile{
		MaximumAge:    13,
		BlockedLabels: []string{"violence", "nudity"},
		Libraries:     []string{libraries.Movies, libraries.Shows},
	}
}

// Tiny is the smallest catalogue that still populates every read model: a few
// of each kind, one collection, personal state on a slice of it. Tests that are
// about one route's behaviour rather than about load use it, and it builds in
// well under a second.
func Tiny() Shape {
	return Shape{
		Movies: 40, Shows: 2, SeasonsPerShow: 1, EpisodesPerSeason: 3,
		Artists: 2, AlbumsPerArtist: 1, SongsPerAlbum: 3, Books: 2, FilesPerBook: 2,
		Collections: 2, ItemsPerCollection: 5,
		GenresPerItem: 2, CreditsPerItem: 3, AttributesPerItem: 4,
		Profiles: 2, WatchedPercent: 30, ProgressPercent: 30, RestrictedProfiles: 1, Seed: 7,
	}
}

func hashExecutable(sum io.Writer) bool {
	path, err := os.Executable()
	if err != nil {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	_, err = io.Copy(sum, file)
	return err == nil
}

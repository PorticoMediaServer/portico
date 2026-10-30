package compactcatalog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
)

// The catalogue write API. Every writer of catalogue facts calls these inside
// its own dbwork transaction: they resolve identity through the ledger, apply
// owner locks, and queue the derived work the change causes. Nothing else
// writes the fact tables (catalog_entities, the kind side tables, assets and
// links, terms, credits, attributes, collections). Side effects owned by other
// subsystems (library revisions, provider job invalidation, artwork dirt) are
// triggers on the fact tables, so they happen whoever writes.

// Source says who is writing a field: an automatic writer (scanner, provider
// metadata) never overwrites a field the owner locked; the owner always writes.
type Source int

const (
	Automatic Source = iota
	Owner
)

// ErrIdentityConflict: an identity key already names an entity of another
// library or kind (two libraries sharing a folder, or a key reused by a kind).
var ErrIdentityConflict = errors.New("catalogue identity belongs to another library or kind")

// LibraryTx returns the catalogue handle of a library. A trigger on libraries
// keeps the handle, so this is one indexed read; the write is only a fallback.
// Library kinds are their own enum (movie 1, tv 2, anime 3, music 4, audiobook 5).
func LibraryTx(ctx context.Context, tx *sql.Tx, library string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_libraries WHERE library_id=?`, library).Scan(&id)
	if !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO catalog_libraries(library_id,root,name,kind)
 SELECT l.id,l.root,l.name,CASE l.kind WHEN 'movie' THEN 1 WHEN 'tv' THEN 2 WHEN 'anime' THEN 3 WHEN 'music' THEN 4 WHEN 'audiobook' THEN 5 ELSE 99 END FROM libraries l WHERE l.id=?
 RETURNING id`, library).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("library %q does not exist", library)
	}
	return id, err
}

// LibraryHandle returns an existing library's handle (0 when it has none yet).
func LibraryHandle(ctx context.Context, q ReadQuery, library string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM catalog_libraries WHERE library_id=?`, library).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// Entity is a catalogue entity's backbone facts.
type Entity struct {
	Library   int64  // catalog_libraries.id
	Kind      Kind   //
	Parent    int64  // 0: none (a season's show, an episode's season, an album's artist, a file's book)
	Key       string // identity source key (see ItemKey and friends)
	Title     string //
	SortTitle string //
	Language  string // metadata language, for sorting
	Year      int    //
	Added     string // RFC 3339 time the entity was added, or "" (containers)
}

// Identity keys: stable names of an entity inside its library's root folder,
// so a rescan or a re-added library finds the same public id. A new kind
// brings its own prefix.
func ItemKey(root, path string, part int) string {
	rel := path
	if r, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(r, "..") {
		rel = filepath.ToSlash(r)
	}
	if part > 0 {
		return fmt.Sprintf("file:%s#%d", rel, part)
	}
	return "file:" + rel
}
func ShowKey(localKey string) string { return "show:" + localKey }
func SeasonKey(showLocalKey string, number int) string {
	return fmt.Sprintf("season:%s/%d", showLocalKey, number)
}
func EpisodeKey(showLocalKey, numbering string, season, number int) string {
	return fmt.Sprintf("episode:%s/%s/%d/%d", showLocalKey, numbering, season, number)
}
func ArtistKey(localKey string) string    { return "artist:" + localKey }
func AlbumKey(localKey string) string     { return "album:" + localKey }
func BookKey(localKey string) string      { return "book:" + localKey }
func CollectionKey(nameKey string) string { return "collection:" + nameKey }
func AuthorKey(name string) string        { return dbwork.AuthorIdentityKey(name) }
func ExtraKey(root, path string) string {
	return "extra:" + strings.TrimPrefix(ItemKey(root, path, 0), "file:")
}
func RecordingKey(recording string) string { return "recording:" + recording }

// libraryRoot is the folder identity keys are relative to: the library's
// identity root, fixed when it was created.
func libraryRoot(ctx context.Context, tx *sql.Tx, library int64) (string, error) {
	var root string
	err := tx.QueryRowContext(ctx, `SELECT root FROM catalog_libraries WHERE id=?`, library).Scan(&root)
	return root, err
}

// LibraryRootTx returns a library handle's root folder (for building keys).
func LibraryRootTx(ctx context.Context, tx *sql.Tx, library int64) (string, error) {
	return libraryRoot(ctx, tx, library)
}

// FindEntityTx returns the live entity an identity key names in a library, or 0.
func FindEntityTx(ctx context.Context, tx *sql.Tx, library int64, key string) (int64, error) {
	root, err := libraryRoot(ctx, tx, library)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT e.id FROM catalog_identities i JOIN catalog_entities e ON e.public_id=i.public_id WHERE i.root=? AND i.source_key=?`, root, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// EnsureEntityTx finds or creates the entity its identity key names. An
// existing entity keeps its facts (a scanner's filename title never replaces a
// provider's); it is only made live again and re-parented if that changed.
func EnsureEntityTx(ctx context.Context, tx *sql.Tx, e Entity) (id int64, created bool, err error) {
	return upsertEntity(ctx, tx, e, false, nil)
}

// UpsertEntityTx finds or creates the entity its identity key names and brings
// its backbone facts up to date (unlocked fields only, for an existing entity).
// A retired entity that reappears is live again.
func UpsertEntityTx(ctx context.Context, tx *sql.Tx, e Entity) (id int64, created bool, err error) {
	return upsertEntity(ctx, tx, e, true, nil)
}

// EnsureEntityWithFactsTx initializes facts only when it creates an entity.
// Creation and initialization share the same complete derivation touch. Existing
// entities and their facts are left as EnsureEntityTx would leave them.
func EnsureEntityWithFactsTx(ctx context.Context, tx *sql.Tx, e Entity, facts map[string]any) (id int64, created bool, err error) {
	return upsertEntity(ctx, tx, e, false, facts)
}

func upsertEntity(ctx context.Context, tx *sql.Tx, e Entity, update bool, initialFacts map[string]any) (id int64, created bool, err error) {
	if err = RequireEntityAcquirableTx(ctx, tx, e.Parent); err != nil {
		return 0, false, err
	}
	if e.Library == 0 || e.Kind < 1 || e.Key == "" {
		return 0, false, fmt.Errorf("catalogue entity needs a library, a kind and an identity key")
	}
	root, err := libraryRoot(ctx, tx, e.Library)
	if err != nil {
		return 0, false, err
	}
	var public []byte
	var existing sql.NullInt64
	var library, kind sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT i.public_id,e.id,e.library_id,e.kind FROM catalog_identities i LEFT JOIN catalog_entities e ON e.public_id=i.public_id WHERE i.root=? AND i.source_key=?`, root, e.Key).Scan(&public, &existing, &library, &kind)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		public = entityid.New()
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_identities(public_id,root,source_key,kind) VALUES(?,?,?,?)`, public, root, e.Key, int(e.Kind)); err != nil {
			return 0, false, err
		}
	case err != nil:
		return 0, false, err
	}
	var parent any
	if e.Parent != 0 {
		parent = e.Parent
	}
	if existing.Valid {
		if library.Int64 != e.Library || kind.Int64 != int64(e.Kind) {
			return 0, false, fmt.Errorf("%w: %s", ErrIdentityConflict, e.Key)
		}
		id = existing.Int64
		if err = RequireEntityAcquirableTx(ctx, tx, id); err != nil {
			return 0, false, err
		}
		res, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=?,retired=0 WHERE id=? AND (parent_id IS NOT ? OR retired<>0)`, parent, id, parent)
		if err != nil {
			return 0, false, err
		}
		if !update {
			if n, _ := res.RowsAffected(); n > 0 {
				return id, false, touchEntity(ctx, tx, id, e.Kind)
			}
			return id, false, nil
		}
		fields := map[string]any{"title": e.Title, "year": e.Year}
		if e.SortTitle != "" {
			fields["sort_title"] = e.SortTitle
		}
		if e.Language != "" {
			fields["metadata_language"] = e.Language
		}
		if err = setFields(ctx, tx, id, Automatic, fields, true); err != nil {
			return 0, false, err
		}
		return id, false, nil
	}
	added := addedMillis(e.Added)
	err = tx.QueryRowContext(ctx, `INSERT INTO catalog_entities(public_id,library_id,kind,parent_id,title,sort_title,metadata_language,sort_key,sort_head,year,added_ms)
 VALUES(?1,?2,?3,?4,?5,?6,?7,portico_sort_title(?5,CASE WHEN COALESCE((SELECT p.sort_ignore_articles FROM library_preferences p JOIN catalog_libraries cl ON cl.library_id=p.library_id WHERE cl.id=?2),1)=0 AND ?6='' THEN ?5 ELSE ?6 END,?7),substr(portico_sort_title(?5,CASE WHEN COALESCE((SELECT p.sort_ignore_articles FROM library_preferences p JOIN catalog_libraries cl ON cl.library_id=p.library_id WHERE cl.id=?2),1)=0 AND ?6='' THEN ?5 ELSE ?6 END,?7),1,1),?8,?9) RETURNING id`,
		public, e.Library, int(e.Kind), parent, e.Title, e.SortTitle, e.Language, e.Year, added).Scan(&id)
	if err != nil {
		return 0, false, err
	}
	// Every playable item has a details row, as every old items row had these
	// columns: readers join catalog_item_details directly.
	if e.Added != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_item_details(entity_id,added_text) VALUES(?,?) ON CONFLICT(entity_id) DO UPDATE SET added_text=excluded.added_text`, id, e.Added); err != nil {
			return 0, false, err
		}
	} else if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_item_details(entity_id) SELECT ? FROM catalog_kinds WHERE id=? AND playable=1`, id, int(e.Kind)); err != nil {
		return 0, false, err
	}
	if len(initialFacts) != 0 {
		if err = requireFactAuthority(ctx, tx, id, initialFacts); err != nil {
			return 0, false, err
		}
		// No worker can consume an uncommitted entity. Initialize its facts
		// before the creation touch rather than incrementing the same dirty
		// revisions a second time in this transaction.
		if err = setFieldsWithQueue(ctx, tx, id, Automatic, initialFacts, false, false); err != nil {
			return 0, false, err
		}
	}
	return id, true, touchEntity(ctx, tx, id, e.Kind)
}

// touchEntity queues the derived work any change to an entity's facts causes,
// including the members that carry its title, parentage or liveness.
func touchEntity(ctx context.Context, tx *sql.Tx, id int64, kind Kind) error {
	if err := touchEntityOwn(ctx, tx, id, kind); err != nil {
		return err
	}
	return touchEntityDependents(ctx, tx, id, kind)
}

// touchEntityOwn queues the entity's own derived work: a change no member
// derives anything from.
func touchEntityOwn(ctx context.Context, tx *sql.Tx, id int64, kind Kind) error {
	for _, domain := range []int{DomainSearch, DomainBrowseRows, DomainBrowseEdges} {
		if err := TouchTx(ctx, tx, domain, id); err != nil {
			return err
		}
	}
	if kind == Movie {
		for _, domain := range []int{DomainCategories, DomainRelated} {
			if err := TouchTx(ctx, tx, domain, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func touchEntityDependents(ctx context.Context, tx *sql.Tx, id int64, kind Kind) error {
	switch kind {
	case Artist, Collection, Show, Album, Book:
		var lifetime string
		err := tx.QueryRowContext(ctx, `SELECT lower(hex(public_id)) FROM catalog_entities WHERE id=?`, id).Scan(&lifetime)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return queueSource(ctx, tx, DomainEntitySources, id, lifetime, 1<<uint(kind))
	}
	return nil
}

// TouchEntityTx queues an entity's derived data after a writer changed facts
// the API doesn't know about (a trigger-less side table).
func TouchEntityTx(ctx context.Context, tx *sql.Tx, id int64) error {
	var kind Kind
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, id).Scan(&kind); err != nil {
		return err
	}
	return touchEntity(ctx, tx, id, kind)
}

// touchOwnTx queues an entity's own derived work after a change to facts its
// members never carry (its terms, credits and attributes): a show's genres
// arriving must not re-derive every episode.
func touchOwnTx(ctx context.Context, tx *sql.Tx, id int64) error {
	var kind Kind
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, id).Scan(&kind); err != nil {
		return err
	}
	return touchEntityOwn(ctx, tx, id, kind)
}

// Where each field lives. A field is written in the kind's side table when
// that table has the column, else in the backbone, else in the item details.
var backboneFields = map[string]bool{"title": true, "sort_title": true, "metadata_language": true, "year": true}

var detailFields = setOf("added_text", "overview", "poster_url", "backdrop_url", "original_title", "edition", "tagline", "release_date", "content_rating", "studio", "network", "country", "tags", "labels")

var sideTables = map[Kind]struct {
	table  string
	fields map[string]bool
}{
	Show:       {"catalog_shows", setOf("source_edition", "local_key", "provider_match_status", "original_title", "tagline", "overview", "content_rating", "studio", "network", "country", "tags", "labels")},
	Season:     {"catalog_seasons", setOf("show_id", "number", "title", "overview")},
	Episode:    {"catalog_episodes", setOf("show_id", "season_id", "numbering", "number", "local_identity_status", "ordering_basis", "air_date")},
	Artist:     {"catalog_artists", setOf("local_key", "overview", "tags")},
	Album:      {"catalog_albums", setOf("artist_id", "local_key", "edition_key", "provider_match_status", "overview", "label", "tags", "release_id", "release_group_id", "release_date", "release_country", "provider_observed_at")},
	Track:      {"catalog_songs", setOf("album_id", "disc_number", "track_number", "release_status", "recording_id", "release_id", "release_group_id", "track_id", "provider_observed_at", "provider_title", "provider_artist")},
	Book:       {"catalog_books", setOf("library_id", "local_key", "author", "narrator", "provider_match_status", "overview", "series", "series_position", "tags", "labels", "local_metadata_payload")},
	Part:       {"catalog_book_files", setOf("book_id", "disc_number", "part_number")},
	Collection: {"catalog_collections", setOf("library_id", "name_key", "created_at")},
}

func setOf(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[n] = true
	}
	return out
}

// OwnerNamespace is the metadata_owner_fields kind of an entity kind: the
// editor's field set ('item' for every playable item).
func OwnerNamespace(k Kind) string {
	switch k {
	case Show:
		return "show"
	case Season:
		return "season"
	case Artist:
		return "artist"
	case Album:
		return "album"
	case Book:
		return "book"
	case Collection:
		return "collection"
	case Author:
		return "author"
	}
	return "item"
}

// SetFieldsTx writes named fields of one entity wherever they live. An
// Automatic write to a field the owner locked records the incoming value as
// the field's automatic_value and keeps the owner's value; an Owner write (or
// an unlocked field) is written. Queues the entity's derived data when
// anything changed.
func SetFieldsTx(ctx context.Context, tx *sql.Tx, id int64, source Source, fields map[string]any) error {
	if err := RequireEntityAcquirableTx(ctx, tx, id); err != nil {
		return err
	}
	return setFields(ctx, tx, id, source, fields, false)
}

// setFields routes fields to their tables; backboneFirst sends the backbone
// names (title, year, …) to catalog_entities even where the kind's side table
// has a column of that name (a season's title override).
func setFields(ctx context.Context, tx *sql.Tx, id int64, source Source, fields map[string]any, backboneFirst bool) error {
	return setFieldsWithQueue(ctx, tx, id, source, fields, backboneFirst, true)
}

// Only the creation path can defer this touch: it always queues the full new
// entity after these writes. Ordinary edits retain their current touch behavior.
func setFieldsWithQueue(ctx context.Context, tx *sql.Tx, id int64, source Source, fields map[string]any, backboneFirst, queue bool) error {
	if len(fields) == 0 {
		return nil
	}
	var kind Kind
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, id).Scan(&kind); err != nil {
		return err
	}
	if source == Automatic {
		locked, err := lockedFields(ctx, tx, OwnerNamespace(kind), id, kind)
		if err != nil {
			return err
		}
		for name, value := range fields {
			owner, ok := locked[name]
			if !ok {
				continue
			}
			if _, err = tx.ExecContext(ctx, `UPDATE metadata_owner_fields SET automatic_value=? WHERE kind=? AND entity_id=? AND field=?`, fmt.Sprint(value), OwnerNamespace(kind), id, owner); err != nil {
				return err
			}
			delete(fields, name)
		}
		if len(fields) == 0 {
			return nil
		}
	}
	groups := map[string]map[string]any{}
	side, hasSide := sideTables[kind]
	for name, value := range fields {
		table := ""
		switch {
		case backboneFirst && backboneFields[name]:
			table = "catalog_entities"
		case hasSide && side.fields[name]:
			table = side.table
		case backboneFields[name]:
			table = "catalog_entities"
		case detailFields[name]:
			table = "catalog_item_details"
		default:
			return fmt.Errorf("catalogue field %q is not a field of kind %d", name, kind)
		}
		if groups[table] == nil {
			groups[table] = map[string]any{}
		}
		groups[table][name] = value
	}
	// When an item was first catalogued is stored twice: as text beside its
	// details (browse and "recently added" order by it) and as milliseconds on
	// the entity (the watched watermark compares it). A write of one is a
	// write of both, so they never disagree.
	if added, ok := groups["catalog_item_details"]["added_text"]; ok {
		if groups["catalog_entities"] == nil {
			groups["catalog_entities"] = map[string]any{}
		}
		text, _ := added.(string)
		groups["catalog_entities"]["added_ms"] = addedMillis(text)
	}
	changed, shared := false, false
	for table, values := range groups {
		n, err := writeFacts(ctx, tx, table, id, values)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		changed = true
		if table == "catalog_entities" {
			shared = true
		}
		for name := range values {
			shared = shared || dependentFields[name]
		}
	}
	if !queue {
		return nil
	}
	switch {
	case shared:
		return touchEntity(ctx, tx, id, kind)
	case changed:
		return touchEntityOwn(ctx, tx, id, kind)
	}
	return nil
}

// dependentFields are the side-table facts a container's members derive from
// (an album's artist, a book's author, where a container lives). The backbone
// (title, sort title, language, year) is the rest: members' search documents
// and season rows carry their parent's title and year. Any other fact of a
// show, album, artist or book (overview, tags, label, release details…) is
// its own, and a provider filling it in one title at a time must not
// re-derive every episode or song of the container for each detail.
var dependentFields = setOf("artist_id", "author", "library_id", "show_id", "album_id", "book_id")

// lockedFields maps each catalogue field the owner locked on the entity to the
// name the lock is recorded under. The metadata editor records locks under
// its own field names (sortTitle, contentRating, description); a lock recorded
// under the catalogue field itself (title, overview) maps to itself.
func lockedFields(ctx context.Context, tx *sql.Tx, namespace string, id int64, kind Kind) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT field FROM metadata_owner_fields WHERE kind=? AND entity_id=? AND locked=1`, namespace, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var f string
		if err = rows.Scan(&f); err != nil {
			return nil, err
		}
		out[f] = f
		if column, ok := OwnerFieldColumn(kind, f); ok {
			out[column] = f
		}
	}
	return out, rows.Err()
}

// OwnerFieldColumn maps a metadata-editor field to the catalogue field the
// write API routes to its table (backbone, kind side table or details), for an
// entity of kind. The editor records owner values and locks under its own
// names; this is the one translation both sides use.
func OwnerFieldColumn(kind Kind, field string) (string, bool) {
	switch field {
	case "title":
		return "title", true
	case "description":
		return "overview", true
	case "sortTitle":
		return "sort_title", true
	case "metadataLanguage":
		return "metadata_language", true
	case "originalTitle":
		return "original_title", true
	case "releaseDate":
		if kind == Episode {
			return "air_date", true
		}
		return "release_date", true
	case "contentRating":
		return "content_rating", true
	case "seriesIndex":
		return "series_position", true
	case "episodeNumber", "number":
		return "number", true
	case "trackNumber":
		return "track_number", true
	case "discNumber":
		return "disc_number", true
	case "partNumber":
		return "part_number", true
	case "tags", "labels", "label", "author", "narrator", "series", "studio", "network", "country", "edition", "tagline", "year":
		return field, true
	}
	return "", false
}

// writeFacts upserts columns of one entity's row in table and reports whether
// a value changed. The backbone recomputes its sort key when a sort input
// changed.
// addedMillis is a first-catalogued time as the entity stores it: RFC 3339 text
// to milliseconds, or NULL when there is no (valid) time.
func addedMillis(text string) any {
	if text == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return nil
	}
	return t.UnixMilli()
}

func writeFacts(ctx context.Context, tx *sql.Tx, table string, id int64, values map[string]any) (int64, error) {
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	args := make([]any, 0, len(names)*2+1)
	var set, differ []string
	for _, n := range names {
		set = append(set, n+"=?")
		args = append(args, values[n])
	}
	for _, n := range names {
		differ = append(differ, n+" IS NOT ?")
		args = append(args, values[n])
	}
	if table == "catalog_entities" {
		args = append(args, id)
		res, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET `+strings.Join(set, ",")+` WHERE (`+strings.Join(differ, " OR ")+`) AND id=?`, args...)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_entities SET sort_key=portico_sort_title(title,CASE WHEN COALESCE((SELECT p.sort_ignore_articles FROM library_preferences p JOIN catalog_libraries cl ON cl.library_id=p.library_id WHERE cl.id=catalog_entities.library_id),1)=0 AND sort_title='' THEN title ELSE sort_title END,metadata_language),sort_head=substr(portico_sort_title(title,CASE WHEN COALESCE((SELECT p.sort_ignore_articles FROM library_preferences p JOIN catalog_libraries cl ON cl.library_id=p.library_id WHERE cl.id=catalog_entities.library_id),1)=0 AND sort_title='' THEN title ELSE sort_title END,metadata_language),1,1) WHERE id=?`, id)
		}
		return n, err
	}
	// An existing side row is updated in place. It cannot be an upsert: SQLite
	// checks NOT NULL on the proposed insert row before ON CONFLICT, so a partial
	// write to a row whose table has a required column (an album's artist) would
	// fail even though the row exists.
	args = append(args, id)
	res, err := tx.ExecContext(ctx, `UPDATE `+table+` SET `+strings.Join(set, ",")+` WHERE (`+strings.Join(differ, " OR ")+`) AND entity_id=?`, args...)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return n, nil
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE entity_id=?)`, id).Scan(&exists); err != nil || exists {
		return 0, err
	}
	// Side rows are created on first write with defaults for the rest.
	insertArgs := append([]any{id}, valuesInOrder(values, names)...)
	res, err = tx.ExecContext(ctx, `INSERT INTO `+table+`(entity_id,`+strings.Join(names, ",")+`) VALUES(?`+strings.Repeat(",?", len(names))+`)`, insertArgs...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func valuesInOrder(values map[string]any, names []string) []any {
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = values[n]
	}
	return out
}

// SetFactsTx writes an entity's side-table row (creating it) for an automatic
// writer: locked fields are skipped as in SetFieldsTx. Use it right after
// UpsertEntityTx for the kind's own facts.
func SetFactsTx(ctx context.Context, tx *sql.Tx, id int64, fields map[string]any) error {
	if err := requireFactAuthority(ctx, tx, id, fields); err != nil {
		return err
	}
	return SetFieldsTx(ctx, tx, id, Automatic, fields)
}

func requireFactAuthority(ctx context.Context, tx *sql.Tx, id int64, fields map[string]any) error {
	if err := RequireEntityAcquirableTx(ctx, tx, id); err != nil {
		return err
	}
	for _, key := range []string{"album_id", "artist_id", "book_id", "show_id", "season_id"} {
		if parent, ok := fields[key].(int64); ok && parent != 0 {
			if err := RequireEntityAcquirableTx(ctx, tx, parent); err != nil {
				return err
			}
		}
	}
	return nil
}

// RetireEntityTx retires an entity (a container that lost its last member):
// it keeps its identity, personal state and history, and leaves browse and
// search.
func RetireEntityTx(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET retired=1 WHERE id=? AND retired=0`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return TouchEntityTx(ctx, tx, id)
}

// DeleteEntityTx deletes an entity and everything that cascades from it. Its
// identity stays in the ledger, so the same file or key gets the same public
// id if it comes back. Derived rows that don't cascade (counted rows, bucket
// contributions, the title index) are dropped by the derivations, which see
// the entity gone.
func DeleteEntityTx(ctx context.Context, tx *sql.Tx, id int64) error {
	if err := RequireNoEntityMBPinsTx(ctx, tx, id); err != nil {
		return err
	}
	if err := RequireEntityAcquirableTx(ctx, tx, id); err != nil {
		return err
	}
	// The containers it belonged to re-derive their rows.
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_dirty(domain,entity_id,revision)
 SELECT ?,entity_id,1 FROM catalog_browse_memberships INDEXED BY catalog_browse_membership_item WHERE item_id=? AND entity_id<>item_id
 ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`, DomainBrowseRows, id); err != nil {
		return err
	}
	// The title index row goes with its document, through the table's own trigger.
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_search_documents WHERE entity_id=?`, id); err != nil {
		return err
	}
	for _, domain := range []int{DomainBrowseRows, DomainItemMetrics, DomainCategories} {
		if err := TouchTx(ctx, tx, domain, id); err != nil {
			return err
		}
	}
	// The availability holder deliberately has no entity FK: normal completion
	// retires it, and external entity removal must release the same point row.
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_availability_work WHERE entity_id=?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_entities WHERE id=?`, id)
	return err
}

// Asset is one physical file's evidence.
type Asset struct {
	Path                              string
	Size, ModifiedNS                  int64
	Container, VideoCodec, AudioCodec string
	Width, Height                     int
	Duration                          float64
}

// NewAssetToken is a fresh public asset id (the asset id clients see).
func NewAssetToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// newPersonToken is a fresh public person id (32 hex digits, as before).
func newPersonToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// UpsertAssetTx records a file's evidence (by path) and marks it available.
// It returns the asset's integer id and its public token.
func UpsertAssetTx(ctx context.Context, tx *sql.Tx, a Asset) (int64, string, error) {
	id, token, err := AssetByPathTx(ctx, tx, a.Path)
	if err != nil {
		return 0, "", err
	}
	if id == 0 {
		token = NewAssetToken()
		id, err = CreateAssetTx(ctx, tx, token, a, true)
		return id, token, err
	}
	// The guarded asset trigger accepts all required consumers once.
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_assets SET size=?2,modified_ns=?3,container=?4,video_codec=?5,audio_codec=?6,width=?7,height=?8,duration=?9,available=1
 WHERE id=?1 AND (size IS NOT ?2 OR modified_ns IS NOT ?3 OR container IS NOT ?4 OR video_codec IS NOT ?5 OR audio_codec IS NOT ?6 OR width IS NOT ?7 OR height IS NOT ?8 OR duration IS NOT ?9 OR available<>1)`, id, a.Size, a.ModifiedNS, a.Container, a.VideoCodec, a.AudioCodec, a.Width, a.Height, a.Duration); err != nil {
		return 0, "", err
	}
	return id, token, nil
}

// CreateAssetTx records a new file under a public token the caller chose
// (inventory keeps an asset's token across moves). It returns the integer id.
func CreateAssetTx(ctx context.Context, tx *sql.Tx, token string, a Asset, available bool) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available)
 VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING id`, token, a.Path, a.Size, a.ModifiedNS, a.Container, a.VideoCodec, a.AudioCodec, a.Width, a.Height, a.Duration, available).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, TouchTx(ctx, tx, DomainAssetMetrics, id)
}

var assetColumns = setOf("path", "size", "modified_ns", "container", "video_codec", "audio_codec", "width", "height", "duration", "available")

// SetAssetTx updates a file's evidence columns (path, size, modified_ns,
// container, codecs, dimensions, duration, available) when they differ, and
// queues the items it backs.
func SetAssetTx(ctx context.Context, tx *sql.Tx, asset int64, values map[string]any) error {
	names := make([]string, 0, len(values))
	for n := range values {
		if !assetColumns[n] {
			return fmt.Errorf("catalogue asset has no column %q", n)
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	var set, differ []string
	args := make([]any, 0, 2*len(names)+1)
	for _, n := range names {
		set = append(set, n+"=?")
		args = append(args, values[n])
	}
	for _, n := range names {
		differ = append(differ, n+" IS NOT ?")
		args = append(args, values[n])
	}
	args = append(args, asset)
	_, err := tx.ExecContext(ctx, `UPDATE catalog_assets SET `+strings.Join(set, ",")+` WHERE (`+strings.Join(differ, " OR ")+`) AND id=?`, args...)
	return err
}

// AssetByPathTx returns the asset recorded at path (0 when none).
func AssetByPathTx(ctx context.Context, tx *sql.Tx, path string) (int64, string, error) {
	var id int64
	var token string
	err := tx.QueryRowContext(ctx, `SELECT id,token FROM catalog_assets WHERE path=?`, path).Scan(&id, &token)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	return id, token, err
}

// AssetByTokenTx returns the integer id of a public asset id (0 when none).
func AssetByTokenTx(ctx context.Context, q ReadQuery, token string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM catalog_assets WHERE token=?`, token).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// SetAssetAvailableTx records whether a file is present (inventory, trash,
// remote sources) and queues the availability of the items it backs.
func SetAssetAvailableTx(ctx context.Context, tx *sql.Tx, asset int64, available bool) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_assets SET available=? WHERE id=? AND available<>?`, available, asset, available)
	return err
}

// A file is present while some place it was last seen still holds it: an
// inventory record that is neither retired nor missing. A file no inventory
// knows (a recording, a file catalogued before inventories) is present while
// it is marked available. A source being offline or unreachable is not
// absence: nothing under it is missing because the disk is away.
const presentFileSQL = `(EXISTS(SELECT 1 FROM inventory_objects o WHERE o.asset_id=a.token AND o.retired=0 AND o.state='available')
 OR (a.available=1 AND NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.asset_id=a.token)))`

// SettleMissingTx brings the missing mark of playable titles in step with
// their files: a title with no present file is marked missing from now (an
// existing mark keeps its time), and a title with one loses the mark. Every
// writer that changes which files a title has, or whether a file is present,
// calls it for the titles concerned.
// Explicit point consumers (especially destructive cleanup) require this
// synchronous live recheck. High-fanout ingress uses source jobs instead.
func SettleMissingTx(ctx context.Context, tx *sql.Tx, items ...int64) error {
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_missing_titles WHERE entity_id=?1
 AND EXISTS(SELECT 1 FROM catalog_asset_links l CROSS JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?1 AND `+presentFileSQL+`)`, item); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_missing_titles(entity_id,library_id,missing_since_ms)
 SELECT e.id,e.library_id,?2 FROM catalog_entities e CROSS JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1
 WHERE e.id=?1 AND NOT EXISTS(SELECT 1 FROM catalog_asset_links l CROSS JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?1 AND `+presentFileSQL+`)
 ON CONFLICT(entity_id) DO NOTHING`, item, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return nil
}

// SettleAssetMissingTx settles the titles a file backs (after the file's
// presence changed).
func SettleAssetMissingTx(ctx context.Context, tx *sql.Tx, asset int64) error {
	return queueAssetSource(ctx, tx, asset, sourceMissing)
}

// DeleteAssetTx performs the existing physical FK delete. Old link DELETE
// markers retain each former owner's downstream work; no Go witness slice is
// materialized. The physical cascade itself still costs its actual old edges.
func DeleteAssetTx(ctx context.Context, tx *sql.Tx, asset int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_asset_issues WHERE asset_id=?`, asset); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_assets WHERE id=?`, asset)
	return err
}

// Link is one of an item's physical parts. Files of the same part are
// versions of one another; Edition labels a version where one is known.
type Link struct {
	Part       int
	Start      float64
	End        *float64
	HasSegment bool
	Edition    string
}

// LinkAssetTx attaches a file to an item (idempotent). An empty edition never
// erases a label an earlier reading gave the version.
func LinkAssetTx(ctx context.Context, tx *sql.Tx, item, asset int64, l Link) error {
	if err := RequireEntityAcquirableTx(ctx, tx, item); err != nil {
		return err
	}
	var end any
	if l.End != nil {
		end = *l.End
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links WHERE entity_id=? AND asset_id=?)`, item, asset).Scan(&exists); err != nil {
		return err
	}
	var res sql.Result
	var err error
	if exists {
		res, err = tx.ExecContext(ctx, `UPDATE catalog_asset_links SET part_index=?3,start_seconds=?4,end_seconds=?5,
 edition=CASE WHEN ?6='' THEN edition ELSE ?6 END WHERE entity_id=?1 AND asset_id=?2
 AND (part_index IS NOT ?3 OR start_seconds IS NOT ?4 OR end_seconds IS NOT ?5 OR (?6<>'' AND edition IS NOT ?6))`, item, asset, l.Part, l.Start, end, l.Edition)
	} else {
		res, err = tx.ExecContext(ctx, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition)
 SELECT ?1,?2,?3,a.available,?4,?5,?6 FROM catalog_assets a WHERE a.id=?2`, item, asset, l.Part, l.Start, end, l.Edition)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if err = touchLinked(ctx, tx, item); err != nil {
		return err
	}
	return queueMissingSource(ctx, tx, item)
}

// UnlinkAssetTx detaches a file from an item. An item left with no file stays
// in the catalogue, marked missing.
func UnlinkAssetTx(ctx context.Context, tx *sql.Tx, item, asset int64) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, asset)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if err = touchLinked(ctx, tx, item); err != nil {
		return err
	}
	return queueMissingSource(ctx, tx, item)
}

func touchLinked(ctx context.Context, tx *sql.Tx, item int64) error {
	for _, domain := range []int{DomainAvailability, DomainItemMetrics, DomainBrowseRows} {
		if err := TouchTx(ctx, tx, domain, item); err != nil {
			return err
		}
	}
	return nil
}

// Term is one provider's term of a vocabulary for an entity.
type Term struct {
	SourceID string // the provider's id for it ('' → the key)
	Name     string // display label
	Key      string // normalized key ('' → lower(Name))
}

// SetTermsTx replaces an entity's terms of one vocabulary from one provider, in
// the provider's order (the first term is the primary one).
func SetTermsTx(ctx context.Context, tx *sql.Tx, entity int64, vocab Vocab, provider string, terms []Term) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_term_sources WHERE entity_id=? AND provider=? AND (SELECT t.vocab FROM catalog_terms t WHERE t.id=catalog_term_sources.term_id)=?`, entity, provider, int(vocab)); err != nil {
		return err
	}
	for ordinal, t := range terms {
		key := t.Key
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(t.Name))
		}
		if key == "" {
			continue
		}
		source := t.SourceID
		if source == "" {
			source = key
		}
		var term int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO catalog_terms(vocab,key,label) VALUES(?,?,?) ON CONFLICT(vocab,key) DO UPDATE SET label=label RETURNING id`, int(vocab), key, t.Name).Scan(&term); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_entity_terms(entity_id,term_id) VALUES(?,?)`, entity, term); err != nil {
			return err
		}
		var override any
		if t.Name != "" {
			override = t.Name
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,label_override,source_name,ordinal) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(entity_id,provider,source_id) DO UPDATE SET term_id=excluded.term_id,label_override=excluded.label_override,source_name=excluded.source_name,ordinal=excluded.ordinal`, entity, term, provider, source, override, t.Name, ordinal); err != nil {
			return err
		}
	}
	// A term no provider names any more leaves the entity.
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_entity_terms WHERE entity_id=?1 AND (SELECT t.vocab FROM catalog_terms t WHERE t.id=catalog_entity_terms.term_id)=?2
 AND NOT EXISTS(SELECT 1 FROM catalog_term_sources s INDEXED BY catalog_term_source_membership WHERE s.entity_id=?1 AND s.term_id=catalog_entity_terms.term_id)`, entity, int(vocab)); err != nil {
		return err
	}
	return touchOwnTx(ctx, tx, entity)
}

// Credit is one person's credit on an entity from one provider.
type Credit struct {
	PersonKey        string // people identity key ('' for an unlinked credit)
	PersonName       string
	PersonSortName   string
	ProviderPersonID string
	CreditID         string
	CreditedName     string
	Role, Department string
	Ordinal          int
}

// SetCreditsTx replaces an entity's credits from one provider, creating the
// people they name.
func setCreditProjectionProviderTx(ctx context.Context, tx *sql.Tx, entity int64, provider string, credits []Credit) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_credits WHERE entity_id=? AND provider=?`, entity, provider); err != nil {
		return err
	}
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(ord),-1)+1 FROM catalog_credits WHERE entity_id=?`, entity).Scan(&next); err != nil {
		return err
	}
	for _, c := range credits {
		var person any
		var aliased int64
		if c.PersonKey != "" {
			// A name someone was known by before a provider identified them.
			if err := tx.QueryRowContext(ctx, `SELECT person_id FROM catalog_people_aliases WHERE alias=?`, c.PersonKey).Scan(&aliased); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if aliased != 0 {
			person = aliased
		} else if c.PersonKey != "" {
			var id int64
			// The people directory orders by sort_name on its index (binary
			// collation), so the stored form is folded: "de Niro" before "Zeta".
			sortName := c.PersonSortName
			if sortName == "" {
				sortName = c.PersonName
			}
			sortName = strings.ToLower(sortName)
			if err := tx.QueryRowContext(ctx, `INSERT INTO catalog_people(token,identity_key,name,sort_name) VALUES(?,?,?,?)
 ON CONFLICT(identity_key) DO UPDATE SET name=name RETURNING id`, newPersonToken(), c.PersonKey, c.PersonName, sortName).Scan(&id); err != nil {
				return err
			}
			person = id
		}
		role, err := creditLabel(ctx, tx, c.Role)
		if err != nil {
			return err
		}
		department, err := creditLabel(ctx, tx, c.Department)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_credits(entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,provider_person_id,source_ordinal) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			entity, next, person, role, department, c.CreditedName, provider, c.CreditID, c.ProviderPersonID, c.Ordinal); err != nil {
			return err
		}
		next++
	}
	return touchOwnTx(ctx, tx, entity)
}

// LocalPersonKey is the people identity key of someone credited by name
// alone (local metadata with no provider id). It folds as SQLite's lower()
// does, so item credits and a show's credits name one person.
func LocalPersonKey(name string) string {
	b := []byte(strings.TrimSpace(name))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return "name:" + string(b)
}

// AdoptLocalPersonTx makes the person known only by name from local metadata
// one with the provider's person, when the provider credits that name on the
// same title the local metadata does. The person keeps their page: either
// they take the provider's identity, or, when that person already exists,
// their credits move to them and the old page id leads there.
func AdoptLocalPersonTx(ctx context.Context, tx *sql.Tx, entity int64, provider, providerPersonID, name string) error {
	if provider == "" || providerPersonID == "" || strings.TrimSpace(name) == "" {
		return nil
	}
	local, providerKey := LocalPersonKey(name), provider+":"+providerPersonID
	var from int64
	var token string
	err := tx.QueryRowContext(ctx, `SELECT p.id,p.token FROM catalog_people p WHERE p.identity_key=? AND EXISTS(SELECT 1 FROM catalog_credits c INDEXED BY catalog_credits_person WHERE c.person_id=p.id AND c.entity_id=?)`, local, entity).Scan(&from, &token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Root17 captured identity is not a mutable name alias. Once accepted
	// evidence names this exact key/token, a later same-name provider observation
	// must remain a distinct identity rather than changing old credit decisions.
	var captured bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credit_source_rows r WHERE r.original_person_identity_key=? AND r.original_person_token=? AND EXISTS(SELECT 1 FROM credit_source_publication_members m JOIN credit_source_publications p ON p.publication_id=m.publication_id WHERE m.source_row_id=r.source_row_id AND p.sealed=1))`, local, token).Scan(&captured); err != nil {
		return err
	}
	if captured {
		return nil
	}
	into := from
	err = tx.QueryRowContext(ctx, `SELECT id FROM catalog_people WHERE identity_key=?`, providerKey).Scan(&into)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		into = from
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_people SET identity_key=?,provider_ids=json_object(?,?),revision=revision+1 WHERE id=?`, providerKey, provider, providerPersonID, from); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		for _, q := range []string{
			`UPDATE catalog_credits SET person_id=?2 WHERE person_id=?1`,
			`UPDATE catalog_people_aliases SET person_id=?2 WHERE person_id=?1`,
			`INSERT OR REPLACE INTO catalog_people_aliases(alias,person_id) VALUES('token:'||?3,?2)`,
			`DELETE FROM catalog_people WHERE id=?1`,
			`UPDATE catalog_people SET revision=revision+1 WHERE id=?2`,
		} {
			if _, err = tx.ExecContext(ctx, q, from, into, token); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO catalog_people_aliases(alias,person_id) VALUES(?,?)`, local, into)
	return err
}

func creditLabel(ctx context.Context, tx *sql.Tx, label string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_credit_labels WHERE label=?`, label).Scan(&id)
	if !errors.Is(err, sql.ErrNoRows) {
		return id, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO catalog_credit_labels(label) VALUES(?) RETURNING id`, label).Scan(&id)
	return id, err
}

// SetAttributesTx replaces an item's values of one attribute field (content
// rating, label, studio, tag, …). A new field is a new catalog_attribute_fields row.
func SetAttributesTx(ctx context.Context, tx *sql.Tx, item int64, field string, values []string) error {
	var fieldID int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO catalog_attribute_fields(field) VALUES(?) ON CONFLICT(field) DO UPDATE SET field=field RETURNING id`, field).Scan(&fieldID); err != nil {
		return err
	}
	// Driven by the item's own edges: a field's terms are server-wide (a studio
	// or tag field can hold hundreds of thousands).
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_item_attribute_edges WHERE item_id=?1 AND (SELECT t.field_id FROM catalog_attribute_terms t WHERE t.id=term_id)=?2`, item, fieldID); err != nil {
		return err
	}
	for i, v := range values {
		key := strings.ToLower(strings.TrimSpace(v))
		if key == "" {
			continue
		}
		var term int64
		if err := tx.QueryRowContext(ctx, `INSERT INTO catalog_attribute_terms(field_id,value_key) VALUES(?,?) ON CONFLICT(field_id,value_key) DO UPDATE SET value_key=value_key RETURNING id`, fieldID, key).Scan(&term); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) VALUES(?,?,?,?)`, item, term, v, i); err != nil {
			return err
		}
	}
	return touchOwnTx(ctx, tx, item)
}

// SetCollectionMemberTx adds (present) or removes an item of a collection.
func SetCollectionMemberTx(ctx context.Context, tx *sql.Tx, collection, item int64, orderKey string, present bool) error {
	var res sql.Result
	var err error
	if present {
		res, err = tx.ExecContext(ctx, `INSERT INTO catalog_collection_members(collection_id,item_id,order_key) VALUES(?,?,?) ON CONFLICT(collection_id,item_id) DO UPDATE SET order_key=excluded.order_key WHERE catalog_collection_members.order_key IS NOT excluded.order_key`, collection, item, orderKey)
	} else {
		res, err = tx.ExecContext(ctx, `DELETE FROM catalog_collection_members WHERE collection_id=? AND item_id=?`, collection, item)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	// member_count follows the member rows by trigger (feeds.sql), cascades
	// included.
	if err = TouchTx(ctx, tx, DomainBrowseEdges, item); err != nil {
		return err
	}
	if err = TouchTx(ctx, tx, DomainSearch, item, collection); err != nil {
		return err
	}
	return TouchTx(ctx, tx, DomainBrowseRows, collection)
}

// SetSongArtistsTx replaces a song's credited artists.
func SetSongArtistsTx(ctx context.Context, tx *sql.Tx, song int64, artists []int64) error {
	if err := RequireEntityAcquirableTx(ctx, tx, song); err != nil {
		return err
	}
	for _, a := range artists {
		if err := RequireEntityAcquirableTx(ctx, tx, a); err != nil {
			return err
		}
	}
	// NOT EXISTS, not NOT IN: a nil list encodes as JSON null, whose one NULL
	// row made NOT IN false for every artist, so clearing kept them all.
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_song_artists WHERE song_id=? AND NOT EXISTS(SELECT 1 FROM json_each(?) j WHERE j.value=artist_id)`, song, mustJSON(artists)); err != nil {
		return err
	}
	for _, a := range artists {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_song_artists(song_id,artist_id) VALUES(?,?)`, song, a); err != nil {
			return err
		}
	}
	if err := TouchTx(ctx, tx, DomainSearch, song); err != nil {
		return err
	}
	return TouchTx(ctx, tx, DomainBrowseEdges, song)
}

// Chapter is one chapter of an audiobook file.
type Chapter struct {
	Title      string
	Start, End float64
}

// SetBookChaptersTx replaces an audiobook file's chapters.
func SetBookChaptersTx(ctx context.Context, tx *sql.Tx, file int64, chapters []Chapter) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_book_chapters WHERE file_id=?`, file); err != nil {
		return err
	}
	for i, c := range chapters {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_book_chapters(file_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,?,?,?)`, file, i, c.Title, c.Start, c.End); err != nil {
			return err
		}
	}
	return nil
}

package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// One read of every editable field for a target. The registry decides which
// fields are read; values come from the backbone, the kind's side table and
// the item details, mirroring where the write API routes each field, so a
// field added to the registry is published, edited and locked without another
// bespoke query.
func readRepairEntity(ctx context.Context, tx *sql.Tx, t RepairTarget) (repairEntity, error) {
	return readRepairTarget(ctx, tx, t, true)
}

// validateArtworkTarget shares the editor's admission checks without projecting
// unused editable fields. Reading still needs the full read: its owner/automatic
// facts and source proofs can reject a target, independently of its identity.
func validateArtworkTarget(ctx context.Context, tx *sql.Tx, t RepairTarget) error {
	_, err := readArtworkTarget(ctx, tx, t)
	return err
}

// readArtworkTarget is the same admission for artwork's own callers, which need
// only the target's library and kind. A container's revision stamp walks every
// descendant file, so it is left to the editor paths that compare it.
func readArtworkTarget(ctx context.Context, tx *sql.Tx, t RepairTarget) (repairEntity, error) {
	return readRepairTarget(ctx, tx, t, false)
}

func readRepairTarget(ctx context.Context, tx *sql.Tx, t RepairTarget, editable bool) (repairEntity, error) {
	e := repairEntity{fields: map[string]string{}}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return e, err
	}
	var kindName string
	var kind, year int
	var title, sortTitle, language string
	err = tx.QueryRowContext(ctx, `SELECT e.kind,cl.library_id,k.name,e.title,e.sort_title,e.metadata_language,e.year FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?`, entity).Scan(&kind, &e.library, &kindName, &title, &sortTitle, &language, &year)
	if err != nil {
		return e, err
	}
	// The target kind names the editor's field set; a public id of another
	// catalogue kind reads as not found, as the old per-table reads did.
	switch t.Kind {
	case "item", "show", "season", "album", "artist", "book":
	default:
		return e, ErrRepairInput
	}
	if compactcatalog.OwnerNamespace(compactcatalog.Kind(kind)) != t.Kind {
		return e, sql.ErrNoRows
	}
	e.itemKind = kindName
	if t.Kind == "item" && !readingRepairKind(kindName) {
		var stamp string
		err = tx.QueryRowContext(ctx, `SELECT h.incarnation||':'||h.source_revision||':'||h.identity_revision||':'||h.metadata_revision FROM metadata_publication_heads h WHERE h.item_id=?`, entity).Scan(&stamp)
		if err != nil {
			return e, err
		}
		e.stamp = stamp
	}
	e.schema = repairSchema(t.Kind, e.itemKind)
	if len(e.schema) == 0 {
		return e, ErrRepairInput
	}
	if !editable && !readingRepairKind(kindName) {
		// The container stamp only fences editor revisions; it never rejects a
		// target that resolved above.
		return e, nil
	}
	backbone := map[string]string{"title": title, "sort_title": sortTitle, "metadata_language": language, "year": strconv.Itoa(year)}
	var dOverview, dOriginal, dEdition, dTagline, dRelease, dRating, dStudio, dNetwork, dCountry, dTags, dLabels string
	err = tx.QueryRowContext(ctx, `SELECT overview,original_title,edition,tagline,release_date,content_rating,studio,network,country,tags,labels FROM catalog_item_details WHERE entity_id=?`, entity).Scan(&dOverview, &dOriginal, &dEdition, &dTagline, &dRelease, &dRating, &dStudio, &dNetwork, &dCountry, &dTags, &dLabels)
	details := map[string]string{"overview": dOverview, "original_title": dOriginal, "edition": dEdition, "tagline": dTagline, "release_date": dRelease, "content_rating": dRating, "studio": dStudio, "network": dNetwork, "country": dCountry, "tags": dTags, "labels": dLabels}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return e, err
	}
	side := map[string]string{}
	var airDate sql.NullInt64
	switch t.Kind {
	case "show":
		var tagline, network, country, contentRating, studio, originalTitle, overview, tags, labels string
		if err = tx.QueryRowContext(ctx, `SELECT original_title,tagline,overview,content_rating,studio,network,country,tags,labels FROM catalog_shows WHERE entity_id=?`, entity).Scan(&originalTitle, &tagline, &overview, &contentRating, &studio, &network, &country, &tags, &labels); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return e, err
		}
		side = map[string]string{"original_title": originalTitle, "tagline": tagline, "overview": overview, "content_rating": contentRating, "studio": studio, "network": network, "country": country, "tags": tags, "labels": labels}
	case "season":
		var number sql.NullInt64
		var title, overview string
		if err = tx.QueryRowContext(ctx, `SELECT number,title,overview FROM catalog_seasons WHERE entity_id=?`, entity).Scan(&number, &title, &overview); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return e, err
		}
		if number.Valid {
			side["number"] = strconv.FormatInt(number.Int64, 10)
		}
		side["title"], side["overview"] = title, overview
	case "artist":
		var overview, tags string
		if err = tx.QueryRowContext(ctx, `SELECT overview,tags FROM catalog_artists WHERE entity_id=?`, entity).Scan(&overview, &tags); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return e, err
		}
		side = map[string]string{"overview": overview, "tags": tags}
	case "album":
		var overview, label, tags, releaseDate string
		if err = tx.QueryRowContext(ctx, `SELECT overview,label,tags,release_date FROM catalog_albums WHERE entity_id=?`, entity).Scan(&overview, &label, &tags, &releaseDate); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return e, err
		}
		side = map[string]string{"overview": overview, "label": label, "tags": tags, "release_date": releaseDate}
	case "book":
		var author, narrator, overview, series, seriesPosition, tags, labels string
		if err = tx.QueryRowContext(ctx, `SELECT author,narrator,overview,series,series_position,tags,labels FROM catalog_books WHERE entity_id=?`, entity).Scan(&author, &narrator, &overview, &series, &seriesPosition, &tags, &labels); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return e, err
		}
		side = map[string]string{"author": author, "narrator": narrator, "overview": overview, "series": series, "series_position": seriesPosition, "tags": tags, "labels": labels}
	case "item":
		switch kindName {
		case "episode":
			var number sql.NullInt64
			if err = tx.QueryRowContext(ctx, `SELECT number,air_date FROM catalog_episodes WHERE entity_id=?`, entity).Scan(&number, &airDate); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return e, err
			}
			if number.Valid {
				side["number"] = strconv.FormatInt(number.Int64, 10)
			}
		case "song":
			var track, disc sql.NullInt64
			if err = tx.QueryRowContext(ctx, `SELECT track_number,disc_number FROM catalog_songs WHERE entity_id=?`, entity).Scan(&track, &disc); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return e, err
			}
			if track.Valid {
				side["track_number"] = strconv.FormatInt(track.Int64, 10)
			}
			if disc.Valid {
				side["disc_number"] = strconv.FormatInt(disc.Int64, 10)
			}
		case "audiobook_file":
			var part sql.NullInt64
			if err = tx.QueryRowContext(ctx, `SELECT part_number FROM catalog_book_files WHERE entity_id=?`, entity).Scan(&part); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return e, err
			}
			if part.Valid {
				side["part_number"] = strconv.FormatInt(part.Int64, 10)
			}
		}
	}
	for _, spec := range e.schema {
		e.fields[spec.Field] = repairFieldValue(t.Kind, kindName, spec.Field, backbone, details, side, airDate)
	}
	if readingRepairKind(kindName) {
		var retired bool
		if err = tx.QueryRowContext(ctx, `SELECT retired FROM catalog_entities WHERE id=?`, entity).Scan(&retired); err != nil {
			return e, err
		}
		if retired {
			return e, sql.ErrNoRows
		}
		values, automatic, err := readReadingOwnerFields(ctx, tx, entity)
		if err != nil {
			return e, err
		}
		for field, value := range values {
			if _, ok := e.fields[field]; ok {
				e.fields[field] = value
			}
		}
		e.readingAutomatic = automatic
	}
	if _, ok := e.fields["seasonNumber"]; ok {
		var number sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT n.number FROM catalog_episodes e NOT INDEXED CROSS JOIN catalog_seasons n ON n.entity_id=e.season_id WHERE e.entity_id=?`, entity).Scan(&number); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return e, err
		}
		if number.Valid {
			e.fields["seasonNumber"] = strconv.FormatInt(number.Int64, 10)
		}
	}
	for _, spec := range e.schema {
		if spec.Type == "list" {
			e.fields[spec.Field] = encodeList(decodeList(e.fields[spec.Field]))
		}
		if spec.Type == "integer" && e.fields[spec.Field] == "0" && spec.Field != "year" {
			// A cleared number reads as absent, never as the number zero, except
			// where zero is a real ordinal (specials are season zero).
			if spec.Field != "number" && spec.Field != "seasonNumber" {
				e.fields[spec.Field] = ""
			}
		}
	}
	err = readRepairTargetStamp(ctx, tx, t, entity, &e)
	return e, err
}

// Keep the existing container/Reading source and publication-head read even
// when editable fields are omitted. Non-Reading item heads are required above.
func readRepairTargetStamp(ctx context.Context, tx *sql.Tx, t RepairTarget, entity int64, e *repairEntity) error {
	if t.Kind != "item" || readingRepairKind(e.itemKind) {
		// A bulk job captures independent target fences before it applies any
		// edits. A library-wide revision would stale every remaining sibling
		// after the first edit. Fence the target's source files and publication
		// heads instead; repairRevision also includes its full snapshot/history.
		err := tx.QueryRowContext(ctx, `SELECT json_array(e.parent_id,e.retired,`+attentionFileFingerprint("e.id")+`,
 (SELECT h.incarnation||':'||h.source_revision||':'||h.hierarchy_revision||':'||h.selection_revision||':'||h.descriptive_revision FROM tvdb_publication_heads h WHERE h.show_id=e.id),
 (SELECT h.incarnation||':'||h.revision||':'||h.membership_revision FROM mb_album_heads h WHERE h.album_id=e.id)) FROM catalog_entities e WHERE e.id=?`, entity).Scan(&e.stamp)
		if err != nil {
			return err
		}
	}
	return nil
}

// repairFieldValue reads one registry field from the layered catalogue facts,
// mirroring the write API's routing (kind side table, else details, else
// backbone).
func repairFieldValue(kind, itemKind, field string, backbone, details, side map[string]string, airDate sql.NullInt64) string {
	switch field {
	case "title":
		if kind == "season" {
			return side["title"]
		}
		return backbone["title"]
	case "sortTitle":
		return backbone["sort_title"]
	case "metadataLanguage":
		return backbone["metadata_language"]
	case "year":
		return backbone["year"]
	case "description":
		if v, ok := side["overview"]; ok {
			return v
		}
		return details["overview"]
	case "originalTitle":
		if v, ok := side["original_title"]; ok {
			return v
		}
		return details["original_title"]
	case "edition":
		return details["edition"]
	case "tagline":
		if v, ok := side["tagline"]; ok {
			return v
		}
		return details["tagline"]
	case "releaseDate":
		if itemKind == "episode" {
			if airDate.Valid {
				return time.Unix(airDate.Int64*86400, 0).UTC().Format("2006-01-02")
			}
			return ""
		}
		if v, ok := side["release_date"]; ok {
			return v
		}
		return details["release_date"]
	case "contentRating":
		if v, ok := side["content_rating"]; ok {
			return v
		}
		return details["content_rating"]
	case "studio":
		if v, ok := side["studio"]; ok {
			return v
		}
		return details["studio"]
	case "network":
		if v, ok := side["network"]; ok {
			return v
		}
		return details["network"]
	case "country":
		if v, ok := side["country"]; ok {
			return v
		}
		return details["country"]
	case "label":
		return side["label"]
	case "author":
		return side["author"]
	case "narrator":
		return side["narrator"]
	case "series":
		return side["series"]
	case "seriesIndex":
		return side["series_position"]
	case "number":
		return side["number"]
	case "episodeNumber":
		return side["number"]
	case "trackNumber":
		return side["track_number"]
	case "discNumber":
		return side["disc_number"]
	case "partNumber":
		return side["part_number"]
	case "tags":
		if v, ok := side["tags"]; ok {
			return v
		}
		return details["tags"]
	case "labels":
		if v, ok := side["labels"]; ok {
			return v
		}
		return details["labels"]
	}
	return ""
}

// automaticFieldSource names where a value that is not an owner decision came
// from, so a client can say "from TMDB" rather than "automatic".
func automaticFieldSource(ctx context.Context, tx *sql.Tx, t RepairTarget, ent repairEntity) (string, error) {
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return "", err
	}
	var provider string
	switch t.Kind {
	case "item":
		err = tx.QueryRowContext(ctx, `SELECT provider FROM screen_metadata_work WHERE target_kind='item' AND target_id=? AND provider<>'' UNION ALL SELECT provider FROM provider_evidence WHERE item_id=? UNION ALL SELECT 'musicbrainz' FROM mb_jobs WHERE kind='song' AND entity_id=? AND selected_id<>'' LIMIT 1`, entity, entity, entity).Scan(&provider)
	case "show":
		err = tx.QueryRowContext(ctx, `SELECT provider FROM screen_metadata_work WHERE target_kind='show' AND target_id=? AND provider<>'' UNION ALL SELECT 'tvdb' FROM tvdb_jobs WHERE show_id=? AND provider_id>0 LIMIT 1`, entity, entity).Scan(&provider)
	case "album":
		err = tx.QueryRowContext(ctx, `SELECT 'musicbrainz' FROM mb_jobs WHERE kind='album' AND entity_id=? AND selected_id<>''`, entity).Scan(&provider)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return "", err
	}
	if provider == "" {
		return "scanner", nil
	}
	return "provider:" + provider, nil
}

// repairOwnerField maps a registry field to the catalogue field name the
// write API routes to its table (backbone, kind side table or details).
// seasonNumber moves the episode and is handled separately.
func repairOwnerField(kind, itemKind, field string) (string, bool) {
	k, _ := compactcatalog.ParseKind(itemKind)
	return compactcatalog.OwnerFieldColumn(k, field)
}

// applyRepairField writes one registry field. The owner row is authoritative;
// the catalogue projection goes through the write API as an owner write, and
// owner locks are enforced there for later scanner and provider writes.
func applyRepairField(ctx context.Context, tx *sql.Tx, t RepairTarget, field string, spec RepairFieldSpec, current RepairField, edit RepairFieldEdit, actor, now string) error {
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	reading := t.Kind == "item" && readingRepairKind(repairItemKind(ctx, tx, entity)) && readingAttributionField(field)
	if reading {
		var err error
		edit, err = readingAttributionEdit(field, current, edit)
		if err != nil {
			return err
		}
	}
	value := current.Value
	locked := current.Locked
	if edit.Automatic {
		value = current.Automatic
		locked = false
	} else if edit.Values != nil {
		normalized, ok := normalizeList(*edit.Values)
		if !ok || spec.Type != "list" {
			return ErrRepairInput
		}
		value = encodeList(normalized)
		locked = true
	} else if edit.Value != nil {
		value = *edit.Value
		if spec.Type == "list" {
			// A malformed or over-long entry is refused, never silently dropped.
			parsed := []string{}
			if strings.TrimSpace(value) != "" && json.Unmarshal([]byte(value), &parsed) != nil {
				return ErrRepairInput
			}
			normalized, ok := normalizeList(parsed)
			if !ok {
				return ErrRepairInput
			}
			value = encodeList(normalized)
		}
		locked = true
	}
	if edit.Locked != nil {
		locked = *edit.Locked
	}
	if !validFieldValue(t.Kind, spec, value) {
		return ErrRepairInput
	}
	// The owner row holds exactly the text the catalogue projects, so later
	// automatic writes record their value as automatic and keep the owner's.
	automatic := current.Automatic
	if reading {
		if err := storeReadingRepairFieldTx(ctx, tx, entity, field, value, automatic, locked, actor, now); err != nil {
			return err
		}
	} else if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_owner_fields VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(kind,entity_id,field) DO UPDATE SET value=excluded.value,automatic_value=excluded.automatic_value,locked=excluded.locked,actor=excluded.actor,observed_at=excluded.observed_at`, t.Kind, entity, field, value, automatic, locked, actor, now); err != nil {
		return err
	}
	if reading {
		return readingAttributionChanged(ctx, tx, entity)
	}
	if field == "seasonNumber" {
		return applySeasonNumber(ctx, tx, t, entity, value)
	}
	catalogField, ok := repairOwnerField(t.Kind, repairItemKind(ctx, tx, entity), field)
	if !ok {
		return ErrRepairInput
	}
	var stored any = value
	switch {
	case catalogField == "year" || catalogField == "number" || catalogField == "track_number" || catalogField == "disc_number" || catalogField == "part_number":
		switch {
		case value != "":
			n, err := strconv.Atoi(value)
			if err != nil {
				return ErrRepairInput
			}
			stored = n
		case field == "episodeNumber":
			// An episode's number is part of its position in the show, not an
			// optional descriptive fact, so it cannot be cleared.
			return ErrRepairInput
		case catalogField == "year" || catalogField == "number":
			stored = 0
		default:
			stored = nil
		}
	case catalogField == "air_date":
		if value == "" {
			stored = nil
		} else {
			day, err := time.Parse("2006-01-02", value)
			if err != nil {
				return ErrRepairInput
			}
			stored = day.Unix() / 86400
		}
	}
	return compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Owner, map[string]any{catalogField: stored})
}

// repairItemKind names the catalogue kind of an entity for field routing.
// Unknown rows read as empty and fail routing downstream.
func repairItemKind(ctx context.Context, tx *sql.Tx, entity int64) string {
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT k.name FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=?`, entity).Scan(&name); err != nil {
		return ""
	}
	return name
}

// applySeasonNumber moves a seasonally numbered episode to the season carrying
// that number, creating the season when the show does not have one yet. An
// absolutely numbered episode has no season to move to.
func applySeasonNumber(ctx context.Context, tx *sql.Tx, t RepairTarget, entity int64, value string) error {
	var show int64
	var numbering string
	if err := tx.QueryRowContext(ctx, `SELECT show_id,numbering FROM catalog_episodes WHERE entity_id=?`, entity).Scan(&show, &numbering); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrRepairInput
		}
		return err
	}
	if numbering != "seasonal" || value == "" {
		return ErrRepairInput
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 || number > 99 {
		return ErrRepairInput
	}
	var season int64
	err = tx.QueryRowContext(ctx, `SELECT entity_id FROM catalog_seasons WHERE show_id=? AND number=?`, show, number).Scan(&season)
	if errors.Is(err, sql.ErrNoRows) {
		var library, localKey string
		if err = tx.QueryRowContext(ctx, `SELECT cl.library_id,sh.local_key FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id JOIN catalog_shows sh ON sh.entity_id=e.id WHERE e.id=?`, show).Scan(&library, &localKey); err != nil {
			return err
		}
		handle, err := compactcatalog.LibraryHandle(ctx, tx, library)
		if err != nil {
			return err
		}
		title := "Season " + value
		if number == 0 {
			title = "Specials"
		}
		season, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: compactcatalog.Season, Parent: show, Key: compactcatalog.SeasonKey(localKey, number), Title: title})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFieldsTx(ctx, tx, season, compactcatalog.Automatic, map[string]any{"show_id": show, "number": number}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	// The episode's season link is an owner edit; the write API queues the
	// episode's derived work with it.
	return compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Owner, map[string]any{"season_id": season})
}

// inheritedChildren selects, as catalog_dirty rows, the items whose browse
// attributes inherit from a show, season, album, artist or book.
var inheritedChildren = map[string]string{
	"show":   `SELECT ?1,entity_id,1 FROM catalog_episodes WHERE show_id=?2`,
	"season": `SELECT ?1,entity_id,1 FROM catalog_episodes WHERE season_id=?2`,
	"album":  `SELECT ?1,entity_id,1 FROM catalog_songs INDEXED BY catalog_songs_album_order WHERE album_id=?2`,
	"artist": `SELECT ?1,s.entity_id,1 FROM catalog_albums a INDEXED BY catalog_albums_artist CROSS JOIN catalog_songs s INDEXED BY catalog_songs_album_order ON s.album_id=a.entity_id WHERE a.artist_id=?2`,
	"book":   `SELECT ?1,entity_id,1 FROM catalog_book_files INDEXED BY catalog_book_files_order WHERE book_id=?2`,
}

// catalogAttributeFields are the browse fields the editor owns. Fields the
// analysis pipeline owns (audioLanguage, dynamicRange) are left alone.
var catalogAttributeFields = []string{"contentRating", "studio", "network", "releaseDate", "tag", "label", "series"}

// syncCatalogAttributes republishes the browse projection for the items a change
// touched. Without it the browse engine's contentRating/studio/network/tag/
// label/series filters match nothing. An item is republished in the caller's
// transaction; a parent's children — every one of them, however many — are
// queued in one statement and republished by the worker in batches, so a large
// show or artist is neither cut off nor one long write.
func syncCatalogAttributes(ctx context.Context, tx *sql.Tx, t RepairTarget) error {
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if t.Kind == "item" {
		return syncItemAttributes(ctx, tx, entity)
	}
	children, ok := inheritedChildren[t.Kind]
	if !ok {
		return ErrRepairInput
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_dirty(domain,entity_id,revision) `+children+` ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`, compactcatalog.DomainInheritedAttributes, entity)
	return err
}

func init() {
	compactcatalog.Register(compactcatalog.DomainInheritedAttributes, compactcatalog.Derivation{
		Version: 1,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []compactcatalog.Key, limit int) ([]int64, error) {
			done := make([]int64, 0, len(keys))
			for _, key := range keys {
				if err := syncItemAttributes(ctx, tx, key.ID); err != nil {
					return done, err
				}
				done = append(done, key.ID)
			}
			return done, nil
		},
		// Nothing to rebuild: the attributes are republished whenever a parent
		// changes, and items republish their own on every write.
		Backfill: func(context.Context, *sql.Tx, int64, int) ([]int64, error) { return nil, nil },
	})
}

func syncItemAttributes(ctx context.Context, tx *sql.Tx, item int64) error {
	var rating, studio, network, release, tags, labels string
	var showRating, showStudio, showNetwork, showTags, showLabels sql.NullString
	var albumLabel, bookSeries sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(d.content_rating,''),COALESCE(d.studio,''),COALESCE(d.network,''),COALESCE(d.release_date,''),COALESCE(d.tags,''),COALESCE(d.labels,''),
  sh.content_rating,sh.studio,sh.network,sh.tags,sh.labels,al.label,bk.series
  FROM catalog_entities e
  LEFT JOIN catalog_item_details d ON d.entity_id=e.id
  LEFT JOIN catalog_episodes ep ON ep.entity_id=e.id LEFT JOIN catalog_shows sh ON sh.entity_id=ep.show_id
  LEFT JOIN catalog_songs sg ON sg.entity_id=e.id LEFT JOIN catalog_albums al ON al.entity_id=sg.album_id
  LEFT JOIN catalog_book_files bf ON bf.entity_id=e.id LEFT JOIN catalog_books bk ON bk.entity_id=bf.book_id
  WHERE e.id=?`, item).Scan(&rating, &studio, &network, &release, &tags, &labels, &showRating, &showStudio, &showNetwork, &showTags, &showLabels, &albumLabel, &bookSeries)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	inherit := func(own string, parent sql.NullString) string {
		if strings.TrimSpace(own) != "" {
			return own
		}
		if parent.Valid {
			return parent.String
		}
		return ""
	}
	values := map[string][]string{
		"contentRating": {inherit(rating, showRating)},
		"studio":        {inherit(studio, showStudio)},
		"network":       {inherit(network, showNetwork)},
		"releaseDate":   {release},
		"series":        {bookSeries.String},
		"tag":           decodeList(tags),
		"label":         decodeList(labels),
	}
	if len(values["tag"]) == 0 && showTags.Valid {
		values["tag"] = decodeList(showTags.String)
	}
	if albumLabel.Valid && strings.TrimSpace(albumLabel.String) != "" {
		values["label"] = append(values["label"], albumLabel.String)
	} else if len(values["label"]) == 0 && showLabels.Valid {
		values["label"] = decodeList(showLabels.String)
	}
	for _, field := range catalogAttributeFields {
		present := []string{}
		for _, v := range values[field] {
			v = strings.TrimSpace(v)
			if v == "" || len(v) > 512 {
				continue
			}
			present = append(present, v)
		}
		if err = compactcatalog.SetAttributesTx(ctx, tx, item, field, present); err != nil {
			return err
		}
	}
	return nil
}

// PublishItemFields is the provider and scanner entry point for the descriptive
// facts the editor also edits. Locked owner decisions survive through the
// write API, and the browse attributes are refreshed in the same transaction.
func publishItemFields(ctx context.Context, tx *sql.Tx, item string, values map[string]string) error {
	entity, err := resolveEntity(ctx, tx, item)
	if err != nil {
		return err
	}
	columns := map[string]string{
		"contentRating": "content_rating", "studio": "studio", "network": "network",
		"releaseDate": "release_date", "originalTitle": "original_title", "tagline": "tagline",
		"country": "country", "edition": "edition", "sortTitle": "sort_title",
	}
	fields := map[string]any{}
	for field, value := range values {
		column, ok := columns[field]
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		spec := RepairFieldSpec{Field: field, Type: "text", MaxLength: textLimit}
		if field == "releaseDate" {
			spec.Type = "date"
		}
		if !validFieldValue("item", spec, value) {
			continue
		}
		fields[column] = value
	}
	if err := compactcatalog.SetFieldsTx(ctx, tx, entity, compactcatalog.Automatic, fields); err != nil {
		return err
	}
	return syncItemAttributes(ctx, tx, entity)
}

// candidatePreviews resolves the proxied previews an entity already has, in one
// read. A raw provider URL is never published: the client only ever sees a path
// this server serves from its own artwork store, and only when bytes are ready.
// The map is keyed by provider and provider image id.
func candidatePreviews(ctx context.Context, tx *sql.Tx, t RepairTarget) (map[string]string, error) {
	out := map[string]string{}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.role,c.subject,c.provider,c.image_id,p.digest FROM artwork_candidates c JOIN artwork_previews p ON p.candidate_id=c.id AND p.source_fence=c.source_fence WHERE c.kind=? AND c.entity_id=? ORDER BY c.role,c.id LIMIT 200`, t.Kind, entity)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, role, subject, provider, image, digest string
		if err = rows.Scan(&id, &role, &subject, &provider, &image, &digest); err != nil {
			return out, err
		}
		key := provider + "\x00" + image
		if _, seen := out[key]; !seen {
			out[key] = artworkPath(t, role, subject, digest) + "&candidate=" + id
		}
	}
	return out, rows.Err()
}

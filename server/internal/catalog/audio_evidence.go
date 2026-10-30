package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Called only inside the scanner's exact source/configuration transaction.
// Equality checks avoid invalidating MusicBrainz claims on an unchanged scan.
func persistAudioEvidence(tx *sql.Tx, library, asset string, f assets.Facts) error {
	if f.InventoryOnly {
		return nil
	}
	if e := persistMusicFacts(tx, asset, f); e != nil {
		return e
	}
	evidence := append([]assets.AudioTagEvidence{}, f.AudioEvidence...)
	if len(evidence) == 0 {
		for k, v := range f.Tags {
			source := f.TagSources[k]
			if source == "" {
				source = "embedded"
			}
			evidence = append(evidence, assets.AudioTagEvidence{Field: k, Value: v, Source: source})
		}
	}
	valid := evidence[:0]
	for _, v := range evidence {
		if assets.ValidAudioTag(v.Field, v.Value) {
			valid = append(valid, v)
		}
	}
	evidence = valid
	sort.SliceStable(evidence, func(i, j int) bool {
		a, b := evidence[i], evidence[j]
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Value < b.Value
	})
	if len(evidence) > 512 {
		return errors.New("local audio evidence budget")
	}
	var size, modified int64
	if e := tx.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE token=?`, asset).Scan(&size, &modified); e != nil {
		return e
	}
	if f.ObservedRevision == "" {
		f.ObservedRevision = fmt.Sprintf("local:%d:%d", size, modified)
	}
	// Retain all independent local evidence privately; selected fields stay in the
	// existing audio_tag_evidence projection consumed by MB's revision triggers.
	for n, v := range evidence {
		if !assets.ValidAudioTag(v.Field, v.Value) {
			continue
		}
		if _, e := tx.Exec(`INSERT INTO audio_local_evidence VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(library_id,asset_id,ordinal) DO UPDATE SET field=excluded.field,source=excluded.source,value=excluded.value,source_revision=excluded.source_revision,size=excluded.size,modified_ns=excluded.modified_ns,observed_at=excluded.observed_at WHERE field IS NOT excluded.field OR source IS NOT excluded.source OR value IS NOT excluded.value OR source_revision IS NOT excluded.source_revision OR size IS NOT excluded.size OR modified_ns IS NOT excluded.modified_ns`, library, asset, n, v.Field, v.Source, v.Value, f.ObservedRevision, size, modified, time.Now().UTC().Format(time.RFC3339Nano)); e != nil {
			return e
		}
	}
	if _, e := tx.Exec(`DELETE FROM audio_local_evidence WHERE library_id=? AND asset_id=? AND ordinal>=?`, library, asset, len(evidence)); e != nil {
		return e
	}
	fields := make([]string, 0, len(f.Tags))
	for k := range f.Tags {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	for _, k := range fields {
		v := f.Tags[k]
		if !assets.ValidAudioTag(k, v) {
			continue
		}
		source := f.TagSources[k]
		if source == "" {
			source = "embedded"
		}
		if _, e := tx.Exec(`INSERT INTO audio_tag_evidence VALUES(?,?,?,?,?) ON CONFLICT(library_id,asset_id,field) DO UPDATE SET source=excluded.source,value=excluded.value WHERE source IS NOT excluded.source OR value IS NOT excluded.value`, library, asset, k, source, v); e != nil {
			return e
		}
	}
	rows, e := tx.Query(`SELECT field FROM audio_tag_evidence WHERE library_id=? AND asset_id=?`, library, asset)
	if e != nil {
		return e
	}
	removed := []string{}
	for rows.Next() {
		var field string
		if e = rows.Scan(&field); e != nil {
			rows.Close()
			return e
		}
		if _, ok := f.Tags[field]; !ok {
			removed = append(removed, field)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, field := range removed {
		if _, e = tx.Exec(`DELETE FROM audio_tag_evidence WHERE library_id=? AND asset_id=? AND field=?`, library, asset, field); e != nil {
			return e
		}
	}
	_, e = tx.Exec(`INSERT INTO audio_source_metadata VALUES(?,?,?) ON CONFLICT(library_id,asset_id) DO UPDATE SET issue=excluded.issue WHERE issue IS NOT excluded.issue`, library, asset, f.LocalMetadataIssue)
	return e
}

type AudioIdentifier struct {
	Scheme string `json:"scheme"`
	Value  string `json:"value"`
}
type AudioSeries struct {
	Name     string `json:"name"`
	Position string `json:"position,omitempty"`
}
type LocalBookMetadata struct {
	Description   string            `json:"description,omitempty"`
	PublishedYear *int              `json:"publishedYear,omitempty"`
	Authors       []string          `json:"authors"`
	Narrators     []string          `json:"narrators"`
	Series        []AudioSeries     `json:"series"`
	Identifiers   []AudioIdentifier `json:"identifiers"`
	Edition       string            `json:"edition,omitempty"`
	Publisher     string            `json:"publisher,omitempty"`
	Language      string            `json:"language,omitempty"`
	Sources       map[string]string `json:"sources"`
}

func audioNames(tags map[string]string, multi, single string) []string {
	var out []string
	if json.Unmarshal([]byte(tags[multi]), &out) != nil || len(out) > 128 {
		out = nil
	}
	if len(out) == 0 && tags[single] != "" {
		out = []string{tags[single]}
	}
	valid := []string{}
	for _, v := range out {
		v = assets.CleanAudioTag(single, v)
		if assets.ValidAudioTag(single, v) {
			valid = append(valid, v)
		}
	}
	return valid
}
func bookLocalMetadata(tags, sources map[string]string) LocalBookMetadata {
	out := LocalBookMetadata{Authors: audioNames(tags, "authors", "author"), Narrators: audioNames(tags, "narrators", "narrator"), Edition: tags["edition"], Publisher: tags["publisher"], Language: tags["language"], Sources: map[string]string{}, Series: []AudioSeries{}, Identifiers: []AudioIdentifier{}}
	out.Description = tags["description"]
	date := firstTag(tags, "date", "year")
	if len(date) >= 4 {
		if year, err := strconv.Atoi(date[:4]); err == nil && year > 0 && year <= 9999 {
			out.PublishedYear = &year
		}
	}
	if len(out.Narrators) == 0 {
		out.Narrators = audioNames(tags, "composers", "composer")
	}
	if json.Unmarshal([]byte(tags["series_list"]), &out.Series) != nil || out.Series == nil || len(out.Series) > 64 {
		out.Series = []AudioSeries{}
	}
	if len(out.Series) == 0 && tags["series"] != "" {
		out.Series = append(out.Series, AudioSeries{tags["series"], tags["series_position"]})
	}
	if json.Unmarshal([]byte(tags["identifiers"]), &out.Identifiers) != nil || out.Identifiers == nil || len(out.Identifiers) > 64 {
		out.Identifiers = []AudioIdentifier{}
	}
	series := out.Series[:0]
	for _, v := range out.Series {
		if assets.ValidAudioTag("series", v.Name) && len(v.Position) <= 128 && (v.Position == "" || assets.ValidAudioTag("series_position", v.Position)) {
			series = append(series, v)
		}
	}
	out.Series = series
	ids := out.Identifiers[:0]
	for _, v := range out.Identifiers {
		if len(v.Scheme) <= 64 && len(v.Value) <= 1024 && assets.ValidAudioTag("identifiers", v.Scheme) && assets.ValidAudioTag("identifiers", v.Value) {
			ids = append(ids, v)
		}
	}
	out.Identifiers = ids
	for _, key := range []string{"isbn", "asin"} {
		if tags[key] != "" {
			found := false
			for _, v := range out.Identifiers {
				found = found || v.Scheme == key && v.Value == tags[key]
			}
			if !found {
				out.Identifiers = append(out.Identifiers, AudioIdentifier{key, tags[key]})
			}
		}
	}
	if len(out.Authors) == 0 && tags["artist"] != "" {
		out.Authors = audioNames(tags, "artists", "artist")
		source := sources["artist"]
		if source == "" {
			source = "embedded"
		}
		out.Sources["authors"] = source
	}
	for _, key := range []string{"author", "authors", "narrator", "narrators", "series", "series_list", "series_position", "identifiers", "isbn", "asin", "edition", "publisher", "language", "description", "date", "year"} {
		if tags[key] != "" {
			source := sources[key]
			if source == "" {
				source = "embedded"
			}
			out.Sources[key] = source
		}
	}
	return out
}
func saveLocalBookMetadata(tx *sql.Tx, book int64, tags, sources map[string]string) error {
	value := bookLocalMetadata(tags, sources)
	// A chapter/file lacking book-wide tags must not erase evidence supplied by
	// another file. Explicit nonempty replacements are still accepted on rescan.
	var previous string
	e := tx.QueryRow(`SELECT local_metadata_payload FROM catalog_books WHERE entity_id=?`, book).Scan(&previous)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil && previous != "" {
		var old LocalBookMetadata
		if json.Unmarshal([]byte(previous), &old) != nil {
			return errors.New("invalid stored book metadata")
		}
		if len(value.Authors) == 0 {
			value.Authors = old.Authors
		}
		if len(value.Narrators) == 0 {
			value.Narrators = old.Narrators
		}
		if len(value.Series) == 0 {
			value.Series = old.Series
		}
		if len(value.Identifiers) == 0 {
			value.Identifiers = old.Identifiers
		}
		if value.Description == "" {
			value.Description = old.Description
		}
		if value.PublishedYear == nil {
			value.PublishedYear = old.PublishedYear
		}
		if value.Edition == "" {
			value.Edition = old.Edition
		}
		if value.Publisher == "" {
			value.Publisher = old.Publisher
		}
		if value.Language == "" {
			value.Language = old.Language
		}
		for k, v := range old.Sources {
			if _, ok := value.Sources[k]; !ok {
				value.Sources[k] = v
			}
		}
	}
	if value.Authors == nil {
		value.Authors = []string{}
	}
	if value.Narrators == nil {
		value.Narrators = []string{}
	}
	if value.Series == nil {
		value.Series = []AudioSeries{}
	}
	if value.Identifiers == nil {
		value.Identifiers = []AudioIdentifier{}
	}
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	if len(raw) > 65536 {
		return errors.New("book evidence budget")
	}
	return compactcatalog.SetFactsTx(context.Background(), tx, book, map[string]any{"local_metadata_payload": string(raw)})
}

// Local modes affect descriptive projection, never the private evidence or exact
// typed-ID repair inputs. Provider values have a separate accepted projection.
func audioProjection(tx *sql.Tx, library string, item int64, f assets.Facts) (assets.Facts, error) {
	var mode string
	e := tx.QueryRow(`SELECT local_mode FROM audio_metadata_policies WHERE library_id=?`, library).Scan(&mode)
	if errors.Is(e, sql.ErrNoRows) {
		return f, nil
	}
	if e != nil {
		return f, e
	}
	if mode == "off" {
		copy := map[string]string{}
		for k, v := range f.Tags {
			if strings.HasPrefix(k, "musicbrainz_") {
				copy[k] = v
			}
		}
		f.Tags = copy
	}
	if mode != "prefer" && item != 0 {
		var title, artist string
		e = tx.QueryRow(`SELECT r.title,r.artist FROM mb_song_links l JOIN mb_recording_evidence r ON r.revision_id=l.recording_revision AND r.sealed=1 WHERE l.item_id=?`, item).Scan(&title, &artist)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return f, e
		}
		if e == nil {
			copy := map[string]string{}
			for k, v := range f.Tags {
				copy[k] = v
			}
			if title != "" {
				copy["title"] = title
			}
			if artist != "" {
				copy["artist"] = artist
			}
			f.Tags = copy
		}
	}
	return f, nil
}

// A selected provider release and a physical/local edition are independent.
// Do not combine two named editions merely because their titles are identical.
func audioEdition(tags map[string]string) string {
	key, _ := json.Marshal([]string{localKey(tags["edition"]), localKey(tags["musicbrainz_albumid"]), localKey(tags["barcode"]), localKey(tags["catalognumber"])})
	return string(key)
}

const emptyAudioEdition = `["","","",""]`

func localEditionCompatible(tx *sql.Tx, album int64, edition string) (bool, error) {
	var old string
	e := tx.QueryRow(`SELECT edition_key FROM catalog_albums WHERE entity_id=?`, album).Scan(&old)
	if errors.Is(e, sql.ErrNoRows) {
		return true, nil
	}
	return old == edition || edition == emptyAudioEdition, e
}

// Unknown fields may be enriched within one physical album, but two different
// known editions are never reconciled by title alone.
func mergeLocalEdition(a, b string) (string, bool) {
	var left, right []string
	if json.Unmarshal([]byte(a), &left) != nil || json.Unmarshal([]byte(b), &right) != nil || len(left) != 4 || len(right) != 4 {
		return "", false
	}
	for i := range left {
		if left[i] != "" && right[i] != "" && left[i] != right[i] {
			return "", false
		}
		if left[i] == "" {
			left[i] = right[i]
		}
	}
	merged, _ := json.Marshal(left)
	return string(merged), true
}

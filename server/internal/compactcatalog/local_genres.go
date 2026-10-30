package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The local audio genre projection. Embedded tags and Portico JSON sidecars
// accept genre; the scan retains the selected evidence privately, but the
// recommendation facet only reads published relations. This is the one shared
// implementation of that publication, used both by the scanner's commit path
// and by the catch-up projection below, so the two can never drift:
//
//   - the input is the effective, policy-applied selected tag evidence, never
//     raw independent observations;
//   - the owner's genre-class lock is checked before any replacement or
//     removal and survives rescans and policy flips;
//   - the source is "local", distinct from "manual" owner edits and remote
//     provider families, so a missing or invalid sidecar never erases other
//     facts and a later scan cannot clobber a manual set;
//   - a changed set replaces only the rows this source published; a genre that
//     is simply absent erases nothing; an unchanged set writes nothing at all
//     (no rescan churn, no per-item wake storms);
//   - a normalized genre name is the facet identity, so independently scanned
//     items that spell a genre the same way share one facet, and the display
//     keeps its first-published casing.
//
// local_mode="off" strips descriptive local evidence: the projection removes
// the rows it published (an immediate withdrawal trigger covers the policy
// flip without waiting for the next scan) and republishes when the policy
// returns to prefer.

// audioGenreBatch is how many items one catch-up transaction covers. Each item
// is a read-compare-write that changes about seventeen rows and runs about
// fifteen thousand VM steps, so eight keep a batch inside a background write
// plan and foreground writes never queue long behind it.
const audioGenreBatch = 8

// AudioLocalGenreProjectionDue lists the audio libraries whose effective local
// genre projection was last completed under a different policy revision, or
// never ran. One indexed read; an unchanged server returns nothing at all.
func AudioLocalGenreProjectionDue(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.library_id FROM audio_metadata_policies p
 JOIN libraries l ON l.id=p.library_id
 LEFT JOIN audio_genre_projection g ON g.library_id=p.library_id
 WHERE l.kind IN('music','audiobook') AND (g.library_id IS NULL OR g.policy_revision<>p.revision OR g.completed_at='')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ProjectAudioLocalGenresBatch publishes local genre facets for one bounded
// keyset window of a library's audio items, reading the selected evidence the
// scan already retained and applying the shared projection rules. It advances
// the durable cursor, so the pass resumes after a restart instead of starting
// over, and reports whether more items remain.
func ProjectAudioLocalGenresBatch(ctx context.Context, db *sql.DB, libraryID string) (bool, error) {
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassFrom(ctx, dbwork.ClassBackgroundMedia))
	if err != nil {
		return false, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var stored string
	if err = tx.QueryRowContext(ctx, `SELECT cursor FROM audio_genre_projection WHERE library_id=?`, libraryID).Scan(&stored); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	cursor, _ := strconv.ParseInt(stored, 10, 64)
	ids, err := scanIDs(ctx, tx, `SELECT e.id FROM catalog_libraries cl JOIN catalog_entities e INDEXED BY catalog_entities_library ON e.library_id=cl.id
 JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND k.listening=1
 WHERE cl.library_id=? AND e.id>? ORDER BY e.id LIMIT ?`, libraryID, cursor, audioGenreBatch)
	if err != nil {
		return false, err
	}
	for _, item := range ids {
		genre, conflicted, err := audioSelectedGenre(ctx, tx, libraryID, item)
		if err != nil {
			return false, err
		}
		if err = projectAudioLocalGenreValue(ctx, tx, libraryID, item, genre, conflicted); err != nil {
			return false, err
		}
		cursor = item
	}
	more := len(ids) == audioGenreBatch
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM audio_metadata_policies WHERE library_id=?`, libraryID).Scan(&revision); err != nil {
		return false, err
	}
	if !more {
		if _, err = tx.ExecContext(ctx, `INSERT INTO audio_genre_projection(library_id,policy_revision,cursor,completed_at) VALUES(?,?,'',?)
 ON CONFLICT(library_id) DO UPDATE SET policy_revision=excluded.policy_revision,cursor='',completed_at=excluded.completed_at`,
			libraryID, revision, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return false, err
		}
	} else {
		// The state row may not exist at all on the first pass (a library that
		// predates the projection and never touched its policy): a plain
		// UPDATE would advance nothing and repeat this window for ever, so
		// the cursor always lands through an upsert. The row stays incomplete
		// (completed_at='') until the pass finishes, which keeps it due.
		if _, err = tx.ExecContext(ctx, `INSERT INTO audio_genre_projection(library_id,policy_revision,cursor,completed_at) VALUES(?,?,?,'')
 ON CONFLICT(library_id) DO UPDATE SET cursor=excluded.cursor`,
			libraryID, revision, strconv.FormatInt(cursor, 10)); err != nil {
			return false, err
		}
	}
	return more, gated.Commit()
}

// audioSelectedGenre reads one item's effective selected genre evidence. The
// private evidence table is the projection the scanner commits, not the raw
// observations, and it is exactly the input the live commit path projects
// from. Linked assets are compared deterministically: distinct values are
// read in sorted order (never SQLite traversal order), the item's genre is
// the lexicographically smallest spelling, and any disagreement between the
// normalized facet sets is reported as a conflict instead of letting
// traversal order decide.
func audioSelectedGenre(ctx context.Context, tx *sql.Tx, library string, item int64) (string, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT t.value FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id
 JOIN audio_tag_evidence t ON t.library_id=? AND t.asset_id=a.token AND t.field=CASE WHEN EXISTS(SELECT 1 FROM audio_tag_evidence g WHERE g.library_id=t.library_id AND g.asset_id=t.asset_id AND g.field='genres') THEN 'genres' ELSE 'genre' END WHERE l.entity_id=?
 ORDER BY t.value LIMIT 8`, library, item)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			rows.Close()
			return "", false, err
		}
		values = append(values, value)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", false, err
	}
	if len(values) == 0 {
		return "", false, nil
	}
	// One value may legitimately carry several genres through delimiters; a
	// conflict is only when two distinct values normalize to different facet
	// sets. The signature comparison is deterministic, not traversal order.
	seen := map[string]bool{}
	for _, value := range values {
		seen[normalizedFacetSignature(value)] = true
	}
	return values[0], len(seen) > 1, nil
}

// ProjectAudioLocalGenresItem projects one item's effective local genre
// evidence inside the caller's transaction. The scanner's commit path and the
// catch-up projection both go through this one function.
func ProjectAudioLocalGenresItem(ctx context.Context, tx *sql.Tx, library string, item int64) error {
	genre, conflicted, err := audioSelectedGenre(ctx, tx, library, item)
	if err != nil {
		return err
	}
	return projectAudioLocalGenreValue(ctx, tx, library, item, genre, conflicted)
}

// projectAudioLocalGenreValue publishes one item's local genre facet set.
func projectAudioLocalGenreValue(ctx context.Context, tx *sql.Tx, library string, item int64, genre string, conflicted bool) error {
	if item == 0 {
		return nil
	}
	var locked int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM metadata_relationship_decisions WHERE kind='item' AND entity_id=? AND relationship='genre' AND locked=1)`, item).Scan(&locked); err != nil {
		return err
	}
	if locked == 1 {
		// The owner's decision outranks this source for every further write,
		// including a policy flip that would otherwise withdraw the rows.
		return nil
	}
	var mode string
	err := tx.QueryRowContext(ctx, `SELECT local_mode FROM audio_metadata_policies WHERE library_id=?`, library).Scan(&mode)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && mode == "off" {
		// Policy off: descriptive local evidence must not serve. Rows this
		// source published go; owner and remote facts stay.
		return SetTermsTx(ctx, tx, item, VocabGenre, "local", nil)
	}
	if conflicted {
		// The item's linked sources disagree on the genre facet. Nothing
		// here may pick a winner by traversal order: conflicts stay evidence,
		// no rows are erased, and the previously published set — if any —
		// keeps serving until the sources agree or the owner decides.
		return nil
	}
	names := normalizeAudioGenres(genre)
	if len(names) == 0 {
		return nil // a missing or empty sidecar does not erase prior valid rows
	}
	current := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT s.source_id,s.source_name FROM catalog_term_sources s JOIN catalog_terms t ON t.id=s.term_id AND t.vocab=?
 WHERE s.entity_id=? AND s.provider='local'`, int(VocabGenre), item)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			rows.Close()
			return err
		}
		current[id] = name
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	desired := map[string]string{}
	for _, name := range names {
		id := localGenreKey(name)
		if _, ok := desired[id]; !ok {
			desired[id] = name
		}
	}
	changed := len(desired) != len(current)
	for id := range desired {
		if _, ok := current[id]; !ok {
			changed = true
		}
	}
	for id := range current {
		if _, ok := desired[id]; !ok {
			changed = true
		}
	}
	if !changed {
		// A different spelling of the same normalized names is the same fact:
		// the display form keeps its first-published casing instead of
		// churning on every rescan.
		return nil
	}
	terms := make([]Term, 0, len(desired))
	for id, name := range desired {
		terms = append(terms, Term{SourceID: id, Name: name, Key: id})
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].Key < terms[j].Key })
	return SetTermsTx(ctx, tx, item, VocabGenre, "local", terms)
}

// normalizeAudioGenres splits the bounded delimiters a tagger may use for one
// genre field, trims, and keeps the first sixteen names under sixty-four
// characters, deduplicated by normalized text.
func normalizeAudioGenres(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	var parts []string
	if json.Unmarshal([]byte(raw), &parts) != nil {
		parts = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '/' })
	}
	if len(parts) > 128 {
		parts = parts[:128]
	}
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" || len(name) > 64 {
			continue
		}
		id := localGenreKey(name)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, name)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

// localGenreKey is the normalized facet identity for a local genre spelling:
// case-folded with whitespace collapsed, so "Science Fiction" and
// "science  fiction" are one facet.
func localGenreKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}

// normalizedFacetSignature is the canonical, order-independent comparison
// form of one genre value: the sorted normalized facet ids it carries.
func normalizedFacetSignature(raw string) string {
	out := make([]string, 0, 16)
	for _, name := range normalizeAudioGenres(raw) {
		out = append(out, localGenreKey(name))
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
)

// Recommendation facets (catalog_rec_facets): what a title is like, as the
// recommendation scorer compares titles — its genres, people, collections,
// studio, network, decade, tags and artists ('g:', 'p:', 'cd:', 'c:', 's:',
// 'n:', 'e:', 't:', 'a:', 'b:', 'k:'). An item holds its own facets; a show,
// album or book holds its work's (screen genres and credits, show tags, album
// artist, author and series). The scorer reads a candidate's facets by key
// instead of deriving them per request. Triggers on every source table queue
// the entity (feeds.sql).
//
// The Portico title dataset's tags ('x:<tag>', strength 2 or 3; a weak tag
// says too little) are facets too. Two companions let strength count: a
// strength-3 tag is also 'x3:<tag>', and the first five billed people are also
// 'p5:<person>', so a lead or a defining theme weighs more than a bit part or a
// passing one.
//
// People are the top of each provider's billing (recPeoplePerItem): a full
// cast and crew list runs past a hundred credits, and its long tail says
// little about what a title is like. A title's complete credits are read from
// its credits, not from here.

// DomainRecFacets keeps catalog_rec_facets.
const DomainRecFacets = 32

const recPeoplePerItem = "16"

// A work is described by its most recurring member traits. A series with
// hundreds of unrelated episode tags must not publish thousands of traits
// every time one episode changes. Complete genres and credits remain facts.
const recMemberFacetLimit = 64

func fold(v string) string { return facettext.FoldFacetSQL(v) }

// recFacetsSQL selects (entity, facet) for the entities in json_each(?1).
var recFacetsSQL = `SELECT DISTINCT id,f FROM (
 SELECT e.id,'g:'||` + fold("g.source_name") + ` f FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11
  CROSS JOIN catalog_term_sources g ON g.entity_id=e.id JOIN catalog_terms gt ON gt.id=g.term_id AND gt.vocab=1 JOIN catalog_libraries cl ON cl.id=e.library_id
  WHERE trim(g.source_name)<>'' AND (e.kind NOT IN(7,9) OR g.provider<>'local'
  OR NOT EXISTS(SELECT 1 FROM audio_metadata_policies p WHERE p.library_id=cl.library_id AND p.local_mode='off')
  OR EXISTS(SELECT 1 FROM metadata_relationship_decisions d WHERE d.kind='item' AND d.entity_id=e.id AND d.relationship='genre' AND d.locked=1))
 UNION ALL SELECT e.id,'p:'||c.provider||':'||c.provider_person_id FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11
  CROSS JOIN catalog_credits c ON c.entity_id=e.id WHERE c.provider_person_id<>'' AND c.source_ordinal<` + recPeoplePerItem + `
 UNION ALL SELECT e.id,'cd:'||lower(d.label)||':'||` + fold("c.credited_name") + ` FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11
  CROSS JOIN catalog_credits c ON c.entity_id=e.id JOIN catalog_credit_labels d ON d.id=c.department_id WHERE d.label IN('Directing','Writing','Creator') AND trim(c.credited_name)<>''
 UNION ALL SELECT e.id,'c:'||pid(co.public_id) FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11
  CROSS JOIN catalog_collection_members cm ON cm.item_id=e.id JOIN catalog_entities co ON co.id=cm.collection_id
 UNION ALL SELECT e.id,'s:'||` + fold("det.studio") + ` FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11
  CROSS JOIN catalog_item_details det ON det.entity_id=e.id WHERE trim(det.studio)<>''
 UNION ALL SELECT e.id,'n:'||` + fold("det.network") + ` FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11
  CROSS JOIN catalog_item_details det ON det.entity_id=e.id WHERE trim(det.network)<>''
 UNION ALL SELECT e.id,'e:'||CAST(e.year/10 AS INTEGER) FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER)
  JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11 WHERE e.year>1800
 UNION ALL SELECT e.id,'t:'||` + fold("t.name") + ` FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER)
  JOIN catalog_kinds k ON k.id=e.kind CROSS JOIN metadata_tags t ON t.entity_kind=CASE e.kind WHEN 2 THEN 'show' ELSE 'item' END AND t.entity_id=e.id
  WHERE (k.playable=1 AND e.kind<>11 OR e.kind=2) AND trim(t.name)<>''
 UNION ALL SELECT e.id,'a:'||pid(artist.public_id) FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11
  CROSS JOIN catalog_song_artists sa ON sa.song_id=e.id JOIN catalog_entities artist ON artist.id=sa.artist_id
 UNION ALL SELECT e.id,'g:'||` + fold("json_extract(g.value,'$.name')") + ` FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) AND e.kind=2
  CROSS JOIN screen_metadata_fields sf ON sf.target_kind='show' AND sf.target_id=e.id AND sf.field='genres',json_each(sf.value) g
 UNION ALL SELECT e.id,'cd:'||lower(json_extract(g.value,'$.department'))||':'||` + fold("json_extract(g.value,'$.name')") + ` FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) AND e.kind=2
  CROSS JOIN screen_metadata_fields sf ON sf.target_kind='show' AND sf.target_id=e.id AND sf.field IN('credits','creditsOnline'),json_each(sf.value) g
 UNION ALL SELECT e.id,'a:'||pid(artist.public_id) FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) AND e.kind=6
  CROSS JOIN catalog_albums a ON a.entity_id=e.id JOIN catalog_entities artist ON artist.id=a.artist_id
 UNION ALL SELECT e.id,'b:'||` + fold("b.author") + ` FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) AND e.kind=8
  CROSS JOIN catalog_books b ON b.entity_id=e.id WHERE trim(b.author)<>''
 UNION ALL SELECT e.id,'k:'||` + fold("b.series") + ` FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) AND e.kind=8
  CROSS JOIN catalog_books b ON b.entity_id=e.id WHERE trim(b.series)<>''
 UNION ALL SELECT e.id,'k:'||` + fold("json_extract(CASE WHEN json_valid(b.local_metadata_payload) THEN b.local_metadata_payload ELSE '{}' END,'$.series[0].name')") + `
  FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) AND e.kind=8 CROSS JOIN catalog_books b ON b.entity_id=e.id
  WHERE b.series='' AND NOT EXISTS(SELECT 1 FROM metadata_owner_fields ow WHERE ow.kind='book' AND ow.entity_id=e.id AND ow.field='series' AND ow.locked=1)
  AND NOT EXISTS(SELECT 1 FROM audio_metadata_policies p WHERE p.library_id=(SELECT library_id FROM catalog_libraries WHERE id=e.library_id) AND p.local_mode='off')
  AND trim(COALESCE(json_extract(CASE WHEN json_valid(b.local_metadata_payload) THEN b.local_metadata_payload ELSE '{}' END,'$.series[0].name'),''))<>''
 UNION ALL SELECT e.id,'x:'||t.tag FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER)
  CROSS JOIN catalog_dataset_tags t ON t.entity_id=e.id WHERE t.strength>=2
 UNION ALL SELECT e.id,'x3:'||t.tag FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER)
  CROSS JOIN catalog_dataset_tags t ON t.entity_id=e.id WHERE t.strength=3
 UNION ALL SELECT e.id,'p5:'||c.provider||':'||c.provider_person_id FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 AND e.kind<>11
  CROSS JOIN catalog_credits c ON c.entity_id=e.id WHERE c.provider_person_id<>'' AND c.source_ordinal<5
 UNION ALL SELECT e.id,m.facet FROM json_each(?1) j CROSS JOIN catalog_entities e ON e.id=CAST(j.value AS INTEGER) AND e.kind IN(2,6,8)
  CROSS JOIN (` + recMemberFacets + `) m ON m.work=e.id
) WHERE f IS NOT NULL AND substr(f,instr(f,':')+1)<>''`

// recMemberFacets are what a work's members say about it: an episode's,
// song's or book file's genres, tags, studio and network, and the people in
// at least three of them (a show's recurring cast, not its guest stars). A
// show carrying its genres only on its episodes, or an album only on its
// songs, is still like the works that share them.
const recMemberFacets = `SELECT CAST(requested.value AS INTEGER) AS work,trait.value AS facet
 FROM json_each(?1) requested CROSS JOIN json_each((
 SELECT json_group_array(facet) FROM (
 SELECT facet FROM catalog_rec_member_counts INDEXED BY catalog_rec_member_best
 WHERE work_id=CAST(requested.value AS INTEGER) AND eligible=1
 ORDER BY total DESC,facet LIMIT 64))) trait`

func deriveRecFacets(ctx context.Context, tx *sql.Tx, list []int64) error {
	// A work some profile holds a signal on moves that profile's taste when
	// its facets change: read its facets before they're replaced.
	held := map[int64][]string{}
	// A member's facets are its work's too: when an episode's, song's or book
	// file's facets change, its work is derived again (the queue coalesces a
	// batch of members into one pass over their work).
	members := map[int64][]string{}
	oldParents := map[int64]int64{}
	newParents := map[int64]int64{}
	for _, id := range list {
		var old int64
		if err := tx.QueryRowContext(ctx, `SELECT work_id FROM catalog_rec_member_work WHERE item_id=?`, id).Scan(&old); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		oldParents[id] = old
		var parent sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT show_id FROM catalog_episodes WHERE entity_id=?1),(SELECT album_id FROM catalog_songs WHERE entity_id=?1),(SELECT book_id FROM catalog_book_files WHERE entity_id=?1))`, id).Scan(&parent); err != nil {
			return err
		}
		if parent.Valid {
			newParents[id] = parent.Int64
			facets, err := RecWorkFacets(ctx, tx, id)
			if err != nil {
				return err
			}
			members[id] = append(facets, "\x00"+itoaInt64(parent.Int64))
		}
	}
	for _, id := range list {
		var signals bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rec_profile_signals INDEXED BY rec_profile_signals_work WHERE work_id=?)`, id).Scan(&signals); err != nil {
			return err
		}
		if signals {
			facets, err := RecWorkFacets(ctx, tx, id)
			if err != nil {
				return err
			}
			held[id] = facets
		}
	}
	// Members first, then works: a show, album or book reads its members'
	// facets, so a member queued in the same batch must be derived before it.
	works, others, err := splitWorks(ctx, tx, list)
	if err != nil {
		return err
	}
	for _, part := range [][]int64{others, works} {
		if len(part) == 0 {
			continue
		}
		batch := mustJSON(part)
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_rec_facets WHERE entity_id IN(SELECT CAST(value AS INTEGER) FROM json_each(?))`, batch); err != nil {
			return err
		}
		// Old facets were removed using the retained parent. Attach the new
		// contribution to the member's current parent before inserting its traits.
		for _, id := range part {
			if parent := newParents[id]; parent != 0 {
				if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_rec_member_work(item_id,work_id) VALUES(?,?) ON CONFLICT(item_id) DO UPDATE SET work_id=excluded.work_id`, id, parent); err != nil {
					return err
				}
			} else if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_rec_member_work WHERE item_id=?`, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_rec_facets(entity_id,facet) `+recFacetsSQL, batch); err != nil {
			return err
		}
	}
	for _, id := range list {
		if oldParents[id] != newParents[id] {
			for _, parent := range []int64{oldParents[id], newParents[id]} {
				if parent != 0 {
					if err := TouchTx(ctx, tx, DomainRecFacets, parent); err != nil {
						return err
					}
				}
			}
		}
	}
	for id, before := range members {
		parent := before[len(before)-1][1:]
		after, err := RecWorkFacets(ctx, tx, id)
		if err != nil {
			return err
		}
		if added, removed := facetDiff(before[:len(before)-1], after); len(added) > 0 || len(removed) > 0 {
			if _, err = tx.ExecContext(ctx, touchOne, DomainRecFacets, parent); err != nil {
				return err
			}
		}
	}
	for id, before := range held {
		after, err := RecWorkFacets(ctx, tx, id)
		if err != nil {
			return err
		}
		added, removed := facetDiff(before, after)
		if err = recRefacet(ctx, tx, id, added, removed); err != nil {
			return err
		}
	}
	df := map[string]int{}
	for _, id := range list {
		if err := derivePostings(ctx, tx, id, df); err != nil {
			return err
		}
	}
	for facet, delta := range df {
		if delta == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_rec_df(facet,works) VALUES(?,max(?,0)) ON CONFLICT(facet) DO UPDATE SET works=max(works+?,0)`, facet, delta, delta); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_rec_df WHERE works=0 AND facet IN(SELECT value FROM json_each(?))`, mustJSON(mapKeys(df))); err != nil {
		return err
	}
	// Rankings are memoised under this revision: facets, postings and rarity
	// just moved.
	_, err = tx.ExecContext(ctx, `UPDATE rec_data_revision SET revision=revision+1 WHERE id=1`)
	return err
}

// splitWorks separates the batch's shows, albums and books from the rest.
func splitWorks(ctx context.Context, tx *sql.Tx, list []int64) (works, others []int64, err error) {
	for _, id := range list {
		var kind sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, id).Scan(&kind); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
		err = nil
		if k := Kind(kind.Int64); k == Show || k == Album || k == Book {
			works = append(works, id)
		} else {
			others = append(others, id)
		}
	}
	return works, others, nil
}

// RecPostingKinds are the works retrieval returns: films, shows, albums, books.
var RecPostingKinds = []Kind{Movie, Show, Album, Book}

// RecAllFacet lists every work (quality order), for cold starts and fill.
const RecAllFacet = "*"

// derivePostings keeps a work's best-first facet lists and moves df by the
// difference. Entities that aren't works (or no longer exist) hold none.
func derivePostings(ctx context.Context, tx *sql.Tx, id int64, df map[string]int) error {
	type posting struct {
		library int64
		quality float64
	}
	old := map[string]posting{}
	rows, err := tx.QueryContext(ctx, `SELECT facet,library_id,quality FROM catalog_rec_postings INDEXED BY catalog_rec_postings_entity WHERE entity_id=?`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var f string
		var p posting
		if err = rows.Scan(&f, &p.library, &p.quality); err != nil {
			rows.Close()
			return err
		}
		old[f] = p
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	next := map[string]posting{}
	var kind, library int64
	err = tx.QueryRowContext(ctx, `SELECT kind,library_id FROM catalog_entities WHERE id=? AND retired=0`, id).Scan(&kind, &library)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && isPostingKind(kind) {
		quality, err := recQuality(ctx, tx, id)
		if err != nil {
			return err
		}
		facets, err := RecWorkFacets(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, f := range append(facets, RecAllFacet) {
			next[f] = posting{library, quality}
		}
	}
	for f, p := range old {
		if q, ok := next[f]; ok && q == p {
			continue
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_rec_postings WHERE facet=? AND library_id=? AND quality=? AND entity_id=?`, f, p.library, p.quality, id); err != nil {
			return err
		}
		if _, ok := next[f]; !ok {
			df[f]--
		}
	}
	for f, p := range next {
		if q, ok := old[f]; ok && q == p {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO catalog_rec_postings(facet,library_id,quality,entity_id) VALUES(?,?,?,?)`, f, p.library, p.quality, id); err != nil {
			return err
		}
		if _, ok := old[f]; !ok {
			df[f]++
		}
	}
	return nil
}

func isPostingKind(kind int64) bool {
	for _, k := range RecPostingKinds {
		if int64(k) == kind {
			return true
		}
	}
	return false
}

// Vote-weighted quality (0-10): the provider rating with the most votes,
// pulled toward recPriorMean by recPriorVotes, so a 9.1 from 12 votes doesn't
// outrank an 8.4 from 40,000. An unrated work sits a little below the mean.
const (
	recPriorMean  = 6.3
	recPriorVotes = 100.0
	recUnrated    = 6.0
)

func recQuality(ctx context.Context, tx *sql.Tx, id int64) (float64, error) {
	var value, scale float64
	var votes int64
	err := tx.QueryRowContext(ctx, `SELECT value,scale,votes FROM metadata_ratings WHERE item_id=? AND scale>0 ORDER BY votes DESC,provider LIMIT 1`, id).Scan(&value, &scale, &votes)
	if errors.Is(err, sql.ErrNoRows) {
		return recUnrated, nil
	}
	if err != nil {
		return 0, err
	}
	rating := 10 * value / scale
	v := float64(max(votes, 0))
	return math.Round((v*rating+recPriorVotes*recPriorMean)/(v+recPriorVotes)*1000) / 1000, nil
}

func facetDiff(before, after []string) (added, removed []string) {
	was := map[string]bool{}
	for _, f := range before {
		was[f] = true
	}
	is := map[string]bool{}
	for _, f := range after {
		is[f] = true
		if !was[f] {
			added = append(added, f)
		}
	}
	for _, f := range before {
		if !is[f] {
			removed = append(removed, f)
		}
	}
	return added, removed
}

func mapKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func init() {
	Register(DomainRecFacets, Derivation{
		Version: 6,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			ids := keyIDs(keys)
			return ids, deriveRecFacets(ctx, tx, ids)
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT id FROM catalog_entities WHERE id>? ORDER BY id LIMIT ?`, after, limit)
		},
	})
}

func itoaInt64(v int64) string { return strconv.FormatInt(v, 10) }

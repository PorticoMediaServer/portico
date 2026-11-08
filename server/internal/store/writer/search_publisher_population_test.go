package writer

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

// Test setup only. Every graph is native source + publisher + two immutable
// images + seal + held reclaimers. Sealed graphs also have three actual rows in
// EACH native fragment FTS index and one original own-prefix row. These are
// deliberately uniform primary-title inputs, not a representative corpus.
// Nothing in this file runs after binding except values-only packet preparation.
type publisherPopulation struct {
	e                  *executor
	fragment           searchFragmentStageSnapshot
	head               searchHeadPublishRequest
	rows               [3]searchmanifest.Row
	offers, background int
	counts             map[string]int64
	baseline           map[string]int64
}

func publisherPopulationNonce(kind string, ordinal int) (out [16]byte) {
	sum := sha256.Sum256([]byte(fmt.Sprintf("publisher-population-v1/%s/%d", kind, ordinal)))
	copy(out[:], sum[:16])
	return out
}

func publisherPopulationID(n int) int64 { return 1000000 + int64(n)*16 }

// One immutable packet per offer; no SQL, retained packet list or live re-read.
// completed is the number of successful preceding head offers, including warmup.
func (p *publisherPopulation) packet(n int, purpose CommandID, completed int) (fragment *searchFragmentStageCommand, head *searchHeadPublishCommand, err error) {
	id := publisherPopulationID(n)
	s := p.fragment
	if purpose == SearchHeadPublish {
		s = p.head.snapshot.stage
	}
	source := publisherPopulationNonce("source", n)
	s.source.entityID, s.source.incarnation = id, source
	s.job.id, s.job.entityID = id+1, id
	s.job.incarnation = publisherPopulationNonce("publisher", n)
	s.job.sourceIncarnation = source
	s.job.publishedImage.Int64, s.job.document.Int64 = id+2, id+3
	s.image.id = id + 3
	s.image.input.SubjectID, s.image.input.SourceIncarnation = id, source
	manifest, err := searchmanifest.Prepare(s.image.input, p.rows[:])
	if err != nil {
		return nil, nil, err
	}
	s.seal.seed, s.seal.expectedDigest = manifest.Seed(), manifest.Digest()
	s.seal.chain = manifest.Seed()
	identity := CommandIdentity{s.job.incarnation, 8, publisherPopulationNonce("operation", n)}
	if purpose == SearchFragmentStage {
		fragment, err = newSearchFragmentStageCommand(identity, searchFragmentStageRequest{"fragment_stage", s, p.rows[0]})
		return fragment, nil, err
	}
	s.seal.chain = manifest.Digest()
	s.job.afterFragmentID, s.seal.afterFragmentID = id+8, id+8
	r := p.head
	r.snapshot.stage = s
	r.snapshot.servingRevision = p.head.snapshot.servingRevision + int64(completed)
	r.snapshot.pending = 3 + int64(p.offers+p.background-completed)
	r.old.imageID, r.next.imageID = id+2, id+3
	r.retirement.id, r.retirement.publicationID, r.retirement.livePublicationID.Int64 = id+4, id+2, id+2
	r.retirement.incarnation = publisherPopulationNonce("old-reclaimer", n)
	head, err = newSearchHeadPublishCommand(identity, r)
	return nil, head, err
}

// Bounded templates copy the canonical seed's complete native row, overriding
// all coordinates that differ. SQL names come only from these fixed tables and
// the native column inventory, never the qualification config.
type publisherPopulationTemplate struct {
	table   string
	columns []string
	values  []any
	insert  string
}

func publisherReadPopulationTemplate(t *testing.T, e *executor, table string, key int64) publisherPopulationTemplate {
	t.Helper()
	keyColumn := "id"
	if table == "search_image_seals" {
		keyColumn = "publication_id"
	}
	// table_xinfo includes generated/hidden columns that SELECT * also
	// returns. They are schema-derived outputs and must never enter INSERT.
	info, err := e.db.QueryContext(t.Context(), `PRAGMA table_xinfo("`+table+`")`)
	if err != nil {
		t.Fatal(err)
	}
	var columns, quoted []string
	for info.Next() {
		var cid, notnull, pk, hidden int
		var column, kind string
		var defaultValue any
		if err := info.Scan(&cid, &column, &kind, &notnull, &defaultValue, &pk, &hidden); err != nil {
			info.Close()
			t.Fatal(err)
		}
		if hidden == 0 {
			columns = append(columns, column)
			quoted = append(quoted, `"`+strings.ReplaceAll(column, `"`, `""`)+`"`)
		}
	}
	if err := errors.Join(info.Err(), info.Close()); err != nil {
		t.Fatal(err)
	}
	if len(columns) == 0 {
		t.Fatal("empty native population template", table)
	}
	rows, err := e.db.QueryContext(t.Context(), `SELECT `+strings.Join(quoted, ",")+` FROM "`+table+`" WHERE "`+keyColumn+`"=?`, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values, dest := make([]any, len(columns)), make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if !rows.Next() {
		t.Fatal("missing population template", table, rows.Err())
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, v := range values {
		if b, ok := v.([]byte); ok {
			values[i] = append([]byte(nil), b...)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("non-singleton population template", table, rows.Err())
	}
	return publisherPopulationTemplate{table, columns, values, `INSERT INTO "` + table + `" (` + strings.Join(quoted, ",") + `) VALUES (` + strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",") + `)`}
}

func (p publisherPopulationTemplate) put(t *testing.T, tx *sql.Tx, overrides map[string]any) {
	t.Helper()
	args := append([]any(nil), p.values...)
	used := 0
	for i, column := range p.columns {
		if v, ok := overrides[column]; ok {
			args[i] = v
			used++
		}
	}
	if used != len(overrides) {
		t.Fatal("unknown population override", p.table, overrides)
	}
	if _, err := tx.ExecContext(t.Context(), p.insert, args...); err != nil {
		t.Fatalf("populate %s: %v", p.table, err)
	}
}

func publisherPopulationCounts(t *testing.T, e *executor) map[string]int64 {
	t.Helper()
	// FTS5 external-content COUNT(*) projects its content view, even when the
	// inverted index is empty. Count actual native document-size rows instead.
	counts := make(map[string]int64)
	for _, table := range []string{"catalog_subjects", "catalog_entities", "search_publish_jobs", "search_publications", "search_image_seals", "search_reclaim_jobs", "search_fragments", "search_fragment_prefix_docsize", "search_fragment_lexical_docsize", "search_fragment_grams_docsize", "search_own_prefix_docsize"} {
		var n int64
		if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM "`+table+`"`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[table] = n
	}
	return counts
}

func preparePublisherPopulation(t *testing.T, purpose CommandID, offers, background int) *publisherPopulation {
	t.Helper()
	if offers < 1 || offers > 1000000 || background < 0 || background > 1000000 {
		t.Fatal("population bounds")
	}
	p := &publisherPopulation{offers: offers, background: background}
	var prepared []searchPreparedFragment
	p.fragment, prepared = searchFragmentStageSnapshotFixture(t)
	p.head, _ = searchHeadRequestFixture(t)
	for i := range p.rows {
		p.rows[i] = prepared[i].row
	}
	if purpose == SearchFragmentStage {
		p.e, _ = searchFragmentNativeSeed(t)
	} else {
		p.e, _ = searchHeadNativeSeed(t)
	}
	e := p.e
	baseline := publisherPopulationCounts(t, e)
	p.baseline = baseline
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if publisherMeasurementOnMini1(host) && baseline["catalog_entities"]+int64(offers+background) >= 100000 {
		t.Fatal("mini-1 physical catalog population >=100k is prohibited, including canonical seed rows")
	}
	subject := publisherReadPopulationTemplate(t, e, "catalog_subjects", 41)
	entity := publisherReadPopulationTemplate(t, e, "catalog_entities", 41)
	job := publisherReadPopulationTemplate(t, e, "search_publish_jobs", 29)
	oldImage := publisherReadPopulationTemplate(t, e, "search_publications", 13)
	nextImage := publisherReadPopulationTemplate(t, e, "search_publications", 307)
	seal := publisherReadPopulationTemplate(t, e, "search_image_seals", 307)
	var reclaimID int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT id FROM search_reclaim_jobs WHERE publication_id=307`).Scan(&reclaimID); err != nil {
		t.Fatal(err)
	}
	reclaim := publisherReadPopulationTemplate(t, e, "search_reclaim_jobs", reclaimID)
	total := offers + background
	// Chunk commits bound setup WAL/transaction memory. This is complete BEFORE
	// Capture and does not reset or repair anything between measured originals.
	for start := 0; start < total; start += 128 {
		tx, err := e.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			end := min(total, start+128)
			for n := start; n < end; n++ {
				graphPurpose := purpose
				if n >= offers {
					graphPurpose = SearchHeadPublish
				}
				f, h, err := p.packet(n, graphPurpose, 0)
				if err != nil {
					t.Fatal("population packet", n, err)
				}
				var s searchFragmentStageSnapshot
				if f != nil {
					s = f.request.snapshot
				} else {
					s = h.request.snapshot.stage
				}
				id := publisherPopulationID(n)
				public := publisherPopulationNonce("public", n)
				subject.put(t, tx, map[string]any{"id": id, "public_id": public[:]})
				entity.put(t, tx, map[string]any{"id": id, "public_id": public[:], "metadata_incarnation": s.source.incarnation[:], "title": "東京"})
				oldPrefix, oldInitial, nextPrefix, nextInitial := "", "", "", ""
				if h != nil {
					oldPrefix, oldInitial, nextPrefix, nextInitial = h.request.old.prefix, h.request.old.initial, h.request.next.prefix, h.request.next.initial
				}
				oldImage.put(t, tx, map[string]any{"id": id + 2, "entity_id": id, "source_incarnation": s.source.incarnation[:], "source_revision": int64(9), "title": "Old", "title_prefix_tokens": oldPrefix, "initial_prefix_tokens": oldInitial})
				nextImage.put(t, tx, map[string]any{"id": id + 3, "entity_id": id, "source_incarnation": s.source.incarnation[:], "title": "東京", "title_prefix_tokens": nextPrefix, "initial_prefix_tokens": nextInitial})
				// Clone both reclaimers, including exact nonce/owner originals. Their
				// canonical default held state is identical to the typed witness.
				for _, v := range []struct {
					key, image int64
					kind       string
				}{{id + 4, id + 2, "old-reclaimer"}, {id + 5, id + 3, "next-reclaimer"}} {
					nonce := publisherPopulationNonce(v.kind, n)
					reclaim.put(t, tx, map[string]any{"id": v.key, "publication_id": v.image, "live_publication_id": v.image, "command_incarnation": nonce[:]})
				}
				j := s.job
				job.put(t, tx, map[string]any{"id": j.id, "entity_id": id, "command_incarnation": j.incarnation[:], "source_incarnation": s.source.incarnation[:], "document_id": id + 3, "published_document_id": id + 2, "phase": j.phase, "after_field": j.afterField, "after_ordinal": j.afterOrdinal, "after_fragment_id": j.afterFragmentID, "expected_fragments": j.expectedFragments, "staged_fragments": j.stagedFragments, "staged_bytes": j.stagedBytes})
				v := s.seal
				seal.put(t, tx, map[string]any{"publication_id": id + 3, "seed": v.seed[:], "expected_digest": v.expectedDigest[:], "chain": v.chain[:], "staged_fragments": v.stagedFragments, "staged_bytes": v.stagedBytes, "verified_fragments": v.verifiedFragments, "verified_bytes": v.verifiedBytes, "after_field": v.afterField, "after_ordinal": v.afterOrdinal, "after_fragment_id": v.afterFragmentID, "sealed": v.sealed})
				if h != nil {
					if _, err := tx.ExecContext(t.Context(), `INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)`, id, oldPrefix, oldInitial); err != nil {
						t.Fatal(err)
					}
					for at, row := range p.rows {
						fragmentID := id + 6 + int64(at)
						if _, err := tx.ExecContext(t.Context(), `INSERT INTO search_fragments(id,owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(?,?,NULL,?,?,?,?,?,?,5,7,?)`, fragmentID, id, int64(row.Field), row.Ordinal, row.Revision, row.Prefix, row.Lexical, row.Normalized, id+3); err != nil {
							t.Fatal(err)
						}
						for _, v := range []struct{ q, text string }{{`INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(?,?)`, row.Prefix}, {`INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(?,?)`, row.Lexical}, {`INSERT INTO search_fragment_grams(rowid,normalized) VALUES(?,?)`, row.Normalized}} {
							if _, err := tx.ExecContext(t.Context(), v.q, fragmentID, v.text); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}()
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE search_generations SET pending_subjects=? WHERE id=7`, 3+total); err != nil {
		t.Fatal(err)
	}
	p.counts = publisherPopulationCounts(t, e)
	sealed := background
	if purpose == SearchHeadPublish {
		sealed += offers
	}
	for table, count := range p.counts {
		delta := int64(total)
		switch table {
		case "search_publications", "search_reclaim_jobs":
			delta *= 2
		case "search_fragments", "search_fragment_prefix_docsize", "search_fragment_lexical_docsize", "search_fragment_grams_docsize":
			delta = int64(sealed) * 3
		case "search_own_prefix_docsize":
			delta = int64(sealed)
		}
		if count != baseline[table]+delta {
			t.Fatalf("physical density %s: %d != %d+%d", table, count, baseline[table], delta)
		}
	}
	// Finish setup with a real checkpoint, then Capture/bind once. The target
	// declares this zero-WAL starting condition; there is no per-offer checkpoint.
	var busy, log, done int
	if err := e.db.QueryRowContext(t.Context(), `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &done); err != nil || busy != 0 {
		t.Fatal("population checkpoint", busy, log, done, err)
	}
	return p
}

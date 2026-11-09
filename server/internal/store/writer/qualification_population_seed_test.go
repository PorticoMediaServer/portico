package writer

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

type qualificationPopulationSpec struct {
	Seed       uint32 `json:"seed"`
	Residents  int    `json:"residents"`
	SearchBank bool   `json:"search_bank,omitempty"`
}

type qualificationPopulationFacts struct {
	Version       string           `json:"version"`
	Seed          uint32           `json:"seed"`
	Residents     int              `json:"residents"`
	LibraryID     int64            `json:"library_id"`
	LibraryKey    string           `json:"library_key"`
	FirstID       int64            `json:"first_id"`
	LastID        int64            `json:"last_id"`
	Counts        map[string]int64 `json:"counts"`
	LogicalSHA256 string           `json:"logical_sha256"`
	// SearchBank is present only for the search population shape. RD's read
	// classes and the producer's ANALYZE policy travel with it.
	SearchBank            *qualificationSearchBankFacts `json:"search_bank,omitempty"`
	Statistics            string                        `json:"statistics,omitempty"`
	SearchBankIntegrity   string                        `json:"search_bank_integrity,omitempty"`
	SearchBankForeignKeys string                        `json:"search_bank_foreign_keys,omitempty"`
}

const qualificationMovieShape = "canonical-movies-v1"

// The search shape keeps the catalogue shape intact but adds one v1 search bank
// for every resident plus the deterministic People/credit subset. Existing
// canonical-movies-v1 artifacts and consumers are unchanged.
const qualificationMovieSearchShape = "canonical-movies-v1-search"

// The producer runs no ANALYZE, so the artifact states that concretely instead
// of repeating a policy to-do. Consumers must not assume SQLite statistics.
const qualificationSearchShapeStatistics = "not analyzed: the producer runs no ANALYZE; the search bank builder leaves SQLite statistics absent"
const qualificationMovieIDBase int64 = 1_000_000_000
const qualificationMovieRoot = "/qualification-media"
const qualificationMovieBatch = 4096

func validateQualificationPopulationSpec(spec qualificationPopulationSpec) error {
	if spec.Residents < 1 || spec.Residents > 1_000_000 {
		return fmt.Errorf("qualification residents must be 1..1000000: %d", spec.Residents)
	}
	return nil
}

// seedQualificationPopulation is one test-only, source-bound SQL owner. The
// caller supplies the existing canonical executor, exclusively, before command
// HOLD; this helper creates no connection pool, issuer, policy or DDL overlay.
//
// Populated tables: libraries, catalog_libraries, catalog_kinds,
// catalog_subjects, catalog_entities, catalog_assets, catalog_asset_links,
// catalog_scalar_contributions, catalog_asset_contributions,
// catalog_membership_clock and catalog_age_item_counts. Every resident is a
// current, unrated movie in library 7, with one available asset and link, a
// source-0 self scalar and one physical contribution. Membership sequences and
// library revisions are synthetic baseline data, not command acceptance proof.
//
// All other tables remain the canonical empty baseline: no source identities,
// inventory/jobs/history/receipts, Basic/probe facts, enrichment, terms/facets,
// search publications/owner receipts, aggregate deltas, or production seals.
// Technical strings are empty and technical numbers zero (unobserved), not
// invented codec/duration facts. Paths describe synthetic files; no media bytes
// exist. This is a minimal movie catalogue, not enriched or all-kind coverage.
func seedQualificationPopulation(t *testing.T, w *executor, spec qualificationPopulationSpec) qualificationPopulationFacts {
	t.Helper()
	if err := validateQualificationPopulationSpec(spec); err != nil {
		t.Fatal(err)
	}
	// CHANGE-014 personal facts, CHANGE-015 security, and CHANGE-016 artwork leave the movie
	// population SQL/defaults and logical shape unchanged. Export fresh images
	// with this exact schema; manifests from earlier schemas remain refused.
	if schema.SHA256 != "7462ff7631edfda0fc21a70015156f28d077a1fbd8f8a9d948768451db8d6f91" {
		t.Fatal("canonical-movies-v1 requires exact canonical schema017")
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	var occupied bool
	if err := conn.QueryRowContext(t.Context(), `SELECT
EXISTS(SELECT 1 FROM libraries) OR EXISTS(SELECT 1 FROM catalog_libraries) OR
EXISTS(SELECT 1 FROM catalog_kinds) OR EXISTS(SELECT 1 FROM catalog_subjects) OR
EXISTS(SELECT 1 FROM catalog_entities) OR EXISTS(SELECT 1 FROM catalog_assets) OR
EXISTS(SELECT 1 FROM catalog_asset_links) OR EXISTS(SELECT 1 FROM catalog_scalar_contributions) OR
EXISTS(SELECT 1 FROM catalog_asset_contributions) OR EXISTS(SELECT 1 FROM catalog_membership_clock) OR
EXISTS(SELECT 1 FROM catalog_age_item_counts)`).Scan(&occupied); err != nil || occupied {
		t.Fatal("population requires empty canonical catalogue", err)
	}

	// Prepared once on the owner's existing connection, rebound to each bounded
	// transaction. No resident slice, SQL text parameter or arbitrary SQL hook.
	queries := [...]string{
		`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,7,1)`,
		`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_title,metadata_language,
sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,year,added_at,
metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,
file_size_key,resolution_key,admission_allow_age) VALUES(?,?,7,1,?,?,?,?,?,?,?,?,?,'2026-11-01T00:00:00.000000000Z',?,?,?,1,?,0,-1)`,
		`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,playback_origin_incarnation)
VALUES(?,?,7,?,?,?,'','','',0,0,0,?)`,
		`INSERT INTO catalog_asset_links(library_id,entity_id,asset_id,playback_origin_incarnation) VALUES(7,?,?,?)`,
		`INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence)
VALUES(7,?,?,0,1,?)`,
		`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision)
VALUES(7,?,?,1,0,?,1)`,
	}
	var prepared [len(queries)]*sql.Stmt
	for i, q := range queries {
		prepared[i], err = conn.PrepareContext(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared[i].Close()
	}

	for start := 1; start <= spec.Residents; start += qualificationMovieBatch {
		end := min(start+qualificationMovieBatch-1, spec.Residents)
		func() {
			tx, err := conn.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if start == 1 {
				libraryBlob := qualificationMovieBlob("QMB1", spec.Seed, 0)
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO libraries(id,name,kind,root,playback_origin_incarnation,
data_revision,data_order_revision,data_metadata_revision,age_input_revision,age_published_revision)
VALUES('qualification-movies','Qualification movies','movie','/qualification-media',?,?,?,?,?,?)`,
					libraryBlob[:], spec.Residents+1, spec.Residents+1, spec.Residents+1, spec.Residents+1, spec.Residents+1); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'qualification-movies','/qualification-media');
INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`); err != nil {
					t.Fatal(err)
				}
			}
			var statements [len(prepared)]*sql.Stmt
			for i, statement := range prepared {
				statements[i] = tx.StmtContext(t.Context(), statement)
				defer statements[i].Close()
			}
			for ordinal := start; ordinal <= end; ordinal++ {
				movie := qualificationMovie(t, spec.Seed, ordinal)
				args := [len(prepared)][]any{
					{movie.id, movie.public[:]},
					{movie.id, movie.public[:], movie.metadata.Title, movie.metadata.SortTitle, movie.metadata.MetadataLanguage,
						movie.metadata.SortKey, movie.metadata.SortHead, movie.metadata.SortKeyKeepArticles, movie.metadata.SortHeadKeepArticles,
						movie.random, movie.year, movie.incarnation[:], movie.artwork[:], movie.origin[:], movie.size},
					{movie.id, movie.token, movie.path, movie.size, movie.modifiedNS, movie.assetOrigin[:]},
					{movie.id, movie.id, movie.linkOrigin[:]},
					{movie.id, movie.id, ordinal},
					{movie.id, movie.id, movie.size},
				}
				for i, statement := range statements {
					if _, err := statement.ExecContext(t.Context(), args[i]...); err != nil {
						t.Fatalf("population ordinal %d statement %d: %v", ordinal, i, err)
					}
				}
			}
			if end == spec.Residents {
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO catalog_membership_clock(singleton,sequence) VALUES(1,?)`, spec.Residents); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.ExecContext(t.Context(), `INSERT INTO catalog_age_item_counts(library_id,kind,block_unrated,admission_age,total) VALUES(7,1,0,-1,?)`, spec.Residents); err != nil {
					t.Fatal(err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}()
	}

	facts := qualificationPopulationFacts{Version: qualificationMovieShape, Seed: spec.Seed, Residents: spec.Residents,
		LibraryID: 7, LibraryKey: "qualification-movies", Counts: make(map[string]int64)}
	if spec.SearchBank {
		facts.Version = qualificationMovieSearchShape
		facts.Statistics = qualificationSearchShapeStatistics
	}
	qualificationPopulationReadback(t, conn, &facts)
	return facts
}

type qualificationMovieRow struct {
	id, random, size, modifiedNS                                  int64
	year                                                          int
	metadata                                                      InventoryFilenameMetadata
	path, token                                                   string
	public, incarnation, artwork, origin, assetOrigin, linkOrigin [16]byte
}

// SplitMix64's fixed integer mixing avoids runtime RNG/version dependencies;
// ordinal IDs and blob encoding remain injective even when title words repeat.
func qualificationMovie(t *testing.T, seed uint32, ordinal int) qualificationMovieRow {
	t.Helper()
	x := uint64(seed)<<32 | uint64(ordinal)
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	words := [...]string{"The Amber River", "A Quiet Orbit", "An Autumn Map", "Blue Harbour", "Étoile", "9 Lanterns", "North & South", "The Glass Island"}
	title := fmt.Sprintf("%s %07d", words[x%uint64(len(words))], ordinal)
	explicit := ""
	if x%11 == 0 {
		explicit = fmt.Sprintf("Archive %07d", ordinal)
	}
	metadata, err := ProjectFilenameMetadata(title, explicit, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	year := 1920 + int((x>>8)%107)
	return qualificationMovieRow{id: qualificationMovieIDBase + int64(ordinal), random: int64(uint32(x)),
		size: 1<<20 + int64((x>>16)%(8<<30)), modifiedNS: 1_700_000_000_000_000_000 + int64(x%1_000_000_000),
		year: year, metadata: metadata, path: fmt.Sprintf("%s/%s (%d).mkv", qualificationMovieRoot, title, year),
		token:  fmt.Sprintf("qualification-movie-%08x-%07d", seed, ordinal),
		public: qualificationMovieBlob("QMP1", seed, ordinal), incarnation: qualificationMovieBlob("QMM1", seed, ordinal),
		artwork: qualificationMovieBlob("QMA1", seed, ordinal), origin: qualificationMovieBlob("QME1", seed, ordinal),
		assetOrigin: qualificationMovieBlob("QMF1", seed, ordinal), linkOrigin: qualificationMovieBlob("QML1", seed, ordinal)}
}

func qualificationMovieBlob(domain string, seed uint32, ordinal int) (out [16]byte) {
	copy(out[:4], domain)
	binary.BigEndian.PutUint32(out[4:8], seed)
	binary.BigEndian.PutUint64(out[8:], uint64(ordinal))
	return out
}

// Logical hash protocol: length-prefixed shape/schema, then each fixed table
// name, ordered column names and all persisted column values in primary-key
// order. Values are typed (NULL/int64/IEEE754/text/blob); row/table delimiters
// distinguish boundaries. SELECT * intentionally covers canonical defaults and
// generated values, including all incarnations. sqlite_sequence, indexes,
// image bytes and physical SQLite layout are outside this logical digest.
// Workspace is one database row; no million-row materialization.
func qualificationPopulationReadback(t *testing.T, conn *sql.Conn, facts *qualificationPopulationFacts) {
	t.Helper()
	digest := sha256.New()
	h := bufio.NewWriterSize(digest, 64<<10)
	qualificationHashBytes(h, []byte(qualificationMovieShape))
	qualificationHashBytes(h, []byte(schema.SHA256))
	tables := [...]struct {
		name, query string
		count       int64
	}{
		{"libraries", `SELECT * FROM libraries ORDER BY id`, 1},
		{"catalog_libraries", `SELECT * FROM catalog_libraries ORDER BY id`, 1},
		{"catalog_kinds", `SELECT * FROM catalog_kinds ORDER BY id`, 1},
		{"catalog_subjects", `SELECT * FROM catalog_subjects ORDER BY id`, int64(facts.Residents)},
		{"catalog_entities", `SELECT * FROM catalog_entities ORDER BY id`, int64(facts.Residents)},
		{"catalog_assets", `SELECT * FROM catalog_assets ORDER BY id`, int64(facts.Residents)},
		{"catalog_asset_links", `SELECT * FROM catalog_asset_links ORDER BY entity_id,asset_id`, int64(facts.Residents)},
		{"catalog_scalar_contributions", `SELECT * FROM catalog_scalar_contributions ORDER BY parent_id,contributor_id,source`, int64(facts.Residents)},
		{"catalog_asset_contributions", `SELECT * FROM catalog_asset_contributions ORDER BY parent_id,asset_id`, int64(facts.Residents)},
		{"catalog_membership_clock", `SELECT * FROM catalog_membership_clock ORDER BY singleton`, 1},
		{"catalog_age_item_counts", `SELECT * FROM catalog_age_item_counts ORDER BY library_id,kind,block_unrated,admission_age`, 1},
	}
	for _, table := range tables {
		qualificationHashBytes(h, []byte(table.name))
		func() {
			rows, err := conn.QueryContext(t.Context(), table.query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			qualificationHashNumber(h, uint64(len(columns)))
			for _, column := range columns {
				qualificationHashBytes(h, []byte(column))
			}
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			for rows.Next() {
				if err := rows.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				h.WriteByte('R')
				for _, value := range values {
					switch v := value.(type) {
					case nil:
						h.WriteByte('N')
					case int64:
						h.WriteByte('I')
						qualificationHashNumber(h, uint64(v))
					case float64:
						h.WriteByte('F')
						qualificationHashNumber(h, math.Float64bits(v))
					case string:
						h.WriteByte('T')
						qualificationHashBytes(h, []byte(v))
					case []byte:
						h.WriteByte('B')
						qualificationHashBytes(h, v)
					default:
						t.Fatalf("unexpected logical SQLite type %T", value)
					}
				}
				facts.Counts[table.name]++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		}()
		h.WriteByte('E')
		if facts.Counts[table.name] != table.count {
			t.Fatalf("%s rows=%d want=%d", table.name, facts.Counts[table.name], table.count)
		}
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*),min(e.id),max(e.id)
FROM catalog_entities e JOIN catalog_subjects s ON s.id=e.id AND s.public_id=e.public_id AND s.library_id=e.library_id AND s.kind=e.kind
JOIN catalog_asset_links l ON l.entity_id=e.id AND l.library_id=e.library_id
JOIN catalog_assets a ON a.id=l.asset_id AND a.library_id=l.library_id
WHERE e.library_id=7 AND e.kind=1 AND e.retired=0 AND e.missing_since_ms IS NULL AND l.available=1 AND a.available=1`).Scan(
		&facts.Residents, &facts.FirstID, &facts.LastID); err != nil {
		t.Fatal(err)
	}
	facts.Counts["current_movies"] = int64(facts.Residents)
	if int64(facts.Residents) != facts.Counts["catalog_entities"] || facts.FirstID != qualificationMovieIDBase+1 || facts.LastID != qualificationMovieIDBase+int64(facts.Residents) {
		t.Fatal("population current movie/ID mismatch")
	}
	// These explicit zeros are actual absence facts, not readiness evidence.
	absence := [...]struct{ name, query string }{
		{"catalog_identities", `SELECT count(*) FROM catalog_identities`},
		{"inventory_objects", `SELECT count(*) FROM inventory_objects`},
		{"inventory_runs", `SELECT count(*) FROM inventory_runs`},
		{"scan_command_receipts", `SELECT count(*) FROM scan_command_receipts`},
		{"catalog_info_changes", `SELECT count(*) FROM catalog_info_changes`},
		{"asset_streams", `SELECT count(*) FROM asset_streams`},
		{"metadata_evidence", `SELECT count(*) FROM metadata_evidence`},
		{"metadata_publications", `SELECT count(*) FROM metadata_publications`},
		{"enrichment_command_receipts", `SELECT count(*) FROM enrichment_command_receipts`},
		{"catalog_entity_terms", `SELECT count(*) FROM catalog_entity_terms`},
		{"catalog_age_facet_counts", `SELECT count(*) FROM catalog_age_facet_counts`},
		{"catalog_aggregate_publications", `SELECT count(*) FROM catalog_aggregate_publications`},
		{"search_publish_jobs", `SELECT count(*) FROM search_publish_jobs`},
		{"search_publish_receipts", `SELECT count(*) FROM search_publish_receipts`},
		{"search_image_seals", `SELECT count(*) FROM search_image_seals`},
	}
	for _, table := range absence {
		if facts.Version == qualificationMovieSearchShape && (table.name == "search_publish_jobs" || table.name == "search_image_seals") {
			continue // populated by the v1 bank for the search shape
		}
		var n int64
		if err := conn.QueryRowContext(t.Context(), table.query).Scan(&n); err != nil || n != 0 {
			t.Fatalf("population absence %s=%d: %v", table.name, n, err)
		}
		facts.Counts[table.name] = n
		qualificationHashBytes(h, []byte(table.name))
		qualificationHashNumber(h, uint64(n))
	}
	if err := h.Flush(); err != nil {
		t.Fatal(err)
	}
	facts.LogicalSHA256 = hex.EncodeToString(digest.Sum(nil))
	var integrity string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("population integrity check", integrity, err)
	}
	rows, err := conn.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("population foreign key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func qualificationHashBytes(h *bufio.Writer, data []byte) {
	qualificationHashNumber(h, uint64(len(data)))
	h.Write(data)
}

func qualificationHashNumber(h *bufio.Writer, value uint64) {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], value)
	h.Write(data[:])
}

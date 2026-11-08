package readfixture

// Paired canonical search-bank attachment controls. The base catalogue and the
// v1 search bank are written by the same test-only producer shape as the
// canonical qualification population (writer/qualification_population_*), at a
// tiny resident count. These are component checks over real persisted rows and
// the real search preparation protocol; they are not scale, density, 1M or
// qualification evidence.

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"math"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestMain(m *testing.M) { os.Exit(schematest.Run(m)) }

const (
	searchAttachmentResidents = 6
	searchAttachmentIDBase    = int64(1_000_000_000)
	searchAttachmentBatch     = 64
	searchAttachmentLibrary   = int64(7)
	searchAttachmentLibraryID = "qualification-movies"
)

// ErrSearchAttachmentContent names a content-identity refusal, never a source
// byte comparison. The reviewed identity is the shape, residents, counts and
// logical digest the consumer expects.
var ErrSearchAttachmentContent = fmt.Errorf("readfixture: paired search population content mismatch")

type searchAttachmentContentError struct{ Reason string }

func (e *searchAttachmentContentError) Error() string {
	return ErrSearchAttachmentContent.Error() + ": " + e.Reason
}
func (e *searchAttachmentContentError) Unwrap() error { return ErrSearchAttachmentContent }

type searchAttachmentIdentity struct {
	SchemaSHA256  string
	Residents     int64
	Counts        map[string]int64
	LogicalSHA256 string
	Bank          *searchAttachmentBankIdentity
	Statistics    string
}

type searchAttachmentBankIdentity struct {
	EngineVersion      string
	RecipeVersion      string
	UnicodeFingerprint string
	Entities           int64
	People             int64
	Publications       int64
	Jobs               int64
	Fragments          int64
	Seals              int64
	OwnPrefix          int64
	Credits            int64
	LogicalSHA256      string
}

type searchAttachmentBankFacts struct {
	Entities      int64
	People        int64
	Credits       int64
	Publications  int64
	Jobs          int64
	Fragments     int64
	Seals         int64
	OwnPrefix     int64
	Bytes         int64
	LogicalSHA256 string
}

// requireContent is the Go-side copy of the reviewed content compatibility
// (ci/qual_population.py): the recorded shape/residents/counts/logical digest
// and the exact schema are the identity. Test-source bytes never participate.
func (actual searchAttachmentIdentity) requireContent(expected searchAttachmentIdentity) error {
	if actual.SchemaSHA256 != expected.SchemaSHA256 {
		return &searchAttachmentContentError{Reason: "schema SHA-256 differs"}
	}
	if actual.Residents != expected.Residents {
		return &searchAttachmentContentError{Reason: fmt.Sprintf("residents %d != %d", actual.Residents, expected.Residents)}
	}
	if len(actual.Counts) != len(expected.Counts) {
		return &searchAttachmentContentError{Reason: "population count set differs"}
	}
	for name, count := range expected.Counts {
		if actual.Counts[name] != count {
			return &searchAttachmentContentError{Reason: fmt.Sprintf("count %s=%d != %d", name, actual.Counts[name], count)}
		}
	}
	if actual.LogicalSHA256 != expected.LogicalSHA256 {
		return &searchAttachmentContentError{Reason: "base logical digest differs"}
	}
	switch {
	case expected.Bank == nil && actual.Bank != nil:
		return &searchAttachmentContentError{Reason: "unexpected search bank"}
	case expected.Bank != nil && actual.Bank == nil:
		return &searchAttachmentContentError{Reason: "missing search bank"}
	case expected.Bank != nil:
		if *actual.Bank != *expected.Bank {
			return &searchAttachmentContentError{Reason: "search bank identity differs"}
		}
	}
	return nil
}

// newSearchAttachmentFixture writes a tiny canonical catalogue and, when
// wantBank is true, the same optional v1 search bank the canonical population
// producer adds. The returned connection stays open for the paired attach.
func newSearchAttachmentFixture(t *testing.T, wantBank bool) (string, *sql.Conn, searchAttachmentIdentity) {
	t.Helper()
	path := schematest.Clone(t)
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	seedSearchAttachmentPopulation(t, conn)
	identity := searchAttachmentBaseIdentity(t, conn)
	identity.SchemaSHA256 = schema.SHA256
	if wantBank {
		facts := seedSearchAttachmentBank(t, conn)
		bank := searchAttachmentBankState(t, conn)
		if bank.Entities != facts.Entities || bank.People != facts.People || bank.Publications != facts.Publications || bank.Credits != facts.Credits {
			t.Fatal("search bank readback differs from its producer facts")
		}
		identity.Bank = &bank
		identity.Statistics = qualificationSearchAttachmentStatistics
	}
	return path, conn, identity
}

func seedSearchAttachmentPopulation(t *testing.T, conn *sql.Conn) {
	t.Helper()
	tx, err := conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO libraries(id,name,kind,root,playback_origin_incarnation,data_revision,data_order_revision,data_metadata_revision,age_input_revision,age_published_revision)
VALUES(?,?, 'movie','/qualification-media',?,?,?,?,?,?)`, searchAttachmentLibraryID, "Qualification movies", searchAttachmentBlob("QMB1", 0), searchAttachmentResidents+1, searchAttachmentResidents+1, searchAttachmentResidents+1, searchAttachmentResidents+1, searchAttachmentResidents+1)
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(?,'qualification-movies','/qualification-media')`, searchAttachmentLibrary)
	exec(`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	for i := 1; i <= searchAttachmentResidents; i++ {
		id := searchAttachmentIDBase + int64(i)
		public := searchAttachmentBlob("QMP1", i)
		incarnation := searchAttachmentBlob("QMM1", i)
		artwork := searchAttachmentBlob("QMA1", i)
		origin := searchAttachmentBlob("QME1", i)
		assetOrigin := searchAttachmentBlob("QMF1", i)
		linkOrigin := searchAttachmentBlob("QML1", i)
		title := fmt.Sprintf("The Étoile 東京 Orbit %07d", i)
		exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,?,1)`, id, public, searchAttachmentLibrary)
		exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_title,metadata_language,
sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,year,added_at,
metadata_incarnation,artwork_incarnation,playback_origin_incarnation,playback_origin_revision,
file_size_key,resolution_key,admission_allow_age) VALUES(?,?,?,1,?,?,'en',?,?,?,?,?,?,'2026-11-01T00:00:00.000000000Z',?,?,?,1,?,0,-1)`,
			id, public, searchAttachmentLibrary, title, title, title, title, title, title, i, i, incarnation, artwork, origin, int64(i))
		exec(`INSERT INTO catalog_assets(id,token,library_id,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,playback_origin_incarnation)
VALUES(?,?,?,?,?,?,'','','',0,0,0,?)`, id, fmt.Sprintf("attachment-movie-%07d", i), searchAttachmentLibrary, fmt.Sprintf("/qualification-media/%s (%d).mkv", title, 1920+i), 1<<20+int64(i), int64(1_700_000_000_000_000_000+i), assetOrigin)
		exec(`INSERT INTO catalog_asset_links(library_id,entity_id,asset_id,playback_origin_incarnation) VALUES(?,?,?,?)`, searchAttachmentLibrary, id, id, linkOrigin)
		exec(`INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence) VALUES(?,?,?,0,1,?)`, searchAttachmentLibrary, id, id, i)
		exec(`INSERT INTO catalog_asset_contributions(library_id,parent_id,asset_id,membership_refs,authored_refs,file_size,file_size_revision) VALUES(?,?,?,1,0,?,1)`, searchAttachmentLibrary, id, id, 1<<20+int64(i))
	}
	exec(`INSERT INTO catalog_membership_clock(singleton,sequence) VALUES(1,?)`, searchAttachmentResidents)
	exec(`INSERT INTO catalog_age_item_counts(library_id,kind,block_unrated,admission_age,total) VALUES(7,1,0,-1,?)`, searchAttachmentResidents)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

const qualificationSearchAttachmentStatistics = "not analyzed: producer runs no ANALYZE; bank leaves SQLite statistics absent"

// seedSearchAttachmentBank is the optional canonical search-bank content copied
// from the merged producer (writer/qualification_population_search_bank_test.go),
// narrowed to one bounded connection. Every row is derived through the real
// search.PrepareTitle/PrepareFragments/PrepareManifest protocol.
func seedSearchAttachmentBank(t *testing.T, conn *sql.Conn) searchAttachmentBankFacts {
	t.Helper()
	profile := searchpolicy.Compiled()
	var occupied bool
	if err := conn.QueryRowContext(t.Context(), `SELECT
EXISTS(SELECT 1 FROM search_publications) OR EXISTS(SELECT 1 FROM search_publish_jobs) OR
EXISTS(SELECT 1 FROM search_fragments) OR EXISTS(SELECT 1 FROM search_image_seals) OR
EXISTS(SELECT 1 FROM search_own_prefix) OR EXISTS(SELECT 1 FROM search_reclaim_jobs) OR
EXISTS(SELECT 1 FROM search_generation_receipts) OR EXISTS(SELECT 1 FROM catalog_people) OR
EXISTS(SELECT 1 FROM catalog_credit_images) OR EXISTS(SELECT 1 FROM catalog_credits)`).Scan(&occupied); err != nil || occupied {
		t.Fatal("search bank requires empty search/People/credit custody", occupied, err)
	}
	facts := searchAttachmentBankFacts{}
	digest := sha256.New()
	searchAttachmentHash(digest, "canonical-search-v1", profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint)
	queries := [...]string{
		`INSERT INTO search_publications(id,entity_id,person_id,search_generation_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title,title_fold,title_prefix_tokens,initial_prefix_tokens,person_name_tokens) VALUES(?,?,?,1,?,?,1,?,?,?,?,?,?,?,?)`,
		`INSERT INTO search_publish_jobs(id,entity_id,person_id,search_generation_id,published_generation,published_document_id,input_generation,source_incarnation,source_revision,recipe_version,phase,document_id,expected_fragments,staged_fragments,staged_bytes,command_incarnation) VALUES(?,?,?,1,1,?,1,?,?,?,'idle',?,?,?,?,?)`,
		`INSERT INTO search_fragments(id,owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(?,?,?,1,?,?,?,?,?,1,1,?)`,
		`INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(?,?)`,
		`INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(?,?)`,
		`INSERT INTO search_fragment_grams(rowid,normalized) VALUES(?,?)`,
		`INSERT INTO search_image_seals(publication_id,algorithm,seed,expected_digest,expected_fragments,expected_bytes,staged_fragments,staged_bytes,verified_fragments,verified_bytes,chain,after_field,after_ordinal,after_fragment_id,sealed) VALUES(?,?,?,?,?,?,?,?,?,?,?,1,?,?,1)`,
		`INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)`,
		`INSERT INTO catalog_people_names(rowid,name) VALUES(?,?)`,
	}
	prepared := make([]*sql.Stmt, len(queries))
	for i, q := range queries {
		statement, err := conn.PrepareContext(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		prepared[i] = statement
		defer statement.Close()
	}
	exec := func(tx *sql.Tx, q string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(t.Context(), q, args...); err != nil {
			t.Fatal(err)
		}
	}
	transaction := func(body func(*sql.Tx, []*sql.Stmt)) {
		tx, err := conn.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		bound := make([]*sql.Stmt, len(prepared))
		for i, p := range prepared {
			bound[i] = tx.StmtContext(t.Context(), p)
			defer bound[i].Close()
		}
		body(tx, bound)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	transaction(func(tx *sql.Tx, _ []*sql.Stmt) {
		exec(tx, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,phase,created_ms,command_incarnation,captured_input_sequence) VALUES(1,'v1',?,?,?,'active','idle',0,?,0) ON CONFLICT(id) DO UPDATE SET recipe_version=excluded.recipe_version,engine_version=excluded.engine_version,unicode_fingerprint=excluded.unicode_fingerprint,state='active',phase='idle',pending_subjects=0,captured_input_sequence=0`, profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint, searchAttachmentBlob("QSEB1", 0))
		exec(tx, `INSERT INTO search_revision(singleton,revision,active_generation_id,building_generation_id,input_sequence) VALUES(1,1,1,NULL,0) ON CONFLICT(singleton) DO UPDATE SET revision=1,active_generation_id=1,building_generation_id=NULL,input_sequence=0`)
		// Exact rows the merged optional producer writes. A paired attach reuses
		// this dictionary and never assigns another meaning to these ids.
		exec(tx, `INSERT OR IGNORE INTO catalog_credit_labels(id,label) VALUES(1,'Actor'),(2,'Acting')`)
		for i := int64(1); i <= int64(min(searchAttachmentResidents, 8)); i++ {
			name := fmt.Sprintf("Qualification performer %02d", i)
			exec(tx, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,search_incarnation) VALUES(?,?,?,?,?,?,?)`, i, fmt.Sprintf("qualification-person-%d", i), fmt.Sprintf("qualification-person-%d", i), name, name, searchAttachmentBlob("QSEA1", int(i)), searchAttachmentBlob("QSES1", int(i+16)))
		}
	})
	var after, publication int64
	for {
		rows, err := conn.QueryContext(t.Context(), `SELECT id,library_id,kind,revision,title,metadata_incarnation FROM catalog_entities WHERE retired=0 AND id>? ORDER BY id LIMIT ?`, after, int64(searchAttachmentBatch))
		if err != nil {
			t.Fatal(err)
		}
		type source struct {
			id, library, kind, revision int64
			title                       string
			incarnation                 [16]byte
		}
		page := make([]source, 0, searchAttachmentBatch)
		for rows.Next() {
			var item source
			var inc []byte
			if err := rows.Scan(&item.id, &item.library, &item.kind, &item.revision, &item.title, &inc); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if len(inc) != 16 || item.revision <= 0 {
				rows.Close()
				t.Fatal("invalid native search source")
			}
			copy(item.incarnation[:], inc)
			page = append(page, item)
		}
		err = rows.Err()
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		transaction(func(tx *sql.Tx, stmts []*sql.Stmt) {
			for _, item := range page {
				publication++
				searchAttachmentImage(t, stmts, digest, &facts, search.ManifestEntity, publication, item.id, item.library, item.kind, item.revision, item.title, item.incarnation[:])
				facts.Entities++
				facts.Publications++
				if (facts.Entities-1)%128 == 0 {
					person := 1 + ((facts.Entities-1)/128)%int64(min(searchAttachmentResidents, 8))
					exec(tx, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) VALUES(?,?,?,?,1,1,1,?,0,0,'en',zeroblob(32),zeroblob(32),'qualification-fixture',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),?,1,zeroblob(32))`, publication, item.id, item.incarnation[:], item.revision, searchAttachmentBlob("QSEP1", 0), searchAttachmentBlob("QSEC1", int(publication)))
					exec(tx, `UPDATE catalog_entities SET credit_image_id=? WHERE id=?`, publication, item.id)
					exec(tx, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(?,?,0,?,1,2,'','qualification','credit',0)`, publication, item.id, person)
					searchAttachmentHash(digest, "credit", strconv.FormatInt(item.id, 10), strconv.FormatInt(person, 10))
					facts.Credits++
				}
			}
		})
		after = page[len(page)-1].id
	}
	transaction(func(tx *sql.Tx, stmts []*sql.Stmt) {
		for i := int64(1); i <= int64(min(searchAttachmentResidents, 8)); i++ {
			publication++
			name := fmt.Sprintf("Qualification performer %02d", i)
			searchAttachmentImage(t, stmts, digest, &facts, search.ManifestPerson, publication, i, 0, 0, 1, name, searchAttachmentBlob("QSES1", int(i+16)))
			facts.People++
			facts.Publications++
		}
		exec(tx, `UPDATE search_generations SET indexed_rows=?,indexed_bytes=?,after_entity_id=?,after_person_id=? WHERE id=1`, facts.Fragments, facts.Bytes, after, facts.People)
	})
	if facts.Entities != int64(searchAttachmentResidents) {
		t.Fatal("search bank resident walk changed", facts.Entities, searchAttachmentResidents)
	}
	facts.LogicalSHA256 = hex.EncodeToString(digest.Sum(nil))
	return facts
}

func searchAttachmentImage(t *testing.T, stmts []*sql.Stmt, digest hash.Hash, facts *searchAttachmentBankFacts, subject search.ManifestSubject, publication, id, library, kind, revision int64, title string, incarnation []byte) {
	t.Helper()
	plan, err := search.PrepareFragments(title, search.DefaultFragmentBudget())
	if err != nil || plan.Len() == 0 {
		t.Fatalf("search image %d preparation: %v", publication, err)
	}
	rows := make([]search.ManifestRow, plan.Len())
	for i := range rows {
		rows[i] = plan.At(i).ManifestRow(1, int64(i), strconv.FormatInt(revision, 10))
	}
	profile := searchpolicy.Compiled()
	var inc [16]byte
	copy(inc[:], incarnation)
	m, err := search.PrepareManifest(search.ManifestInput{Subject: subject, SubjectID: id, LibraryID: library, Kind: kind, SearchGenerationID: 1, BankKey: "v1", Recipe: profile.RecipeVersion, EngineVersion: profile.EngineVersion, UnicodeFingerprint: profile.UnicodeFingerprint, InputGeneration: 1, SourceRevision: revision, SourceIncarnation: inc}, rows)
	if err != nil {
		t.Fatalf("search image %d manifest: %v", publication, err)
	}
	prepared := search.PrepareTitle(title)
	var entity, person, lib, kindValue any
	personTokens := ""
	if subject == search.ManifestEntity {
		entity, lib, kindValue = id, library, kind
	} else {
		person = id
		personTokens = search.PreparePersonName(title)
	}
	write := func(i int, args ...any) {
		t.Helper()
		if _, err := stmts[i].ExecContext(t.Context(), args...); err != nil {
			t.Fatalf("search image %d statement %d: %v", publication, i, err)
		}
	}
	write(0, publication, entity, person, lib, kindValue, inc[:], revision, profile.RecipeVersion, title, prepared.Fold(), prepared.PrefixTokens(), prepared.InitialTokens(), personTokens)
	write(1, publication, entity, person, publication, inc[:], revision, profile.RecipeVersion, publication, m.Count(), m.Count(), m.Bytes(), searchAttachmentBlob("QSEJ1", int(publication)))
	for _, row := range rows {
		facts.Fragments++
		write(2, facts.Fragments, entity, person, row.Ordinal, row.Revision, row.Prefix, row.Lexical, row.Normalized, publication)
		write(3, facts.Fragments, row.Prefix)
		write(4, facts.Fragments, row.Lexical)
		write(5, facts.Fragments, row.Normalized)
	}
	seed, chain := m.Seed(), m.Digest()
	write(6, publication, search.ManifestAlgorithm, seed[:], chain[:], m.Count(), m.Bytes(), m.Count(), m.Bytes(), m.Count(), m.Bytes(), chain[:], int64(plan.Len()-1), facts.Fragments)
	if subject == search.ManifestEntity {
		write(7, id, prepared.PrefixTokens(), prepared.InitialTokens())
		facts.OwnPrefix++
	} else {
		write(8, id, personTokens)
	}
	facts.Seals++
	facts.Jobs++
	facts.Bytes += m.Bytes()
	searchAttachmentHash(digest, strconv.FormatInt(publication, 10), string(seed[:]), string(chain[:]), title, prepared.Fold(), prepared.PrefixTokens(), prepared.InitialTokens(), personTokens)
}

func searchAttachmentBlob(domain string, ordinal int) []byte {
	var out [16]byte
	copy(out[:4], domain)
	binary.BigEndian.PutUint64(out[8:], uint64(ordinal))
	return out[:]
}

func searchAttachmentHash(h hash.Hash, fields ...string) {
	var size [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		h.Write(size[:])
		h.Write([]byte(field))
	}
}

// searchAttachmentBaseIdentity digests the base population slice only: the
// canonical library, kinds and its residents. The foreground overlay lives in
// library 1 and must not change this identity.
func searchAttachmentBaseIdentity(t *testing.T, conn *sql.Conn) searchAttachmentIdentity {
	t.Helper()
	digest := sha256.New()
	h := bufio.NewWriterSize(digest, 64<<10)
	searchAttachmentHashBytes(h, []byte("canonical-movies-v1"))
	searchAttachmentHashBytes(h, []byte(schema.SHA256))
	tables := [...]struct {
		name, query string
	}{
		{"libraries", `SELECT * FROM libraries WHERE id='qualification-movies' ORDER BY id`},
		{"catalog_libraries", `SELECT * FROM catalog_libraries WHERE id=7 ORDER BY id`},
		{"catalog_kinds", `SELECT * FROM catalog_kinds WHERE id=1 ORDER BY id`},
		{"catalog_subjects", `SELECT * FROM catalog_subjects WHERE library_id=7 ORDER BY id`},
		{"catalog_entities", `SELECT * FROM catalog_entities WHERE library_id=7 ORDER BY id`},
		{"catalog_assets", `SELECT * FROM catalog_assets WHERE library_id=7 ORDER BY id`},
		{"catalog_asset_links", `SELECT * FROM catalog_asset_links WHERE library_id=7 ORDER BY entity_id,asset_id`},
		{"catalog_scalar_contributions", `SELECT * FROM catalog_scalar_contributions WHERE library_id=7 ORDER BY parent_id,contributor_id,source`},
		{"catalog_asset_contributions", `SELECT * FROM catalog_asset_contributions WHERE library_id=7 ORDER BY parent_id,asset_id`},
		{"catalog_membership_clock", `SELECT * FROM catalog_membership_clock ORDER BY singleton`},
		{"catalog_age_item_counts", `SELECT * FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 ORDER BY library_id,kind,block_unrated,admission_age`},
	}
	counts := make(map[string]int64)
	for _, table := range tables {
		searchAttachmentHashBytes(h, []byte(table.name))
		rows, err := conn.QueryContext(t.Context(), table.query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		searchAttachmentHashNumber(h, uint64(len(columns)))
		for _, column := range columns {
			searchAttachmentHashBytes(h, []byte(column))
		}
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		for rows.Next() {
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			h.WriteByte('R')
			for _, value := range values {
				switch v := value.(type) {
				case nil:
					h.WriteByte('N')
				case int64:
					h.WriteByte('I')
					searchAttachmentHashNumber(h, uint64(v))
				case float64:
					h.WriteByte('F')
					searchAttachmentHashNumber(h, math.Float64bits(v))
				case string:
					h.WriteByte('T')
					searchAttachmentHashBytes(h, []byte(v))
				case []byte:
					h.WriteByte('B')
					searchAttachmentHashBytes(h, v)
				default:
					rows.Close()
					t.Fatalf("unexpected logical SQLite type %T", value)
				}
			}
			counts[table.name]++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		h.WriteByte('E')
	}
	if err := h.Flush(); err != nil {
		t.Fatal(err)
	}
	var residents int64
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_entities WHERE library_id=7 AND kind=1 AND retired=0`).Scan(&residents); err != nil {
		t.Fatal(err)
	}
	counts["current_movies"] = residents
	return searchAttachmentIdentity{SchemaSHA256: schema.SHA256, Residents: residents, Counts: counts, LogicalSHA256: hex.EncodeToString(digest.Sum(nil))}
}

func searchAttachmentHashBytes(h *bufio.Writer, data []byte) {
	searchAttachmentHashNumber(h, uint64(len(data)))
	h.Write(data)
}

func searchAttachmentHashNumber(h *bufio.Writer, value uint64) {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], value)
	h.Write(data[:])
}

func searchAttachmentBankState(t *testing.T, conn *sql.Conn) searchAttachmentBankIdentity {
	t.Helper()
	profile := searchpolicy.Compiled()
	var identity searchAttachmentBankIdentity
	var indexedRows, indexedBytes int64
	if err := conn.QueryRowContext(t.Context(), `SELECT recipe_version,engine_version,unicode_fingerprint,indexed_rows,indexed_bytes FROM search_generations WHERE id=1 AND bank_key='v1'`).Scan(&identity.RecipeVersion, &identity.EngineVersion, &identity.UnicodeFingerprint, &indexedRows, &indexedBytes); err != nil {
		t.Fatal(err)
	}
	if identity.RecipeVersion != profile.RecipeVersion || identity.EngineVersion != profile.EngineVersion || identity.UnicodeFingerprint != profile.UnicodeFingerprint {
		t.Fatal("search bank engine/fingerprint differs from the compiled profile")
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT
(SELECT count(*) FROM search_publications WHERE entity_id IS NOT NULL),
(SELECT count(*) FROM search_publications WHERE person_id IS NOT NULL),
(SELECT count(*) FROM search_publications),
(SELECT count(*) FROM search_publish_jobs),
(SELECT count(*) FROM search_fragments),
(SELECT count(*) FROM search_image_seals WHERE sealed=1),
(SELECT count(*) FROM search_own_prefix_docsize),
(SELECT count(*) FROM catalog_credits WHERE entity_id IN (SELECT entity_id FROM search_publications WHERE entity_id IS NOT NULL))`).Scan(&identity.Entities, &identity.People, &identity.Publications, &identity.Jobs, &identity.Fragments, &identity.Seals, &identity.OwnPrefix, &identity.Credits); err != nil {
		t.Fatal(err)
	}
	if identity.Fragments != indexedRows || indexedBytes <= 0 {
		t.Fatal("search bank indexed rows/bytes mismatch")
	}
	identity.LogicalSHA256 = searchAttachmentBankDigest(t, conn)
	return identity
}

func searchAttachmentBankDigest(t *testing.T, conn *sql.Conn) string {
	t.Helper()
	tables := [...]struct{ name, query string }{
		{"search_generations", `SELECT * FROM search_generations ORDER BY id`},
		{"search_revision", `SELECT * FROM search_revision ORDER BY singleton`},
		{"search_publications", `SELECT * FROM search_publications ORDER BY id`},
		{"search_publish_jobs", `SELECT * FROM search_publish_jobs ORDER BY id`},
		{"search_fragments", `SELECT * FROM search_fragments ORDER BY id`},
		{"search_image_seals", `SELECT * FROM search_image_seals ORDER BY publication_id`},
		{"search_own_prefix", `SELECT rowid,title_prefix_tokens,initial_prefix_tokens FROM search_own_prefix ORDER BY rowid`},
		{"search_fragment_prefix", `SELECT rowid,prefix_tokens FROM search_fragment_prefix ORDER BY rowid`},
		{"search_fragment_lexical", `SELECT rowid,lexical_tokens FROM search_fragment_lexical ORDER BY rowid`},
		{"search_fragment_grams", `SELECT rowid,normalized FROM search_fragment_grams ORDER BY rowid`},
		{"catalog_people_names", `SELECT rowid,name FROM catalog_people_names ORDER BY rowid`},
	}
	digest := sha256.New()
	h := bufio.NewWriterSize(digest, 64<<10)
	for _, table := range tables {
		searchAttachmentHashBytes(h, []byte(table.name))
		rows, err := conn.QueryContext(t.Context(), table.query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		searchAttachmentHashNumber(h, uint64(len(columns)))
		for _, column := range columns {
			searchAttachmentHashBytes(h, []byte(column))
		}
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		for rows.Next() {
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			h.WriteByte('R')
			for _, value := range values {
				switch v := value.(type) {
				case nil:
					h.WriteByte('N')
				case int64:
					h.WriteByte('I')
					searchAttachmentHashNumber(h, uint64(v))
				case string:
					h.WriteByte('T')
					searchAttachmentHashBytes(h, []byte(v))
				case []byte:
					h.WriteByte('B')
					searchAttachmentHashBytes(h, v)
				default:
					rows.Close()
					t.Fatalf("unexpected logical SQLite type %T", value)
				}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		h.WriteByte('E')
	}
	if err := h.Flush(); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// attachPairedSearchForeground is the consumer gate: content identity first,
// then the unchanged foreground attach. It never compares producer source bytes
// and never rebuilds the catalogue or bank.
func attachPairedSearchForeground(t *testing.T, path string, conn *sql.Conn, expected searchAttachmentIdentity) (*Source, error) {
	t.Helper()
	// The recorded base logical digest is a producer-side content address of the
	// pre-bank seed. The bank legitimately claims catalog_entities.credit_image_id
	// rows, so the consumer checks the unchanged shape/residents/counts and the
	// bank identity, then the attach's own source witness check.
	base := searchAttachmentBaseIdentity(t, conn)
	if base.Residents != expected.Residents || len(base.Counts) != len(expected.Counts) {
		return nil, &searchAttachmentContentError{Reason: "base shape differs from the reviewed identity"}
	}
	for name, count := range expected.Counts {
		if base.Counts[name] != count {
			return nil, &searchAttachmentContentError{Reason: fmt.Sprintf("count %s=%d != %d", name, base.Counts[name], count)}
		}
	}
	if expected.Bank == nil {
		var banked int64
		if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM search_publications`).Scan(&banked); err != nil {
			return nil, err
		}
		if banked != 0 {
			return nil, &searchAttachmentContentError{Reason: "unexpected search bank"}
		}
	} else {
		actual := searchAttachmentBankState(t, conn)
		if actual != *expected.Bank {
			return nil, &searchAttachmentContentError{Reason: "search bank identity differs"}
		}
	}
	return AttachForegroundOn(t, path, conn, searchAttachmentLibraryID)
}

func TestSearchAttachmentPairedForegroundAttach(t *testing.T) {
	path, conn, identity := newSearchAttachmentFixture(t, true)
	baseline := searchAttachmentBaseIdentity(t, conn)
	before := searchAttachmentBankState(t, conn)
	var revisionBefore int64
	if err := conn.QueryRowContext(t.Context(), `SELECT revision FROM search_revision WHERE singleton=1`).Scan(&revisionBefore); err != nil {
		t.Fatal(err)
	}
	source, err := attachPairedSearchForeground(t, path, conn, identity)
	if err != nil {
		t.Fatal(err)
	}
	receipt := source.CreditReceipt()
	if receipt.CreditImageID <= 1 {
		t.Fatalf("credit image was not moved off the bank's reserved id: %+v", receipt)
	}
	want := map[int64]string{1: "Actor", 2: "Acting", 3: "Director", 4: "Directing"}
	if len(receipt.Resolutions) != len(want) {
		t.Fatalf("receipt resolutions: %+v", receipt.Resolutions)
	}
	for _, resolution := range receipt.Resolutions {
		if want[resolution.FixtureID] != resolution.Label {
			t.Fatalf("receipt label: %+v", resolution)
		}
		var label string
		if err := conn.QueryRowContext(t.Context(), `SELECT label FROM catalog_credit_labels WHERE id=?`, resolution.ActualID).Scan(&label); err != nil || label != resolution.Label {
			t.Fatalf("receipt id %d label %q: %v", resolution.ActualID, label, err)
		}
	}
	var dictionary string
	rows, err := conn.QueryContext(t.Context(), `SELECT id,label FROM catalog_credit_labels ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for rows.Next() {
		var id int64
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		labels = append(labels, fmt.Sprintf("%d:%s\n", id, label))
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(labels)
	for _, line := range labels {
		dictionary += line
	}
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(dictionary))); got != receipt.DictionarySHA256 {
		t.Fatalf("dictionary receipt %s != actual %s", receipt.DictionarySHA256, got)
	}
	// Post-attach native query oracle: the bank's real FTS rows still answer.
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM search_fragment_lexical WHERE search_fragment_lexical MATCH ?`, `"0000003"`).Scan(new(int64)); err != nil {
		t.Fatal("native query after attach", err)
	}
	var hits int64
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM search_fragment_lexical WHERE search_fragment_lexical MATCH 'orbit'`).Scan(&hits); err != nil || hits != searchAttachmentResidents {
		t.Fatal("native orbit hits", hits, err)
	}
	// Role oracle: the fixture's own credits use the resolved meanings; the
	// bank's pre-existing credit keeps its exact role.
	var actors, directors, acting, directing, bankCredits int64
	if err := conn.QueryRowContext(t.Context(), `SELECT
(SELECT count(*) FROM catalog_credits c JOIN catalog_credit_labels l ON l.id=c.role_id WHERE c.entity_id=1 AND l.label='Actor'),
(SELECT count(*) FROM catalog_credits c JOIN catalog_credit_labels l ON l.id=c.role_id WHERE c.entity_id=1 AND l.label='Director'),
(SELECT count(*) FROM catalog_credits c JOIN catalog_credit_labels l ON l.id=c.department_id WHERE c.entity_id=1 AND l.label='Acting'),
(SELECT count(*) FROM catalog_credits c JOIN catalog_credit_labels l ON l.id=c.department_id WHERE c.entity_id=1 AND l.label='Directing'),
(SELECT count(*) FROM catalog_credits c JOIN catalog_credit_labels l ON l.id=c.role_id WHERE c.entity_id=? AND l.label='Actor')`, searchAttachmentIDBase+1).Scan(&actors, &directors, &acting, &directing, &bankCredits); err != nil {
		t.Fatal("role oracle", err)
	}
	if actors != 100 || directors != 50 || acting != 100 || directing != 50 || bankCredits != 1 {
		t.Fatalf("role oracle actors=%d directors=%d acting=%d directing=%d bank=%d", actors, directors, acting, directing, bankCredits)
	}
	// The base population slice and the bank keep the same identity.
	after := searchAttachmentBaseIdentity(t, conn)
	after.SchemaSHA256 = schema.SHA256
	after.Bank = nil
	if after.LogicalSHA256 != baseline.LogicalSHA256 || after.Residents != baseline.Residents {
		t.Fatal("paired attach changed the base population identity")
	}
	afterBank := searchAttachmentBankState(t, conn)
	if afterBank != before {
		t.Fatalf("paired attach changed the search bank:\nbefore %+v\nafter  %+v", before, afterBank)
	}
	var revisionAfter int64
	if err := conn.QueryRowContext(t.Context(), `SELECT revision FROM search_revision WHERE singleton=1`).Scan(&revisionAfter); err != nil || revisionAfter != revisionBefore {
		t.Fatal("search revision changed", revisionAfter, revisionBefore, err)
	}
}

func TestSearchAttachmentRefusesConflictingCreditLabel(t *testing.T) {
	_, conn, _ := newSearchAttachmentFixture(t, true)
	// A reserved id with a different meaning is a refusal, not a remap: the
	// fixture must never reinterpret an existing credit label.
	if _, err := conn.ExecContext(t.Context(), `UPDATE catalog_credit_labels SET label='Director' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	var entities int64
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_entities`).Scan(&entities); err != nil {
		t.Fatal(err)
	}
	_, err := AttachForegroundOn(t, "ignored", conn, searchAttachmentLibraryID)
	if err == nil {
		t.Fatal("attached despite a conflicting reserved credit label")
	}
	if !errors.Is(err, ErrCreditLabelCollision) {
		t.Fatalf("not a typed credit label collision: %v", err)
	}
	var collision *CreditLabelCollisionError
	if !errors.As(err, &collision) || collision.ID != 2 || collision.Existing != "Director" || collision.Required != "Acting" {
		t.Fatalf("collision detail: %+v", collision)
	}
	var label string
	if err := conn.QueryRowContext(t.Context(), `SELECT label FROM catalog_credit_labels WHERE id=2`).Scan(&label); err != nil || label != "Director" {
		t.Fatal("refusal changed the existing label", label, err)
	}
	var after int64
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_entities`).Scan(&after); err != nil || after != entities {
		t.Fatal("refusal added foreground rows", after, entities, err)
	}
}

func TestSearchAttachmentRemapsExactLabels(t *testing.T) {
	path, conn, _ := newSearchAttachmentFixture(t, false)
	// The same meanings at non-reserved ids are reused exactly; the fixture
	// never inserts a duplicate meaning at its own reserved ids.
	for id, label := range map[int64]string{5: "Actor", 6: "Acting", 7: "Director", 8: "Directing"} {
		if _, err := conn.ExecContext(t.Context(), `INSERT INTO catalog_credit_labels(id,label) VALUES(?,?)`, id, label); err != nil {
			t.Fatal(err)
		}
	}
	source, err := AttachForegroundOn(t, path, conn, searchAttachmentLibraryID)
	if err != nil {
		t.Fatal(err)
	}
	receipt := source.CreditReceipt()
	if len(receipt.Resolutions) != 4 {
		t.Fatalf("resolutions: %+v", receipt.Resolutions)
	}
	want := map[int64]int64{1: 5, 2: 6, 3: 7, 4: 8}
	for _, resolution := range receipt.Resolutions {
		if want[resolution.FixtureID] != resolution.ActualID || resolution.Inserted {
			t.Fatalf("remap: %+v", resolution)
		}
	}
	var actors, directors int64
	if err := conn.QueryRowContext(t.Context(), `SELECT
(SELECT count(*) FROM catalog_credits c WHERE c.entity_id=1 AND c.role_id=5),
(SELECT count(*) FROM catalog_credits c WHERE c.entity_id=1 AND c.role_id=7)`).Scan(&actors, &directors); err != nil {
		t.Fatal(err)
	}
	if actors != 100 || directors != 50 {
		t.Fatalf("remapped role counts actors=%d directors=%d", actors, directors)
	}
	var reserved int64
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_credit_labels WHERE id IN (1,2,3,4)`).Scan(&reserved); err != nil || reserved != 0 {
		t.Fatal("fixture inserted reserved ids beside exact meanings", reserved, err)
	}
}

func TestSearchAttachmentRefusesStaleBankCatalogue(t *testing.T) {
	_, conn, _ := newSearchAttachmentFixture(t, true)
	// Same bank bytes, changed catalogue source witness: the attach must refuse.
	if _, err := conn.ExecContext(t.Context(), `UPDATE catalog_entities SET title=title||' changed' WHERE id=?`, searchAttachmentIDBase+2); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachForegroundOn(t, "ignored", conn, searchAttachmentLibraryID); !errors.Is(err, ErrSearchBankStale) {
		t.Fatalf("attach did not refuse stale bank/catalogue: %v", err)
	}
}

func TestSearchAttachmentContentIdentityNotSourceBytes(t *testing.T) {
	_, plainConn, plain := newSearchAttachmentFixture(t, false)
	_, _, bank := newSearchAttachmentFixture(t, true)
	if plain.LogicalSHA256 != bank.LogicalSHA256 || plain.Residents != bank.Residents {
		t.Fatal("search shape changed the reviewed base population content identity")
	}
	if bank.Bank == nil || plain.Bank != nil {
		t.Fatal("bank identity presence")
	}
	same := searchAttachmentBaseIdentity(t, plainConn)
	if err := same.requireContent(plain); err != nil {
		t.Fatal("identical content refused", err)
	}
	changedOwner := plain
	changedOwner.LogicalSHA256 = fmt.Sprintf("%064d", 1)
	if err := same.requireContent(changedOwner); !errors.Is(err, ErrSearchAttachmentContent) {
		t.Fatalf("changed content accepted: %v", err)
	}
	if _, err := plainConn.ExecContext(t.Context(), `UPDATE catalog_entities SET title=title||' x' WHERE id=?`, searchAttachmentIDBase+1); err != nil {
		t.Fatal(err)
	}
	changed := searchAttachmentBaseIdentity(t, plainConn)
	if err := changed.requireContent(plain); !errors.Is(err, ErrSearchAttachmentContent) {
		t.Fatalf("real content change accepted: %v", err)
	}
}

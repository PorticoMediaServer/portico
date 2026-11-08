package read

// Test-only v1 search bank content for the canonical qualification population.
// Every publication, posting, fold and manifest is derived through the real
// search.PrepareTitle/PrepareFragments/PrepareManifest protocol over the exact
// catalogue rows the producer persisted. There is no reduced bank, hand-built
// token string, DDL overlay or accepting-command authority here: this is
// population content for the merged READ path, not acceptance proof.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// qualificationSearchBankFacts are actual persisted bank counts plus the
// builder's deterministic digest and stated statistics policy. They are not
// readiness, density or qualification evidence.
type qualificationSearchBankFacts struct {
	Version       string               `json:"version"`
	Profile       searchpolicy.Profile `json:"profile"`
	Entities      int64                `json:"entities"`
	People        int64                `json:"people"`
	Credits       int64                `json:"credits"`
	Publications  int64                `json:"search_publications"`
	Jobs          int64                `json:"search_publish_jobs"`
	Fragments     int64                `json:"search_fragments"`
	Bytes         int64                `json:"prepared_bytes"`
	OwnPrefix     int64                `json:"search_own_prefix_docsize"`
	Seals         int64                `json:"search_image_seals"`
	LogicalSHA256 string               `json:"logical_sha256"`
	Statistics    string               `json:"statistics"`
}

const qualificationSearchBankBatch = 256

const qualificationSearchBankStatistics = "bank builder leaves SQLite statistics absent; the producer must state its final ANALYZE policy before export"

type qualificationSearchSource struct {
	id, library, kind, revision int64
	title                       string
	incarnation                 [16]byte
}

// seedQualificationSearchBank requires exclusive custody of the producer's
// canonical executor after its catalogue seed. It refuses populated
// search/People/credit state rather than erasing historical or native custody.
// One empty bootstrap v1 generation/revision may be replaced in place. Source
// titles, revisions and incarnations are read back from the catalogue, never
// reconstructed from the seeder's generator.
//
// Content: one active v1 generation and revision, then for every current
// resident one published own-title image (field 1), its idle job, all prepared
// fragments with their three native rows, the own-prefix postings and one
// sealed manifest. A People subset (catalog_people + person images + names) and
// an owner-only credit on the first and every 128th resident cover the
// CHANGE-013 People/credit read paths. Every resident is included.
func seedQualificationSearchBank(t *testing.T, conn *sql.Conn) qualificationSearchBankFacts {
	t.Helper()
	profile := searchpolicy.Compiled()
	occupied, err := qualificationSearchBankOccupied(t.Context(), conn)
	if err != nil {
		t.Fatal("search bank state", err)
	}
	if occupied {
		t.Fatal("search qualification builder requires empty search/People/credit custody")
	}
	var residents int64
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM catalog_entities WHERE retired=0`).Scan(&residents); err != nil || residents < 1 || residents > 1_000_000 {
		t.Fatal("search qualification residents must be 1..1000000", residents, err)
	}
	facts := qualificationSearchBankFacts{Version: "canonical-search-v1", Profile: profile, Statistics: qualificationSearchBankStatistics}
	digest := sha256.New()
	qualificationSearchHash(digest, facts.Version, profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint)

	// Fixed SQL, prepared once on the owner's connection and rebound to each
	// bounded transaction. No caller SQL text, arbitrary identifier or hook.
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
		prepared[i], err = conn.PrepareContext(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared[i].Close()
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
		exec(tx, `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,phase,created_ms,command_incarnation,captured_input_sequence) VALUES(1,'v1',?,?,?,'active','idle',0,?,0) ON CONFLICT(id) DO UPDATE SET recipe_version=excluded.recipe_version,engine_version=excluded.engine_version,unicode_fingerprint=excluded.unicode_fingerprint,state='active',phase='idle',pending_subjects=0,captured_input_sequence=0`, profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint, qualificationSearchBirth(0))
		exec(tx, `INSERT INTO search_revision(singleton,revision,active_generation_id,building_generation_id,input_sequence) VALUES(1,1,1,NULL,0) ON CONFLICT(singleton) DO UPDATE SET revision=1,active_generation_id=1,building_generation_id=NULL,input_sequence=0`)
		exec(tx, `INSERT OR IGNORE INTO catalog_credit_labels(id,label) VALUES(1,'Actor'),(2,'Acting')`)
		for i := int64(1); i <= min(residents, 8); i++ {
			name := fmt.Sprintf("Qualification performer %02d", i)
			exec(tx, `INSERT INTO catalog_people(id,token,identity_key,name,sort_name,artwork_incarnation,search_incarnation) VALUES(?,?,?,?,?,?,?)`, i, fmt.Sprintf("qualification-person-%d", i), fmt.Sprintf("qualification-person-%d", i), name, name, qualificationSearchBirth(i), qualificationSearchBirth(i+16))
		}
	})
	var after, publication int64
	for {
		rows, err := conn.QueryContext(t.Context(), `SELECT id,library_id,kind,revision,title,metadata_incarnation FROM catalog_entities WHERE retired=0 AND id>? ORDER BY id LIMIT ?`, after, int64(qualificationSearchBankBatch))
		if err != nil {
			t.Fatal(err)
		}
		page := make([]qualificationSearchSource, 0, qualificationSearchBankBatch)
		for rows.Next() {
			var s qualificationSearchSource
			var inc []byte
			if err := rows.Scan(&s.id, &s.library, &s.kind, &s.revision, &s.title, &inc); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if len(inc) != 16 || s.revision <= 0 {
				rows.Close()
				t.Fatal("invalid native search source")
			}
			copy(s.incarnation[:], inc)
			page = append(page, s)
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
			for _, s := range page {
				publication++
				qualificationSearchImage(t, stmts, digest, &facts, search.ManifestEntity, publication, s)
				facts.Entities++
				facts.Publications++
				// One owner name shared by many works, never copied into child
				// fragments. The first and each 128th resident get one credit.
				if (facts.Entities-1)%128 == 0 {
					person := 1 + ((facts.Entities-1)/128)%min(residents, 8)
					exec(tx, `INSERT INTO catalog_credit_images(id,entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,build_seed,build_chain,current_input_digest,command_incarnation,sealed,manifest_digest) VALUES(?,?,?,?,1,1,1,?,0,0,'en',zeroblob(32),zeroblob(32),'qualification-fixture',0,0,zeroblob(32),zeroblob(32),zeroblob(32),zeroblob(32),?,1,zeroblob(32))`, publication, s.id, s.incarnation[:], s.revision, qualificationSearchBirth(0), qualificationSearchBirth(publication))
					exec(tx, `UPDATE catalog_entities SET credit_image_id=? WHERE id=?`, publication, s.id)
					exec(tx, `INSERT INTO catalog_credits(image_id,entity_id,ord,person_id,role_id,department_id,credited_name,provider,credit_id,source_ordinal) VALUES(?,?,0,?,1,2,'','qualification','credit',0)`, publication, s.id, person)
					qualificationSearchHash(digest, "credit", strconv.FormatInt(s.id, 10), strconv.FormatInt(person, 10))
					facts.Credits++
				}
			}
		})
		after = page[len(page)-1].id
	}
	transaction(func(tx *sql.Tx, stmts []*sql.Stmt) {
		for i := int64(1); i <= min(residents, 8); i++ {
			publication++
			var inc [16]byte
			copy(inc[:], qualificationSearchBirth(i+16))
			qualificationSearchImage(t, stmts, digest, &facts, search.ManifestPerson, publication, qualificationSearchSource{id: i, revision: 1, title: fmt.Sprintf("Qualification performer %02d", i), incarnation: inc})
			facts.People++
			facts.Publications++
		}
		exec(tx, `UPDATE search_generations SET indexed_rows=?,indexed_bytes=?,after_entity_id=?,after_person_id=? WHERE id=1`, facts.Fragments, facts.Bytes, after, facts.People)
	})
	if facts.Entities != residents {
		t.Fatal("search bank resident walk changed", facts.Entities, residents)
	}
	facts.LogicalSHA256 = hex.EncodeToString(digest.Sum(nil))
	if err := qualificationSearchBankReadback(t, conn, residents, &facts); err != nil {
		t.Fatal(err)
	}
	return facts
}

// qualificationSearchImage writes one complete immutable image through the
// real preparation protocol. Exactly nativeFixtureImage's persisted shape.
func qualificationSearchImage(t *testing.T, stmts []*sql.Stmt, digest hash.Hash, facts *qualificationSearchBankFacts, subject search.ManifestSubject, publication int64, s qualificationSearchSource) {
	t.Helper()
	plan, err := search.PrepareFragments(s.title, search.DefaultFragmentBudget())
	if err != nil || plan.Len() == 0 {
		t.Fatalf("search image %d preparation: %v", publication, err)
	}
	rows := make([]search.ManifestRow, plan.Len())
	for i := range rows {
		rows[i] = plan.At(i).ManifestRow(1, int64(i), strconv.FormatInt(s.revision, 10))
	}
	m, err := search.PrepareManifest(search.ManifestInput{Subject: subject, SubjectID: s.id, LibraryID: s.library, Kind: s.kind, SearchGenerationID: 1, BankKey: "v1", Recipe: facts.Profile.RecipeVersion, EngineVersion: facts.Profile.EngineVersion, UnicodeFingerprint: facts.Profile.UnicodeFingerprint, InputGeneration: 1, SourceRevision: s.revision, SourceIncarnation: s.incarnation}, rows)
	if err != nil {
		t.Fatalf("search image %d manifest: %v", publication, err)
	}
	title := search.PrepareTitle(s.title)
	var entity, person, library, kind any
	personTokens := ""
	if subject == search.ManifestEntity {
		entity, library, kind = s.id, s.library, s.kind
	} else {
		person = s.id
		personTokens = search.PreparePersonName(s.title)
	}
	write := func(i int, args ...any) {
		t.Helper()
		if _, err := stmts[i].ExecContext(t.Context(), args...); err != nil {
			t.Fatalf("search image %d statement %d: %v", publication, i, err)
		}
	}
	write(0, publication, entity, person, library, kind, s.incarnation[:], s.revision, facts.Profile.RecipeVersion, s.title, title.Fold(), title.PrefixTokens(), title.InitialTokens(), personTokens)
	write(1, publication, entity, person, publication, s.incarnation[:], s.revision, facts.Profile.RecipeVersion, publication, m.Count(), m.Count(), m.Bytes(), qualificationSearchBirth(publication))
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
		write(7, s.id, title.PrefixTokens(), title.InitialTokens())
		facts.OwnPrefix++
	} else {
		write(8, s.id, personTokens)
	}
	facts.Seals++
	facts.Jobs++
	facts.Bytes += m.Bytes()
	qualificationSearchHash(digest, strconv.FormatInt(publication, 10), string(seed[:]), string(chain[:]), s.title, title.Fold(), title.PrefixTokens(), title.InitialTokens(), personTokens)
}

// qualificationSearchBirth is a deterministic 16-byte fixture identity.
func qualificationSearchBirth(id int64) []byte {
	var b [16]byte
	copy(b[:8], "QSEARCH1")
	binary.BigEndian.PutUint64(b[8:], uint64(id))
	return b[:]
}

func qualificationSearchHash(h hash.Hash, fields ...string) {
	var size [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		h.Write(size[:])
		h.Write([]byte(field))
	}
}

// qualificationSearchBankOccupied reports whether bank/People/credit custody
// already exists. An empty bootstrap v1 generation/revision may be replaced.
func qualificationSearchBankOccupied(ctx context.Context, conn *sql.Conn) (bool, error) {
	var occupied bool
	err := conn.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM search_publications) OR EXISTS(SELECT 1 FROM search_publish_jobs) OR
EXISTS(SELECT 1 FROM search_fragments) OR EXISTS(SELECT 1 FROM search_image_seals) OR
EXISTS(SELECT 1 FROM search_own_prefix) OR EXISTS(SELECT 1 FROM search_reclaim_jobs) OR
EXISTS(SELECT 1 FROM search_generation_receipts) OR EXISTS(SELECT 1 FROM catalog_people) OR
EXISTS(SELECT 1 FROM catalog_credit_images) OR EXISTS(SELECT 1 FROM catalog_credits) OR
EXISTS(SELECT 1 FROM search_generations WHERE id<>1 OR bank_key<>'v1')`).Scan(&occupied)
	return occupied, err
}

// qualificationSearchBankReadback checks actual persisted counts and the one
// active/pending-zero bank row; it is not a logical-hash or qualification claim.
func qualificationSearchBankReadback(t *testing.T, conn *sql.Conn, residents int64, facts *qualificationSearchBankFacts) error {
	t.Helper()
	var publications, jobs, fragments, seals, own, prefixRows, lexicalRows, gramsRows, active, bank, pending, peopleNames int64
	var state string
	if err := conn.QueryRowContext(t.Context(), `SELECT
(SELECT count(*) FROM search_publications),
(SELECT count(*) FROM search_publish_jobs),
(SELECT count(*) FROM search_fragments),
(SELECT count(*) FROM search_image_seals),
(SELECT count(*) FROM search_own_prefix_docsize),
(SELECT count(*) FROM search_fragment_prefix_docsize),
(SELECT count(*) FROM search_fragment_lexical_docsize),
(SELECT count(*) FROM search_fragment_grams_docsize),
(SELECT active_generation_id FROM search_revision WHERE singleton=1),
(SELECT id FROM search_generations WHERE bank_key='v1'),
(SELECT state FROM search_generations WHERE id=1),
(SELECT pending_subjects FROM search_generations WHERE id=1),
(SELECT count(*) FROM catalog_people_names_docsize)`).Scan(
		&publications, &jobs, &fragments, &seals, &own,
		&prefixRows, &lexicalRows, &gramsRows,
		&active, &bank, &state, &pending, &peopleNames); err != nil {
		return err
	}
	if prefixRows != fragments || lexicalRows != fragments || gramsRows != fragments {
		t.Fatalf("native rows prefix/lexical/grams=%d/%d/%d fragments=%d", prefixRows, lexicalRows, gramsRows, fragments)
	}
	wantPublications := residents + facts.People
	if publications != wantPublications || jobs != wantPublications || seals != wantPublications ||
		own != residents || peopleNames != facts.People || fragments != facts.Fragments {
		t.Fatalf("bank counts publications=%d/%d jobs=%d seals=%d own=%d peopleNames=%d fragments=%d facts=%+v",
			publications, wantPublications, jobs, seals, own, peopleNames, fragments, *facts)
	}
	if active != 1 || bank != 1 || state != "active" || pending != 0 {
		t.Fatalf("bank row active=%d bank=%d state=%q pending=%d", active, bank, state, pending)
	}
	facts.Publications, facts.Jobs, facts.Seals, facts.OwnPrefix = publications, jobs, seals, own
	return nil
}

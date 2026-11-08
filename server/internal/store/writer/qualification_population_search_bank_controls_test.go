package writer

// Focused controls for the test-only 1M search bank content builder. These are
// component checks over real persisted rows and the real preparation protocol;
// they are not scale, latency, native-density or qualification evidence.

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// qualificationSearchCatalogue is a tiny independent catalogue for boundary
// controls. It writes the minimum source rows the bank reads back.
func qualificationSearchCatalogue(t *testing.T, count int) *executor {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	tx, err := w.db.BeginTx(t.Context(), nil)
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
	exec(`INSERT INTO libraries(id,name,kind,root,playback_origin_incarnation) VALUES('search-qualification','Search qualification','movie','/search-qualification',?)`, qualificationSearchBirth(0))
	exec(`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'search-qualification','/search-qualification')`)
	exec(`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	for i := 1; i <= count; i++ {
		id := int64(i + 1_000_000_000)
		birth := qualificationSearchBirth(id)
		title := fmt.Sprintf("The Étoile 東京 Orbit %07d", i)
		exec(`INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(?,?,7,1)`, id, birth)
		exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation,revision) VALUES(?,?,7,1,?,'s','s','s','s',1,?,?,?)`, id, birth, title, birth, birth, i)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestQualificationSearchBankAllResidentsAndNativeRows crosses the source-page
// and transaction boundary over every resident, then re-derives every seal from
// the persisted native fragment rows rather than the builder's plan.
func TestQualificationSearchBankAllResidentsAndNativeRows(t *testing.T) {
	w := qualificationSearchCatalogue(t, 260)
	facts := seedQualificationSearchBank(t, w)
	if facts.Entities != 260 || facts.People != 8 || facts.Credits != 3 || facts.Publications != 268 ||
		facts.Jobs != 268 || facts.Seals != 268 || facts.OwnPrefix != 260 || facts.Fragments < 268 ||
		len(facts.LogicalSHA256) != 64 || facts.Statistics == "" {
		t.Fatalf("bank facts: %+v", facts)
	}
	var coherent bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT
(SELECT count(*)=1 FROM search_generations WHERE id=1 AND bank_key='v1' AND state='active' AND pending_subjects=0 AND indexed_rows=? AND indexed_bytes=?)
AND (SELECT count(*)=1 FROM search_revision WHERE singleton=1 AND active_generation_id=1 AND building_generation_id IS NULL AND revision=1)
AND (SELECT count(*)=260 FROM search_publications p JOIN catalog_entities e ON p.entity_id=e.id WHERE p.title=e.title AND p.library_id=e.library_id AND p.kind=e.kind AND p.source_revision=e.revision AND p.source_incarnation=e.metadata_incarnation)
AND (SELECT count(*)=268 FROM search_publish_jobs WHERE phase='idle' AND published_generation=1 AND published_document_id IS NOT NULL AND job_due_ms IS NULL)
AND (SELECT count(*)=268 FROM search_image_seals WHERE sealed=1 AND staged_fragments=expected_fragments AND verified_fragments=expected_fragments AND staged_bytes=expected_bytes AND verified_bytes=expected_bytes AND chain=expected_digest)
AND (SELECT count(*)=8 FROM search_publications p JOIN catalog_people e ON p.person_id=e.id WHERE p.title=e.name AND p.source_revision=e.revision AND p.source_incarnation=e.search_incarnation)
AND (SELECT count(*)=0 FROM search_fragments WHERE field<>1)
AND (SELECT count(*)=3 FROM catalog_credits c JOIN catalog_entities e ON e.id=c.entity_id AND e.credit_image_id=c.image_id)
AND (SELECT count(*)=0 FROM search_fragments f LEFT JOIN search_fragment_prefix_docsize p ON p.id=f.id LEFT JOIN search_fragment_lexical_docsize l ON l.id=f.id LEFT JOIN search_fragment_grams_docsize g ON g.id=f.id WHERE p.id IS NULL OR l.id IS NULL OR g.id IS NULL)
AND (SELECT count(*)=260 FROM search_own_prefix_docsize)
AND (SELECT count(*)=8 FROM catalog_people_names_docsize)`, facts.Fragments, facts.Bytes).Scan(&coherent); err != nil || !coherent {
		t.Fatal("incomplete logical/native bank", coherent, err)
	}
	for _, probe := range []struct {
		table, term string
		want        int
	}{
		{"search_fragment_lexical", `"0000042"`, 1},
		{"search_fragment_lexical", `orbit`, 260},
		{"search_fragment_lexical", `zzqxvw`, 0},
		{"search_fragment_grams", `0000042`, 1},
		{"catalog_people_names", `performer`, 8},
	} {
		var hits int
		if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+probe.table+` WHERE `+probe.table+` MATCH ?`, probe.term).Scan(&hits); err != nil || hits != probe.want {
			t.Fatal("native probe", probe.table, probe.term, hits, err)
		}
	}
	rows, err := w.db.QueryContext(t.Context(), `SELECT p.id,p.entity_id,p.person_id,p.library_id,p.kind,p.source_revision,p.source_incarnation,s.seed,s.expected_digest,s.expected_fragments,s.expected_bytes FROM search_publications p JOIN search_image_seals s ON s.publication_id=p.id ORDER BY p.id`)
	if err != nil {
		t.Fatal(err)
	}
	type image struct {
		id, entity, person, library, kind, revision int64
		inc, seed, chain                            []byte
		count, size                                 int64
	}
	var images []image
	for rows.Next() {
		var x image
		if err := rows.Scan(&x.id, nullableSearchInt(&x.entity), nullableSearchInt(&x.person), nullableSearchInt(&x.library), nullableSearchInt(&x.kind), &x.revision, &x.inc, &x.seed, &x.chain, &x.count, &x.size); err != nil {
			t.Fatal(err)
		}
		images = append(images, x)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	profile := searchpolicy.Compiled()
	for _, x := range images {
		fragments, err := w.db.QueryContext(t.Context(), `SELECT field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized FROM search_fragments WHERE publication_id=? ORDER BY field,ordinal`, x.id)
		if err != nil {
			t.Fatal(err)
		}
		var actual []search.ManifestRow
		for fragments.Next() {
			var row search.ManifestRow
			if err := fragments.Scan(&row.Field, &row.Ordinal, &row.Revision, &row.Prefix, &row.Lexical, &row.Normalized); err != nil {
				fragments.Close()
				t.Fatal(err)
			}
			actual = append(actual, row)
		}
		if err := fragments.Err(); err != nil {
			fragments.Close()
			t.Fatal(err)
		}
		fragments.Close()
		var inc [16]byte
		copy(inc[:], x.inc)
		subject, id := search.ManifestEntity, x.entity
		if x.person != 0 {
			subject, id = search.ManifestPerson, x.person
		}
		m, err := search.PrepareManifest(search.ManifestInput{Subject: subject, SubjectID: id, LibraryID: x.library, Kind: x.kind, SearchGenerationID: 1, BankKey: "v1", Recipe: profile.RecipeVersion, EngineVersion: profile.EngineVersion, UnicodeFingerprint: profile.UnicodeFingerprint, InputGeneration: 1, SourceRevision: x.revision, SourceIncarnation: inc}, actual)
		if err != nil {
			t.Fatal(err)
		}
		seed, chain := m.Seed(), m.Digest()
		if !bytes.Equal(seed[:], x.seed) || !bytes.Equal(chain[:], x.chain) || m.Count() != x.count || m.Bytes() != x.size {
			t.Fatal("seal is not actual prepared content", x.id)
		}
	}
	var badTable string
	var badRow any
	var badParent string
	var badFK int
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA foreign_key_check`).Scan(&badTable, &badRow, &badParent, &badFK); err == nil {
		t.Fatal("bank FK violation", badTable, badRow, badParent, badFK)
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if occupied, err := qualificationSearchBankOccupied(t.Context(), conn); err != nil || !occupied {
		t.Fatal("bank occupancy", occupied, err)
	}
}

func nullableSearchInt(target *int64) qualificationSearchNullableInt {
	return qualificationSearchNullableInt{target}
}

// The scanner is test-only and maps SQL NULL owner branches to zero.
type qualificationSearchNullableInt struct{ target *int64 }

func (s qualificationSearchNullableInt) Scan(value any) error {
	if value == nil {
		*s.target = 0
		return nil
	}
	v, ok := value.(int64)
	if !ok {
		return fmt.Errorf("unexpected owner type %T", value)
	}
	*s.target = v
	return nil
}

// TestQualificationSearchBankCanonicalPopulation runs the builder over the real
// deterministic population seeder and checks titles/manifests against it.
func TestQualificationSearchBankCanonicalPopulation(t *testing.T) {
	spec := qualificationPopulationSpec{Seed: 29, Residents: 32}
	w, _ := testCanonicalExecutor(t)
	population := seedQualificationPopulation(t, w, spec)
	bank := seedQualificationSearchBank(t, w)
	if population.Residents != 32 || bank.Entities != 32 || bank.People != 8 || bank.Credits != 1 ||
		bank.Publications != 40 || bank.Jobs != 40 || bank.Seals != 40 || bank.OwnPrefix != 32 {
		t.Fatal(population, bank)
	}
	var matched int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM search_publications p JOIN catalog_entities e ON p.entity_id=e.id
WHERE p.title=e.title AND p.library_id=e.library_id AND p.kind=e.kind AND p.source_revision=e.revision AND p.source_incarnation=e.metadata_incarnation`).Scan(&matched); err != nil || matched != 32 {
		t.Fatal("publication/source mismatch", matched, err)
	}
	profile := searchpolicy.Compiled()
	for _, ordinal := range []int{1, 7, 32} {
		movie := qualificationMovie(t, spec.Seed, ordinal)
		plan, err := search.PrepareFragments(movie.metadata.Title, search.DefaultFragmentBudget())
		if err != nil {
			t.Fatal(ordinal, err)
		}
		rows := make([]search.ManifestRow, plan.Len())
		for i := 0; i < plan.Len(); i++ {
			rows[i] = plan.At(i).ManifestRow(1, int64(i), "1")
		}
		m, err := search.PrepareManifest(search.ManifestInput{Subject: search.ManifestEntity, SubjectID: movie.id, LibraryID: 7, Kind: 1, SearchGenerationID: 1, BankKey: "v1", Recipe: profile.RecipeVersion, EngineVersion: profile.EngineVersion, UnicodeFingerprint: profile.UnicodeFingerprint, InputGeneration: 1, SourceRevision: 1, SourceIncarnation: movie.incarnation}, rows)
		if err != nil {
			t.Fatal(ordinal, err)
		}
		var seed, digest, chain []byte
		var sealed bool
		if err := w.db.QueryRowContext(t.Context(), `SELECT seed,expected_digest,chain,sealed FROM search_image_seals WHERE publication_id=?`, ordinal).Scan(&seed, &digest, &chain, &sealed); err != nil {
			t.Fatal(ordinal, err)
		}
		wantSeed, wantDigest := m.Seed(), m.Digest()
		if !sealed || !bytes.Equal(seed, wantSeed[:]) || !bytes.Equal(digest, wantDigest[:]) || !bytes.Equal(chain, wantDigest[:]) {
			t.Fatal("canonical seal", ordinal)
		}
	}
	var hits int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM search_fragment_lexical WHERE search_fragment_lexical MATCH ?`, `"0000007"`).Scan(&hits); err != nil || hits != 1 {
		t.Fatal("canonical rare token", hits, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM search_fragment_lexical WHERE search_fragment_lexical MATCH 'zzqxvw'`).Scan(&hits); err != nil || hits != 0 {
		t.Fatal("canonical absent token", hits, err)
	}
}

// TestQualificationSearchBankDeterministic proves the bank is a pure function of
// the catalogue rows: same seed persists the same bank, a different seed does not.
func TestQualificationSearchBankDeterministic(t *testing.T) {
	var first qualificationSearchBankFacts
	var firstDigest string
	for i, seed := range []uint32{61, 61, 62} {
		w, _ := testCanonicalExecutor(t)
		spec := qualificationPopulationSpec{Seed: seed, Residents: 16}
		seedQualificationPopulation(t, w, spec)
		facts := seedQualificationSearchBank(t, w)
		conn, err := w.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		digest := qualificationSearchBankDigest(t, conn)
		conn.Close()
		switch i {
		case 0:
			first, firstDigest = facts, digest
		case 1:
			if !reflect.DeepEqual(first, facts) || firstDigest != digest {
				t.Fatal("same seed changed the logical bank", first, facts)
			}
		case 2:
			if firstDigest == digest {
				t.Fatal("different seed kept the logical bank", firstDigest, digest)
			}
		}
	}
}

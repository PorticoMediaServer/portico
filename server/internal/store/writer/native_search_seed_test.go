package writer

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// SeedNativeSearchBankForTest installs ONE deterministic native search image
// for an existing entity so the full-search read has a real v1 bank. It uses
// the same published preparation APIs as RD's own fixture; it clones no
// product writer, plan, receipt or admission. Test-only setup, never a route.
func (c *N1PairedComponent) SeedNativeSearchBankForTest(t *testing.T, entityID int64, title string) {
	t.Helper()
	if c == nil || c.fixture == nil || c.fixture.owner == nil || c.fixture.owner.executor == nil {
		t.Fatal("paired component writer unavailable")
	}
	ctx := t.Context()
	conn, err := c.fixture.owner.executor.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var library, kind, revision int64
	var incarnation []byte
	if err := tx.QueryRowContext(ctx, `SELECT library_id,kind,revision,metadata_incarnation FROM catalog_entities WHERE id=?`, entityID).Scan(&library, &kind, &revision, &incarnation); err != nil {
		t.Fatal(err)
	}
	profile := searchpolicy.Compiled()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// The canonical baseline may pre-create an empty search generation; replace
	// that fixture state with ONE active v1 bank so the read has exact data.
	exec(`PRAGMA defer_foreign_keys=ON`)
	for _, table := range []string{"search_revision", "search_image_seals", "search_fragment_grams", "search_fragment_lexical", "search_fragment_prefix", "search_fragments", "search_publish_jobs", "search_publications", "search_own_prefix", "search_generations"} {
		exec(`DELETE FROM ` + table)
	}
	exec(`INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms,command_incarnation) VALUES(1,'v1',?,?,?,'active',0,zeroblob(16))`, profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint)
	exec(`INSERT INTO search_revision(singleton,revision,active_generation_id,input_sequence) VALUES(1,1,1,0)`)
	fragments, err := search.PrepareFragments(title, search.DefaultFragmentBudget())
	if err != nil {
		t.Fatal(err)
	}
	var rows []search.ManifestRow
	for i := 0; i < fragments.Len(); i++ {
		rows = append(rows, fragments.At(i).ManifestRow(1, int64(i), fmt.Sprintf("revision-field-%d", 1)))
	}
	var inc [16]byte
	copy(inc[:], incarnation)
	manifest, err := search.PrepareManifest(search.ManifestInput{Subject: search.ManifestEntity, SubjectID: entityID, LibraryID: library, Kind: kind, SearchGenerationID: 1, BankKey: "v1", EngineVersion: profile.EngineVersion, UnicodeFingerprint: profile.UnicodeFingerprint, Recipe: profile.RecipeVersion, InputGeneration: 1, SourceRevision: revision, SourceIncarnation: inc}, rows)
	if err != nil {
		t.Fatal(err)
	}
	prepared := search.PrepareTitle(title)
	exec(`INSERT INTO search_publications(id,entity_id,person_id,search_generation_id,library_id,kind,generation,source_incarnation,source_revision,recipe_version,title,title_fold,title_prefix_tokens,initial_prefix_tokens,person_name_tokens) VALUES(1,?,NULL,1,?,?,1,?,?,?,?,?,?,?,'')`, entityID, library, kind, incarnation, revision, profile.RecipeVersion, title, prepared.Fold(), prepared.PrefixTokens(), prepared.InitialTokens())
	var jobBirth [16]byte
	binary.BigEndian.PutUint64(jobBirth[8:], 1)
	exec(`INSERT INTO search_publish_jobs(entity_id,person_id,search_generation_id,published_generation,published_document_id,input_generation,source_incarnation,source_revision,recipe_version,phase,command_incarnation) VALUES(?,NULL,1,1,1,1,?,?,?,'idle',?)`, entityID, incarnation, revision, profile.RecipeVersion, jobBirth[:])
	for i, row := range rows {
		fragment := int64(i) + 1
		exec(`INSERT INTO search_fragments(id,owner_entity,owner_person,field,ordinal,source_revision,prefix_tokens,lexical_tokens,normalized,generation,search_generation_id,publication_id) VALUES(?,?,NULL,?,?,?,?,?,?,1,1,1)`, fragment, entityID, row.Field, row.Ordinal, row.Revision, row.Prefix, row.Lexical, row.Normalized)
		exec(`INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(?,?)`, fragment, row.Prefix)
		exec(`INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(?,?)`, fragment, row.Lexical)
		exec(`INSERT INTO search_fragment_grams(rowid,normalized) VALUES(?,?)`, fragment, row.Normalized)
	}
	seed, digest := manifest.Seed(), manifest.Digest()
	exec(`INSERT INTO search_image_seals(publication_id,algorithm,seed,expected_digest,expected_fragments,expected_bytes,staged_fragments,staged_bytes,verified_fragments,verified_bytes,chain,sealed) VALUES(1,?,?,?,?,?,?,?,?,?,?,1)`, search.ManifestAlgorithm, seed[:], digest[:], manifest.Count(), manifest.Bytes(), manifest.Count(), manifest.Bytes(), manifest.Count(), manifest.Bytes(), digest[:])
	exec(`INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)`, entityID, prepared.PrefixTokens(), prepared.InitialTokens())
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

package writer

import (
	"database/sql"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

// Values-only protocol fixtures, not qualified source/engine/native custody.
func searchFragmentStageSnapshotFixture(t *testing.T) (searchFragmentStageSnapshot, []searchPreparedFragment) {
	t.Helper()
	r, _ := searchImageBeginRequestFixture(t)
	rows := []searchmanifest.Row{
		{Field: 1, Ordinal: 0, Revision: "11", Prefix: "006771004eac", Lexical: "東 京 東京", Normalized: "東京"},
		{Field: 1, Ordinal: 1, Revision: "11", Prefix: "", Lexical: "", Normalized: "東京"},
		{Field: 1, Ordinal: 2, Revision: "11", Prefix: "", Lexical: "", Normalized: ""},
	}
	in := searchImageBeginManifestInput(r)
	manifest, err := searchmanifest.Prepare(in, rows)
	if err != nil {
		t.Fatal(err)
	}
	s := searchFragmentStageSnapshot{bank: r.bank, job: r.owner,
		source: searchFragmentSource{r.source.entityID, r.source.libraryID, r.source.kind, r.source.revision, r.source.incarnation},
		image:  searchFragmentImage{307, in}, active: sql.NullInt64{Int64: r.bank.id, Valid: true},
		seal: searchFragmentSeal{algorithm: searchmanifest.Algorithm, seed: manifest.Seed(), expectedDigest: manifest.Digest(), chain: manifest.Seed(), expectedFragments: manifest.Count(), expectedBytes: manifest.Bytes()},
	}
	s.job.phase, s.job.document = "stage", sql.NullInt64{Int64: 307, Valid: true}
	s.job.expectedFragments = manifest.Count()
	var prepared []searchPreparedFragment
	for _, row := range rows {
		p, err := prepareSearchFragment(row, searchFragmentPreparationFixture())
		if err != nil {
			t.Fatal(err)
		}
		prepared = append(prepared, p)
	}
	return s, prepared
}

func TestSearchFragmentTransitionStagesBothCountersWithoutVerification(t *testing.T) {
	s, rows := searchFragmentStageSnapshotFixture(t)
	for at, p := range rows {
		before := s
		next, err := planSearchFragmentStage(s, p)
		if err != nil {
			t.Fatal(at, err)
		}
		if s != before {
			t.Fatal("planning mutated captured facts")
		}
		if next.jobRevision != s.job.revision+1 || next.stagedFragments != int64(at)+1 || next.stagedBytes != s.seal.stagedBytes+int64(p.bytes) || next.afterField != 1 || next.afterOrdinal != int64(at) {
			t.Fatal("wrong prospective stage transition", next)
		}
		if next.sealChain != s.seal.chain || next.verifiedFragments != s.seal.verifiedFragments || next.verifiedBytes != s.seal.verifiedBytes {
			t.Fatal("stage claimed independent verification", next)
		}
		// This models BOTH native CAS effects. Actual IDs/revisions still
		// require INSERT/UPDATE RETURNING in the later qualified writer turn.
		s.job.revision = next.jobRevision
		s.job.afterField, s.job.afterOrdinal, s.job.afterFragmentID = next.afterField, next.afterOrdinal, int64(500+at)
		s.job.stagedFragments, s.job.stagedBytes = next.stagedFragments, next.stagedBytes
		s.seal.stagedFragments, s.seal.stagedBytes = next.stagedFragments, next.stagedBytes
	}
	if s.seal.stagedFragments != s.seal.expectedFragments || s.seal.stagedBytes != s.seal.expectedBytes || s.seal.sealed != 0 || s.seal.verifiedFragments != 0 {
		t.Fatal("empty final fragment lost or staged image was sealed", s.seal)
	}
}

func TestSearchFragmentTransitionPreservesIndependentVerifierProgress(t *testing.T) {
	s, rows := searchFragmentStageSnapshotFixture(t)
	s.job.stagedFragments, s.seal.stagedFragments = 2, 2
	s.job.stagedBytes = int64(rows[0].bytes + rows[1].bytes)
	s.seal.stagedBytes = s.job.stagedBytes
	s.job.afterField, s.job.afterOrdinal, s.job.afterFragmentID = 1, 1, 501
	verified, err := searchmanifest.Advance(searchmanifest.Checkpoint{Chain: s.seal.seed}, rows[0].row)
	if err != nil {
		t.Fatal(err)
	}
	s.seal.verifiedFragments, s.seal.verifiedBytes, s.seal.chain = verified.Count, verified.PreparedBytes, verified.Chain
	s.seal.afterField, s.seal.afterOrdinal, s.seal.afterFragmentID = 1, 0, 500
	next, err := planSearchFragmentStage(s, rows[2])
	if err != nil || next.verifiedFragments != 1 || next.verifiedBytes != verified.PreparedBytes || next.sealChain != verified.Chain {
		t.Fatal(next, err)
	}
}

func TestSearchFragmentTransitionRejectsWitnessCursorAndCounterDrift(t *testing.T) {
	base, rows := searchFragmentStageSnapshotFixture(t)
	for _, mutate := range []func(*searchFragmentStageSnapshot){
		func(s *searchFragmentStageSnapshot) { s.source.revision++ },
		func(s *searchFragmentStageSnapshot) { s.source.incarnation[0]++ },
		func(s *searchFragmentStageSnapshot) { s.image.input.SourceRevision++ },
		func(s *searchFragmentStageSnapshot) { s.image.input.InputGeneration++ },
		func(s *searchFragmentStageSnapshot) { s.job.document.Int64++ },
		func(s *searchFragmentStageSnapshot) { s.bank.profile.RecipeVersion = "other" },
		func(s *searchFragmentStageSnapshot) { s.active.Int64++ },
		func(s *searchFragmentStageSnapshot) { s.job.stagedFragments++ },
		func(s *searchFragmentStageSnapshot) { s.seal.stagedBytes++ },
		func(s *searchFragmentStageSnapshot) { s.job.afterFragmentID++ },
		func(s *searchFragmentStageSnapshot) { s.seal.chain[0]++ },
		func(s *searchFragmentStageSnapshot) { s.seal.verifiedFragments++ },
		func(s *searchFragmentStageSnapshot) { s.seal.afterOrdinal++ },
		func(s *searchFragmentStageSnapshot) { s.seal.seed[0]++ },
		func(s *searchFragmentStageSnapshot) { s.seal.expectedBytes = 0 },
		func(s *searchFragmentStageSnapshot) { s.seal.expectedFragments = 0 },
		func(s *searchFragmentStageSnapshot) { s.seal.sealed = 1 },
		func(s *searchFragmentStageSnapshot) { s.job.revision = math.MaxInt64 },
	} {
		s := base
		mutate(&s)
		if out, err := planSearchFragmentStage(s, rows[0]); err == nil || out != (searchFragmentTransition{}) {
			t.Fatal("drift released a prospective transition", s, out, err)
		}
	}
}

func TestSearchFragmentTransitionUsesOnlyFirstNativeDecimalProjection(t *testing.T) {
	s, rows := searchFragmentStageSnapshotFixture(t)
	for _, rev := range []string{"011", "+11", " 11", "0xb", "11.0", "opaque"} {
		row := rows[0].row
		row.Revision = rev
		p, err := prepareSearchFragment(row, searchFragmentPreparationFixture())
		if err != nil {
			t.Fatal("generic opaque TEXT was parsed/relabelled", rev, err)
		}
		if out, err := planSearchFragmentStage(s, p); err == nil || out != (searchFragmentTransition{}) {
			t.Fatal("caller revision substituted for native FIRST projection", rev, out, err)
		}
	}
	for _, edit := range []func(*searchmanifest.Row){func(r *searchmanifest.Row) { r.Field = 2 }, func(r *searchmanifest.Row) { r.Ordinal = 1 }} {
		row := rows[0].row
		edit(&row)
		p, err := prepareSearchFragment(row, searchFragmentPreparationFixture())
		if err != nil {
			t.Fatal(err)
		}
		if out, err := planSearchFragmentStage(s, p); err == nil || out != (searchFragmentTransition{}) {
			t.Fatal(out, err)
		}
	}
}

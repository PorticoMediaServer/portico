package writer

import (
	"database/sql"
	"math"
	"testing"
)

func searchHeadPublishSnapshotFixture(t *testing.T) searchHeadPublishSnapshot {
	s, rows := searchFragmentStageSnapshotFixture(t)
	s.job.phase = "publish"
	s.job.stagedFragments, s.job.stagedBytes = s.seal.expectedFragments, s.seal.expectedBytes
	s.seal.stagedFragments, s.seal.verifiedFragments = s.seal.expectedFragments, s.seal.expectedFragments
	s.seal.stagedBytes, s.seal.verifiedBytes, s.seal.chain, s.seal.sealed = s.seal.expectedBytes, s.seal.expectedBytes, s.seal.expectedDigest, 1
	s.job.afterField, s.job.afterOrdinal, s.job.afterFragmentID = 1, int64(len(rows)-1), 503
	s.seal.afterField, s.seal.afterOrdinal, s.seal.afterFragmentID = s.job.afterField, s.job.afterOrdinal, s.job.afterFragmentID
	return searchHeadPublishSnapshot{stage: s, servingRevision: 19, pending: 3}
}

func TestSearchHeadTransitionRequiresVerifiedCompleteImage(t *testing.T) {
	s := searchHeadPublishSnapshotFixture(t)
	out, err := planSearchHeadPublish(s)
	if err != nil || out.acceptedJobRevision != s.stage.job.revision+1 || out.servingRevision != 20 || out.pending != 2 || out.oldImageID != s.stage.job.publishedImage.Int64 || out.imageID != s.stage.image.id {
		t.Fatal(out, err)
	}
	for _, edit := range []func(*searchHeadPublishSnapshot){
		func(v *searchHeadPublishSnapshot) { v.stage.seal.sealed = 0 },
		func(v *searchHeadPublishSnapshot) { v.stage.seal.verifiedFragments-- },
		func(v *searchHeadPublishSnapshot) { v.stage.seal.chain[0]++ },
		func(v *searchHeadPublishSnapshot) { v.nextStoredFragment = sql.NullInt64{Int64: 504, Valid: true} },
		func(v *searchHeadPublishSnapshot) { v.stage.source.revision++ },
		func(v *searchHeadPublishSnapshot) { v.pending = 0 },
		func(v *searchHeadPublishSnapshot) { v.servingRevision = math.MaxInt64 },
	} {
		v := s
		edit(&v)
		if out, err := planSearchHeadPublish(v); err == nil || out != (searchHeadPublishTransition{}) {
			t.Fatal("stale/unverified head released", out, err)
		}
	}
}

func TestSearchHeadBuildingTransitionPreservesServingRevision(t *testing.T) {
	s := searchHeadPublishSnapshotFixture(t)
	s.stage.active, s.stage.building = sql.NullInt64{Int64: 1, Valid: true}, sql.NullInt64{Int64: s.stage.bank.id, Valid: true}
	s.stage.bank.role = "building"
	s.servingRevision = math.MaxInt64
	out, err := planSearchHeadPublish(s)
	if err != nil || out.servingRevision != s.servingRevision || out.pending != s.pending-1 {
		t.Fatal(out, err)
	}
}

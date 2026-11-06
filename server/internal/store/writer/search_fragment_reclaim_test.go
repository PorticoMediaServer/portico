package writer

import (
	"database/sql"
	"math"
	"testing"
)

func TestSearchFragmentReclaimRetainsOriginalOpaqueStringsAndOneUnit(t *testing.T) {
	s, rows := searchFragmentStageSnapshotFixture(t)
	row := rows[0].row
	row.Revision = "09" // retained bytes, never FIRST's current numeric projection
	f, memory, err := prepareSearchRetainedFragment(searchRetainedFragment{501, s.image.id, s.bank.id, s.job.inputGeneration, s.source.entityID, s.source.libraryID, s.source.kind, row})
	if err != nil || f.row != row || memory < uint64(len(row.Revision)) {
		t.Fatal(f, memory, err)
	}
	o := searchFragmentReclaimOwner{id: 61, publicationID: s.image.id, livePublicationID: sql.NullInt64{Int64: s.image.id, Valid: true}, revision: 4,
		phase: "ready", incarnation: [16]byte{9}, lastSequence: 2, acknowledgedSequence: 2, leaseOwner: 1, leaseGeneration: 3, leaseExpiresMS: 2100000000000}
	q := CommandIdentity{o.incarnation, 3, [16]byte{7}}
	snapshot := searchFragmentReclaimSnapshot{owner: o, fragment: f}
	next, err := planSearchFragmentReclaim(snapshot, q)
	if err != nil || next.acceptedRevision != 5 || next.afterFragmentID != 501 || next.nativeDeletes != 3 || next.contentDeletes != 1 || next.revisionBytes != 2 || next.retainedColumnBytes != rows[0].bytes {
		t.Fatal(next, err)
	}
	for _, edit := range []func(*searchFragmentReclaimSnapshot){
		func(v *searchFragmentReclaimSnapshot) { v.currentHead = v.owner.livePublicationID },
		func(v *searchFragmentReclaimSnapshot) { v.currentStage = v.owner.livePublicationID },
		func(v *searchFragmentReclaimSnapshot) { v.owner.acknowledgedSequence-- },
		func(v *searchFragmentReclaimSnapshot) { v.owner.phase = "held" },
		func(v *searchFragmentReclaimSnapshot) { v.owner.afterFragmentID = f.id },
		func(v *searchFragmentReclaimSnapshot) { v.owner.revision = math.MaxInt64 },
	} {
		v := snapshot
		edit(&v)
		if out, err := planSearchFragmentReclaim(v, q); err == nil || out != (searchFragmentReclaimTransition{}) {
			t.Fatal("live/unacked/stale retirement advanced", out, err)
		}
	}
}

func TestSearchRetainedFragmentRejectsOversizeBeforeCopy(t *testing.T) {
	s, rows := searchFragmentStageSnapshotFixture(t)
	f := searchRetainedFragment{501, s.image.id, s.bank.id, s.job.inputGeneration, s.source.entityID, s.source.libraryID, s.source.kind, rows[0].row}
	f.row.Normalized = string(make([]byte, searchFragmentWindowBytes+1))
	if out, memory, err := prepareSearchRetainedFragment(f); err == nil || out != (searchRetainedFragment{}) || memory != 0 {
		t.Fatal("unbounded retained payload copied", out, memory, err)
	}
}

package writer

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func basicSnapshotValues() (CommandIdentity, scanRunSnapshot, scanSourceFacts, basicOriginalScope) {
	in, identity := basicStageFactCommandFixture()
	o := in.original
	n := scanRunSnapshot{run: scanRunNative{scope: scanRunScope{runID: o.scope.runID, libraryID: o.scope.libraryID, libraryKey: o.scope.libraryKey,
		resolvedRoot: string(o.source.resolvedRoot), sourceID: o.source.id, incarnation: o.source.incarnation, rootIdentity: o.source.rootIdentity,
		sourceGeneration: o.source.generation, followSymlinks: basicBool(o.source.followSymlinks), policyRevision: o.run.policyRevision,
		scanMode: o.run.scanMode, commandIncarnation: identity.Incarnation}, phase: o.run.phase, revision: o.run.revision,
		leaseOwner: o.run.leaseOwner, leaseGeneration: o.run.leaseGeneration, leaseExpiresMS: o.run.leaseExpiresMS,
		lastSequence: o.run.lastSequence, acknowledgedSequence: o.run.acknowledgedSequence},
		job: scanRunJob{libraryKey: o.scope.libraryKey, status: "running"}, root: scanRunRoot{identity: "observed-directory", revision: "directory-revision", state: "pending"}}
	f := scanSourceFacts{libraryID: o.scope.libraryID, libraryKey: o.scope.libraryKey, source: o.source, policyRevision: o.run.policyRevision, identityRoot: []byte("/retained-catalogue-root")}
	return identity, n, f, o
}

func TestBasicOriginalSnapshotKeepsNativeVectorAndOwnsRoot(t *testing.T) {
	identity, n, source, want := basicSnapshotValues()
	got, err := basicOriginalFromSnapshot(identity, n, source)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
	source.source.resolvedRoot[0] = '!'
	if reflect.DeepEqual(got.source.resolvedRoot, source.source.resolvedRoot) || got.source.rootIdentity != n.run.scope.rootIdentity || got.run.revision != n.run.revision || got.source.generation != n.run.scope.sourceGeneration {
		t.Fatal("captured source/root or independent revision was lost", got)
	}
}

func TestBasicOriginalSnapshotRefusesMixedSourceAndOriginal(t *testing.T) {
	for _, mode := range []string{"source", "root", "policy", "sequence", "unacked", "incarnation"} {
		t.Run(mode, func(t *testing.T) {
			identity, n, source, _ := basicSnapshotValues()
			switch mode {
			case "source":
				source.source.generation++
			case "root":
				source.source.rootIdentity = "different-root"
			case "policy":
				source.policyRevision++
			case "sequence":
				identity.Sequence++
			case "unacked":
				n.run.acknowledgedSequence--
			case "incarnation":
				identity.Incarnation[0]++
			}
			got, err := basicOriginalFromSnapshot(identity, n, source)
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, basicOriginalScope{}) {
				t.Fatal(got, err)
			}
		})
	}
}

func TestBasicOriginalSnapshotBoundsBeforeProjection(t *testing.T) {
	identity, n, source, _ := basicSnapshotValues()
	n.run.lastSequence = math.MaxInt64
	if _, err := basicOriginalFromSnapshot(identity, n, source); !errors.Is(err, ErrCap) {
		t.Fatal(err)
	}
	n.job.errorText = strings.Repeat("x", maxCanonicalRequestBytes+1)
	if allocations := testing.AllocsPerRun(3, func() {
		if got, err := basicOriginalFromSnapshot(identity, n, source); !errors.Is(err, ErrCap) || got.scope.runID != "" || got.source.resolvedRoot != nil || got.run.revision != 0 {
			panic("over-cap projection accepted")
		}
	}); allocations != 0 {
		t.Fatal("over-cap projection allocated", allocations)
	}
}

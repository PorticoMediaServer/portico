package writer

import (
	"bytes"
	"math"
)

// Project the actual issuer capture into the immutable Basic request. This
// values-only step neither issues authority nor creates an operation identity.
// The caller keeps W's funded preparation alive across projection and probe.
func basicOriginalFromSnapshot(identity CommandIdentity, snapshot scanRunSnapshot, source scanSourceFacts) (basicOriginalScope, error) {
	remaining := maxCanonicalRequestBytes
	for _, raw := range [][]byte{source.source.resolvedRoot, source.identityRoot} {
		if len(raw) > remaining {
			return basicOriginalScope{}, ErrCap
		}
		remaining -= len(raw)
	}
	r, scope := snapshot.run, snapshot.run.scope
	for _, text := range [...]string{scope.runID, scope.libraryKey, scope.resolvedRoot, scope.sourceID, scope.incarnation, scope.rootIdentity, scope.scanMode,
		r.phase, r.finishedAt, snapshot.job.libraryKey, snapshot.job.status, snapshot.job.createdAt, snapshot.job.errorText,
		snapshot.root.identity, snapshot.root.revision, snapshot.root.cursor, snapshot.root.state,
		source.libraryKey, source.source.id, source.source.incarnation, source.source.rootIdentity} {
		if len(text) > remaining {
			return basicOriginalScope{}, ErrCap
		}
		remaining -= len(text)
	}
	if r.lastSequence == math.MaxInt64 {
		return basicOriginalScope{}, ErrCap
	}
	if !validScanRunSnapshot(snapshot) || identity.Incarnation == ([16]byte{}) || identity.OperationID == ([16]byte{}) ||
		identity.Incarnation != scope.commandIncarnation || identity.Sequence != r.lastSequence+1 ||
		r.acknowledgedSequence != r.lastSequence || r.leaseOwner <= 0 || r.leaseGeneration <= 0 || r.leaseExpiresMS <= 0 ||
		r.finishedAt != "" || r.rootRebound != 0 || snapshot.job.status != "running" || (r.phase != "inventory" && r.phase != "verifying") ||
		source.libraryID != scope.libraryID || source.libraryKey != scope.libraryKey || source.policyRevision != scope.policyRevision ||
		source.source.id != scope.sourceID || source.source.generation != scope.sourceGeneration || source.source.incarnation != scope.incarnation ||
		source.source.rootIdentity != scope.rootIdentity || source.source.followSymlinks != (scope.followSymlinks == 1) ||
		!bytes.Equal(source.source.resolvedRoot, []byte(scope.resolvedRoot)) {
		return basicOriginalScope{}, ErrConflict
	}
	return cloneBasicOriginal(basicOriginalScope{
		scope:  inventoryScope{runID: scope.runID, libraryID: scope.libraryID, libraryKey: scope.libraryKey},
		source: source.source,
		run: inventoryRun{revision: r.revision, policyRevision: scope.policyRevision, phase: r.phase, scanMode: scope.scanMode,
			leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, leaseExpiresMS: r.leaseExpiresMS,
			lastSequence: r.lastSequence, acknowledgedSequence: r.acknowledgedSequence},
	}), nil
}

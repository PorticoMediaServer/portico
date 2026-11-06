package writer

import "database/sql"

// Native effect values only. None of these values authenticates a source,
// authorizes birth or proves that an old lease generation has physically
// retired. W establishes those facts before calling on its SAME admitted turn.
type scanRunScope struct {
	runID, libraryKey, resolvedRoot, sourceID, incarnation, rootIdentity, scanMode string
	libraryID, followSymlinks, sourceGeneration, policyRevision                    int64
	commandIncarnation, createOperation                                            [16]byte
	createDigest                                                                   RequestDigest
}

type scanRunNative struct {
	scope                                                 scanRunScope
	phase, finishedAt                                     string
	authoritative, rootRebound, discovered                int64
	leaseOwner, leaseGeneration, leaseExpiresMS, attempts int64
	nextAttemptMS                                         sql.NullInt64
	revision, lastSequence, acknowledgedSequence          int64
}

type scanRunJob struct {
	libraryKey, status, createdAt, errorText string
	processed                                int64
}

type scanRunRoot struct {
	identity, revision, cursor, state string
	pages, skipped                    int64
}

type scanRunSnapshot struct {
	run  scanRunNative
	job  scanRunJob
	root scanRunRoot
}

type scanRunCreateEffects struct {
	scope     scanRunScope
	root      scanRunRoot // actual supervised directory witness, not source.root_identity
	identity  CommandIdentity
	request   RequestDigest
	appliedMS int64 // ONE trusted W time sample; this helper never reads a clock
}

type scanRunClaimMode uint8

const (
	scanRunInitialClaim scanRunClaimMode = iota + 1
	scanRunLiveRenew
	scanRunExpiredReclaim
)

type scanRunClaimEffects struct {
	before                scanRunSnapshot
	mode                  scanRunClaimMode // closed W selection; not authority/exclusion proof
	identity              CommandIdentity
	request               RequestDigest
	leaseOwner, expiresMS int64
	appliedMS             int64
}

type scanRunLifecycleKind uint8

const (
	scanRunCreated scanRunLifecycleKind = iota + 1
	scanRunClaimed
)

type scanRunLifecycleFacts struct {
	kind      scanRunLifecycleKind
	identity  CommandIdentity
	request   RequestDigest
	appliedMS int64
	post      scanRunSnapshot // ACTUAL native RETURNING/readback, including R and lease
}

type scanLifecycleReceiptEffects struct {
	facts  scanRunLifecycleFacts
	digest ResultDigest // W's closed lifecycle codec, never a request/result alias
}

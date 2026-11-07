package writer

import (
	"context"
	"database/sql"
	"math"
)

// Closed effect/receipt adapters, not ingress or birth/lease permits. The sole
// issuer first establishes current source scope, original-history miss, and
// creation absence or physical old-generation exclusion. Its SAME frame owns
// whole-command rollback and final physical publication; these return no Outcome.
func applyInventoryCreate(ctx context.Context, turn *commandTurn, input *inventoryCreateCommand) (value scanRunLifecycleResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if input == nil || !input.valid {
		return value, ErrInvalid
	}
	r := input.request
	bytes, err := scanLifecycleWorkingBytes(r.Source.resolvedRoot, r.Scope.runID, r.Scope.libraryKey,
		r.Source.id, r.Source.incarnation, r.Source.rootIdentity, r.ScanMode, r.RootIdentity, r.RootRevision)
	if err != nil {
		return value, err
	}
	if err = turn.requireScanLifecycleMemory(ctx, bytes); err != nil {
		return value, err
	}
	applied, err := turn.scanCreateTimeMS(ctx)
	if err != nil {
		return value, err
	}
	follow := int64(0)
	if r.Source.followSymlinks {
		follow = 1
	}
	scope := scanRunScope{runID: r.Scope.runID, libraryID: r.Scope.libraryID, libraryKey: r.Scope.libraryKey,
		resolvedRoot: string(r.Source.resolvedRoot), followSymlinks: follow, sourceID: r.Source.id,
		sourceGeneration: r.Source.generation, incarnation: r.Source.incarnation, rootIdentity: r.Source.rootIdentity,
		policyRevision: r.PolicyRevision, scanMode: r.ScanMode, commandIncarnation: input.identity.Incarnation,
		createOperation: input.identity.OperationID, createDigest: input.digest}
	facts, err := applyScanRunCreate(ctx, turn, scanRunCreateEffects{scope: scope,
		root:     scanRunRoot{identity: r.RootIdentity, revision: r.RootRevision, state: "pending"},
		identity: input.identity, request: input.digest, appliedMS: applied})
	if err != nil {
		return value, err
	}
	return persistScanRunLifecycleResult(ctx, turn, facts)
}

func applyInventoryClaim(ctx context.Context, turn *commandTurn, input *inventoryClaimCommand, mode scanRunClaimMode, owner, expiry int64) (value scanRunLifecycleResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if input == nil || !input.valid {
		return value, ErrInvalid
	}
	r, o := input.request, input.request.Original
	bytes, err := scanLifecycleWorkingBytes(o.ResolvedRoot, o.RunID, o.LibraryKey, o.SourceID, o.SourceIncarnation,
		o.RootIdentity, o.ScanMode, r.Run.phase, r.Run.scanMode, r.Root.Path, r.Root.Identity, r.Root.Revision,
		r.Root.Cursor, r.Root.State, r.FinishedAt, r.JobStatus, r.JobCreatedAt, r.JobErrorText)
	if err != nil {
		return value, err
	}
	if err = turn.requireScanLifecycleMemory(ctx, bytes); err != nil {
		return value, err
	}
	applied, err := turn.scanClaimTimeMS(ctx, mode, r.Run.leaseExpiresMS, expiry)
	if err != nil {
		return value, err
	}
	if r.NextAttemptMS.Present && r.NextAttemptMS.Value > applied {
		return value, ErrConflict
	}
	before := scanClaimSnapshot(input)
	facts, err := applyScanRunClaim(ctx, turn, scanRunClaimEffects{before: before, mode: mode,
		identity: input.identity, request: input.digest, leaseOwner: owner, expiresMS: expiry, appliedMS: applied})
	if err != nil {
		return value, err
	}
	return persistScanRunLifecycleResult(ctx, turn, facts)
}

func scanClaimSnapshot(input *inventoryClaimCommand) scanRunSnapshot {
	r, o := input.request, input.request.Original
	return scanRunSnapshot{run: scanRunNative{scope: scanNativeOriginal(o, input.identity.Incarnation), phase: r.Run.phase, finishedAt: r.FinishedAt,
		authoritative: r.Authoritative, rootRebound: r.RootRebound, discovered: r.Discovered,
		leaseOwner: r.Run.leaseOwner, leaseGeneration: r.Run.leaseGeneration, leaseExpiresMS: r.Run.leaseExpiresMS,
		attempts: r.Attempts, nextAttemptMS: sql.NullInt64{Int64: r.NextAttemptMS.Value, Valid: r.NextAttemptMS.Present},
		revision: r.Run.revision, lastSequence: r.Run.lastSequence, acknowledgedSequence: r.Run.acknowledgedSequence},
		job:  scanRunJob{libraryKey: o.LibraryKey, status: r.JobStatus, createdAt: r.JobCreatedAt, errorText: r.JobErrorText, processed: r.Processed},
		root: scanRunRoot{identity: r.Root.Identity, revision: r.Root.Revision, cursor: r.Root.Cursor, state: r.Root.State, pages: r.Root.Pages, skipped: r.Root.Skipped}}
}

func scanNativeOriginal(o scanRunOriginalScope, incarnation [16]byte) scanRunScope {
	follow := int64(0)
	if o.FollowSymlinks {
		follow = 1
	}
	return scanRunScope{runID: o.RunID, libraryID: o.LibraryID, libraryKey: o.LibraryKey,
		resolvedRoot: string(o.ResolvedRoot), followSymlinks: follow, sourceID: o.SourceID,
		sourceGeneration: o.SourceGeneration, incarnation: o.SourceIncarnation, rootIdentity: o.RootIdentity,
		policyRevision: o.PolicyRevision, scanMode: o.ScanMode, commandIncarnation: incarnation,
		createOperation: o.CreateOperationID, createDigest: o.CreateRequestDigest}
}

// Full original payload is forecast for 32 simultaneous copies: frozen input,
// binds, six complete readback cohorts, RETURNING/receipt verification, result
// and digest work. The fixed envelope covers their bounded scalar/row metadata.
// This is a prospective allocation bound, not measured physical qualification
// or an installed production budget. Native/common reserve is separate.
func scanLifecycleWorkingBytes(root []byte, texts ...string) (uint64, error) {
	n := uint64(len(root))
	for _, s := range texts {
		if uint64(len(s)) > math.MaxUint64-n {
			return 0, ErrCap
		}
		n += uint64(len(s))
	}
	const fixed = uint64(64 << 10)
	if n > (math.MaxUint64-fixed)/32 {
		return 0, ErrCap
	}
	return fixed + n*32, nil
}

func persistScanRunLifecycleResult(ctx context.Context, turn *commandTurn, facts scanRunLifecycleFacts) (value scanRunLifecycleResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	s, p := facts.post.run.scope, facts.post
	value = scanRunLifecycleResult{Original: scanRunOriginalScope{RunID: s.runID, LibraryID: s.libraryID, LibraryKey: s.libraryKey,
		SourceID: s.sourceID, SourceGeneration: s.sourceGeneration, SourceIncarnation: s.incarnation, RootIdentity: s.rootIdentity,
		ResolvedRoot: []byte(s.resolvedRoot), FollowSymlinks: s.followSymlinks == 1, PolicyRevision: s.policyRevision, ScanMode: s.scanMode,
		CreateOperationID: s.createOperation, CreateRequestDigest: s.createDigest},
		AcceptedRunRevision: p.run.revision, AppliedMS: facts.appliedMS, LeaseOwner: p.run.leaseOwner,
		LeaseGeneration: p.run.leaseGeneration, LeaseExpiresMS: p.run.leaseExpiresMS, AcknowledgedSequence: p.run.acknowledgedSequence,
		Discovered: p.run.discovered, Processed: p.job.processed, RootIdentity: p.root.identity, RootRevision: p.root.revision,
		RootCursor: p.root.cursor, RootPages: p.root.pages, RootSkipped: p.root.skipped}
	var digest ResultDigest
	switch facts.kind {
	case scanRunCreated:
		digest, err = inventoryCreateResultDigest(facts.identity, facts.request, inventoryCreateResult{value})
	case scanRunClaimed:
		digest, err = inventoryClaimResultDigest(facts.identity, facts.request, inventoryClaimResult{value})
	default:
		err = ErrInvalid
	}
	if err == nil {
		err = writeScanLifecycleReceipt(ctx, turn, scanLifecycleReceiptEffects{facts: facts, digest: digest})
	}
	return value, err
}

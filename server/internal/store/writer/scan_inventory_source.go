package writer

import (
	"bytes"
	"context"
)

// checkInventoryDriverFreshSource belongs only after the original receipt
// miss, on the complete driver turn. It does not install that turn or enable
// effects. Historical resolution never calls this fresh-only reader.
func checkInventoryDriverFreshSource(ctx context.Context, turn *commandTurn, in *inventoryInvocation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != InventoryAccept ||
		in == nil || in.input == nil || !in.input.valid || in.issuer == nil {
		return ErrInvalid
	}
	v := in.issuer
	if v.action != scanIssuerN1Apply || v.frame != turn.transaction || v.retained == nil || v.retained.source == nil {
		return ErrInvalid
	}
	n := v.retained.inventory
	if n == nil || n.closed || n.observationFailed.Load() || n.preparation != v.retained ||
		n.command != in.input || n.reference != in.request {
		return ErrConflict
	}
	r := in.input.request
	actual, err := captureScanSource(ctx, turn, r.scope.libraryID, r.scope.libraryKey, r.source.id)
	if err != nil {
		return err
	}
	if !inventoryDriverSourceMatches(in.input, *v.retained.source, actual) {
		return ErrConflict
	}
	// The source tuple alone does not prove this run still owns the active
	// source slot. Recheck that actual point on the same protected snapshot.
	return checkScanSourceActive(ctx, turn, r.source.id, r.scope.runID)
}

// Values-only equality, not an authority witness. The actual reader above
// supplies current facts in the same transaction; retained facts bind the
// logical filename root as well as the separate physical source root.
func inventoryDriverSourceMatches(c *inventoryAcceptCommand, prepared, actual scanSourceFacts) bool {
	if c == nil || !c.valid {
		return false
	}
	r := c.request
	o := scanRunOriginalScope{LibraryID: r.scope.libraryID, LibraryKey: r.scope.libraryKey,
		SourceID: r.source.id, SourceIncarnation: r.source.incarnation, SourceGeneration: r.source.generation,
		ResolvedRoot: r.source.resolvedRoot, RootIdentity: r.source.rootIdentity,
		FollowSymlinks: r.source.followSymlinks, PolicyRevision: r.run.policyRevision}
	return prepared.matches(o) && actual.matches(o) && bytes.Equal(actual.identityRoot, prepared.identityRoot)
}

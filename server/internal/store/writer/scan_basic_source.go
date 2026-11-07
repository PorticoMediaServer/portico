package writer

import "context"

func basicSourcePreparationPurpose(purpose CommandID) bool {
	return purpose == BasicStage || purpose == BasicSeal || purpose == BasicAccept
}

// Issuer-owned read-only preparation, not a Basic effect or an N1 observation.
// The existing Claim issuer profile funds current native reads. Before values
// leave that frame, its SAME governor admits a distinct original ready holding
// for the exact Basic purpose, in mode3. No worker can mint or rebind it.
func prepareBasicSource(ctx context.Context, d *ScanDriver, lease *ScanRunLease, last ScanRunReference, purpose CommandID) (prep *scanPreparation, snapshot scanRunSnapshot, source scanSourceFacts, err error) {
	life := ctx
	locked := false
	var candidate *scanPreparation
	defer func() {
		if !locked {
			return
		}
		if err != nil && candidate != nil {
			candidate.mu.Lock()
			candidate.failed = true // retained for ReleasePending, never silently refunded
			candidate.mu.Unlock()
		}
		d.mu.Unlock()
	}()
	if d != nil && d.self == d {
		life = d.lifetime
	}
	defer func() {
		v, e := finishCommand(ctx, life, struct {
			Preparation *scanPreparation
			Snapshot    scanRunSnapshot
			Source      scanSourceFacts
		}{prep, snapshot, source}, err)
		prep, snapshot, source, err = v.Preparation, v.Snapshot, v.Source, e
	}()
	if !basicSourcePreparationPurpose(purpose) || ctx == nil || last.Purpose != InventoryClaim || !validScanInventoryReference(last.Scan) {
		return nil, snapshot, source, ErrInvalid
	}
	if err = d.current(); err != nil {
		return nil, snapshot, source, err
	}
	if !d.mu.TryLock() {
		return nil, snapshot, source, ErrBusy
	}
	locked = true
	if d.preparation != nil || d.operation != nil || d.work != nil || d.birth != nil {
		return nil, snapshot, source, ErrBusy
	}
	call := &scanIssuerInvocation{driver: d, action: scanIssuerBasicSource, lease: lease, basicPurpose: purpose}
	out, err := d.invokeIssuer(ctx, last.Scan, InventoryClaim, call, false)
	if err != nil {
		return nil, snapshot, source, err
	}
	candidate = d.preparation
	if candidate == nil || candidate.source == nil || candidate.mode != scanBasicPreparation || candidate.expected.Key.Domain != uint16(purpose) {
		return nil, snapshot, source, ErrInvalid
	}
	if err = candidate.current(); err != nil {
		return nil, snapshot, source, err
	}
	return candidate, out.run, out.source, nil
}

func runBasicSourcePreparationTurn(ctx context.Context, turn *commandTurn, in *inventoryInvocation) (out commandOutput, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Out        commandOutput
			Historical bool
		}{out, historical}, err)
		out, historical, err = v.Out, v.Historical, e
	}()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != InventoryClaim || in == nil || in.issuer == nil || in.issuer.action != scanIssuerBasicSource || in.issuer.frame != turn.transaction {
		return out, false, ErrInvalid
	}
	v, q := in.issuer, in.request
	if !basicSourcePreparationPurpose(v.basicPurpose) || v.lease == nil {
		return out, false, ErrInvalid
	}
	out.run, err = readCheckedScanRunSnapshot(ctx, turn, q.RunID)
	if err != nil {
		return out, false, err
	}
	r := out.run.run
	original := scanOriginalFromNative(r.scope)
	current := inventoryRun{leaseOwner: r.leaseOwner, leaseGeneration: r.leaseGeneration, leaseExpiresMS: r.leaseExpiresMS}
	if !v.lease.matches(original, current) || v.lease.incarnation != r.scope.commandIncarnation || r.scope.libraryID != q.LibraryID || r.scope.libraryKey != q.LibraryKey ||
		r.acknowledgedSequence != r.lastSequence || q.Receipt.Identity.Sequence > r.acknowledgedSequence || r.finishedAt != "" || r.rootRebound != 0 || out.run.job.status != "running" {
		return out, false, ErrConflict
	}
	if _, err = turn.commandTimeMS(ctx, InventoryClaim, r.leaseExpiresMS); err != nil {
		return out, false, err
	}
	if err = checkScanSourceActive(ctx, turn, r.scope.sourceID, q.RunID); err != nil {
		return out, false, err
	}
	out.source, err = captureScanSource(ctx, turn, q.LibraryID, q.LibraryKey, r.scope.sourceID)
	if err != nil {
		return out, false, err
	}
	if !out.source.matches(original) {
		return out, false, ErrConflict
	}
	extra, err := scanSnapshotWorkingBytes(out.run)
	if err != nil {
		return out, false, err
	}
	_, err = v.driver.retainSourcePreparation(ctx, turn, in, out.source, extra)
	return out, true, err // read-only native facts, never Applied or receipt mutation
}

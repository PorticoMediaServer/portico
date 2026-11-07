package writer

import (
	"context"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Called by the sole dispatcher while its source/snapshot frame is owned.
// The preparation holding is acquired BEFORE its payload is transferred from
// the writer frame. On every late failure the driver retains the holding.
func (d *ScanDriver) retainSourcePreparation(ctx context.Context, turn *commandTurn, in *inventoryInvocation, source scanSourceFacts, extra uint64) (out *scanPreparation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if d.preparation != nil || in.issuer == nil || in.issuer.deadline.IsZero() {
		return nil, ErrBusy
	}
	required, e := scanLifecycleWorkingBytes(source.source.resolvedRoot, source.libraryKey, source.source.id, source.source.incarnation, source.source.rootIdentity)
	if e == nil {
		if uint64(len(source.identityRoot)) > (math.MaxUint64-required)/32 {
			e = ErrCap
		} else {
			required += uint64(len(source.identityRoot)) * 32
		}
	}
	if e != nil {
		return nil, e
	}
	if extra > math.MaxUint64-required {
		return nil, ErrCap
	}
	required += extra
	return d.retainIssuerPreparation(ctx, turn, in, &source, required)
}

func (d *ScanDriver) retainIssuerPreparation(ctx context.Context, turn *commandTurn, in *inventoryInvocation, source *scanSourceFacts, required uint64) (out *scanPreparation, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if d.preparation != nil || in.issuer == nil || in.issuer.deadline.IsZero() {
		return nil, ErrBusy
	}
	if (source == nil) != (in.issuer.action == scanIssuerRecover || in.issuer.action == scanIssuerN1Recover) {
		return nil, ErrInvalid
	}
	plan := d.readyPlan
	if plan.ready <= 0 || plan.need[governor.MemoryBytes] <= 0 {
		return nil, &Refusal{Code: RefusalUnqualified}
	}
	if required > uint64(plan.need[governor.MemoryBytes]) {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	r := d.writer.effective
	purpose := in.lifecycle
	if in.issuer.action == scanIssuerBasicSource {
		if !basicSourcePreparationPurpose(in.issuer.basicPurpose) || source == nil {
			return nil, ErrInvalid
		}
		purpose = in.issuer.basicPurpose
	}
	if purpose == 0 && in.issuer.action == scanIssuerN1Recover {
		purpose = InventoryAccept
	}
	expected := governor.Request{Key: governor.JobKey{Domain: uint16(purpose), ID: in.request.RunID}, Owner: r.owner,
		Class: governor.ScanMinimum, Writer: false, Need: plan.need, IO: plan.io, MaxHold: plan.ready,
		Deadline: in.issuer.deadline, Budget: plan.budget, Footprint: []governor.Scope{{Library: in.request.LibraryID, Domain: uint16(purpose)}}}
	decision := r.governor.Try(expected)
	if decision.Grant == nil {
		return nil, effectiveAdmissionRefusal(decision.Refusal)
	}
	var candidate *scanPreparation
	defer func() {
		if candidate == nil {
			err = errors.Join(err, r.authority.Retired(decision.Grant))
		}
	}()
	if in.issuer.action == scanIssuerBasicSource {
		candidate, err = newScanPreparation(ctx, d.opening, d.owner, r, nativeinventory.Observer{}, expected, decision.Grant, nativeinventory.Request{}, scanBasicPreparation)
	} else if source == nil {
		candidate, err = newScanPreparation(ctx, d.opening, d.owner, r, nativeinventory.Observer{}, expected, decision.Grant, nativeinventory.Request{}, scanHistoryPreparation)
	} else {
		request := nativeinventory.Request{Root: source.source.resolvedRoot, RootIdentity: source.source.rootIdentity, RelativePath: []byte("."), FollowSymlinks: source.source.followSymlinks, Limit: 1}
		candidate, err = newScanPreparation(ctx, d.opening, d.owner, r, d.observer, expected, decision.Grant, request)
	}
	if err != nil {
		return nil, err
	}
	// Attach BEFORE returning source data or starting a helper. Its original
	// reservation remains charged through result consumption and Close.
	candidate.stopDriver = context.AfterFunc(d.lifetime, candidate.cancel)
	candidate.frame = turn.transaction
	candidate.source = source
	d.preparation = candidate
	return candidate, nil
}

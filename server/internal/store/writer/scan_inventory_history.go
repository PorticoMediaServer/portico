package writer

import (
	"context"
	"errors"
	"slices"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// These closed actions reuse the one dispatcher. They never recapture source,
// root or lease authority from a historical receipt.
func isScanInventoryHistory(action scanIssuerAction) bool {
	return action == scanIssuerN1History || action == scanIssuerN1Recover || action == scanIssuerN1Apply
}

func runScanInventoryHistory(ctx context.Context, turn *commandTurn, in *inventoryInvocation, ack bool) (out commandOutput, floor AckResult, historical bool, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Output     commandOutput
			Floor      AckResult
			Historical bool
		}{out, floor, historical}, err)
		out, floor, historical, err = v.Output, v.Floor, v.Historical, e
	}()
	lookup, err := lookupInventoryAccept(ctx, turn, in.request)
	if err != nil {
		return out, floor, false, err
	}
	if ack {
		floor.AcknowledgedThrough, err = acknowledgeInventory(ctx, turn, in.request, lookup)
		return out, floor, true, err
	}
	if in.issuer.action == scanIssuerN1Apply && lookup.decision == receiptFresh {
		// History wins before fresh eligibility. Only the installed complete
		// driver plan may reach the domain body on this SAME protected frame.
		// A funded observation or history-only registry is still insufficient.
		if in.issuer.driver.writer.effective.inventoryDriverPlan == nil {
			if err = checkInventoryFreshLibrary(ctx, turn, in.request); err == nil {
				err = &Refusal{Code: RefusalUnqualified}
			}
			return out, floor, false, err
		}
		if err = checkInventoryDriverTurn(ctx, turn, in); err == nil {
			err = checkInventoryFreshLibrary(ctx, turn, in.request)
		}
		var sourceRecordBytes uint64
		if err == nil {
			sourceRecordBytes, err = checkInventoryDriverFreshSource(ctx, turn, in)
		}
		if err == nil {
			err = checkInventoryExistingRecords(ctx, turn, in, sourceRecordBytes)
		}
		if err == nil {
			out.inventory, err = applyInventoryAccept(ctx, turn, in.input)
		}
		return out, floor, false, err
	}
	if retained := in.issuer.retained; retained != nil {
		required, e := scanInventoryRetainedBytes(in.request, inventoryHistoricalResult(in.request, lookup))
		if e != nil {
			return out, floor, false, e
		}
		if n := retained.inventory; n != nil {
			if n.preparedBytes > ^uint64(0)-required {
				return out, floor, false, ErrCap
			}
			required += n.preparedBytes
		}
		if retained.expected.Need[governor.MemoryBytes] <= 0 || required > uint64(retained.expected.Need[governor.MemoryBytes]) {
			return out, floor, false, &Refusal{Code: RefusalCapacity}
		}
	}
	out.inventory = inventoryHistoricalResult(in.request, lookup)
	if in.issuer.action == scanIssuerN1Recover {
		required, e := scanInventoryRetainedBytes(in.request, out.inventory)
		if e != nil {
			return out, floor, false, e
		}
		_, err = in.issuer.driver.retainIssuerPreparation(ctx, turn, in, nil, required)
	}
	return out, floor, lookup.decision != receiptFresh, err
}

func scanInventoryRetainedBytes(q ScanReceiptRequest, v InventoryResult) (uint64, error) {
	if len(v.Members) > inventoryReceiptMaxMembers {
		return 0, ErrCap
	}
	required, err := scanLifecycleWorkingBytes(nil, q.RunID, q.LibraryKey, v.RunID, v.SourceID, v.SourceIncarnation, v.SourceRevision, v.NextCursor)
	if err != nil {
		return 0, err
	}
	for _, m := range v.Members {
		// Same retained/copy allowance as lifecycle TEXT, plus fixed child storage.
		if required > ^uint64(0)-512 || uint64(len(m.ObjectID)) > (^uint64(0)-required-512)/32 {
			return 0, ErrCap
		}
		required += 512 + 32*uint64(len(m.ObjectID))
	}
	return required, nil
}

func validScanInventoryReference(q ScanReceiptRequest) bool {
	return q.RunID != "" && q.LibraryID > 0 && q.LibraryKey != "" && q.Receipt.Identity.Sequence > 0 &&
		len(q.RunID) <= maxCanonicalRequestBytes && len(q.LibraryKey) <= maxCanonicalRequestBytes-len(q.RunID) &&
		utf8.ValidString(q.RunID) && utf8.ValidString(q.LibraryKey)
}

// ResolveScan owns retained result memory under the driver. A restart creates
// a new history-only preparation from current authority and the exact ORIGINAL
// reference; native source and lease freshness are deliberately irrelevant.
// The consumer may receive one saved payload before AckScan/Close releases it.
func (d *ScanDriver) ResolveScan(ctx context.Context, q ScanReceiptRequest) (value InventoryResult, err error) {
	life := ctx
	var n *N1Preparation
	locked, published := false, false
	defer func() {
		if !locked {
			return
		}
		if err != nil && published && n != nil {
			n.resultPublished = false
		}
		d.mu.Unlock()
	}()
	if d != nil && d.self == d {
		life = d.lifetime
	}
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if err = d.current(); err != nil {
		return value, err
	}
	if !validScanInventoryReference(q) {
		return value, ErrInvalid
	}
	if !d.mu.TryLock() {
		return value, ErrBusy
	}
	locked = true
	if d.operation != nil || d.birth != nil {
		return value, ErrBusy
	}
	action := scanIssuerN1Recover
	call := &scanIssuerInvocation{driver: d, action: action}
	if d.preparation != nil {
		n = d.preparation.inventory
		if n == nil || n.closed || n.preparation != d.preparation {
			return value, ErrBusy
		}
		if n.reference != q {
			return value, ErrConflict
		}
		if n.resultPublished {
			return value, ErrBusy
		}
		life = n.preparation.lifetime
		call.action, call.retained, call.deadline = scanIssuerN1History, n.preparation, n.preparation.expected.Deadline
	}
	if nbytes, e := scanInventoryRetainedBytes(q, InventoryResult{}); e != nil || d.readyPlan.need[governor.MemoryBytes] <= 0 || nbytes > uint64(d.readyPlan.need[governor.MemoryBytes]) {
		return value, errors.Join(&Refusal{Code: RefusalCapacity}, e)
	}
	out, _, err := d.writer.effective.runCommand(ctx, d.writer, nil, EnrichmentReceiptRequest{}, nil, false,
		&inventoryInvocation{disclosure: d.sourceDisclosure(q), request: q, issuer: call})
	if err != nil {
		return value, err
	}
	if n == nil {
		p := d.preparation
		if p == nil || p.mode != scanHistoryPreparation {
			return value, ErrInvalid
		}
		n = &N1Preparation{driver: d, preparation: p, reference: q, historyOnly: true}
		n.self, p.inventory = n, n
		life = p.lifetime
	}
	if bytes, e := scanInventoryRetainedBytes(q, out.inventory); e != nil || bytes > uint64(n.preparation.expected.Need[governor.MemoryBytes]) {
		return value, errors.Join(&Refusal{Code: RefusalCapacity}, e)
	}
	n.last = out.inventory
	n.last.Outcome.Evidence = nil // execution evidence is output, never owner authority
	n.last.Members = slices.Clone(out.inventory.Members)
	n.resultPublished = true
	published = true
	return out.inventory, nil
}

// AckScan is the consumer's explicit handoff after durable consumption. The
// native helper revalidates this SAME original in the SAME transaction. ACK
// refunds nothing: source/result/prepared bytes stay charged until Close.
func (d *ScanDriver) AckScan(ctx context.Context, q ScanReceiptRequest) (value AckResult, err error) {
	life := ctx
	var n *N1Preparation
	locked, stateCaptured := false, false
	var previousAck, previousPublication bool
	defer func() {
		if !locked {
			return
		}
		if err != nil && n != nil && stateCaptured {
			n.acknowledged, n.resultPublished = previousAck, previousPublication
		}
		d.mu.Unlock()
	}()
	if d != nil && d.self == d {
		life = d.lifetime
	}
	defer func() { value, err = finishCommand(ctx, life, value, err) }()
	if err = d.current(); err != nil {
		return value, err
	}
	if !validScanInventoryReference(q) {
		return value, ErrInvalid
	}
	if !d.mu.TryLock() {
		return value, ErrBusy
	}
	locked = true
	if d.preparation == nil || d.operation != nil {
		return value, ErrInvalid
	}
	n = d.preparation.inventory
	if n == nil || n.closed || n.preparation != d.preparation || n.reference != q {
		return value, ErrConflict
	}
	previousAck, previousPublication = n.acknowledged, n.resultPublished
	stateCaptured = true
	if !n.resultPublished && !n.acknowledged {
		return value, ErrResolveRequired
	}
	if n.last.Outcome.State != Applied && n.last.Outcome.State != ResultRetired {
		return value, ErrResolveRequired
	}
	life = n.preparation.lifetime
	call := &scanIssuerInvocation{driver: d, action: scanIssuerN1History, retained: n.preparation, deadline: n.preparation.expected.Deadline}
	_, value, err = d.writer.effective.runCommand(ctx, d.writer, nil, EnrichmentReceiptRequest{}, nil, true,
		&inventoryInvocation{disclosure: d.sourceDisclosure(q), request: q, issuer: call})
	if err == nil {
		n.acknowledged = true
		n.resultPublished = false
	}
	return value, err
}

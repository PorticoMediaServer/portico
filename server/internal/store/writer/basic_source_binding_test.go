package writer

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func basicBindingIdentity(last ScanRunReference) CommandIdentity {
	identity := last.Scan.Receipt.Identity
	identity.Sequence++
	identity.OperationID = [16]byte{0x71}
	return identity
}

func basicBindingRunVector(t *testing.T, d *ScanDriver, runID string) (value [4]int64) {
	t.Helper()
	if err := d.opening.executor.db.QueryRowContext(t.Context(), `SELECT job_revision,last_command_sequence,acknowledged_command_sequence,job_lease_generation FROM inventory_runs WHERE job_id=?`, runID).Scan(&value[0], &value[1], &value[2], &value[3]); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestBasicSourceBindingOwnsActualOriginalAndHolding(t *testing.T) {
	for _, purpose := range []BasicPurpose{BasicPurposeStage, BasicPurposeSeal, BasicPurposeAccept} {
		t.Run(string(purpose), func(t *testing.T) {
			f, lease, last := basicSourceIssuerFixture(t)
			d := f.driver
			before := basicBindingRunVector(t, d, last.Scan.RunID)
			identity := basicBindingIdentity(last)
			p, original, err := prepareBasicCommandOriginal(t.Context(), d, lease, last, purpose, identity)
			if err != nil || p == nil || p != d.preparation || p.source == nil {
				t.Fatal(p, original, err)
			}
			kind, _ := basicResourceCommand(purpose)
			if p.mode != scanBasicPreparation || p.expected.Key.Domain != uint16(kind) || p.expected.Writer ||
				original.scope.runID != last.Scan.RunID || original.scope.libraryID != last.Scan.LibraryID || original.scope.libraryKey != last.Scan.LibraryKey ||
				original.run.revision != before[0] || original.run.lastSequence != before[1] || original.run.acknowledgedSequence != before[2] || original.run.leaseGeneration != before[3] ||
				!reflect.DeepEqual(original.source, p.source.source) || original.run.policyRevision != p.source.policyRevision {
				t.Fatal("binding changed an actual native value or purpose", original)
			}
			if after := basicBindingRunVector(t, d, last.Scan.RunID); after != before {
				t.Fatal("source binding changed run progress", before, after)
			}
			if d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
				t.Fatal("holding refunded while the consumer owns the projection")
			}
			root := bytes.Clone(p.source.source.resolvedRoot)
			original.source.resolvedRoot[0] = '!'
			if !bytes.Equal(root, p.source.source.resolvedRoot) {
				t.Fatal("consumer projection aliases W's native capture")
			}
			original = basicOriginalScope{} // release consumer-owned values first
			if err := p.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := d.ReleasePending(t.Context()); err != nil || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("physically returned holding not retired", err)
			}
		})
	}
}

func TestBasicSourceBindingRefusesWrongIdentityWithoutLosingCustody(t *testing.T) {
	f, lease, last := basicSourceIssuerFixture(t)
	d := f.driver
	identity := basicBindingIdentity(last)
	identity.Sequence++
	p, original, err := prepareBasicCommandOriginal(t.Context(), d, lease, last, BasicPurposeStage, identity)
	if !errors.Is(err, ErrConflict) || p != nil || !reflect.DeepEqual(original, basicOriginalScope{}) {
		t.Fatal(p, original, err)
	}
	if d.preparation == nil || d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("mapping refusal abandoned the funded original")
	}
	if err := d.ReleasePending(context.Background()); err != nil || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("retained original did not physically retire", err)
	}
}

// Cancel only the holding during the adapter's final release check. The outer
// caller remains live, so checking only its lifetime would publish stale facts.
type basicBindingHoldingCancelContext struct {
	context.Context
	driver *ScanDriver
	fired  atomic.Bool
}

func (c *basicBindingHoldingCancelContext) Err() error {
	var pcs [24]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, ".prepareBasicCommandOriginal.func") && c.fired.CompareAndSwap(false, true) {
			if p := c.driver.preparation; p != nil {
				p.cancel()
			}
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

func TestBasicSourceBindingLateCancellationRedactsOriginal(t *testing.T) {
	f, lease, last := basicSourceIssuerFixture(t)
	d := f.driver
	ctx := &basicBindingHoldingCancelContext{Context: t.Context(), driver: d}
	p, original, err := prepareBasicCommandOriginal(ctx, d, lease, last, BasicPurposeStage, basicBindingIdentity(last))
	if !ctx.fired.Load() || !errors.Is(err, context.Canceled) || p != nil || !reflect.DeepEqual(original, basicOriginalScope{}) {
		t.Fatal("canceled original escaped final release", p, original, err)
	}
	if d.preparation == nil || d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("discarded projection refunded live custody")
	}
	if err := d.ReleasePending(context.Background()); err != nil || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal(err)
	}
}

func TestBasicSourceBindingPurposeIsClosedBeforeIssuance(t *testing.T) {
	for _, purpose := range []BasicPurpose{"", "inventory_accept"} {
		p, original, err := prepareBasicCommandOriginal(t.Context(), nil, nil, ScanRunReference{}, purpose, CommandIdentity{})
		if !errors.Is(err, ErrInvalid) || p != nil || !reflect.DeepEqual(original, basicOriginalScope{}) {
			t.Fatal(purpose, p, original, err)
		}
	}
}

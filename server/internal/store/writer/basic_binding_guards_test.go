package writer

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Interpose only at existing context checks; no production test hook or second
// source issuer is needed. The callbacks run synchronously on the calling actor.
type basicBindingGuardContext struct {
	context.Context
	check func() error
}

func (c *basicBindingGuardContext) Err() error { return errors.Join(c.Context.Err(), c.check()) }
func basicBindingInFunction(fragment string) bool {
	var pcs [40]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	for {
		f, more := frames.Next()
		if strings.Contains(f.Function, fragment) {
			return true
		}
		if !more {
			return false
		}
	}
}

func TestBasicSourceBindingActorExcludesReleaseThroughFinish(t *testing.T) {
	f, lease, last := basicSourceIssuerFixture(t)
	d := f.driver
	installed, checks := false, 0
	outer := &basicBindingGuardContext{Context: t.Context()}
	outer.check = func() error {
		// W has finished its native validation but still owns its actor. Replace
		// only the test context wrapper, preserving the actual funded lifetime.
		if !installed && d.preparation != nil && basicBindingInFunction(".prepareBasicSource.func2") {
			installed = true
			p := d.preparation
			life := p.lifetime
			p.lifetime = &basicBindingGuardContext{Context: life, check: func() error {
				if !basicBindingInFunction(".prepareBasicCommandOriginal") {
					return nil
				}
				checks++
				if d.mu.TryLock() {
					d.mu.Unlock()
					t.Error("projection or final publication escaped driver actor")
				}
				before := d.writer.effective.authority.Usage()
				if err := d.ReleasePending(context.Background()); !errors.Is(err, ErrBusy) {
					t.Errorf("release during projection: %v", err)
				}
				if before == ([4]governor.Amounts{}) || d.writer.effective.authority.Usage() != before {
					t.Error("projection custody refunded")
				}
				return nil
			}}
		}
		return nil
	}
	p, original, err := prepareBasicCommandOriginal(outer, d, lease, last, BasicPurposeAccept, basicBindingIdentity(last))
	if err != nil || p == nil || !installed || checks < 3 || original.scope.runID != last.Scan.RunID {
		t.Fatal("actor checks not exercised", installed, checks, p, err)
	}
	original = basicOriginalScope{}
	if err := d.ReleasePending(context.Background()); err != nil || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("post-consumer release", err)
	}
}

func TestBasicSourceBindingRejectsChangedHandoffWithoutRefund(t *testing.T) {
	for _, mode := range []string{"pointer", "mode", "domain"} {
		t.Run(mode, func(t *testing.T) {
			f, lease, last := basicSourceIssuerFixture(t)
			d := f.driver
			var saved *scanPreparation
			var restore func()
			ctx := &basicBindingGuardContext{Context: t.Context()}
			ctx.check = func() error {
				if saved == nil && d.preparation != nil && basicBindingInFunction(".prepareBasicSource.func2") {
					saved = d.preparation
					switch mode {
					case "pointer":
						d.preparation = nil
						restore = func() { d.preparation = saved }
					case "mode":
						old := saved.mode
						saved.mode = scanHistoryPreparation
						restore = func() { saved.mode = old }
					case "domain":
						old := saved.expected.Key.Domain
						saved.expected.Key.Domain = uint16(InventoryAccept)
						restore = func() { saved.expected.Key.Domain = old }
					}
				}
				return nil
			}
			p, original, err := prepareBasicCommandOriginal(ctx, d, lease, last, BasicPurposeAccept, basicBindingIdentity(last))
			if saved == nil {
				t.Fatal("handoff not reached")
			}
			restore()
			if !errors.Is(err, ErrInvalid) || p != nil || !reflect.DeepEqual(original, basicOriginalScope{}) {
				t.Fatal("changed handoff released facts", p, original, err)
			}
			if d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
				t.Fatal("changed handoff refunded original")
			}
			if err := d.ReleasePending(context.Background()); err != nil || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal(err)
			}
		})
	}
}

func TestBasicSourceBindingNilContextRefusesBeforeIssuance(t *testing.T) {
	p, original, err := prepareBasicCommandOriginal(nil, nil, nil, ScanRunReference{}, BasicPurposeAccept, CommandIdentity{})
	if !errors.Is(err, ErrInvalid) || p != nil || !reflect.DeepEqual(original, basicOriginalScope{}) {
		t.Fatal(p, original, err)
	}
}

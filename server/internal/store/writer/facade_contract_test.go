package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// A marker is never an execution callback. These panic if an entry attempts to
// invoke a payload rather than use its closed, compiled dispatcher.
type contractInventory struct{}
type contractPublisher struct{}

func (contractInventory) isInventoryAcceptCommand()  { panic("marker executed") }
func (contractPublisher) isSearchImageStageCommand() { panic("marker executed") }

func TestFacadeContractRefusesEveryEntry(t *testing.T) {
	for _, w := range []*Writer{nil, {}} {
		for _, ctx := range []context.Context{nil, context.Background()} {
			for _, c := range []struct {
				name string
				call func() (any, error)
				zero any
			}{
				{"inventory", func() (any, error) { return w.AcceptInventory(ctx, &ScanDisclosure{}, contractInventory{}) }, InventoryResult{}},
				{"scan_resolve", func() (any, error) { return w.ResolveScan(ctx, &ScanDisclosure{}, ScanReceiptRequest{}) }, InventoryResult{}},
				{"scan_ack", func() (any, error) { return w.AckScan(ctx, nil, ScanReceiptRequest{}) }, AckResult{}},
				{"publisher", func() (any, error) { return w.StageSearchImage(ctx, &PublisherDisclosure{}, contractPublisher{}) }, SearchImageStageResult{}},
				{"publisher_resolve", func() (any, error) { return w.ResolveSearchImage(ctx, nil, PublisherReceiptRequest{}) }, SearchImageStageResult{}},
				{"publisher_ack", func() (any, error) { return w.AckSearchImage(ctx, nil, PublisherReceiptRequest{}) }, AckResult{}},
				{"effective", func() (any, error) {
					return w.StageEffective(ctx, &EnrichmentDisclosure{}, (*effectiveStageBeginCommand)(nil))
				}, EffectiveStageResult{}},
				{"effective_resolve", func() (any, error) { return w.ResolveEffective(ctx, nil, EnrichmentReceiptRequest{}) }, EffectiveStageResult{}},
				{"effective_ack", func() (any, error) { return w.AckEffective(ctx, nil, EnrichmentReceiptRequest{}) }, AckResult{}},
			} {
				t.Run(c.name, func(t *testing.T) {
					got, err := c.call()
					if !errors.Is(err, ErrNotImplemented) || !reflect.DeepEqual(got, c.zero) {
						t.Fatal("contract performed work or reported an outcome", got, err)
					}
				})
			}
		}
	}
}

func TestFacadeRefusalTaxonomy(t *testing.T) {
	for _, code := range []RefusalCode{RefusalCapacity, RefusalWAL, RefusalQuantum, RefusalDeadline, RefusalUnqualified} {
		err := &Refusal{Code: code}
		if !errors.Is(err, ErrRefused) || errors.Is(err, ErrResolveRequired) || errors.Is(err, ErrNotImplemented) || err.RetryAt != nil {
			t.Fatal("resource refusal acquired commit/implementation semantics", err)
		}
	}
}

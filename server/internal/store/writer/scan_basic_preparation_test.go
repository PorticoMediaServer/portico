package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestBasicPreparationUsesExactOriginalHolding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		purpose CommandID
	}{{"stage", BasicStage}, {"seal", BasicSeal}, {"accept", BasicAccept}} {
		t.Run(tc.name, func(t *testing.T) {
			purpose := tc.purpose
			opening, owner, r, expected, grant := scanPreparationPurposeFundingFixture(t, time.Now, purpose)
			p, err := newScanPreparation(t.Context(), opening, owner, r, nativeinventory.Observer{}, expected, grant, nativeinventory.Request{}, scanBasicPreparation)
			if err != nil {
				_ = r.authority.Retired(grant)
				t.Fatal(err)
			}
			defer func() {
				if err := p.Close(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			deadline, ok := p.lifetime.Deadline()
			if !ok || !deadline.Equal(expected.Deadline) || p.mode != scanBasicPreparation || p.grant != grant || p.expected.Key.Domain != uint16(purpose) {
				t.Fatal("holding lost its original purpose/deadline/grant")
			}
			if r.authority.Usage() == ([4]governor.Amounts{}) {
				t.Fatal("live holding was refunded")
			}
			// Basic holds already-prepared values, never permission to start an
			// Inventory helper, even if the caller invokes the private method.
			value, err := p.ObserveInventory(t.Context())
			if !errors.Is(err, ErrClosed) || value.Version != 0 || p.observed != nil || p.observing {
				t.Fatal("Basic acquired native observation authority", value, err)
			}
			if err := p.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !p.retired || r.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("holding not physically retired")
			}
			if err := opening.close(); err != nil {
				t.Fatal(err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal("Basic retained a native borrower", err)
			}
		})
	}
}

func TestBasicPreparationRejectsOtherModesAndObservation(t *testing.T) {
	opening, owner, r, expected, grant := scanPreparationPurposeFundingFixture(t, time.Now, BasicStage)
	defer func() {
		if err := r.authority.Retired(grant); err != nil {
			t.Error(err)
		}
	}()
	for _, tc := range []struct {
		name    string
		modes   []scanPreparationMode
		request nativeinventory.Request
	}{
		{"default-native", nil, nativeinventory.Request{Root: []byte("/media"), RelativePath: []byte("."), Limit: 1}},
		{"history", []scanPreparationMode{scanHistoryPreparation}, nativeinventory.Request{}},
		{"basic-with-native-payload", []scanPreparationMode{scanBasicPreparation}, nativeinventory.Request{Root: []byte("/media"), RelativePath: []byte("."), Limit: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := newScanPreparation(t.Context(), opening, owner, r, nativeinventory.Observer{}, expected, grant, tc.request, tc.modes...)
			if p != nil || !errors.Is(err, ErrInvalid) {
				t.Fatal(p, err)
			}
			if r.authority.Usage() == ([4]governor.Amounts{}) {
				t.Fatal("refused constructor refunded issuer grant")
			}
		})
	}
	// Changing expected purpose is not a way to recapture the admitted grant.
	changed := expected
	changed.Key.Domain = uint16(BasicSeal)
	changed.Footprint = []governor.Scope{{Library: 7, Domain: uint16(BasicSeal)}}
	p, err := newScanPreparation(t.Context(), opening, owner, r, nativeinventory.Observer{}, changed, grant, nativeinventory.Request{}, scanBasicPreparation)
	if p != nil || err == nil {
		t.Fatal("foreign-purpose grant accepted", p, err)
	}
}

func TestBasicPreparationCannotRetagInventoryHolding(t *testing.T) {
	opening, owner, r, expected, grant := scanPreparationFundingFixture(t, time.Now)
	defer func() {
		if err := r.authority.Retired(grant); err != nil {
			t.Error(err)
		}
	}()
	p, err := newScanPreparation(t.Context(), opening, owner, r, nativeinventory.Observer{}, expected, grant, nativeinventory.Request{}, scanBasicPreparation)
	if p != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("Inventory holding retagged as Basic", p, err)
	}
}

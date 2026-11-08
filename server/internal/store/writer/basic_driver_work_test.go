package writer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func basicDriverWorkFixture(t *testing.T, purpose CommandID) (*ScanDriver, *scanPreparation, ScanReceiptRequest) {
	t.Helper()
	opening, owner, r, expected, grant := scanPreparationPurposeFundingFixture(t, time.Now, purpose)
	d, err := newScanDriver(t.Context(), opening, owner, &Writer{effective: r}, nativeinventory.Observer{})
	if err != nil {
		_ = r.authority.Retired(grant)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(context.Background()); err != nil {
			t.Error(err)
		}
		if err := opening.close(); err != nil {
			t.Error(err)
		}
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	p, err := newScanPreparation(t.Context(), opening, owner, r, nativeinventory.Observer{}, expected, grant, nativeinventory.Request{}, scanBasicPreparation)
	if err != nil {
		_ = r.authority.Retired(grant)
		t.Fatal(err)
	}
	if err = d.keepPreparation(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	q := ScanReceiptRequest{RunID: expected.Key.ID, LibraryID: 7, LibraryKey: "library", Receipt: ReceiptReference{Identity: CommandIdentity{Incarnation: [16]byte{1}, Sequence: 3, OperationID: [16]byte{2}}, RequestDigest: RequestDigest{3}}}
	return d, p, q
}

func TestBasicDriverWorkKeepsOriginalPurposeAndHolding(t *testing.T) {
	for _, purpose := range []CommandID{BasicStage, BasicSeal, BasicAccept} {
		name, _ := basicPurposeForCommand(purpose)
		t.Run(string(name), func(t *testing.T) {
			d, p, q := basicDriverWorkFixture(t, purpose)
			work, err := d.prepareWork(t.Context(), purpose, q, 5, p)
			if err != nil || work == nil || work != d.work || work.preparation != p || work.original != q || work.generation != 5 || work.purpose != purpose || work.submission != scanPrepared {
				t.Fatal(work, err)
			}
			deadline, ok := p.lifetime.Deadline()
			if !ok || !deadline.Equal(p.expected.Deadline) || p.retired || d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
				t.Fatal("work changed the original funded lifetime")
			}
			if err = d.Close(t.Context()); err != nil || !p.retired || d.writer.effective.authority.Usage() != ([4]governor.Amounts{}) {
				t.Fatal("unsubmitted work did not physically release its holding", err)
			}
		})
	}
}

func TestBasicDriverWorkRefusesBorrowedModesAndGeneration(t *testing.T) {
	d, p, q := basicDriverWorkFixture(t, BasicStage)
	for _, mode := range []string{"history", "zero_generation", "foreign_purpose", "wrong_footprint"} {
		generation, purpose := int64(5), BasicStage
		oldMode, oldDomain := p.mode, p.expected.Footprint[0].Domain
		switch mode {
		case "history":
			p.mode = scanHistoryPreparation
		case "zero_generation":
			generation = 0
		case "foreign_purpose":
			purpose = InventoryAccept
		case "wrong_footprint":
			p.expected.Footprint[0].Domain = uint16(BasicSeal)
		}
		work, err := d.prepareWork(t.Context(), purpose, q, generation, p)
		p.mode, p.expected.Footprint[0].Domain = oldMode, oldDomain
		if !errors.Is(err, ErrInvalid) || work != nil || d.work != nil || p.retired || d.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
			t.Fatal(mode, work, err)
		}
	}
}

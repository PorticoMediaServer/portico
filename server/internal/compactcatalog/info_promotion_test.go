package compactcatalog_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A producer whose input was queued in slot 1 keeps staging it after the input
// ahead of it retires and promotes it to slot 0. The promotion keeps its
// change and revision; refusing the handle as drift failed the file's analysis
// and left a half-staged input for recovery (SRV-A6, behind SRV-A4).
func TestQueuedInputProducerContinuesAfterPromotion(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	var epoch int64
	f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&epoch)
	first := compactcatalog.InfoInputSpec{AssetID: f.asset, Token: f.token, ExpectedEpoch: epoch, Fingerprint: [32]byte{1}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{1}}}}
	a := infoInputBegin(t, f.c, first)
	if a.Ref.Slot != 0 {
		t.Fatalf("first input in slot %d", a.Ref.Slot)
	}
	infoInputAccept(t, f.c, a.Ref)
	second := compactcatalog.InfoInputSpec{AssetID: f.asset, Token: f.token, ExpectedEpoch: epoch, FamilyMask: 1, Fingerprint: [32]byte{2}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{2}}}}
	b := infoInputBegin(t, f.c, second)
	if b.Ref.Slot != 1 {
		t.Fatalf("second input in slot %d, want queued in 1", b.Ref.Slot)
	}
	// The first input publishes and retires; the queued one is promoted.
	for i := 0; ; i++ {
		f.step(t)
		var slot int
		if err := f.c.DB.QueryRow(`SELECT slot FROM catalog_info_changes WHERE id=?`, b.Ref.ChangeID).Scan(&slot); err != nil {
			t.Fatal(err)
		}
		if slot == 0 {
			break
		}
		if i == 200 {
			t.Fatal("queued input was never promoted")
		}
	}
	// Its producer still holds the slot-1 handle.
	err := dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		_, err := compactcatalog.StageInfoRowsTx(ctx, tx, b.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
		return err
	})
	if err != nil {
		t.Fatalf("staging after promotion: %v", err)
	}
	infoInputSeal(t, f.c, b.Ref, 1, 0)
	infoInputAccept(t, f.c, b.Ref)
	// The real worker (with its materializer) publishes it.
	f.c.Drain()
	var left int
	f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, f.asset).Scan(&left)
	var after int64
	f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&after)
	if left != 0 || after != epoch+2 {
		t.Fatalf("promoted input did not publish: %d changes left, epoch %d want %d", left, after, epoch+2)
	}
}

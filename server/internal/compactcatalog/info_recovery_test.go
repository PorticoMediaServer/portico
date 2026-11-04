package compactcatalog_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestAbandonInfoSourceReprovesExactUnacceptedOwner(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	ctx := context.Background()
	var epoch int64
	if err := f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	spec := compactcatalog.InfoInputSpec{AssetID: f.asset, Token: f.token, ExpectedEpoch: epoch, FamilyMask: 1, Fingerprint: [32]byte{41}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{41}}}}
	old := infoInputBegin(t, f.c, spec)
	infoInputStage(t, f.c, old.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
	abandon := func(asset int64, token string, witness compactcatalog.InfoWitness, fingerprint [32]byte, want bool) {
		t.Helper()
		f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
			got, err := compactcatalog.AbandonInfoSourceTx(ctx, tx, asset, token, witness, fingerprint)
			if err == nil && got != want {
				t.Fatalf("abandoned=%v want=%v", got, want)
			}
			return err
		})
	}
	other := spec.Witnesses[0]
	other.Fingerprint = [32]byte{99}
	abandon(f.asset+1, f.token, spec.Witnesses[0], spec.Fingerprint, false)
	abandon(f.asset, f.token+"-other", spec.Witnesses[0], spec.Fingerprint, false)
	abandon(f.asset, f.token, other, spec.Fingerprint, false)
	abandon(f.asset, f.token, spec.Witnesses[0], [32]byte{99}, false)
	var unchanged bool
	if err := f.c.DB.QueryRow(`SELECT phase=0 AND copy_aux>=0 AND input_revision=? FROM catalog_info_changes WHERE id=?`, old.Ref.InputRevision, old.Ref.ChangeID).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("another source was changed", unchanged, err)
	}
	nextSpec := spec
	nextSpec.FamilyMask = 0
	nextSpec.Fingerprint = [32]byte{42}
	nextSpec.Witnesses = []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: nextSpec.Fingerprint}}
	next := infoInputBegin(t, f.c, nextSpec)
	infoInputAccept(t, f.c, next.Ref)
	abandon(f.asset, f.token, nextSpec.Witnesses[0], nextSpec.Fingerprint, false)
	abandon(f.asset, f.token, spec.Witnesses[0], spec.Fingerprint, true)
	var revision, cursor int64
	if err := f.c.DB.QueryRow(`SELECT input_revision,copy_family FROM catalog_info_changes WHERE id=? AND copy_aux=-1`, old.Ref.ChangeID).Scan(&revision, &cursor); err != nil {
		t.Fatal(err)
	}
	abandon(f.asset, f.token, spec.Witnesses[0], spec.Fingerprint, false)
	if err := f.c.DB.QueryRow(`SELECT input_revision=? AND copy_family=? AND copy_aux=-1 FROM catalog_info_changes WHERE id=?`, revision, cursor, old.Ref.ChangeID).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("reset recovery ownership", unchanged, err)
	}
	if err := f.c.DB.QueryRow(`SELECT slot=1 AND phase=1 FROM catalog_info_changes WHERE id=?`, next.Ref.ChangeID).Scan(&unchanged); err != nil || !unchanged {
		t.Fatal("changed accepted receipt", unchanged, err)
	}
	for range 180 {
		if f.step(t) {
			break
		}
	}
	var pending int
	if err := f.c.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog_info_changes WHERE id=? AND phase<5`, next.Ref.ChangeID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("stranded accepted receipt", pending, err)
	}
}

func TestInfoRecoveryInterruptedProducerKeepsOldAndReclaimsNew(t *testing.T) {
	f := newInfoPublicationFixture(t, 2)
	var epoch int64
	f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&epoch)
	f.c.Exec(`UPDATE catalog_assets SET duration=5700 WHERE id=?`, f.asset)
	spec := compactcatalog.InfoInputSpec{AssetID: f.asset, Token: f.token, ExpectedEpoch: epoch, FamilyMask: 1, Fingerprint: [32]byte{1}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{1}}}}
	in := infoInputBegin(t, f.c, spec)
	infoInputStage(t, f.c, in.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
	// A slow live physical witness has no TTL and never gets worker-accepted.
	for i := 0; i < 3; i++ {
		if f.step(t) {
			t.Fatal("unaccepted producer was acknowledged")
		}
	}
	var phase int
	f.c.DB.QueryRow(`SELECT phase FROM catalog_info_changes WHERE id=?`, in.Ref.ChangeID).Scan(&phase)
	if phase != 0 {
		t.Fatal(phase)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		ok, e := compactcatalog.AbandonInfoInputTx(ctx, tx, in.Ref)
		if !ok && e == nil {
			t.Fatal("not abandoned")
		}
		return e
	})
	for i := 0; i < 100; i++ {
		if f.step(t) {
			break
		}
		if i == 99 {
			t.Fatal("recovery did not finish")
		}
	}
	var fault string
	var after, rows int64
	f.c.DB.QueryRow(`SELECT CASE copy_aux WHEN -1 THEN 'unaccepted-reclaim' WHEN -2 THEN 'old-frozen' ELSE fault END FROM catalog_info_changes WHERE id=?`, in.Ref.ChangeID).Scan(&fault)
	f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&after)
	f.c.DB.QueryRow(`SELECT count(*) FROM catalog_info_new_stream_facts WHERE change_id=?`, in.Ref.ChangeID).Scan(&rows)
	if fault != "old-frozen" || after != epoch || rows != 0 || f.frame(t).Duration != 5400 {
		t.Fatal(fault, after, rows, f.frame(t))
	}
	for _, id := range f.items {
		if !f.head(t, id).Ready() {
			t.Fatal("OLD reader blocked", f.head(t, id))
		}
	}
	// The canceled ref cannot stage again. A new exact independent frame can
	// claim the frozen scalar anchor and publish the actual desired facts.
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, e := compactcatalog.StageInfoRowsTx(ctx, tx, in.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
		if !errors.Is(e, compactcatalog.ErrInfoInputDrift) {
			t.Fatal(e)
		}
		return nil
	})
	spec.FamilyMask = 0
	spec.Fingerprint = [32]byte{2}
	spec.Witnesses[0].Fingerprint = spec.Fingerprint
	next := infoInputBegin(t, f.c, spec)
	infoInputAccept(t, f.c, next.Ref)
	for i := 0; i < 180; i++ {
		if f.step(t) {
			break
		}
		if i == 179 {
			t.Fatal("new frame did not finish")
		}
	}
	if a := f.frame(t); a.Duration != 5700 || a.Epoch != epoch+1 {
		t.Fatal(a)
	}
}

func TestInfoRecoveryQueuedSuccessorInheritsFirstOldWithoutCanceledEpoch(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	var epoch int64
	f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&epoch)
	f.c.Exec(`UPDATE catalog_assets SET duration=5700 WHERE id=?`, f.asset)
	spec := compactcatalog.InfoInputSpec{AssetID: f.asset, Token: f.token, ExpectedEpoch: epoch, FamilyMask: 1, Fingerprint: [32]byte{1}, Witnesses: []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{1}}}}
	old := infoInputBegin(t, f.c, spec)
	infoInputStage(t, f.c, old.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
	spec.FamilyMask = 0
	spec.Fingerprint = [32]byte{2}
	spec.Witnesses[0].Fingerprint = spec.Fingerprint
	next := infoInputBegin(t, f.c, spec)
	if next.Ref.Slot != 1 {
		t.Fatal(next)
	}
	f.c.Exec(`UPDATE catalog_assets SET duration=6000 WHERE id=?`, f.asset)
	infoInputAccept(t, f.c, next.Ref)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		// The real raw NEW2 point hook advanced the same unaccepted header.
		// Refresh its exact revision after proving this fixture still owns it.
		ref := old.Ref
		if e := tx.QueryRowContext(ctx, `SELECT input_revision FROM catalog_info_changes WHERE id=? AND phase=0 AND fingerprint=?`, ref.ChangeID, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}).Scan(&ref.InputRevision); e != nil {
			return e
		}
		ok, e := compactcatalog.AbandonInfoInputTx(ctx, tx, ref)
		if !ok && e == nil {
			t.Fatal("same-witness input not abandoned")
		}
		return e
	})
	for i := 0; i < 100; i++ {
		f.step(t)
		var slot, phase int
		e := f.c.DB.QueryRow(`SELECT slot,phase FROM catalog_info_changes WHERE id=?`, next.Ref.ChangeID).Scan(&slot, &phase)
		if e == nil && slot == 0 && phase == 1 {
			var got int64
			f.c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, f.asset).Scan(&got)
			if got != epoch || f.frame(t).Duration != 5400 {
				t.Fatal("premature successor/epoch", got, f.frame(t))
			}
			break
		}
		if i == 99 {
			rows, _ := f.c.DB.Query(`SELECT id,slot,phase,expected_epoch,input_revision,copy_family,fault FROM catalog_info_changes WHERE asset_id=? ORDER BY slot`, f.asset)
			for rows.Next() {
				var id, slot, phase, expected, rev, copy int
				var fault string
				rows.Scan(&id, &slot, &phase, &expected, &rev, &copy, &fault)
				t.Log(id, slot, phase, expected, rev, copy, fault)
			}
			rows.Close()
			t.Fatal("successor not promoted")
		}
	}
	for i := 0; i < 180; i++ {
		if f.step(t) {
			break
		}
		if i == 179 {
			t.Fatal("successor did not finish")
		}
	}
	if a := f.frame(t); a.Duration != 6000 || a.Epoch != epoch+1 {
		t.Fatal(a)
	}
}

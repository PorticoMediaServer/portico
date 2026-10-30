package compactcatalog_test

// Reader census for the phase-0 cancellation sentinels in
// catalog_info_changes.copy_aux: -1 is unaccepted-reclaim, -2 is old-frozen and
// every non-negative value is a cursor/flag owned by phases 2 and 4. These
// tests exercise the public source-input, publication, materializer,
// invalidation and worker paths only; the private helpers are reached through
// them. See the reader census in the P2-29 gate notes for the per-caller map.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// infoSentinelSpec is one exact physical observation of the fixture asset. Kind
// 1/2 are the direct media/playback witnesses the supersede rule admits.
func infoSentinelSpec(f infoPublicationFixture, epoch int64, kind int, fingerprint byte, mask int64) compactcatalog.InfoInputSpec {
	fp := [32]byte{fingerprint}
	return compactcatalog.InfoInputSpec{
		AssetID:       f.asset,
		Token:         f.token,
		ExpectedEpoch: epoch,
		FamilyMask:    mask,
		Fingerprint:   fp,
		Witnesses:     []compactcatalog.InfoWitness{{Kind: kind, Fingerprint: fp}},
	}
}

func infoSentinelAux(t *testing.T, c *catalogtest.Catalog, change int64) int64 {
	t.Helper()
	var aux int64
	if err := c.DB.QueryRow(`SELECT copy_aux FROM catalog_info_changes WHERE id=?`, change).Scan(&aux); err != nil {
		t.Fatal(err)
	}
	return aux
}

func infoSentinelChange(t *testing.T, c *catalogtest.Catalog, asset int64, token string) int64 {
	t.Helper()
	var change int64
	if err := c.DB.QueryRow(`SELECT id FROM catalog_info_changes WHERE asset_id=? AND asset_token=?`, asset, token).Scan(&change); err != nil {
		t.Fatal(err)
	}
	return change
}

func infoSentinelAbandon(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef) bool {
	t.Helper()
	ok := false
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		ok, err = compactcatalog.AbandonInfoInputTx(ctx, tx, ref)
		return err
	})
	return ok
}

// infoSentinelAwait drives one public source quantum per turn until the header
// carries want. It never fabricates progress: a header that is reclaimed before
// reaching want, or that outlives the bounded turns, fails the test.
func infoSentinelAwait(t *testing.T, f infoPublicationFixture, change int64, want int64) {
	t.Helper()
	for turn := 0; turn < 300; turn++ {
		if infoSentinelAux(t, f.c, change) == want {
			return
		}
		f.step(t)
	}
	t.Fatalf("header %d never reached copy_aux=%d", change, want)
}

func infoSentinelBudget(t *testing.T, c *catalogtest.Catalog) (used, capacity int64) {
	t.Helper()
	if err := c.DB.QueryRow(`SELECT used_bytes,cap_bytes FROM catalog_info_input_budget WHERE singleton=1`).Scan(&used, &capacity); err != nil {
		t.Fatal(err)
	}
	return used, capacity
}

// infoSentinelDictionary is the shared shape-dictionary charge, the same
// literal 168+2*container+2*video_codec bytes the DDL trigger accounts.
func infoSentinelDictionary(t *testing.T, c *catalogtest.Catalog) int64 {
	t.Helper()
	var n int64
	if err := c.DB.QueryRow(`SELECT COALESCE(SUM(168+2*length(CAST(container AS BLOB))+2*length(CAST(video_codec AS BLOB))),0) FROM catalog_info_shapes`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// infoSentinelAnchor recomputes the durable charge of one frozen OLD anchor:
// the header row plus its first-OLD scalar and path deltas. Every other ledger
// row of that input is reclaimed before the marker is written.
func infoSentinelAnchor(t *testing.T, c *catalogtest.Catalog, change int64) int64 {
	t.Helper()
	var total int64
	if err := c.DB.QueryRow(`SELECT 344+2*COALESCE(length(CAST(target_owner_object AS BLOB)),0)+COALESCE(length(CAST(target_owner_revision AS BLOB)),0)+COALESCE(length(CAST(asset_token AS BLOB)),0)+COALESCE(length(CAST(fingerprint AS BLOB)),0)+COALESCE(length(CAST(fault AS BLOB)),0)+COALESCE(length(CAST(copy_key AS BLOB)),0)+COALESCE(length(CAST(copy_key2 AS BLOB)),0)+COALESCE(length(CAST(copy_key3 AS BLOB)),0) FROM catalog_info_changes WHERE id=?`, change).Scan(&total); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT COALESCE(SUM(176+COALESCE(length(CAST(old_text AS BLOB)),0)),0) FROM catalog_info_scalar_old WHERE change_id=?`,
		`SELECT COALESCE(SUM(144+COALESCE(length(CAST(old_path AS BLOB)),0)),0) FROM catalog_info_path_old WHERE change_id=?`,
	} {
		var n int64
		if err := c.DB.QueryRow(query, change).Scan(&n); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	return total
}

// The sentinel column admits exactly {-2,-1,>=0}: two phase-0 markers and the
// non-negative phase-2/phase-4 cursor domain. The DDL constraint itself must be
// the one the baseline owns, so a weaker CHECK (for example >=-3) cannot slip
// in behind the marker code.
func TestInfoSentinelCheckAndReadersAdmitFrozenReclaimAndCursor(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	var schema string
	if err := f.c.DB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='catalog_info_changes'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema, "CHECK(copy_aux>=-2)") {
		t.Fatalf("catalog_info_changes does not declare exactly CHECK(copy_aux>=-2): %s", schema)
	}
	epoch := infoInputAssetEpoch(t, f.c, f.asset)
	f.c.Exec(`UPDATE catalog_assets SET duration=5700 WHERE id=?`, f.asset)
	change := infoSentinelChange(t, f.c, f.asset, f.token)
	for _, aux := range []int64{-2, -1, 0, 7} {
		if _, err := f.c.DB.Exec(`UPDATE catalog_info_changes SET copy_aux=? WHERE id=?`, aux, change); err != nil {
			t.Fatalf("copy_aux=%d was refused: %v", aux, err)
		}
	}
	for _, aux := range []int64{-3, -9} {
		if _, err := f.c.DB.Exec(`UPDATE catalog_info_changes SET copy_aux=? WHERE id=?`, aux, change); err == nil {
			t.Fatalf("the CHECK admitted copy_aux=%d", aux)
		}
	}
	if _, err := f.c.DB.Exec(`INSERT INTO catalog_info_changes(asset_id,asset_token,slot,expected_epoch,fingerprint,created_ms,copy_aux) VALUES(?,?,1,?,zeroblob(32),0,-3)`, f.asset, f.token, epoch+1); err == nil {
		t.Fatal("the CHECK admitted an inserted copy_aux below -2")
	}
	// A frozen anchor is claimable in place by this asset's next exact producer;
	// the reclaim marker is not, and is reported as busy, never as a cursor.
	f.c.Exec(`UPDATE catalog_info_changes SET copy_aux=-2 WHERE id=?`, change)
	in := infoInputBegin(t, f.c, infoSentinelSpec(f, epoch, compactcatalog.InfoWitnessAsset, 4, 1))
	if in.Outcome != compactcatalog.InfoInputCoalesced || in.Ref.Slot != 0 || in.Ref.ChangeID != change {
		t.Fatalf("frozen anchor was not claimed in place: %+v", in)
	}
	if aux := infoSentinelAux(t, f.c, change); aux != 0 {
		t.Fatalf("claim left sentinel %d on the claimed anchor", aux)
	}
	if !infoSentinelAbandon(t, f.c, in.Ref) {
		t.Fatal("the claimed anchor could not be abandoned")
	}
	if aux := infoSentinelAux(t, f.c, change); aux != -1 {
		t.Fatalf("abandon wrote %d, want the reclaim marker", aux)
	}
	if err := infoInputTryBegin(t, f.c, infoSentinelSpec(f, epoch, compactcatalog.InfoWitnessAsset, 5, 1)); !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
		t.Fatalf("the reclaim marker admitted a new producer: %v", err)
	}
	// The normal worker finishes the same reclaim and leaves the frozen anchor.
	f.c.Drain()
	if aux := infoSentinelAux(t, f.c, change); aux != -2 {
		t.Fatalf("worker left sentinel %d, want old-frozen", aux)
	}
	if got := infoInputAssetEpoch(t, f.c, f.asset); got != epoch {
		t.Fatalf("frozen claim consumed epoch %d -> %d", epoch, got)
	}
	if a := f.frame(t); a.Duration != 5400 {
		t.Fatalf("frozen claim exposed %+v", a)
	}
	if !f.head(t, f.items[0]).Ready() {
		t.Fatalf("frozen claim unready OLD reader %+v", f.head(t, f.items[0]))
	}
}

// A frozen anchor is one shared header: it keeps the first-OLD readers ready,
// consumes no asset epoch, clears every projection cursor, and is reused by a
// later crash instead of appending a generation per holder.
func TestInfoSentinelFrozenAnchorSurvivesRepeatedCrashAsOneSharedHeader(t *testing.T) {
	f := newInfoPublicationFixture(t, 2)
	epoch := infoInputAssetEpoch(t, f.c, f.asset)
	f.c.Exec(`UPDATE catalog_assets SET duration=5700 WHERE id=?`, f.asset)
	spec := infoSentinelSpec(f, epoch, compactcatalog.InfoWitnessAsset, 1, 1)
	in := infoInputBegin(t, f.c, spec)
	infoInputStage(t, f.c, in.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
	if !infoSentinelAbandon(t, f.c, in.Ref) {
		t.Fatal("the unaccepted body was not abandoned")
	}
	infoSentinelAwait(t, f, in.Ref.ChangeID, -2)
	// The freeze clears copy_phase/copy_id/copy_aux/copy_key*, so no phase-4
	// subtitle projection can ever read the marker as an after_rowid cursor.
	var phase, copyFamily, copyPhase, copyID int64
	var key1, key2, key3, fault string
	if err := f.c.DB.QueryRow(`SELECT phase,copy_family,copy_phase,copy_id,copy_key,copy_key2,copy_key3,fault FROM catalog_info_changes WHERE id=?`, in.Ref.ChangeID).
		Scan(&phase, &copyFamily, &copyPhase, &copyID, &key1, &key2, &key3, &fault); err != nil {
		t.Fatal(err)
	}
	if phase != 0 || copyFamily != 0 || copyPhase != 0 || copyID != 0 || key1 != "" || key2 != "" || key3 != "" || fault != "" {
		t.Fatalf("frozen anchor is phase %d copy %d/%d/%d %q/%q/%q fault %q", phase, copyFamily, copyPhase, copyID, key1, key2, key3, fault)
	}
	for _, query := range []string{
		`SELECT count(*) FROM catalog_info_new_stream_facts WHERE change_id=?`,
		`SELECT count(*) FROM catalog_info_input_families WHERE change_id=?`,
		`SELECT count(*) FROM catalog_info_input_witnesses WHERE change_id=?`,
	} {
		if n := infoTestCount(t, f.c.DB, query, in.Ref.ChangeID); n != 0 {
			t.Fatalf("%q retained %d unaccepted rows", query, n)
		}
	}
	// First-OLD stays authoritative for every holder and no epoch is consumed.
	if got := infoInputAssetEpoch(t, f.c, f.asset); got != epoch {
		t.Fatalf("frozen claim consumed epoch %d -> %d", epoch, got)
	}
	for _, id := range f.items {
		if !f.head(t, id).Ready() {
			t.Fatalf("holder %d was blocked by a phase-0 freeze: %+v", id, f.head(t, id))
		}
	}
	if a := f.frame(t); a.Duration != 5400 || a.Epoch != epoch {
		t.Fatalf("frozen anchor exposed %+v", a)
	}
	// The phase-2 materializer rejects a phase-0 header before it can interpret
	// the negative sentinel as its copy-cursor `valid` flag.
	err := dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassMaintenance, func(ctx context.Context, tx *sql.Tx) error {
		_, e := compactcatalog.MaterializeInfoChangeTx(ctx, tx, in.Ref.ChangeID)
		return e
	})
	if !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("the phase-2 materializer accepted a phase-0 sentinel: %v", err)
	}
	// A frozen anchor is a completed source quantum: it neither advances the
	// pre-fence nor spins the source row.
	if !f.step(t) {
		t.Fatal("the frozen anchor is not a completed source quantum")
	}
	// The canceled producer's late writes fail the revision CAS.
	if err := infoInputTryStage(t, f.c, in.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 1, AssetID: f.token, Revision: 2}); !errors.Is(err, compactcatalog.ErrInfoInputDrift) {
		t.Fatalf("a canceled producer staged after the freeze: %v", err)
	}
	if err := infoInputTrySeal(t, f.c, in.Ref, 1, 0); !errors.Is(err, compactcatalog.ErrInfoInputDrift) {
		t.Fatalf("a canceled producer sealed after the freeze: %v", err)
	}
	if infoSentinelAbandon(t, f.c, in.Ref) {
		t.Fatal("a frozen anchor was abandoned a second time")
	}
	// A repeated crash claims and re-freezes the same header: no new generation
	// and still no epoch consumed.
	spec.Fingerprint = [32]byte{2}
	spec.Witnesses[0].Fingerprint = spec.Fingerprint
	again := infoInputBegin(t, f.c, spec)
	if again.Ref.ChangeID != in.Ref.ChangeID || again.Ref.Slot != 0 {
		t.Fatalf("a repeated crash opened %d/%d instead of reusing the anchor", again.Ref.ChangeID, again.Ref.Slot)
	}
	if aux := infoSentinelAux(t, f.c, again.Ref.ChangeID); aux != 0 {
		t.Fatalf("the reused anchor kept sentinel %d", aux)
	}
	infoInputStage(t, f.c, again.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 3})
	if !infoSentinelAbandon(t, f.c, again.Ref) {
		t.Fatal("the second crash was not abandoned")
	}
	infoSentinelAwait(t, f, again.Ref.ChangeID, -2)
	if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, f.asset); n != 1 {
		t.Fatalf("%d headers hold one source input", n)
	}
	if got := infoInputAssetEpoch(t, f.c, f.asset); got != epoch {
		t.Fatalf("the repeated crash consumed epoch %d -> %d", epoch, got)
	}
	for _, id := range f.items {
		if !f.head(t, id).Ready() {
			t.Fatalf("holder %d lost its shared OLD anchor: %+v", id, f.head(t, id))
		}
	}
	if a := f.frame(t); a.Duration != 5400 {
		t.Fatalf("repeated crash exposed %+v", a)
	}
}

// An accepted, independently witnessed queued successor supersedes a merely
// slow kind-1/kind-2 physical producer without any explicit abandon. The
// canceled predecessor consumes no asset epoch and its first OLD activates the
// successor exactly once.
func TestInfoSentinelAcceptedSuccessorSupersedesSlowProducerExactlyOnce(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	epoch := infoInputAssetEpoch(t, f.c, f.asset)
	f.c.Exec(`UPDATE catalog_assets SET duration=5700 WHERE id=?`, f.asset)
	slow := infoInputBegin(t, f.c, infoSentinelSpec(f, epoch, compactcatalog.InfoWitnessAsset, 1, 1))
	infoInputStage(t, f.c, slow.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
	// The producer is only slow: nothing here calls AbandonInfoInputTx.
	next := infoInputBegin(t, f.c, infoSentinelSpec(f, epoch, compactcatalog.InfoWitnessPlayback, 2, 0))
	if next.Ref.Slot != 1 {
		t.Fatalf("an independent observation was admitted into slot %d", next.Ref.Slot)
	}
	f.c.Exec(`UPDATE catalog_assets SET duration=6000 WHERE id=?`, f.asset)
	infoInputAccept(t, f.c, next.Ref)
	if aux := infoSentinelAux(t, f.c, slow.Ref.ChangeID); aux != 0 {
		t.Fatalf("successor admission already moved the slow producer to %d", aux)
	}
	promoted := false
	for turn := 0; turn < 200; turn++ {
		f.step(t)
		var slot, phase int
		if err := f.c.DB.QueryRow(`SELECT slot,phase FROM catalog_info_changes WHERE id=?`, next.Ref.ChangeID).Scan(&slot, &phase); err != nil {
			continue
		}
		if slot != 0 || phase != 1 {
			continue
		}
		if got := infoInputAssetEpoch(t, f.c, f.asset); got != epoch {
			t.Fatalf("the supersede consumed epoch %d -> %d", epoch, got)
		}
		if a := f.frame(t); a.Duration != 5400 {
			t.Fatalf("the promoted successor skipped first-OLD: %+v", a)
		}
		promoted = true
		break
	}
	if !promoted {
		t.Fatal("the accepted queued successor was not promoted")
	}
	if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, f.asset); n != 1 {
		t.Fatalf("%d headers remain after the supersede", n)
	}
	f.untilEpoch(t, epoch+1)
	if a := f.frame(t); a.Duration != 6000 || a.Epoch != epoch+1 {
		t.Fatalf("the superseded successor published %+v", a)
	}
	f.c.Drain()
	if got := infoInputAssetEpoch(t, f.c, f.asset); got != epoch+1 {
		t.Fatalf("the supersede consumed a second epoch: %d", got)
	}
	if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, f.asset); n != 0 {
		t.Fatalf("%d source bodies survived the supersede", n)
	}
}

// The -1/-2 markers are cap-neutral: they must be writable and recoverable with
// the shared 32 MiB ledger completely full. The test reserves the fixture's
// remaining headroom for other sources, so only this input's own charge may be
// returned, and then reconciles used_bytes against the surviving anchor.
func TestInfoSentinelFullLedgerRecoveryMarkerIsCapNeutral(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	var library int64
	if err := f.c.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, f.other).Scan(&library); err != nil {
		t.Fatal(err)
	}
	// A second, already published asset is the probe for "the ledger is full".
	spare := f.c.Movie(library, "/info/spare.mkv", "Spare", 2026)
	f.c.Drain()
	epoch := infoInputAssetEpoch(t, f.c, f.asset)
	spareEpoch := infoInputAssetEpoch(t, f.c, spare.Asset)
	f.c.Exec(`UPDATE catalog_assets SET duration=5700 WHERE id=?`, f.asset)
	in := infoInputBegin(t, f.c, infoSentinelSpec(f, epoch, compactcatalog.InfoWitnessAsset, 1, 1))
	infoInputStage(t, f.c, in.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
	used, capacity := infoSentinelBudget(t, f.c)
	if capacity <= used {
		t.Fatalf("the fixture ledger %d/%d has no headroom to reserve", used, capacity)
	}
	reserved := capacity - used
	dictionary := infoSentinelDictionary(t, f.c)
	f.c.Exec(`UPDATE catalog_info_input_budget SET used_bytes=cap_bytes WHERE singleton=1`)
	if got, _ := infoSentinelBudget(t, f.c); got != capacity {
		t.Fatalf("the ledger is not full: %d/%d", got, capacity)
	}
	spec := compactcatalog.InfoInputSpec{
		AssetID: spare.Asset, Token: spare.Token, ExpectedEpoch: spareEpoch, FamilyMask: 1,
		Fingerprint: [32]byte{9},
		Witnesses:   []compactcatalog.InfoWitness{{Kind: compactcatalog.InfoWitnessAsset, Fingerprint: [32]byte{9}}},
	}
	if err := infoInputTryBegin(t, f.c, spec); !errors.Is(err, compactcatalog.ErrInfoInputDeferred) {
		t.Fatalf("the full ledger admitted a new producer: %v", err)
	}
	if !infoSentinelAbandon(t, f.c, in.Ref) {
		t.Fatal("the phase-0 abandon was refused by the full ledger")
	}
	if got, _ := infoSentinelBudget(t, f.c); got != capacity {
		t.Fatalf("the abandon rewrote ledger bytes: %d/%d", got, capacity)
	}
	infoSentinelAwait(t, f, in.Ref.ChangeID, -2)
	// The cap never moves, only this source's own charge is returned, and the
	// remaining bytes are exactly the shared dictionary plus the OLD anchor.
	got, capNow := infoSentinelBudget(t, f.c)
	if capNow != capacity || got >= capacity {
		t.Fatalf("cap-neutral recovery left %d/%d, want < %d at cap %d", got, capNow, capacity, capacity)
	}
	if after := infoSentinelDictionary(t, f.c); after != dictionary {
		t.Fatalf("recovery rewrote the shared shape dictionary %d -> %d", dictionary, after)
	}
	if actual, want := got-reserved, dictionary+infoSentinelAnchor(t, f.c, in.Ref.ChangeID); actual != want {
		t.Fatalf("the ledger holds %d bytes, want %d actual behind %d reserved", actual, want, reserved)
	}
	if aux := infoSentinelAux(t, f.c, in.Ref.ChangeID); aux != -2 {
		t.Fatalf("the full ledger left sentinel %d, want old-frozen", aux)
	}
	if got := infoInputAssetEpoch(t, f.c, f.asset); got != epoch {
		t.Fatalf("cap-neutral recovery consumed epoch %d -> %d", epoch, got)
	}
	if a := f.frame(t); a.Duration != 5400 {
		t.Fatalf("cap-neutral recovery exposed %+v", a)
	}
	if !f.head(t, f.items[0]).Ready() {
		t.Fatalf("cap-neutral recovery unready OLD reader %+v", f.head(t, f.items[0]))
	}
	// The full ledger was only a reservation owned by other sources: releasing
	// it settles used_bytes on the actual surviving charge.
	f.c.Exec(`UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=1`, got-reserved)
	if settled, _ := infoSentinelBudget(t, f.c); settled != dictionary+infoSentinelAnchor(t, f.c, in.Ref.ChangeID) {
		t.Fatalf("releasing the reservation left %d bytes", settled)
	}
	if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, spare.Asset); n != 0 {
		t.Fatalf("the probe asset kept %d headers", n)
	}
}

// The invalidation reader also owns the -1 marker: a new physical revision
// supersedes only the unaccepted body, the producer's late writes fail the
// revision CAS, a fresh producer is refused as busy, and the normal worker
// finishes the reclaim and the unknown baseline.
func TestInfoSentinelInvalidationReclaimMarkerRefusesLateProducer(t *testing.T) {
	f := newInfoPublicationFixture(t, 1)
	epoch := infoInputAssetEpoch(t, f.c, f.asset)
	f.c.Exec(`UPDATE catalog_assets SET duration=5700 WHERE id=?`, f.asset)
	in := infoInputBegin(t, f.c, infoSentinelSpec(f, epoch, compactcatalog.InfoWitnessAsset, 1, 1))
	infoInputStage(t, f.c, in.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 0, AssetID: f.token, Revision: 1})
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.InvalidateInfoTechnicalTx(ctx, tx, f.asset)
	})
	if aux := infoSentinelAux(t, f.c, in.Ref.ChangeID); aux != -1 {
		t.Fatalf("invalidation wrote %d, want the reclaim marker", aux)
	}
	if err := infoInputTryStage(t, f.c, in.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{InputOrdinal: 1, AssetID: f.token, Revision: 2}); !errors.Is(err, compactcatalog.ErrInfoInputDrift) {
		t.Fatalf("a superseded producer staged after invalidation: %v", err)
	}
	if err := infoInputTrySeal(t, f.c, in.Ref, 1, 0); !errors.Is(err, compactcatalog.ErrInfoInputDrift) {
		t.Fatalf("a superseded producer sealed after invalidation: %v", err)
	}
	// The public receipt reports the reclaiming body as unaccepted staging: the
	// marker is a fault, never a negative cursor.
	if receipt := infoInputReceipt(t, f.c, in.Ref.ChangeID); receipt.State != compactcatalog.InfoInputStaging || receipt.ChangeID != in.Ref.ChangeID {
		t.Fatalf("the reclaiming body reported %+v", receipt)
	}
	if err := infoInputTryBegin(t, f.c, infoSentinelSpec(f, epoch, compactcatalog.InfoWitnessAsset, 6, 1)); !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
		t.Fatalf("a reclaiming body admitted a fresh producer: %v", err)
	}
	f.c.Drain()
	if n := infoTestCount(t, f.c.DB, `SELECT count(*) FROM catalog_info_changes WHERE asset_id=?`, f.asset); n != 0 {
		t.Fatalf("%d informational bodies survived invalidation", n)
	}
	if got := infoInputAssetEpoch(t, f.c, f.asset); got != epoch+1 {
		t.Fatalf("invalidation published epoch %d, want %d", got, epoch+1)
	}
	for _, id := range f.items {
		if !f.head(t, id).Ready() {
			t.Fatalf("holder %d stayed blocked after invalidation: %+v", id, f.head(t, id))
		}
	}
}

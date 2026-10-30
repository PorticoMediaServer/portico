package compactcatalog_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// These tests exercise the typed source-input staging API against the real
// fresh-install schema (catalogtest.Open applies 0001_baseline.sql, including
// the normalized staging tables, their immutability triggers and the fixed
// 32 MiB logical ledger). They are source-only: no canonical family, item head,
// link obligation, availability flag, materializer quantum or reader is
// involved. Activation and apply stay owned by info_publication.go.

func infoInputWitness() compactcatalog.InfoWitness {
	return compactcatalog.InfoWitness{
		Kind:             0,
		JobID:            "job-1",
		SourceID:         "source-1",
		SourceGeneration: 1,
		RootIncarnation:  "root-1",
		ObjectID:         "object-1",
		ObjectRevision:   "revision-1",
		PolicyRevision:   1,
	}
}

func infoInputBegin(t *testing.T, c *catalogtest.Catalog, spec compactcatalog.InfoInputSpec) compactcatalog.InfoInput {
	t.Helper()
	var input compactcatalog.InfoInput
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		input, err = compactcatalog.BeginInfoInputTx(ctx, tx, spec)
		return err
	})
	return input
}

func infoInputStage(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef, family, witness int, rowsExpected int64, rows ...compactcatalog.InfoStageRow) compactcatalog.InfoStageReceipt {
	t.Helper()
	var receipt compactcatalog.InfoStageReceipt
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		receipt, err = compactcatalog.StageInfoRowsTx(ctx, tx, ref, family, witness, rowsExpected, rows...)
		return err
	})
	return receipt
}

func infoInputSeal(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef, family, witness int) compactcatalog.InfoSealReceipt {
	t.Helper()
	var receipt compactcatalog.InfoSealReceipt
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		receipt, err = compactcatalog.SealInfoFamilyTx(ctx, tx, ref, family, witness)
		return err
	})
	return receipt
}

func infoInputAccept(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef) compactcatalog.InfoAcceptReceipt {
	t.Helper()
	var receipt compactcatalog.InfoAcceptReceipt
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		receipt, err = compactcatalog.AcceptInfoInputTx(ctx, tx, ref)
		return err
	})
	return receipt
}

func infoInputTryAccept(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef) error {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := compactcatalog.AcceptInfoInputTx(context.Background(), tx, ref)
		return err
	})
	if err == nil {
		t.Fatal("acceptance accepted an input it must refuse")
	}
	return err
}

func infoInputStageParent(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef, family, witness int, rowsExpected int64, rows ...compactcatalog.InfoStageRow) compactcatalog.InfoStageReceipt {
	t.Helper()
	var receipt compactcatalog.InfoStageReceipt
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		receipt, err = compactcatalog.StageInfoParentRowsTx(ctx, tx, ref, family, witness, rowsExpected, rows...)
		return err
	})
	return receipt
}

func infoInputTryStageParent(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef, family, witness int, rowsExpected int64, rows ...compactcatalog.InfoStageRow) error {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := compactcatalog.StageInfoParentRowsTx(context.Background(), tx, ref, family, witness, rowsExpected, rows...)
		return err
	})
	if err == nil {
		t.Fatal("parent-witness staging accepted a batch it must refuse")
	}
	return err
}

// infoInputTryStage runs one batch that must fail, and returns the error after
// its transaction has rolled back.
func infoInputTryStage(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef, family, witness int, rowsExpected int64, rows ...compactcatalog.InfoStageRow) error {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := compactcatalog.StageInfoRowsTx(context.Background(), tx, ref, family, witness, rowsExpected, rows...)
		return err
	})
	if err == nil {
		t.Fatal("staging accepted a batch it must refuse")
	}
	return err
}

func infoInputTrySeal(t *testing.T, c *catalogtest.Catalog, ref compactcatalog.InfoInputRef, family, witness int) error {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := compactcatalog.SealInfoFamilyTx(context.Background(), tx, ref, family, witness)
		return err
	})
	if err == nil {
		t.Fatal("sealing accepted a family it must refuse")
	}
	return err
}

func infoInputTryBegin(t *testing.T, c *catalogtest.Catalog, spec compactcatalog.InfoInputSpec) error {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := compactcatalog.BeginInfoInputTx(context.Background(), tx, spec)
		return err
	})
	if err == nil {
		t.Fatal("begin accepted a source input it must refuse")
	}
	return err
}

func infoInputReceipt(t *testing.T, c *catalogtest.Catalog, change int64) compactcatalog.InfoInputReceipt {
	t.Helper()
	var receipt compactcatalog.InfoInputReceipt
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		receipt, err = compactcatalog.InfoInputReceiptTx(ctx, tx, change)
		return err
	})
	return receipt
}

func infoInputAssetEpoch(t *testing.T, c *catalogtest.Catalog, asset int64) int64 {
	t.Helper()
	var epoch int64
	if err := c.DB.QueryRow(`SELECT info_epoch FROM catalog_assets WHERE id=?`, asset).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	return epoch
}

// A two-family input stages typed rows in bounded batches and reports staging
// until every changed family is sealed at its exact count. Sealing alone does
// not accept the body: whole-input acceptance freezes the availability target,
// captures the holder frontier and moves the header to phase 1 in one
// transaction, so the accepted-pending body is immutable before the worker turn.
func TestInfoInputStagesAndSealsTypedFamilies(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	spec := compactcatalog.InfoInputSpec{
		AssetID:       movie.Asset,
		Token:         movie.Token,
		ExpectedEpoch: infoInputAssetEpoch(t, c, movie.Asset),
		FamilyMask:    1<<0 | 1<<1,
		Fingerprint:   [32]byte{1},
		Witnesses:     []compactcatalog.InfoWitness{infoInputWitness()},
	}
	input := infoInputBegin(t, c, spec)
	if input.Ref.Slot != 0 || input.Ref.InputRevision != 1 {
		t.Fatalf("new input is slot %d revision %d", input.Ref.Slot, input.Ref.InputRevision)
	}
	if input.Outcome != compactcatalog.InfoInputOpened {
		t.Fatalf("a new input reports outcome %s", input.Outcome)
	}
	if input.Receipt.State != compactcatalog.InfoInputStaging {
		t.Fatalf("a fresh input reports %s, not staging", input.Receipt.State)
	}

	facts := infoInputStage(t, c, input.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: movie.Token, Revision: 1, Size: size, ModifiedNS: modified, Fingerprint: "streams-v1",
	})
	if facts.FamilyRowsExpected != 1 || facts.FamilyRowsStaged != 1 {
		t.Fatalf("stream facts manifest is %d/%d", facts.FamilyRowsStaged, facts.FamilyRowsExpected)
	}
	lanes := infoInputStage(t, c, input.Ref, 2, 0, 2,
		compactcatalog.InfoStreamRow{InputOrdinal: 1, AssetID: movie.Token, StreamIndex: 0, Type: "video", Codec: "hevc", IsDefault: 1},
		compactcatalog.InfoStreamRow{InputOrdinal: 2, AssetID: movie.Token, StreamIndex: 1, Type: "audio", Codec: "truehd", Channels: 8, ChannelLayout: "7.1"},
	)
	if lanes.RowsStaged != 3 {
		t.Fatalf("staged rows after two batches are %d", lanes.RowsStaged)
	}
	if lanes.State != compactcatalog.InfoInputStaging {
		t.Fatalf("an unsealed input reports %s", lanes.State)
	}
	if sealed := infoInputSeal(t, c, input.Ref, 1, 0); sealed.State != compactcatalog.InfoInputStaging {
		t.Fatalf("one sealed family of two reports %s", sealed.State)
	}
	sealed := infoInputSeal(t, c, input.Ref, 2, 0)
	if sealed.State != compactcatalog.InfoInputStaging {
		t.Fatalf("a sealed but not yet accepted input reports %s", sealed.State)
	}
	if sealed.FamiliesMasked != 2 || sealed.FamiliesSealed != 2 {
		t.Fatalf("sealed families are %d of %d", sealed.FamiliesSealed, sealed.FamiliesMasked)
	}
	if sealed.RowsExpected != 3 || sealed.RowsStaged != 3 {
		t.Fatalf("sealed rows are %d of %d", sealed.RowsStaged, sealed.RowsExpected)
	}
	accepted := infoInputAccept(t, c, input.Ref)
	if accepted.State != compactcatalog.InfoInputAcceptedPending || !accepted.FrontierCaptured {
		t.Fatalf("acceptance reports %s with frontier captured %v", accepted.State, accepted.FrontierCaptured)
	}
	if accepted.TargetAvailable == nil || *accepted.TargetAvailable != 1 {
		t.Fatalf("accepted target availability is %v", accepted.TargetAvailable)
	}
	if accepted.RowsExpected != 3 || accepted.RowsStaged != 3 {
		t.Fatalf("accepted rows are %d of %d", accepted.RowsStaged, accepted.RowsExpected)
	}
	if receipt := infoInputReceipt(t, c, input.Ref.ChangeID); receipt.State != compactcatalog.InfoInputAcceptedPending || receipt.InputRevision != input.Ref.InputRevision {
		t.Fatalf("receipt after acceptance is %s at revision %d", receipt.State, receipt.InputRevision)
	}
	var phase int
	if err := c.DB.QueryRow(`SELECT phase FROM catalog_info_changes WHERE id=?`, input.Ref.ChangeID).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if phase != 1 {
		t.Fatalf("acceptance left the header at phase %d", phase)
	}
	if err := infoInputTryStage(t, c, input.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 1, AssetID: movie.Token, Revision: 2, Size: size, ModifiedNS: modified, Fingerprint: "late",
	}); !errors.Is(err, compactcatalog.ErrInfoInputSealed) {
		t.Fatalf("staging into an accepted-pending body returned %v", err)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_stream_facts WHERE asset_id=?`, movie.Token); got != 0 {
		t.Fatalf("staging wrote %d canonical stream fact rows", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_streams WHERE asset_id=?`, movie.Token); got != 0 {
		t.Fatalf("staging wrote %d canonical stream rows", got)
	}
}

// Drift, a sealed body and a foreign asset row are explicit refusals, not a
// silent rewrite.
func TestInfoInputRejectsDriftSealedRewriteAndForeignRows(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	witness := infoInputWitness()
	spec := compactcatalog.InfoInputSpec{
		AssetID:       movie.Asset,
		Token:         movie.Token,
		ExpectedEpoch: infoInputAssetEpoch(t, c, movie.Asset),
		FamilyMask:    1 << 0,
		Fingerprint:   [32]byte{2},
		Witnesses:     []compactcatalog.InfoWitness{witness},
	}
	input := infoInputBegin(t, c, spec)

	foreign := compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: "another-asset", Revision: 1, Size: size, ModifiedNS: modified, Fingerprint: "fp",
	}
	if err := infoInputTryStage(t, c, input.Ref, 1, 0, 1, foreign); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("a foreign asset row returned %v", err)
	}
	stale := input.Ref
	stale.InputRevision++
	valid := compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: movie.Token, Revision: 1, Size: size, ModifiedNS: modified, Fingerprint: "fp",
	}
	if err := infoInputTryStage(t, c, stale, 1, 0, 1, valid); !errors.Is(err, compactcatalog.ErrInfoInputDrift) {
		t.Fatalf("a stale input revision returned %v", err)
	}
	infoInputStage(t, c, input.Ref, 1, 0, 1, valid)
	infoInputSeal(t, c, input.Ref, 1, 0)

	if err := infoInputTryStage(t, c, input.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 1, AssetID: movie.Token, Revision: 2, Size: size, ModifiedNS: modified, Fingerprint: "fp2",
	}); !errors.Is(err, compactcatalog.ErrInfoInputSealed) {
		t.Fatalf("staging into a sealed family returned %v", err)
	}

	// Another exact observation never merges into the active header; it takes
	// the free queued slot as a separate input.
	other := spec
	otherWitness := witness
	otherWitness.JobID = "job-2"
	other.Witnesses = []compactcatalog.InfoWitness{otherWitness}
	queued := infoInputBegin(t, c, other)
	if queued.Outcome != compactcatalog.InfoInputQueued || queued.Ref.Slot != 1 || queued.Ref.ChangeID == input.Ref.ChangeID {
		t.Fatalf("a second observation is outcome %s in slot %d", queued.Outcome, queued.Ref.Slot)
	}
	if got := infoTestString(t, c.DB, `SELECT job_id FROM catalog_info_input_witnesses WHERE change_id=? AND ordinal=0`, input.Ref.ChangeID); got != "job-1" {
		t.Fatalf("the active witness became %q", got)
	}
	if !compactcatalog.InfoInputNeedsAbort(compactcatalog.ErrInfoInputDrift) {
		t.Fatal("revision drift is not classified as an abort")
	}
}

// A changed input queues in slot 1 without rewriting the active body; a second
// changed frame coalesces the empty successor, and a staged successor refuses
// another coalesce.
func TestInfoInputQueuesOneCoalescibleSuccessor(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	epoch := infoInputAssetEpoch(t, c, movie.Asset)
	spec := func(fingerprint byte) compactcatalog.InfoInputSpec {
		return compactcatalog.InfoInputSpec{
			AssetID:       movie.Asset,
			Token:         movie.Token,
			ExpectedEpoch: epoch,
			FamilyMask:    1 << 0,
			Fingerprint:   [32]byte{fingerprint},
			Witnesses:     []compactcatalog.InfoWitness{infoInputWitness()},
		}
	}
	active := infoInputBegin(t, c, spec(1))
	infoInputStage(t, c, active.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: movie.Token, Revision: 1, Size: size, ModifiedNS: modified, Fingerprint: "one",
	})
	infoInputSeal(t, c, active.Ref, 1, 0)

	queued := infoInputBegin(t, c, spec(2))
	if queued.Ref.Slot != 1 || queued.Ref.ChangeID == active.Ref.ChangeID {
		t.Fatalf("a changed input landed in slot %d as change %d", queued.Ref.Slot, queued.Ref.ChangeID)
	}
	if queued.Receipt.State != compactcatalog.InfoInputStaging || queued.Receipt.ExpectedEpoch != epoch+1 {
		t.Fatalf("queued successor is %s at expected epoch %d", queued.Receipt.State, queued.Receipt.ExpectedEpoch)
	}
	var fingerprint []byte
	var mask int64
	if err := c.DB.QueryRow(`SELECT fingerprint,family_mask FROM catalog_info_changes WHERE id=?`, active.Ref.ChangeID).Scan(&fingerprint, &mask); err != nil {
		t.Fatal(err)
	}
	if want := ([32]byte{1}); !bytes.Equal(fingerprint, want[:]) || mask != 1<<0 {
		t.Fatalf("the active body moved to fingerprint %v mask %#x", fingerprint, mask)
	}

	coalesced := infoInputBegin(t, c, spec(3))
	if coalesced.Ref.ChangeID != queued.Ref.ChangeID || coalesced.Ref.Slot != 1 {
		t.Fatalf("an empty successor was not coalesced: change %d slot %d", coalesced.Ref.ChangeID, coalesced.Ref.Slot)
	}
	var queuedFingerprint []byte
	if err := c.DB.QueryRow(`SELECT fingerprint FROM catalog_info_changes WHERE id=?`, queued.Ref.ChangeID).Scan(&queuedFingerprint); err != nil {
		t.Fatal(err)
	}
	if want := ([32]byte{3}); !bytes.Equal(queuedFingerprint, want[:]) {
		t.Fatalf("the coalesced successor fingerprint is %v", queuedFingerprint)
	}
	if coalesced.Ref.InputRevision <= queued.Ref.InputRevision {
		t.Fatalf("coalescing did not move the successor revision past %d", queued.Ref.InputRevision)
	}

	infoInputStage(t, c, coalesced.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: movie.Token, Revision: 2, Size: size, ModifiedNS: modified, Fingerprint: "three",
	})
	retry := infoInputBegin(t, c, spec(4))
	if retry.Outcome != compactcatalog.InfoInputDuplicate || retry.Ref.ChangeID != queued.Ref.ChangeID {
		t.Fatalf("a same-observation retry is outcome %s as change %d", retry.Outcome, retry.Ref.ChangeID)
	}
	third := spec(5)
	thirdWitness := infoInputWitness()
	thirdWitness.JobID = "job-3"
	third.Witnesses = []compactcatalog.InfoWitness{thirdWitness}
	if err := infoInputTryBegin(t, c, third); !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
		t.Fatalf("a third observation claimed an occupied queued slot: %v", err)
	}
}

// One batch may not exceed eight raw rows, and a masked family that stages no
// rows seals as an exact empty manifest so phase 0 can see every family.
func TestInfoInputBoundsTheBatchAndSealsEmptyFamilies(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	other := c.Movie(films, "/m/Two.mkv", "Two", 2021)
	c.Drain()
	spec := compactcatalog.InfoInputSpec{
		AssetID:       movie.Asset,
		Token:         movie.Token,
		ExpectedEpoch: infoInputAssetEpoch(t, c, movie.Asset),
		FamilyMask:    1 << 3,
		Fingerprint:   [32]byte{4},
		Witnesses:     []compactcatalog.InfoWitness{infoInputWitness()},
	}
	input := infoInputBegin(t, c, spec)
	rows := make([]compactcatalog.InfoStageRow, 9)
	for i := range rows {
		rows[i] = compactcatalog.InfoChapterFactRow{
			InputOrdinal: int64(i), AssetID: movie.Token, Revision: 1, Size: 1, ModifiedNS: 1, Status: "known", Fingerprint: "chapters",
		}
	}
	if err := infoInputTryStage(t, c, input.Ref, 4, 0, 9, rows...); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("a nine-row batch returned %v", err)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM catalog_info_input_families WHERE change_id=?`, input.Ref.ChangeID); got != 0 {
		t.Fatalf("a refused batch left %d manifests", got)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM catalog_info_new_chapter_facts WHERE change_id=?`, input.Ref.ChangeID); got != 0 {
		t.Fatalf("a refused batch left %d staged rows", got)
	}
	sealed := infoInputSeal(t, c, input.Ref, 4, 0)
	if !sealed.Sealed || sealed.RowsExpected != 0 || sealed.RowsStaged != 0 {
		t.Fatalf("empty family sealed as %d/%d", sealed.RowsStaged, sealed.RowsExpected)
	}
	if sealed.State != compactcatalog.InfoInputStaging {
		t.Fatalf("a sealed empty-only input reports %s before acceptance", sealed.State)
	}
	if accepted := infoInputAccept(t, c, input.Ref); accepted.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatalf("an empty-only input reports %s", accepted.State)
	}
	// A seal before the declared row count is incomplete, not accepted.
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, other.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	short := infoInputBegin(t, c, compactcatalog.InfoInputSpec{
		AssetID:       other.Asset,
		Token:         other.Token,
		ExpectedEpoch: infoInputAssetEpoch(t, c, other.Asset),
		FamilyMask:    1 << 0,
		Fingerprint:   [32]byte{5},
		Witnesses:     []compactcatalog.InfoWitness{infoInputWitness()},
	})
	infoInputStage(t, c, short.Ref, 1, 0, 2, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: other.Token, Revision: 1, Size: size, ModifiedNS: modified, Fingerprint: "short",
	})
	if err := infoInputTryAccept(t, c, short.Ref); !errors.Is(err, compactcatalog.ErrInfoInputIncomplete) {
		t.Fatalf("a half-staged input was accepted: %v", err)
	}
	if err := infoInputTrySeal(t, c, short.Ref, 1, 0); !errors.Is(err, compactcatalog.ErrInfoInputIncomplete) {
		t.Fatalf("a half-staged family sealed: %v", err)
	}
}

// A changed child family can depend on an UNCHANGED parent family through the
// NEW foreign key. The parent rows are staged as an unmasked witness family and
// counted separately, so the materializer will not reinstall that parent.
func TestInfoInputStagesParentWitnessWithoutMaskingIt(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var size, modified int64
	if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, movie.Asset).Scan(&size, &modified); err != nil {
		t.Fatal(err)
	}
	input := infoInputBegin(t, c, compactcatalog.InfoInputSpec{
		AssetID:       movie.Asset,
		Token:         movie.Token,
		ExpectedEpoch: infoInputAssetEpoch(t, c, movie.Asset),
		FamilyMask:    1 << 1, // streams only; stream facts stay an UNCHANGED parent
		Fingerprint:   [32]byte{6},
		Witnesses:     []compactcatalog.InfoWitness{infoInputWitness()},
	})
	if err := infoInputTryStage(t, c, input.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: movie.Token, Revision: 1, Size: size, ModifiedNS: modified, Fingerprint: "facts",
	}); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("changed staging into an unmasked family returned %v", err)
	}
	if err := infoInputTryStageParent(t, c, input.Ref, 2, 0, 1, compactcatalog.InfoStreamRow{
		InputOrdinal: 0, AssetID: movie.Token, StreamIndex: 0, Type: "video", Codec: "hevc",
	}); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("parent staging into a masked family returned %v", err)
	}
	parent := infoInputStageParent(t, c, input.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: movie.Token, Revision: 1, Size: size, ModifiedNS: modified, Fingerprint: "facts",
	})
	if parent.FamilyRole != compactcatalog.InfoFamilyParent || parent.FamiliesParent != 1 || parent.RowsParentStaged != 1 {
		t.Fatalf("parent receipt is role %s with %d/%d parent rows", parent.FamilyRole, parent.RowsParentStaged, parent.FamiliesParent)
	}
	if parent.FamiliesMasked != 1 || parent.FamiliesSealed != 0 {
		t.Fatalf("parent staging changed the changed-family counts to %d/%d", parent.FamiliesSealed, parent.FamiliesMasked)
	}
	if sealed := infoInputSeal(t, c, input.Ref, 1, 0); sealed.FamiliesParentSealed != 1 || sealed.State != compactcatalog.InfoInputStaging {
		t.Fatalf("sealed parent witness reports %s with %d parent families sealed", sealed.State, sealed.FamiliesParentSealed)
	}
	if err := infoInputTrySeal(t, c, input.Ref, 4, 0); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("an unmasked family with no staged rows sealed: %v", err)
	}
	infoInputStage(t, c, input.Ref, 2, 0, 1, compactcatalog.InfoStreamRow{
		InputOrdinal: 1, AssetID: movie.Token, StreamIndex: 0, Type: "video", Codec: "hevc",
	})
	sealed := infoInputSeal(t, c, input.Ref, 2, 0)
	if sealed.State != compactcatalog.InfoInputStaging || sealed.FamiliesParent != 1 || sealed.FamiliesParentSealed != 1 {
		t.Fatalf("sealed changed+parent input reports %s with %d/%d parent families", sealed.State, sealed.FamiliesParentSealed, sealed.FamiliesParent)
	}
	if accepted := infoInputAccept(t, c, input.Ref); accepted.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatalf("changed+parent input reports %s after acceptance", accepted.State)
	}
	if got := infoTestCount(t, c.DB, `SELECT count(*) FROM asset_stream_facts WHERE asset_id=?`, movie.Token); got != 0 {
		t.Fatalf("parent-witness staging wrote %d canonical parent rows", got)
	}
}

// Disc families are scoped to exactly one witness object: one manifest per
// change/family cannot hold two independent objects, and rows of another object
// are refused rather than mixed in.
func TestInfoInputScopesDiscFamiliesToOneWitnessObject(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	disc := compactcatalog.InfoDiscFactRow{
		InputOrdinal: 0, ObjectID: "object-1", AssetID: movie.Token, SourceRevision: "rev-1",
		SourceGeneration: 1, RootIncarnation: "root-1", Kind: "bluray", Packaging: "iso",
		Encryption: "none", Fingerprint: strings.Repeat("a", 64), ChoiceRevision: 1,
	}
	input := infoInputBegin(t, c, compactcatalog.InfoInputSpec{
		AssetID:       movie.Asset,
		Token:         movie.Token,
		ExpectedEpoch: infoInputAssetEpoch(t, c, movie.Asset),
		FamilyMask:    1 << 10, // disc titles changed; disc facts are the FK parent
		Fingerprint:   [32]byte{7},
		Witnesses:     []compactcatalog.InfoWitness{infoInputWitness()},
	})
	infoInputStageParent(t, c, input.Ref, 10, 0, 1, disc)
	wrongObject := compactcatalog.InfoDiscTitleRow{
		InputOrdinal: 1, ObjectID: "other-object", TitleKey: "A", SelectionToken: "a",
		TitleNumber: 1, DurationMS: 600000, AngleCount: 1,
	}
	if err := infoInputTryStage(t, c, input.Ref, 11, 0, 1, wrongObject); !errors.Is(err, compactcatalog.ErrInfoInvariant) {
		t.Fatalf("a disc row of another witness object returned %v", err)
	}
	if err := infoInputTryStage(t, c, input.Ref, 11, 1, 1, compactcatalog.InfoDiscTitleRow{
		InputOrdinal: 1, ObjectID: "object-1", TitleKey: "A", SelectionToken: "a",
		TitleNumber: 1, DurationMS: 600000, AngleCount: 1,
	}); !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
		t.Fatalf("an extra witness ordinal was not deferred: %v", err)
	}
	infoInputStage(t, c, input.Ref, 11, 0, 1, compactcatalog.InfoDiscTitleRow{
		InputOrdinal: 1, ObjectID: "object-1", TitleKey: "A", SelectionToken: "a",
		TitleNumber: 1, DurationMS: 600000, AngleCount: 1,
	})
	infoInputSeal(t, c, input.Ref, 10, 0)
	if sealed := infoInputSeal(t, c, input.Ref, 11, 0); sealed.State != compactcatalog.InfoInputStaging {
		t.Fatalf("sealed object-scoped input reports %s before acceptance", sealed.State)
	}
	if accepted := infoInputAccept(t, c, input.Ref); accepted.State != compactcatalog.InfoInputAcceptedPending {
		t.Fatalf("object-scoped input reports %s after acceptance", accepted.State)
	}
	if err := infoInputTryStage(t, c, input.Ref, 11, 0, 1, compactcatalog.InfoDiscTitleRow{
		InputOrdinal: 2, ObjectID: "other-object", TitleKey: "B", SelectionToken: "b",
		TitleNumber: 2, DurationMS: 600000, AngleCount: 1,
	}); !errors.Is(err, compactcatalog.ErrInfoInputSealed) {
		t.Fatalf("staging after seal returned %v", err)
	}
}

// Whole-input acceptance freezes target_available, captures the active slot's
// holder frontier, and leaves a queued successor's frontier for promotion. The
// queued successor's first OLD availability protects the active NEW1, and once
// both slots are sealed incoming metadata defers instead of dropping either.
func TestInfoInputAcceptFreezesTargetAndFrontier(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	first := c.Movie(films, "/m/One.mkv", "One", 2020)
	second := c.Movie(films, "/m/Two.mkv", "Two", 2021)
	c.Drain()
	assetFacts := func(item catalogtest.Item) (int64, int64) {
		t.Helper()
		var size, modified int64
		if err := c.DB.QueryRow(`SELECT size,modified_ns FROM catalog_assets WHERE id=?`, item.Asset).Scan(&size, &modified); err != nil {
			t.Fatal(err)
		}
		return size, modified
	}
	firstSize, firstModified := assetFacts(first)
	firstInput := infoInputBegin(t, c, compactcatalog.InfoInputSpec{
		AssetID:       first.Asset,
		Token:         first.Token,
		ExpectedEpoch: infoInputAssetEpoch(t, c, first.Asset),
		FamilyMask:    1 << 0,
		Fingerprint:   [32]byte{8},
		Witnesses:     []compactcatalog.InfoWitness{infoInputWitness()},
	})
	infoInputStage(t, c, firstInput.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: first.Token, Revision: 1, Size: firstSize, ModifiedNS: firstModified, Fingerprint: "one",
	})
	infoInputSeal(t, c, firstInput.Ref, 1, 0)
	accepted := infoInputAccept(t, c, firstInput.Ref)
	if accepted.State != compactcatalog.InfoInputAcceptedPending || !accepted.FrontierCaptured {
		t.Fatalf("slot-0 acceptance is %s with frontier captured %v", accepted.State, accepted.FrontierCaptured)
	}
	var phase, target, upper int64
	if err := c.DB.QueryRow(`SELECT phase,target_available,upper_entity FROM catalog_info_changes WHERE id=?`, firstInput.Ref.ChangeID).
		Scan(&phase, &target, &upper); err != nil {
		t.Fatal(err)
	}
	if phase != 1 || target != 1 || upper <= 0 {
		t.Fatalf("accepted slot 0 is phase %d target %d frontier %d", phase, target, upper)
	}

	secondSize, secondModified := assetFacts(second)
	epoch := infoInputAssetEpoch(t, c, second.Asset)
	spec := func(fingerprint byte) compactcatalog.InfoInputSpec {
		return compactcatalog.InfoInputSpec{
			AssetID:       second.Asset,
			Token:         second.Token,
			ExpectedEpoch: epoch,
			FamilyMask:    1 << 0,
			Fingerprint:   [32]byte{fingerprint},
			Witnesses:     []compactcatalog.InfoWitness{infoInputWitness()},
		}
	}
	active := infoInputBegin(t, c, spec(9))
	infoInputStage(t, c, active.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: second.Token, Revision: 1, Size: secondSize, ModifiedNS: secondModified, Fingerprint: "two",
	})
	infoInputSeal(t, c, active.Ref, 1, 0)
	queued := infoInputBegin(t, c, spec(10))
	if queued.Outcome != compactcatalog.InfoInputQueued || queued.Ref.Slot != 1 {
		t.Fatalf("changed input is outcome %s in slot %d", queued.Outcome, queued.Ref.Slot)
	}
	// The queued NEW2 keeps the first OLD availability that the active NEW1
	// must publish, even though the raw asset still reads available.
	c.Exec(`INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer) VALUES(?,1,1,0)`, queued.Ref.ChangeID)
	activeAccepted := infoInputAccept(t, c, active.Ref)
	if activeAccepted.TargetAvailable == nil || *activeAccepted.TargetAvailable != 0 {
		t.Fatalf("active target availability is %v", activeAccepted.TargetAvailable)
	}
	infoInputStage(t, c, queued.Ref, 1, 0, 1, compactcatalog.InfoStreamFactRow{
		InputOrdinal: 0, AssetID: second.Token, Revision: 2, Size: secondSize, ModifiedNS: secondModified, Fingerprint: "three",
	})
	infoInputSeal(t, c, queued.Ref, 1, 0)
	queuedAccepted := infoInputAccept(t, c, queued.Ref)
	if queuedAccepted.State != compactcatalog.InfoInputAcceptedPending || queuedAccepted.FrontierCaptured {
		t.Fatalf("slot-1 acceptance is %s with frontier captured %v", queuedAccepted.State, queuedAccepted.FrontierCaptured)
	}
	if queuedAccepted.TargetAvailable == nil || *queuedAccepted.TargetAvailable != 1 {
		t.Fatalf("queued target availability is %v", queuedAccepted.TargetAvailable)
	}
	var queuedPhase, queuedTarget, queuedUpper int64
	if err := c.DB.QueryRow(`SELECT phase,target_available,upper_entity FROM catalog_info_changes WHERE id=?`, queued.Ref.ChangeID).
		Scan(&queuedPhase, &queuedTarget, &queuedUpper); err != nil {
		t.Fatal(err)
	}
	if queuedPhase != 1 || queuedTarget != 1 || queuedUpper != 0 {
		t.Fatalf("accepted slot 1 is phase %d target %d frontier %d", queuedPhase, queuedTarget, queuedUpper)
	}
	third := spec(11)
	thirdWitness := infoInputWitness()
	thirdWitness.JobID = "job-3"
	thirdWitness.ObjectID = "object-3"
	third.Witnesses = []compactcatalog.InfoWitness{thirdWitness}
	if err := infoInputTryBegin(t, c, third); !errors.Is(err, compactcatalog.ErrInfoInputBusy) {
		t.Fatalf("metadata arriving while both slots are sealed returned %v", err)
	}
}

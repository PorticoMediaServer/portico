package compactcatalog

// Typed source-input staging for one informational publication input.
//
// This file owns the producer side of the normalized NEW source input: it
// opens one exact job/object/probe observation of an asset, stages typed rows of
// the eighteen normalized families in bounded batches, and seals each family at
// its exact declared row count. It is the counterpart of the canonical
// materializer in info_materialize.go: AcceptInfoInputTx freezes a fully sealed
// input at phase 1, and the parent then hands NewInfoMaterializer() to
// AdvanceInfoPublicationTx as the phase-2 callback that installs the immutable
// sealed changed families.
//
// What this API deliberately does not do
//
//   - It does not open or close a transaction. Every function runs inside the
//     caller's *sql.Tx and returns its error so the caller's transaction rolls
//     back; no intent is reported as durable outside a commit.
//   - It does not activate an epoch, release a holder, publish availability,
//     materialize a canonical row or touch a query cache or result set.
//     AcceptInfoInputTx only freezes the sealed input at phase 1 (and, for
//     active slot 0, captures the holder frontier); every later phase belongs
//     to the parent and info_publication.go.
//   - It does not reserve disk or memory. The fixed 32 MiB logical payload
//     ledger is enforced by the existing INSERT triggers on the staging tables;
//     a ledger breach is reported as ErrInfoInputDeferred and there is no
//     larger pool to fall back to.
//   - It does not accept a whole-JSON or blob fact model. Rows are typed
//     columns from the literal normalized DDL; the only JSON columns staged are
//     the existing source detail_json/breadth_json evidence columns of families
//     2 and 17, never a serialized input frame.
//   - It does not rewrite canonical family tables, reconcile a whole canonical
//     disc set or build a second permanent graph. Canonical copying and the
//     raw-frontier comparison stay in the phase-2 materializer.
//   - It does not merge two independent disc observations of different objects
//     into one family manifest. Families 10..18 are object-scoped: one manifest
//     per change/family names exactly one witness object (the DDL keeps one
//     manifest per change/family), so independent objects are separate inputs
//     that are sequenced through the two header slots, never combined.
//
// Header model
//
// One active input lives in slot 0 and at most one queued successor in slot 1,
// matching catalog_info_changes. This release admits one witness at ordinal 0,
// so an input is always one exact job/object/probe. A new input is never merged
// into a header another observation owns:
//
//   - Slot 0 phase 0 with the same witness, fingerprint and an already-covered
//     mask returns the open body unchanged; a new mask bit coalesces by
//     extending family_mask and bumping input_revision. The witness is
//     immutable.
//   - A same-observation frame with a changed fingerprint, or a mask extension
//     after acceptance, queues in slot 1 without touching the active body.
//   - A different witness (another object or job) also queues in slot 1 as its
//     own separate input; when slot 1 is already claimed by another witness
//     Begin reports ErrInfoInputBusy before claiming anything.
//   - Slot 1 phase 0 with no staged family manifest coalesces newer metadata
//     of the SAME witness into the empty successor and reports
//     InfoInputSuperseded. A successor that staged or sealed anything is never
//     replaced: a same-witness retry observes it with InfoInputDuplicate and a
//     different observation defers with ErrInfoInputBusy. So an accepted-pending
//     input cannot be dropped by a newer object's probe.
//   - A scalar phase-0 header with no witness and nothing staged may be claimed
//     by this asset's producer input at ordinal 0.
//
// Changed and parent-witness families
//
// The header family_mask is the changed set. StageInfoRowsTx only writes a
// masked family. A changed child family can carry foreign keys into an
// UNCHANGED parent family (for example a disc title into the disc facts), so
// StageInfoParentRowsTx stages that unmasked parent family for FK support only.
// The parent stays outside the mask, its manifest is sealed like any other, and
// the materializer must leave the canonical parent rows untouched. Receipts
// count changed and parent families and rows separately.
//
// Rows, revision and completeness
//
// Every StageInfoRowsTx and SealInfoFamilyTx call carries the header
// input_revision the caller observed. A coalesce between quanta bumps that
// revision, so a stale handle fails with ErrInfoInputDrift instead of staging
// into a body it does not describe. At most eight raw rows are written per
// StageInfoRowsTx call, and a caller invokes it at most once per write
// transaction, so no transaction stages more than eight raw rows through this
// API. Ordinals are stable and strictly increasing per family, and rows_staged
// is maintained from the actual INSERT row count. A seal proves the staging
// table really holds rows_staged = rows_expected rows before it marks the family
// sealed. Callers that need to abandon a handle use InfoInputNeedsAbort; the
// bounded abort/GC protocol itself is the parent's.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"time"
)

var (
	// ErrInfoInputDrift means the caller's handle no longer describes the
	// durable input: the header revision moved, the slot was promoted or the
	// asset epoch/fingerprint/witness set disagrees with the staged body.
	ErrInfoInputDrift = errors.New("info source input drifted")
	// ErrInfoInputIncomplete means a seal was asked for before the family
	// reached its declared exact row count, or a batch overran it.
	ErrInfoInputIncomplete = errors.New("info source input is incomplete")
	// ErrInfoInputSealed means the family or input body is already sealed and
	// must not be rewritten.
	ErrInfoInputSealed = errors.New("info source input is sealed")
	// ErrInfoInputBusy means both header slots are occupied in a way that
	// cannot be coalesced; the parent must defer or finish the active input.
	ErrInfoInputBusy = errors.New("info source input slots are busy")
	// ErrInfoInputDeferred means the logical 32 MiB source ledger refused the
	// write. There is no larger pool; the input is deferred before acceptance.
	ErrInfoInputDeferred = errors.New("info source input deferred by the logical ledger")
	// ErrInfoInputUnwitnessed means the input carries no source witness.
	ErrInfoInputUnwitnessed = errors.New("info source input has no source witness")
	// ErrInfoInputNoAsset means the physical asset row is gone, so the input
	// can no longer be accepted.
	ErrInfoInputNoAsset = errors.New("info source input asset is absent")
)

// InfoInputNeedsAbort reports whether a failed call left a durable input that
// cannot be continued and must go through the parent's bounded abort/GC
// protocol. Drift means the handle no longer names the durable body, and
// ErrInfoInvariant means the body is internally inconsistent, so both fail
// closed. Deferrals and refusals (Busy, Deferred, Incomplete, Sealed, NoAsset,
// Unwitnessed) claimed or changed nothing the caller can abandon: the parent
// finishes the active input, retries later or repairs its own call instead.
func InfoInputNeedsAbort(err error) bool {
	return errors.Is(err, ErrInfoInputDrift) || errors.Is(err, ErrInfoInvariant)
}

// infoInputFamilies is the fixed number of normalized source-input families,
// equal to the literal CREATE TABLE order of the staging DDL.
const infoInputFamilies = 18

// infoInputFamilyMask covers families 1..18.
const infoInputFamilyMask = int64(1)<<infoInputFamilies - 1

// infoInputRowsPerCall is the raw-row budget of one StageInfoRowsTx call. It is
// the same eight-row unit the pre-fence and materializer quanta use.
const infoInputRowsPerCall = 8

// infoInputWitnessesPerInput is the future witness-table geometry. This release
// admits only infoInputWitnessesAdmitted (ordinal 0) so that one input always
// names one exact job/object/probe and its family manifests stay unambiguous.
const infoInputWitnessesPerInput = 8

// infoInputWitnessesAdmitted is the number of source observations this release
// accepts per input.
const infoInputWitnessesAdmitted = 1

// InfoInputState is the durable acceptance state of one source input.
type InfoInputState int

const (
	// InfoInputStaging means the body is not accepted yet: at least one changed
	// or parent-witness family is not sealed at its exact declared row count, or
	// every family is sealed but whole-input acceptance has not frozen the
	// availability target and moved the body to phase 1.
	InfoInputStaging InfoInputState = iota
	// InfoInputAcceptedPending means every changed family and every staged
	// parent-witness family is sealed and durable but the asset's accepted epoch
	// has not advanced yet. Producers must return this explicit receipt rather
	// than reporting NEW as applied.
	InfoInputAcceptedPending
	// InfoInputApplied means activation advanced the asset epoch past the
	// input's expected epoch, so the accepted frame is coherent.
	InfoInputApplied
)

func (s InfoInputState) String() string {
	switch s {
	case InfoInputStaging:
		return "staging"
	case InfoInputAcceptedPending:
		return "accepted-pending"
	case InfoInputApplied:
		return "applied"
	default:
		return fmt.Sprintf("info-input-state(%d)", int(s))
	}
}

// InfoWitness is one immutable live source observation that admits an input.
// Every field is stored exactly as the source job reported it; witnesses are
// inserted once and never updated.
type InfoWitness struct {
	Kind                   int
	JobID                  string
	SourceID               string
	SourceGeneration       int64
	RootIncarnation        string
	ObjectID               string
	ObjectRevision         string
	PolicyRevision         int64
	ExpectedChoiceRevision int64
	Fingerprint            [32]byte
}

const (
	InfoWitnessInventory = iota
	InfoWitnessAsset
	InfoWitnessPlayback
	InfoWitnessDisc
	InfoWitnessRefresh
)

func (w InfoWitness) validate() error {
	if w.Kind < 0 || w.Kind > 4 {
		return fmt.Errorf("%w: witness kind %d", ErrInfoInvariant, w.Kind)
	}
	if w.Kind == InfoWitnessAsset || w.Kind == InfoWitnessPlayback {
		// Direct media/DVR and a live descriptor-bound playback probe do not
		// invent an inventory object or scan job. Their owner validates the
		// exact physical snapshot fingerprint before sealing/activation.
		if w.Fingerprint == ([32]byte{}) {
			return fmt.Errorf("%w: physical input has no snapshot fingerprint", ErrInfoInvariant)
		}
		if w.SourceGeneration < 0 || w.PolicyRevision < 0 || w.ExpectedChoiceRevision < 0 {
			return fmt.Errorf("%w: physical witness revision", ErrInfoInvariant)
		}
		return nil
	}
	if w.JobID == "" || w.SourceID == "" || w.RootIncarnation == "" || w.ObjectID == "" || w.ObjectRevision == "" {
		return fmt.Errorf("%w: witness has a blank job/source/object field", ErrInfoInvariant)
	}
	if w.SourceGeneration <= 0 {
		return fmt.Errorf("%w: witness generation %d", ErrInfoInvariant, w.SourceGeneration)
	}
	if w.PolicyRevision < 0 || w.ExpectedChoiceRevision < 0 {
		return fmt.Errorf("%w: witness policy/choice revision is negative", ErrInfoInvariant)
	}
	return nil
}

// InfoInputSpec is one exact source observation to admit or continue. It is one
// job/object/probe: this release accepts exactly one witness, always ordinal 0.
type InfoInputSpec struct {
	AssetID       int64
	Token         string
	ExpectedEpoch int64
	FamilyMask    int64
	Fingerprint   [32]byte
	Witnesses     []InfoWitness
	// CreatedMS is the durable creation time; zero uses the wall clock.
	CreatedMS int64
}

func (s InfoInputSpec) validate() error {
	if s.AssetID <= 0 || s.Token == "" {
		return fmt.Errorf("%w: asset identity", ErrInfoInvariant)
	}
	if s.ExpectedEpoch <= 0 {
		return fmt.Errorf("%w: expected epoch %d", ErrInfoInvariant, s.ExpectedEpoch)
	}
	if s.FamilyMask < 0 || s.FamilyMask&^infoInputFamilyMask != 0 {
		return fmt.Errorf("%w: family mask %#x is outside families 1..18", ErrInfoInvariant, s.FamilyMask)
	}
	if len(s.Witnesses) == 0 {
		return ErrInfoInputUnwitnessed
	}
	if len(s.Witnesses) > infoInputWitnessesAdmitted {
		// The witness table keeps room for several independent observations,
		// but this release admits one exact job/object/probe at ordinal 0. A
		// second observation must wait for its own slot instead of merging into
		// this input's families.
		return fmt.Errorf("%w: %d witnesses offered; this release admits ordinal 0 only", ErrInfoInputBusy, len(s.Witnesses))
	}
	for i := range s.Witnesses {
		if err := s.Witnesses[i].validate(); err != nil {
			return err
		}
	}
	return nil
}

// InfoInputRef is the caller's handle to one staged input. InputRevision is the
// exact header revision the caller last observed; every later staging call
// compares it and fails closed if a coalesce moved it.
type InfoInputRef struct {
	ChangeID      int64
	AssetID       int64
	Token         string
	Slot          int
	InputRevision int64
}

// InfoFamilyRole says why rows of one normalized family are staged.
//
// A changed family is a bit of the header family mask: the phase-2 materializer
// installs it into the canonical tables. A parent family is intentionally not in
// the mask: its rows exist only to satisfy the NEW source-input foreign keys of a
// changed child family, and the materializer must leave canonical parent rows
// untouched because that parent family is UNCHANGED. The distinction is derived
// from the header mask at read time; it is never stored as a new column, and the
// approved DDL is unchanged.
type InfoFamilyRole int

const (
	// InfoFamilyChanged is a family in the header mask.
	InfoFamilyChanged InfoFamilyRole = iota
	// InfoFamilyParent is an unmasked family staged only for NEW foreign keys.
	InfoFamilyParent
)

func (r InfoFamilyRole) String() string {
	switch r {
	case InfoFamilyChanged:
		return "changed"
	case InfoFamilyParent:
		return "parent-witness"
	default:
		return fmt.Sprintf("info-family-role(%d)", int(r))
	}
}

// InfoInputReceipt is the explicit durable acceptance state of an input. The
// changed and parent-witness families are counted separately because only
// changed families are materialized.
type InfoInputReceipt struct {
	State                InfoInputState
	ChangeID             int64
	Slot                 int
	InputRevision        int64
	ExpectedEpoch        int64
	FamiliesMasked       int
	FamiliesSealed       int
	FamiliesParent       int
	FamiliesParentSealed int
	RowsExpected         int64
	RowsStaged           int64
	RowsParentExpected   int64
	RowsParentStaged     int64
	// TargetAvailable is the availability frozen into the header at whole-input
	// acceptance. It is nil when the input publishes no availability target.
	TargetAvailable *int64
}

// InfoAcceptReceipt is the whole-input acceptance result.
type InfoAcceptReceipt struct {
	InfoInputReceipt
	// FrontierCaptured reports that this call captured the active slot's holder
	// frontier (upper_entity). A queued successor recaptures its frontier only
	// when the parent promotes it into slot 0.
	FrontierCaptured bool
}

// InfoInputOutcome says what BeginInfoInputTx did to the two header slots, so a
// merge or supersession is never implicit.
type InfoInputOutcome int

const (
	// InfoInputOpened created a new slot-0 input.
	InfoInputOpened InfoInputOutcome = iota
	// InfoInputCoalesced grew the family mask of the open phase-0 active input.
	InfoInputCoalesced
	// InfoInputUnchanged returned the already-open phase-0 input untouched.
	InfoInputUnchanged
	// InfoInputQueued created the slot-1 successor behind the active input.
	InfoInputQueued
	// InfoInputSuperseded folded this observation's newest raw frame into its
	// own empty, unaccepted slot-1 body. No staged rows were lost and the
	// witness is unchanged; the previous handle is stale and fails with
	// ErrInfoInputDrift.
	InfoInputSuperseded
	// InfoInputDuplicate returned the observation's existing body for a retry.
	// At phase 0 the caller may continue staging with the returned revision;
	// once the body is accepted the receipt is read-only.
	InfoInputDuplicate
)

func (o InfoInputOutcome) String() string {
	switch o {
	case InfoInputOpened:
		return "opened"
	case InfoInputCoalesced:
		return "coalesced"
	case InfoInputUnchanged:
		return "unchanged"
	case InfoInputQueued:
		return "queued"
	case InfoInputSuperseded:
		return "superseded"
	case InfoInputDuplicate:
		return "duplicate"
	default:
		return fmt.Sprintf("info-input-outcome(%d)", int(o))
	}
}

// InfoInput is what BeginInfoInputTx returns: the handle, its state and what
// happened to the slots.
type InfoInput struct {
	Ref     InfoInputRef
	Receipt InfoInputReceipt
	Outcome InfoInputOutcome
}

// InfoStageReceipt is one bounded staging batch's result. The embedded receipt
// carries the input-wide totals; the Family* fields describe only the family
// this call touched.
type InfoStageReceipt struct {
	InfoInputReceipt
	Family             int
	FamilyRole         InfoFamilyRole
	FamilyRowsExpected int64
	FamilyRowsStaged   int64
}

// InfoSealReceipt is one family seal's result.
type InfoSealReceipt struct {
	InfoInputReceipt
	Family             int
	FamilyRole         InfoFamilyRole
	FamilyRowsExpected int64
	FamilyRowsStaged   int64
	Sealed             bool
}

type infoInputHeader struct {
	changeID        int64
	assetID         int64
	token           string
	slot            int
	phase           int
	inputRevision   int64
	expectedEpoch   int64
	familyMask      int64
	fingerprint     []byte
	targetAvailable sql.NullInt64
	fault           string
}

func (h infoInputHeader) ref() InfoInputRef {
	return InfoInputRef{
		ChangeID:      h.changeID,
		AssetID:       h.assetID,
		Token:         h.token,
		Slot:          h.slot,
		InputRevision: h.inputRevision,
	}
}

// BeginInfoInputTx admits one exact source observation against the asset's two
// header slots. This release admits exactly one witness (ordinal 0), so an
// input is always one job/object/probe. It never merges a second independent
// observation into a header another observation already claimed:
//
//   - Same witness, same fingerprint, phase 0: the caller continues the open
//     active body and may only grow its mask.
//   - Same witness, changed frame or a mask extension after acceptance: the
//     frame queues in slot 1 behind the active body.
//   - A different witness (another object or job) never touches the active
//     body; it claims the free queued slot as its own separate input, or gets
//     ErrInfoInputBusy before anything is claimed when slot 1 is already taken.
//   - A scalar phase-0 header with no witness and nothing staged may be claimed
//     by this asset's producer input.
//
// The returned receipt describes state inside the caller's transaction; the
// parent may report accepted-pending only after that transaction commits and
// whole-input acceptance has moved the body to phase 1.
func BeginInfoInputTx(ctx context.Context, tx *sql.Tx, spec InfoInputSpec) (InfoInput, error) {
	var invalidated bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain=41 AND source_id=? AND token=? AND flags&128<>0)`, spec.AssetID, spec.Token).Scan(&invalidated); err != nil {
		return InfoInput{}, err
	}
	if invalidated {
		return InfoInput{}, ErrInfoInputBusy
	}
	input, err := beginInfoInputTx(ctx, tx, spec)
	if err != nil {
		return InfoInput{}, err
	}
	var queued bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain=41 AND source_id=? AND token=? AND flags&32<>0)`, spec.AssetID, spec.Token).Scan(&queued); err != nil {
		return InfoInput{}, err
	}
	if !queued {
		err = queueSource(ctx, tx, DomainAssetSources, spec.AssetID, spec.Token, sourceInfo|sourceMetadata|sourceRevision)
	}
	return input, err
}

func beginInfoInputTx(ctx context.Context, tx *sql.Tx, spec InfoInputSpec) (InfoInput, error) {
	if err := spec.validate(); err != nil {
		return InfoInput{}, err
	}
	created := spec.CreatedMS
	if created <= 0 {
		created = time.Now().UnixMilli()
	}
	var epoch int64
	err := tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, spec.AssetID, spec.Token).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return InfoInput{}, fmt.Errorf("%w: asset %d", ErrInfoInputNoAsset, spec.AssetID)
	}
	if err != nil {
		return InfoInput{}, err
	}
	if epoch != spec.ExpectedEpoch {
		return InfoInput{}, fmt.Errorf("%w: asset epoch is %d, input expected %d", ErrInfoInputDrift, epoch, spec.ExpectedEpoch)
	}

	active, err := infoInputHeaderBySlot(ctx, tx, spec.AssetID, spec.Token, 0)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err = insertInfoInputHeader(ctx, tx, spec, 0, spec.ExpectedEpoch, created); err != nil {
			return InfoInput{}, err
		}
		return begunInfoInputAs(ctx, tx, spec.AssetID, spec.Token, 0, InfoInputOpened)
	case err != nil:
		return InfoInput{}, err
	}
	if active.fault == infoReclaim {
		return InfoInput{}, ErrInfoInputBusy
	}
	witnesses, err := infoInputWitnesses(ctx, tx, active.changeID)
	if err != nil {
		return InfoInput{}, err
	}
	switch len(witnesses) {
	case 0:
		// A scalar phase-0 header carries no witness; the same asset's producer
		// input may claim it before anything is staged.
		if active.phase != 0 {
			return queueInfoInputSuccessor(ctx, tx, active, spec, created)
		}
		if err = infoInputClaimable(ctx, tx, active); err != nil {
			return InfoInput{}, err
		}
		return claimInfoInputHeader(ctx, tx, active, spec)
	case 1:
		if witnesses[0].witness != spec.Witnesses[0] {
			// Another exact source observation owns this header. Never merge;
			// admit this one only into the free queued slot.
			return queueInfoInputSuccessor(ctx, tx, active, spec, created)
		}
		if bytes.Equal(active.fingerprint, spec.Fingerprint[:]) {
			if active.phase == 0 {
				return extendInfoInputMask(ctx, tx, active, spec.FamilyMask)
			}
			if active.familyMask|spec.FamilyMask == active.familyMask {
				return begunInfoInputAs(ctx, tx, spec.AssetID, spec.Token, 0, InfoInputDuplicate)
			}
		}
		return queueInfoInputSuccessor(ctx, tx, active, spec, created)
	default:
		return InfoInput{}, fmt.Errorf("%w: input %d holds %d witnesses; this release admits ordinal 0 only",
			ErrInfoInvariant, active.changeID, len(witnesses))
	}
}

// StageInfoRowsTx writes at most eight typed normalized rows of one changed
// family of the active input. Rows carry input_ordinal and every typed source
// column of the literal DDL; the family manifest's rows_staged advances by the
// actual INSERT row count.
func StageInfoRowsTx(ctx context.Context, tx *sql.Tx, ref InfoInputRef, family, witnessOrdinal int, rowsExpected int64, rows ...InfoStageRow) (InfoStageReceipt, error) {
	return stageInfoRowsTx(ctx, tx, ref, InfoFamilyChanged, family, witnessOrdinal, rowsExpected, rows...)
}

// StageInfoParentRowsTx stages rows of an UNMASKED family that exist only to
// satisfy the NEW source-input foreign keys of a changed child family. The
// parent family stays outside the header mask, so the phase-2 materializer must
// leave its canonical rows untouched: only the changed child is installed. A
// parent family is sealed through SealInfoFamilyTx like any other staged family.
func StageInfoParentRowsTx(ctx context.Context, tx *sql.Tx, ref InfoInputRef, family, witnessOrdinal int, rowsExpected int64, rows ...InfoStageRow) (InfoStageReceipt, error) {
	return stageInfoRowsTx(ctx, tx, ref, InfoFamilyParent, family, witnessOrdinal, rowsExpected, rows...)
}

func stageInfoRowsTx(ctx context.Context, tx *sql.Tx, ref InfoInputRef, role InfoFamilyRole, family, witnessOrdinal int, rowsExpected int64, rows ...InfoStageRow) (InfoStageReceipt, error) {
	if witnessOrdinal != 0 {
		return InfoStageReceipt{}, fmt.Errorf("%w: witness ordinal %d is deferred; this release admits ordinal 0", ErrInfoInputBusy, witnessOrdinal)
	}
	if family < 1 || family > infoInputFamilies {
		return InfoStageReceipt{}, fmt.Errorf("%w: family %d", ErrInfoInvariant, family)
	}
	if role != InfoFamilyChanged && role != InfoFamilyParent {
		return InfoStageReceipt{}, fmt.Errorf("%w: family role %d", ErrInfoInvariant, int(role))
	}
	if len(rows) == 0 {
		return InfoStageReceipt{}, fmt.Errorf("%w: family %d staged no rows", ErrInfoInputIncomplete, family)
	}
	if len(rows) > infoInputRowsPerCall {
		return InfoStageReceipt{}, fmt.Errorf("%w: %d rows exceed the %d-row staging batch", ErrInfoInvariant, len(rows), infoInputRowsPerCall)
	}
	if rowsExpected < int64(len(rows)) {
		return InfoStageReceipt{}, fmt.Errorf("%w: family %d declares %d rows but stages %d", ErrInfoInputIncomplete, family, rowsExpected, len(rows))
	}
	h, err := infoInputHeaderForWrite(ctx, tx, ref)
	if err != nil {
		return InfoStageReceipt{}, err
	}
	if err = infoInputRequireRole(h, family, role); err != nil {
		return InfoStageReceipt{}, err
	}
	witness, err := infoInputWitnessAt(ctx, tx, h.changeID, witnessOrdinal)
	if err != nil {
		return InfoStageReceipt{}, err
	}
	table := infoStageTables[family]
	if table.table == "" || table.columns == "" {
		return InfoStageReceipt{}, fmt.Errorf("%w: family %d has no staged table", ErrInfoInvariant, family)
	}
	expected, staged, sealed, manifestWitness, err := infoInputManifest(ctx, tx, h.changeID, family)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed)
 VALUES(?,?,?,?,0,0)`, h.changeID, family, witnessOrdinal, rowsExpected); err != nil {
			return InfoStageReceipt{}, infoInputClassifyWrite(err)
		}
		expected, staged, sealed, manifestWitness = rowsExpected, 0, 0, witnessOrdinal
	case err != nil:
		return InfoStageReceipt{}, err
	}
	if sealed != 0 {
		return InfoStageReceipt{}, fmt.Errorf("%w: family %d is sealed", ErrInfoInputSealed, family)
	}
	if expected != rowsExpected {
		return InfoStageReceipt{}, fmt.Errorf("%w: family %d declares %d rows, manifest holds %d", ErrInfoInputDrift, family, rowsExpected, expected)
	}
	if staged+int64(len(rows)) > expected {
		return InfoStageReceipt{}, fmt.Errorf("%w: family %d stages %d rows past its %d-row declaration", ErrInfoInputIncomplete, family, staged+int64(len(rows)), expected)
	}
	if infoInputObjectScoped(family) && manifestWitness != witnessOrdinal {
		return InfoStageReceipt{}, fmt.Errorf("%w: object-scoped family %d belongs to witness %d, not %d", ErrInfoInputDrift, family, manifestWitness, witnessOrdinal)
	}

	last, err := infoInputMaxOrdinal(ctx, tx, table.table, h.changeID)
	if err != nil {
		return InfoStageReceipt{}, err
	}
	columns := strings.Count(table.columns, ",") + 1
	// The INSERT supplies change_id as the first value of every row group, so
	// the arguments are interleaved per row rather than hoisted once.
	args := make([]any, 0, len(rows)*(columns+1))
	for _, row := range rows {
		stagedFamily, values := row.infoStageRow()
		if stagedFamily != family {
			return InfoStageReceipt{}, fmt.Errorf("%w: family %d row reports family %d", ErrInfoInvariant, family, stagedFamily)
		}
		if len(values) != columns {
			return InfoStageReceipt{}, fmt.Errorf("%w: family %d row holds %d of %d source columns", ErrInfoInvariant, family, len(values), columns)
		}
		ordinal, ok := values[0].(int64)
		if !ok || ordinal < 0 || ordinal <= last {
			return InfoStageReceipt{}, fmt.Errorf("%w: family %d input ordinal is not monotonic", ErrInfoInputDrift, family)
		}
		if table.assetAt >= 0 {
			asset, ok := values[table.assetAt].(string)
			if !ok || asset != h.token {
				return InfoStageReceipt{}, fmt.Errorf("%w: family %d stages another asset's row", ErrInfoInvariant, family)
			}
		}
		if infoInputObjectScoped(family) {
			object, ok := values[1].(string)
			if !ok || object != witness.ObjectID {
				return InfoStageReceipt{}, fmt.Errorf("%w: family %d belongs to witness object %q, not %q", ErrInfoInvariant, family, witness.ObjectID, object)
			}
		}
		last = ordinal
		args = append(args, h.changeID)
		args = append(args, values...)
	}
	result, err := tx.ExecContext(ctx, infoStageInsertSQL(table, len(rows)), args...)
	if err != nil {
		return InfoStageReceipt{}, infoInputClassifyWrite(err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return InfoStageReceipt{}, err
	}
	if inserted != int64(len(rows)) {
		return InfoStageReceipt{}, fmt.Errorf("%w: family %d inserted %d of %d rows", ErrInfoInvariant, family, inserted, len(rows))
	}
	receipt, err := infoInputReceipt(ctx, tx, h)
	if err != nil {
		return InfoStageReceipt{}, err
	}
	return InfoStageReceipt{
		InfoInputReceipt:   receipt,
		Family:             family,
		FamilyRole:         role,
		FamilyRowsExpected: expected,
		FamilyRowsStaged:   staged + int64(len(rows)),
	}, nil
}

// SealInfoFamilyTx seals one family at its exact declared row count. A masked
// family that stages no rows seals as an empty manifest, so every bit of the
// header mask has a durable manifest before acceptance. An unmasked parent
// family must already have staged rows; it is never invented empty.
func SealInfoFamilyTx(ctx context.Context, tx *sql.Tx, ref InfoInputRef, family, witnessOrdinal int) (InfoSealReceipt, error) {
	if witnessOrdinal != 0 {
		return InfoSealReceipt{}, fmt.Errorf("%w: witness ordinal %d is deferred; this release admits ordinal 0", ErrInfoInputBusy, witnessOrdinal)
	}
	if family < 1 || family > infoInputFamilies {
		return InfoSealReceipt{}, fmt.Errorf("%w: family %d", ErrInfoInvariant, family)
	}
	h, err := infoInputHeaderForWrite(ctx, tx, ref)
	if err != nil {
		return InfoSealReceipt{}, err
	}
	role := InfoFamilyChanged
	if h.familyMask&(int64(1)<<uint(family-1)) == 0 {
		role = InfoFamilyParent
	}
	table := infoStageTables[family]
	if table.table == "" {
		return InfoSealReceipt{}, fmt.Errorf("%w: family %d has no staged table", ErrInfoInvariant, family)
	}
	expected, staged, sealed, manifestWitness, err := infoInputManifest(ctx, tx, h.changeID, family)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if role != InfoFamilyChanged {
			return InfoSealReceipt{}, fmt.Errorf("%w: parent witness family %d has no staged rows", ErrInfoInvariant, family)
		}
		if _, err = infoInputWitnessAt(ctx, tx, h.changeID, witnessOrdinal); err != nil {
			return InfoSealReceipt{}, err
		}
		var actual int64
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table.table+` WHERE change_id=?)`, h.changeID).Scan(&actual); err != nil {
			return InfoSealReceipt{}, err
		}
		if actual != 0 {
			return InfoSealReceipt{}, fmt.Errorf("%w: family %d stages %d rows without a manifest", ErrInfoInvariant, family, actual)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed)
 VALUES(?,?,?,0,0,1)`, h.changeID, family, witnessOrdinal); err != nil {
			return InfoSealReceipt{}, infoInputClassifyWrite(err)
		}
		expected, staged, sealed, manifestWitness = 0, 0, 1, witnessOrdinal
	case err != nil:
		return InfoSealReceipt{}, err
	}
	if infoInputObjectScoped(family) && manifestWitness != witnessOrdinal {
		return InfoSealReceipt{}, fmt.Errorf("%w: object-scoped family %d belongs to witness %d, not %d", ErrInfoInputDrift, family, manifestWitness, witnessOrdinal)
	}
	if sealed == 0 {
		if staged != expected {
			return InfoSealReceipt{}, fmt.Errorf("%w: family %d manifest %d/%d", ErrInfoInputIncomplete, family, staged, expected)
		}
		result, err := tx.ExecContext(ctx, `UPDATE catalog_info_input_families SET sealed=1
 WHERE change_id=? AND family=? AND sealed=0 AND rows_staged=rows_expected`, h.changeID, family)
		if err != nil {
			return InfoSealReceipt{}, infoInputClassifyWrite(err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return InfoSealReceipt{}, err
		}
		if updated != 1 {
			return InfoSealReceipt{}, fmt.Errorf("%w: family %d seal did not apply", ErrInfoInputDrift, family)
		}
	}
	receipt, err := infoInputReceipt(ctx, tx, h)
	if err != nil {
		return InfoSealReceipt{}, err
	}
	return InfoSealReceipt{
		InfoInputReceipt:   receipt,
		Family:             family,
		FamilyRole:         role,
		FamilyRowsExpected: expected,
		FamilyRowsStaged:   staged,
		Sealed:             true,
	}, nil
}

// AcceptInfoInputTx freezes one fully sealed input into its immutable accepted
// form. In the same transaction it:
//
//   - proves every changed family and every staged parent-witness family is
//     sealed at its exact row count;
//   - freezes target_available: the queued successor's first OLD availability
//     when an active slot 0 needs protecting from that queued NEW2, otherwise
//     the asset's current raw availability. The queued successor itself freezes
//     the current raw availability so a later raw change cannot move it;
//   - moves the header to phase 1 and, for active slot 0 only, captures the
//     current MAX holder upper_entity in the same transaction. A queued slot 1
//     keeps upper_entity for recapture at promotion by the parent;
//   - reloads the receipt, which now reports InfoInputAcceptedPending.
//
// After this call the body is immutable: staging and sealing both fail with
// ErrInfoInputSealed, so no producer can rewrite an accepted-pending input
// before the worker takes its turn. While both slots are sealed this way,
// BeginInfoInputTx defers new metadata with ErrInfoInputBusy; raw availability
// revocation stays independent of that metadata lane.
func AcceptInfoInputTx(ctx context.Context, tx *sql.Tx, ref InfoInputRef) (InfoAcceptReceipt, error) {
	h, err := infoInputHeaderForWrite(ctx, tx, ref)
	if err != nil {
		return InfoAcceptReceipt{}, err
	}
	witnesses, err := infoInputWitnesses(ctx, tx, h.changeID)
	if err != nil {
		return InfoAcceptReceipt{}, err
	}
	if len(witnesses) != infoInputWitnessesAdmitted || witnesses[0].ordinal != 0 {
		return InfoAcceptReceipt{}, fmt.Errorf("%w: input %d holds %d witnesses; acceptance needs the exact ordinal-0 observation",
			ErrInfoInputUnwitnessed, h.changeID, len(witnesses))
	}
	receipt, err := infoInputReceipt(ctx, tx, h)
	if err != nil {
		return InfoAcceptReceipt{}, err
	}
	if receipt.FamiliesSealed != receipt.FamiliesMasked || receipt.FamiliesParentSealed != receipt.FamiliesParent {
		return InfoAcceptReceipt{}, fmt.Errorf("%w: input %d has %d/%d changed and %d/%d parent families sealed",
			ErrInfoInputIncomplete, h.changeID, receipt.FamiliesSealed, receipt.FamiliesMasked,
			receipt.FamiliesParentSealed, receipt.FamiliesParent)
	}
	if _, err = EnsureInfoShapeTargetTx(ctx, tx, h.changeID); err != nil {
		return InfoAcceptReceipt{}, err
	}
	var target sql.NullInt64
	if h.slot == 0 {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(
 (SELECT o.old_integer FROM catalog_info_changes n JOIN catalog_info_scalar_old o ON o.change_id=n.id AND o.field=1 WHERE n.asset_id=? AND n.asset_token=? AND n.slot=1),
 (SELECT available FROM catalog_assets WHERE id=? AND token=?))`, h.assetID, h.token, h.assetID, h.token).Scan(&target)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT available FROM catalog_assets WHERE id=? AND token=?`, h.assetID, h.token).Scan(&target)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return InfoAcceptReceipt{}, fmt.Errorf("%w: asset %d", ErrInfoInputNoAsset, h.assetID)
	}
	if err != nil {
		return InfoAcceptReceipt{}, err
	}
	if !target.Valid {
		// The COALESCE form of the slot-0 target cannot raise ErrNoRows when the
		// asset vanished, so absence is detected as an unusable target instead.
		return InfoAcceptReceipt{}, fmt.Errorf("%w: asset %d has no availability target", ErrInfoInputNoAsset, h.assetID)
	}
	if err = freezeInfoInputOwnerTx(ctx, tx, h); err != nil {
		return InfoAcceptReceipt{}, err
	}
	var result sql.Result
	if h.slot == 0 {
		result, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=1,has_after=0,target_available=?,
 upper_entity=COALESCE((SELECT MAX(entity_id) FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?),0)
 WHERE id=? AND slot=0 AND phase=0 AND input_revision=?`, target, h.assetID, h.changeID, h.inputRevision)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=1,has_after=0,target_available=?
 WHERE id=? AND slot=1 AND phase=0 AND input_revision=?`, target, h.changeID, h.inputRevision)
	}
	if err != nil {
		return InfoAcceptReceipt{}, infoInputClassifyWrite(err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return InfoAcceptReceipt{}, err
	}
	if updated != 1 {
		return InfoAcceptReceipt{}, fmt.Errorf("%w: acceptance of input %d did not apply", ErrInfoInputDrift, h.changeID)
	}
	h, err = infoInputHeaderByID(ctx, tx, h.changeID)
	if err != nil {
		return InfoAcceptReceipt{}, err
	}
	receipt, err = infoInputReceipt(ctx, tx, h)
	if err != nil {
		return InfoAcceptReceipt{}, err
	}
	// Accepted normalized input must drain even if its last link disappeared.
	if err = queueSource(ctx, tx, DomainAssetSources, h.assetID, h.token, sourceInfo|sourceMetadata|sourceRevision); err != nil {
		return InfoAcceptReceipt{}, err
	}
	return InfoAcceptReceipt{InfoInputReceipt: receipt, FrontierCaptured: h.slot == 0}, nil
}

// InfoInputReceiptTx reads the current durable state of one input. It never
// reports AcceptedPending until every masked family is sealed at its exact row
// count, and never reports Applied until the asset epoch advanced.
func InfoInputReceiptTx(ctx context.Context, tx *sql.Tx, changeID int64) (InfoInputReceipt, error) {
	h, err := infoInputHeaderByID(ctx, tx, changeID)
	if err != nil {
		return InfoInputReceipt{}, err
	}
	receipt, err := infoInputReceipt(ctx, tx, h)
	return receipt, err
}

func begunInfoInputAs(ctx context.Context, tx *sql.Tx, asset int64, token string, slot int, outcome InfoInputOutcome) (InfoInput, error) {
	h, err := infoInputHeaderBySlot(ctx, tx, asset, token, slot)
	if err != nil {
		return InfoInput{}, err
	}
	receipt, err := infoInputReceipt(ctx, tx, h)
	if err != nil {
		return InfoInput{}, err
	}
	return InfoInput{Ref: h.ref(), Receipt: receipt, Outcome: outcome}, nil
}

// extendInfoInputMask coalesces the same raw frame into the open active body.
// The witness set was already proven identical, so only the mask can grow.
func extendInfoInputMask(ctx context.Context, tx *sql.Tx, h infoInputHeader, mask int64) (InfoInput, error) {
	merged := h.familyMask | mask
	if merged != h.familyMask {
		result, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET family_mask=?,input_revision=input_revision+1
 WHERE id=? AND slot=? AND phase=0 AND input_revision=?`, merged, h.changeID, h.slot, h.inputRevision)
		if err != nil {
			return InfoInput{}, infoInputClassifyWrite(err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return InfoInput{}, err
		}
		if updated != 1 {
			return InfoInput{}, fmt.Errorf("%w: active input advanced while coalescing", ErrInfoInputDrift)
		}
	}
	outcome := InfoInputCoalesced
	if merged == h.familyMask {
		outcome = InfoInputUnchanged
	}
	return begunInfoInputAs(ctx, tx, h.assetID, h.token, h.slot, outcome)
}

// queueInfoInputSuccessor admits a separate input into slot 1 behind the active
// body. The active body is never touched. Slot 1 belongs to the observation
// that claimed it: a different observation is refused with ErrInfoInputBusy
// before any claim, and a same-observation retry either coalesces an empty
// body or observes the handle it already has.
func queueInfoInputSuccessor(ctx context.Context, tx *sql.Tx, active infoInputHeader, spec InfoInputSpec, created int64) (InfoInput, error) {
	expected := active.expectedEpoch + 1
	queued, err := infoInputHeaderBySlot(ctx, tx, spec.AssetID, spec.Token, 1)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err = insertInfoInputHeader(ctx, tx, spec, 1, expected, created); err != nil {
			return InfoInput{}, err
		}
		return begunInfoInputAs(ctx, tx, spec.AssetID, spec.Token, 1, InfoInputQueued)
	case err != nil:
		return InfoInput{}, err
	}
	if queued.expectedEpoch != expected {
		return InfoInput{}, fmt.Errorf("%w: queued expected epoch %d, active %d", ErrInfoInputDrift, queued.expectedEpoch, active.expectedEpoch)
	}
	witnesses, err := infoInputWitnesses(ctx, tx, queued.changeID)
	if err != nil {
		return InfoInput{}, err
	}
	switch len(witnesses) {
	case 0:
		// An unwitnessed scalar header may be claimed by this asset's producer
		// input before anything is staged.
		if err = infoInputClaimable(ctx, tx, queued); err != nil {
			return InfoInput{}, err
		}
		return claimInfoInputHeader(ctx, tx, queued, spec)
	case 1:
		if witnesses[0].witness != spec.Witnesses[0] {
			return InfoInput{}, fmt.Errorf("%w: queued slot is claimed by another source observation", ErrInfoInputBusy)
		}
		if queued.phase != 0 {
			// The same observation is already accepted/publishing here; a retry
			// observes the immutable body instead of rewriting it.
			return begunInfoInputAs(ctx, tx, spec.AssetID, spec.Token, 1, InfoInputDuplicate)
		}
		var manifests int64
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_info_input_families WHERE change_id=?`, queued.changeID).Scan(&manifests); err != nil {
			return InfoInput{}, err
		}
		if manifests != 0 {
			// The same observation already staged rows; a retry continues from
			// the handle it is given instead of discarding the body.
			return begunInfoInputAs(ctx, tx, spec.AssetID, spec.Token, 1, InfoInputDuplicate)
		}
		return coalesceInfoInputQueued(ctx, tx, queued, spec)
	default:
		return InfoInput{}, fmt.Errorf("%w: queued input %d holds %d witnesses; this release admits ordinal 0 only",
			ErrInfoInvariant, queued.changeID, len(witnesses))
	}
}

// infoInputClaimable proves an unwitnessed, phase-0 header has not staged
// anything, so this asset's producer input can adopt it without overwriting an
// earlier observation.
func infoInputClaimable(ctx context.Context, tx *sql.Tx, h infoInputHeader) error {
	if h.phase != 0 {
		return fmt.Errorf("%w: unwitnessed input %d is at phase %d", ErrInfoInputBusy, h.changeID, h.phase)
	}
	var manifests int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_info_input_families WHERE change_id=?`, h.changeID).Scan(&manifests); err != nil {
		return err
	}
	if manifests != 0 {
		return fmt.Errorf("%w: unwitnessed input %d already staged %d families", ErrInfoInputBusy, h.changeID, manifests)
	}
	return nil
}

// claimInfoInputHeader adopts an unwitnessed scalar header: it records the
// exact observation at ordinal 0 and folds this input's frame and mask into the
// header. Nothing was staged before the claim, so no accepted body is rewritten.
func claimInfoInputHeader(ctx context.Context, tx *sql.Tx, h infoInputHeader, spec InfoInputSpec) (InfoInput, error) {
	if err := infoInputInsertWitness(ctx, tx, h.changeID, 0, spec.Witnesses[0]); err != nil {
		return InfoInput{}, err
	}
	merged := h.familyMask | spec.FamilyMask
	result, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET fingerprint=?,family_mask=?,fault='',copy_aux=0,input_revision=input_revision+1
 WHERE id=? AND slot=? AND phase=0 AND input_revision=?`, spec.Fingerprint[:], merged, h.changeID, h.slot, h.inputRevision)
	if err != nil {
		return InfoInput{}, infoInputClassifyWrite(err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return InfoInput{}, err
	}
	if updated != 1 {
		return InfoInput{}, fmt.Errorf("%w: input %d advanced while being claimed", ErrInfoInputDrift, h.changeID)
	}
	outcome := InfoInputQueued
	if h.slot == 0 {
		outcome = InfoInputCoalesced
	}
	return begunInfoInputAs(ctx, tx, h.assetID, h.token, h.slot, outcome)
}

// coalesceInfoInputQueued folds newer metadata from the same observation into
// an empty queued body. It never changes the witness.
func coalesceInfoInputQueued(ctx context.Context, tx *sql.Tx, queued infoInputHeader, spec InfoInputSpec) (InfoInput, error) {
	merged := queued.familyMask | spec.FamilyMask
	outcome := InfoInputUnchanged
	if !bytes.Equal(queued.fingerprint, spec.Fingerprint[:]) || merged != queued.familyMask {
		result, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET fingerprint=?,family_mask=?,input_revision=input_revision+1
 WHERE id=? AND slot=1 AND phase=0 AND input_revision=?`, spec.Fingerprint[:], merged, queued.changeID, queued.inputRevision)
		if err != nil {
			return InfoInput{}, infoInputClassifyWrite(err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return InfoInput{}, err
		}
		if updated != 1 {
			return InfoInput{}, fmt.Errorf("%w: successor advanced while coalescing", ErrInfoInputDrift)
		}
		outcome = InfoInputSuperseded
	}
	return begunInfoInputAs(ctx, tx, spec.AssetID, spec.Token, 1, outcome)
}

func insertInfoInputHeader(ctx context.Context, tx *sql.Tx, spec InfoInputSpec, slot int, expected, created int64) error {
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog_info_changes(asset_id,asset_token,slot,phase,expected_epoch,input_revision,family_mask,fingerprint,created_ms)
 VALUES(?,?,?,0,?,1,?,?,?)`, spec.AssetID, spec.Token, slot, expected, spec.FamilyMask, spec.Fingerprint[:], created)
	if err != nil {
		return infoInputClassifyWrite(err)
	}
	changeID, err := result.LastInsertId()
	if err != nil {
		return err
	}
	for ordinal := range spec.Witnesses {
		if err = infoInputInsertWitness(ctx, tx, changeID, ordinal, spec.Witnesses[ordinal]); err != nil {
			return err
		}
	}
	return nil
}

func infoInputInsertWitness(ctx context.Context, tx *sql.Tx, changeID int64, ordinal int, witness InfoWitness) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_info_input_witnesses(change_id,ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,expected_choice_revision,fingerprint)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, changeID, ordinal, witness.Kind, witness.JobID, witness.SourceID,
		witness.SourceGeneration, witness.RootIncarnation, witness.ObjectID, witness.ObjectRevision,
		witness.PolicyRevision, witness.ExpectedChoiceRevision, witness.Fingerprint[:])
	return infoInputClassifyWrite(err)
}

// infoInputStoredWitness is one immutable witness row together with the
// ordinal that names it inside the input.
type infoInputStoredWitness struct {
	ordinal int
	witness InfoWitness
}

func infoInputWitnesses(ctx context.Context, tx *sql.Tx, changeID int64) ([]infoInputStoredWitness, error) {
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,expected_choice_revision,fingerprint
 FROM catalog_info_input_witnesses WHERE change_id=? ORDER BY ordinal`, changeID)
	if err != nil {
		return nil, err
	}
	var witnesses []infoInputStoredWitness
	for rows.Next() {
		var stored infoInputStoredWitness
		var fingerprint []byte
		if err = rows.Scan(&stored.ordinal, &stored.witness.Kind, &stored.witness.JobID, &stored.witness.SourceID,
			&stored.witness.SourceGeneration, &stored.witness.RootIncarnation, &stored.witness.ObjectID,
			&stored.witness.ObjectRevision, &stored.witness.PolicyRevision,
			&stored.witness.ExpectedChoiceRevision, &fingerprint); err != nil {
			break
		}
		if len(fingerprint) != len(stored.witness.Fingerprint) {
			err = fmt.Errorf("%w: witness fingerprint length %d", ErrInfoInvariant, len(fingerprint))
			break
		}
		copy(stored.witness.Fingerprint[:], fingerprint)
		witnesses = append(witnesses, stored)
		if len(witnesses) > infoInputWitnessesPerInput {
			err = fmt.Errorf("%w: more than %d witnesses", ErrInfoInvariant, infoInputWitnessesPerInput)
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return witnesses, err
}

// infoInputWitnessAt loads one immutable witness of the input. Disc families
// are scoped to the exact witness object it names.
func infoInputWitnessAt(ctx context.Context, tx *sql.Tx, changeID int64, ordinal int) (InfoWitness, error) {
	var w InfoWitness
	var fingerprint []byte
	err := tx.QueryRowContext(ctx, `SELECT kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,expected_choice_revision,fingerprint
 FROM catalog_info_input_witnesses WHERE change_id=? AND ordinal=?`, changeID, ordinal).
		Scan(&w.Kind, &w.JobID, &w.SourceID, &w.SourceGeneration, &w.RootIncarnation, &w.ObjectID,
			&w.ObjectRevision, &w.PolicyRevision, &w.ExpectedChoiceRevision, &fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return InfoWitness{}, fmt.Errorf("%w: witness ordinal %d", ErrInfoInvariant, ordinal)
	}
	if err != nil {
		return InfoWitness{}, err
	}
	if len(fingerprint) != len(w.Fingerprint) {
		return InfoWitness{}, fmt.Errorf("%w: witness fingerprint length %d", ErrInfoInvariant, len(fingerprint))
	}
	copy(w.Fingerprint[:], fingerprint)
	return w, nil
}

// infoInputObjectScoped reports whether a family is scoped to one disc
// observation object rather than to the whole asset. Families 10..18 are the
// normalized disc families: their rows carry object_id right after
// input_ordinal, and the family manifest's witness ordinal names that object.
func infoInputObjectScoped(family int) bool {
	return family >= 10
}

// infoInputRequireRole proves the caller's role matches the header mask, so a
// changed family is never silently treated as a parent witness or the reverse.
func infoInputRequireRole(h infoInputHeader, family int, role InfoFamilyRole) error {
	masked := h.familyMask&(int64(1)<<uint(family-1)) != 0
	switch role {
	case InfoFamilyChanged:
		if !masked {
			return fmt.Errorf("%w: family %d is not in the changed mask", ErrInfoInvariant, family)
		}
	case InfoFamilyParent:
		if masked {
			return fmt.Errorf("%w: family %d is changed, not a parent witness", ErrInfoInvariant, family)
		}
	}
	return nil
}

// infoInputHeaderForWrite loads the header and proves the caller's handle still
// describes an open phase-0 body.
func infoInputHeaderForWrite(ctx context.Context, tx *sql.Tx, ref InfoInputRef) (infoInputHeader, error) {
	if ref.ChangeID <= 0 || ref.AssetID <= 0 || ref.Token == "" {
		return infoInputHeader{}, fmt.Errorf("%w: invalid input handle", ErrInfoInvariant)
	}
	h, err := infoInputHeaderByID(ctx, tx, ref.ChangeID)
	if errors.Is(err, sql.ErrNoRows) {
		return infoInputHeader{}, fmt.Errorf("%w: input %d is gone", ErrInfoInputDrift, ref.ChangeID)
	}
	if err != nil {
		return infoInputHeader{}, err
	}
	if h.assetID != ref.AssetID || h.token != ref.Token || h.slot != ref.Slot || h.inputRevision != ref.InputRevision {
		return infoInputHeader{}, fmt.Errorf("%w: handle revision %d/slot %d, header %d/%d", ErrInfoInputDrift,
			ref.InputRevision, ref.Slot, h.inputRevision, h.slot)
	}
	if h.phase != 0 {
		return infoInputHeader{}, fmt.Errorf("%w: input is at phase %d", ErrInfoInputSealed, h.phase)
	}
	return h, nil
}

func infoInputHeaderBySlot(ctx context.Context, tx *sql.Tx, asset int64, token string, slot int) (infoInputHeader, error) {
	return scanInfoInputHeader(tx.QueryRowContext(ctx, `SELECT id,asset_id,asset_token,slot,phase,input_revision,expected_epoch,family_mask,fingerprint,target_available,CASE copy_aux WHEN -1 THEN 'unaccepted-reclaim' WHEN -2 THEN 'old-frozen' ELSE fault END
 FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND slot=?`, asset, token, slot))
}

func infoInputHeaderByID(ctx context.Context, tx *sql.Tx, changeID int64) (infoInputHeader, error) {
	return scanInfoInputHeader(tx.QueryRowContext(ctx, `SELECT id,asset_id,asset_token,slot,phase,input_revision,expected_epoch,family_mask,fingerprint,target_available,CASE copy_aux WHEN -1 THEN 'unaccepted-reclaim' WHEN -2 THEN 'old-frozen' ELSE fault END
 FROM catalog_info_changes WHERE id=?`, changeID))
}

type infoInputScanner interface {
	Scan(dest ...any) error
}

func scanInfoInputHeader(row infoInputScanner) (infoInputHeader, error) {
	var h infoInputHeader
	err := row.Scan(&h.changeID, &h.assetID, &h.token, &h.slot, &h.phase, &h.inputRevision, &h.expectedEpoch, &h.familyMask, &h.fingerprint, &h.targetAvailable, &h.fault)
	return h, err
}

func infoInputManifest(ctx context.Context, tx *sql.Tx, changeID int64, family int) (expected, staged, sealed int64, witness int, err error) {
	err = tx.QueryRowContext(ctx, `SELECT rows_expected,rows_staged,sealed,witness_ordinal FROM catalog_info_input_families WHERE change_id=? AND family=?`, changeID, family).
		Scan(&expected, &staged, &sealed, &witness)
	return expected, staged, sealed, witness, err
}

func infoInputMaxOrdinal(ctx context.Context, tx *sql.Tx, table string, changeID int64) (int64, error) {
	var last int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(input_ordinal),-1) FROM `+table+` WHERE change_id=?`, changeID).Scan(&last)
	return last, err
}

// infoInputReceipt derives the durable state. It computes nothing from memory:
// accepted-pending requires every changed family and every staged parent
// witness family to be sealed at its exact row count, and applied requires the
// accepted asset epoch to have moved. Changed and parent families are counted
// separately because only changed families are materialized.
func infoInputReceipt(ctx context.Context, tx *sql.Tx, h infoInputHeader) (InfoInputReceipt, error) {
	masked := bits.OnesCount64(uint64(h.familyMask))
	var changedManifests, changedSealed, parentManifests, parentSealed int
	var changedExpected, changedStaged, parentExpected, parentStaged int64
	rows, err := tx.QueryContext(ctx, `SELECT family,rows_expected,rows_staged,sealed
 FROM catalog_info_input_families WHERE change_id=? ORDER BY family`, h.changeID)
	if err != nil {
		return InfoInputReceipt{}, err
	}
	for rows.Next() {
		var family int
		var expected, staged, sealed int64
		if err = rows.Scan(&family, &expected, &staged, &sealed); err != nil {
			break
		}
		if family < 1 || family > infoInputFamilies {
			err = fmt.Errorf("%w: manifest family %d", ErrInfoInvariant, family)
			break
		}
		exact := sealed == 1 && staged == expected
		if h.familyMask&(int64(1)<<uint(family-1)) != 0 {
			changedManifests++
			changedExpected += expected
			changedStaged += staged
			if exact {
				changedSealed++
			}
			continue
		}
		parentManifests++
		parentExpected += expected
		parentStaged += staged
		if exact {
			parentSealed++
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return InfoInputReceipt{}, err
	}
	receipt := InfoInputReceipt{
		State:                InfoInputStaging,
		ChangeID:             h.changeID,
		Slot:                 h.slot,
		InputRevision:        h.inputRevision,
		ExpectedEpoch:        h.expectedEpoch,
		FamiliesMasked:       masked,
		FamiliesSealed:       changedSealed,
		FamiliesParent:       parentManifests,
		FamiliesParentSealed: parentSealed,
		RowsExpected:         changedExpected,
		RowsStaged:           changedStaged,
		RowsParentExpected:   parentExpected,
		RowsParentStaged:     parentStaged,
	}
	if h.targetAvailable.Valid {
		target := h.targetAvailable.Int64
		receipt.TargetAvailable = &target
	}
	if h.phase < 1 {
		// A sealed phase-0 body is not accepted: whole-input acceptance must
		// have frozen target_available and moved the header to phase 1.
		return receipt, nil
	}
	// Between acceptance and the first GC step every changed and parent-witness
	// family must still be sealed at its exact count. Once the header is in its
	// GC phase those staging rows are being retired in bounded quanta, so the
	// durable asset epoch below is the proof instead of the row count.
	if h.phase < 5 && (changedManifests != masked || changedSealed != masked || parentSealed != parentManifests) {
		return receipt, nil
	}
	if h.phase < 4 {
		receipt.State = InfoInputAcceptedPending
		return receipt, nil
	}
	var epoch int64
	err = tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, h.assetID, h.token).Scan(&epoch)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Physical deletion discharges the obligation and retires the input.
		receipt.State = InfoInputApplied
	case err != nil:
		return InfoInputReceipt{}, err
	case epoch == h.expectedEpoch+1:
		receipt.State = InfoInputApplied
	default:
		return InfoInputReceipt{}, fmt.Errorf("%w: phase %d asset epoch %d, input expected %d", ErrInfoInvariant, h.phase, epoch, h.expectedEpoch)
	}
	return receipt, nil
}

// infoInputClassifyWrite preserves the ledger/seal meaning of a failed write
// instead of reporting a generic error. The 32 MiB cap is logical; there is no
// larger pool to try.
func infoInputClassifyWrite(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "used_bytes<=cap_bytes"):
		return fmt.Errorf("%w: %v", ErrInfoInputDeferred, err)
	case strings.Contains(text, "info_source_budget_missing"):
		return fmt.Errorf("%w: source ledger missing: %v", ErrInfoInvariant, err)
	case strings.Contains(text, "info_source_input_sealed"):
		return fmt.Errorf("%w: %v", ErrInfoInputSealed, err)
	}
	return err
}

// InfoStageRow is one typed source row of exactly one family. The interface is
// sealed inside this package: callers can only stage the normalized row types
// below, whose fields are the literal DDL columns and never caller SQL text.
type InfoStageRow interface {
	infoStageRow() (family int, values []any)
}

// InfoStreamFactRow is family 1, asset_stream_facts.
type InfoStreamFactRow struct {
	InputOrdinal  int64
	AssetID       string
	Revision      int64
	Size          int64
	ModifiedNS    int64
	Fingerprint   string
	DetailVersion int64
}

func (r InfoStreamFactRow) infoStageRow() (int, []any) {
	return 1, []any{r.InputOrdinal, r.AssetID, r.Revision, r.Size, r.ModifiedNS, r.Fingerprint, r.DetailVersion}
}

// InfoStreamRow is family 2, asset_streams.
type InfoStreamRow struct {
	InputOrdinal   int64
	AssetID        string
	StreamIndex    int64
	Type           string
	Codec          string
	Language       string
	Title          string
	Channels       int64
	ChannelLayout  string
	IsDefault      int64
	IsForced       int64
	ColorTransfer  string
	ColorPrimaries string
	DetailJSON     string
}

func (r InfoStreamRow) infoStageRow() (int, []any) {
	return 2, []any{r.InputOrdinal, r.AssetID, r.StreamIndex, r.Type, r.Codec, r.Language, r.Title,
		r.Channels, r.ChannelLayout, r.IsDefault, r.IsForced, r.ColorTransfer, r.ColorPrimaries, r.DetailJSON}
}

// InfoStreamBreadthRow is family 3, asset_stream_breadth.
type InfoStreamBreadthRow struct {
	InputOrdinal        int64
	AssetID             string
	StreamIndex         int64
	AudioDescription    int64
	AudioVariant        string
	ChromaSubsampling   string
	FPSNumerator        int64
	FPSDenominator      int64
	FrameRateMode       string
	InterlaceAnalysis   string
	Telecine            int64
	DOVIRPU             int64
	DOVIBaseLayer       int64
	DOVIMetadataKnown   int64
	DOVIEnhancementType string
	DOVICMVersion       string
}

func (r InfoStreamBreadthRow) infoStageRow() (int, []any) {
	return 3, []any{r.InputOrdinal, r.AssetID, r.StreamIndex, r.AudioDescription, r.AudioVariant,
		r.ChromaSubsampling, r.FPSNumerator, r.FPSDenominator, r.FrameRateMode, r.InterlaceAnalysis,
		r.Telecine, r.DOVIRPU, r.DOVIBaseLayer, r.DOVIMetadataKnown, r.DOVIEnhancementType, r.DOVICMVersion}
}

// InfoChapterFactRow is family 4, asset_chapter_facts.
type InfoChapterFactRow struct {
	InputOrdinal int64
	AssetID      string
	Revision     int64
	Size         int64
	ModifiedNS   int64
	Status       string
	Fingerprint  string
}

func (r InfoChapterFactRow) infoStageRow() (int, []any) {
	return 4, []any{r.InputOrdinal, r.AssetID, r.Revision, r.Size, r.ModifiedNS, r.Status, r.Fingerprint}
}

// InfoChapterRow is family 5, asset_chapters.
type InfoChapterRow struct {
	InputOrdinal int64
	AssetID      string
	ChapterIndex int64
	Title        string
	StartSeconds float64
	EndSeconds   float64
}

func (r InfoChapterRow) infoStageRow() (int, []any) {
	return 5, []any{r.InputOrdinal, r.AssetID, r.ChapterIndex, r.Title, r.StartSeconds, r.EndSeconds}
}

// InfoSubtitleFactRow is family 6, asset_subtitle_facts.
type InfoSubtitleFactRow struct {
	InputOrdinal int64
	AssetID      string
	Revision     int64
	Size         int64
	ModifiedNS   int64
	OriginUS     int64
	TimingKnown  int64
	Status       string
	Fingerprint  string
}

func (r InfoSubtitleFactRow) infoStageRow() (int, []any) {
	return 6, []any{r.InputOrdinal, r.AssetID, r.Revision, r.Size, r.ModifiedNS, r.OriginUS, r.TimingKnown, r.Status, r.Fingerprint}
}

// InfoSubtitleSidecarRow is family 7, subtitle_sidecars.
type InfoSubtitleSidecarRow struct {
	InputOrdinal int64
	ID           string
	Path         string
	Size         int64
	ModifiedNS   int64
	Digest       string
}

func (r InfoSubtitleSidecarRow) infoStageRow() (int, []any) {
	return 7, []any{r.InputOrdinal, r.ID, r.Path, r.Size, r.ModifiedNS, r.Digest}
}

// InfoSubtitleRow is family 8, asset_subtitles. SidecarID is nil for an
// embedded track and non-nil for a sidecar track, exactly as the DDL requires.
type InfoSubtitleRow struct {
	InputOrdinal    int64
	ID              string
	AssetID         string
	Origin          string
	Locator         string
	StreamIndex     int64
	SidecarID       *string
	Format          string
	Language        string
	Title           string
	IsDefault       int64
	IsForced        int64
	Reason          string
	HearingImpaired int64
}

func (r InfoSubtitleRow) infoStageRow() (int, []any) {
	var sidecar any
	if r.SidecarID != nil {
		sidecar = *r.SidecarID
	}
	return 8, []any{r.InputOrdinal, r.ID, r.AssetID, r.Origin, r.Locator, r.StreamIndex, sidecar,
		r.Format, r.Language, r.Title, r.IsDefault, r.IsForced, r.Reason, r.HearingImpaired}
}

// InfoSubtitleEvidenceRow is family 9, subtitle_inventory_evidence.
type InfoSubtitleEvidenceRow struct {
	InputOrdinal int64
	AssetID      string
	Revision     int64
	Evidence     string
}

func (r InfoSubtitleEvidenceRow) infoStageRow() (int, []any) {
	return 9, []any{r.InputOrdinal, r.AssetID, r.Revision, r.Evidence}
}

// InfoDiscFactRow is family 10, inventory_disc_facts.
type InfoDiscFactRow struct {
	InputOrdinal         int64
	ObjectID             string
	AssetID              string
	SourceRevision       string
	SourceGeneration     int64
	RootIncarnation      string
	Kind                 string
	Packaging            string
	Encryption           string
	Fingerprint          string
	OwnerTitleKey        string
	ChoiceRevision       int64
	MediaTitleKey        string
	MediaStreamRevision  int64
	MediaChapterRevision int64
}

func (r InfoDiscFactRow) infoStageRow() (int, []any) {
	return 10, []any{r.InputOrdinal, r.ObjectID, r.AssetID, r.SourceRevision, r.SourceGeneration,
		r.RootIncarnation, r.Kind, r.Packaging, r.Encryption, r.Fingerprint, r.OwnerTitleKey,
		r.ChoiceRevision, r.MediaTitleKey, r.MediaStreamRevision, r.MediaChapterRevision}
}

// InfoDiscTitleRow is family 11, inventory_disc_titles. Playlist is nullable.
type InfoDiscTitleRow struct {
	InputOrdinal   int64
	ObjectID       string
	TitleKey       string
	SelectionToken string
	TitleNumber    int64
	DiscOrder      int64
	DurationMS     int64
	AngleCount     int64
	Playlist       *int64
	DVDTitle       int64
	VTS            int64
	PGC            int64
}

func (r InfoDiscTitleRow) infoStageRow() (int, []any) {
	var playlist any
	if r.Playlist != nil {
		playlist = *r.Playlist
	}
	return 11, []any{r.InputOrdinal, r.ObjectID, r.TitleKey, r.SelectionToken, r.TitleNumber,
		r.DiscOrder, r.DurationMS, r.AngleCount, playlist, r.DVDTitle, r.VTS, r.PGC}
}

// InfoDiscClipRow is family 12, inventory_disc_clips.
type InfoDiscClipRow struct {
	InputOrdinal int64
	ObjectID     string
	TitleKey     string
	Sequence     int64
	File         string
	InTime90k    int64
	OutTime90k   int64
}

func (r InfoDiscClipRow) infoStageRow() (int, []any) {
	return 12, []any{r.InputOrdinal, r.ObjectID, r.TitleKey, r.Sequence, r.File, r.InTime90k, r.OutTime90k}
}

// InfoDiscStreamRow is family 13, inventory_disc_streams. DVBasePID is nullable.
type InfoDiscStreamRow struct {
	InputOrdinal       int64
	ObjectID           string
	TitleKey           string
	StreamOrder        int64
	ClipSequence       int64
	PID                int64
	Kind               string
	Coding             int64
	Language           string
	DVEnhancement      int64
	DVEnhancementKnown int64
	DVBasePID          *int64
	VideoRole          string
}

func (r InfoDiscStreamRow) infoStageRow() (int, []any) {
	var base any
	if r.DVBasePID != nil {
		base = *r.DVBasePID
	}
	return 13, []any{r.InputOrdinal, r.ObjectID, r.TitleKey, r.StreamOrder, r.ClipSequence, r.PID,
		r.Kind, r.Coding, r.Language, r.DVEnhancement, r.DVEnhancementKnown, base, r.VideoRole}
}

// InfoDiscChapterRow is family 14, inventory_disc_chapters.
type InfoDiscChapterRow struct {
	InputOrdinal int64
	ObjectID     string
	TitleKey     string
	ChapterOrder int64
	StartMS      int64
}

func (r InfoDiscChapterRow) infoStageRow() (int, []any) {
	return 14, []any{r.InputOrdinal, r.ObjectID, r.TitleKey, r.ChapterOrder, r.StartMS}
}

// InfoDiscMediaMemberRow is family 15, inventory_disc_media_members.
type InfoDiscMediaMemberRow struct {
	InputOrdinal   int64
	ObjectID       string
	Path           string
	ObjectIdentity string
	Size           int64
	ModifiedNS     int64
	Required       int64
	Directory      int64
}

func (r InfoDiscMediaMemberRow) infoStageRow() (int, []any) {
	return 15, []any{r.InputOrdinal, r.ObjectID, r.Path, r.ObjectIdentity, r.Size, r.ModifiedNS, r.Required, r.Directory}
}

// InfoDiscTitleMediaRow is family 16, inventory_disc_title_media.
type InfoDiscTitleMediaRow struct {
	InputOrdinal   int64
	ObjectID       string
	TitleKey       string
	SourceRevision string
	Fingerprint    string
	Revision       string
	Container      string
	VideoCodec     string
	AudioCodec     string
	Width          int64
	Height         int64
	DurationMS     int64
	OriginUS       int64
	TimingKnown    int64
}

func (r InfoDiscTitleMediaRow) infoStageRow() (int, []any) {
	return 16, []any{r.InputOrdinal, r.ObjectID, r.TitleKey, r.SourceRevision, r.Fingerprint,
		r.Revision, r.Container, r.VideoCodec, r.AudioCodec, r.Width, r.Height, r.DurationMS,
		r.OriginUS, r.TimingKnown}
}

// InfoDiscTitleMediaStreamRow is family 17, inventory_disc_title_media_streams.
type InfoDiscTitleMediaStreamRow struct {
	InputOrdinal   int64
	ObjectID       string
	TitleKey       string
	StreamIndex    int64
	Type           string
	Codec          string
	Language       string
	Title          string
	Channels       int64
	ChannelLayout  string
	IsDefault      int64
	IsForced       int64
	ColorTransfer  string
	ColorPrimaries string
	DetailJSON     string
	BreadthJSON    string
}

func (r InfoDiscTitleMediaStreamRow) infoStageRow() (int, []any) {
	return 17, []any{r.InputOrdinal, r.ObjectID, r.TitleKey, r.StreamIndex, r.Type, r.Codec,
		r.Language, r.Title, r.Channels, r.ChannelLayout, r.IsDefault, r.IsForced,
		r.ColorTransfer, r.ColorPrimaries, r.DetailJSON, r.BreadthJSON}
}

// InfoDiscTitleMediaMemberRow is family 18, inventory_disc_title_media_members.
type InfoDiscTitleMediaMemberRow struct {
	InputOrdinal   int64
	ObjectID       string
	TitleKey       string
	Path           string
	ObjectIdentity string
	Size           int64
	ModifiedNS     int64
	Required       int64
	Directory      int64
}

func (r InfoDiscTitleMediaMemberRow) infoStageRow() (int, []any) {
	return 18, []any{r.InputOrdinal, r.ObjectID, r.TitleKey, r.Path, r.ObjectIdentity, r.Size,
		r.ModifiedNS, r.Required, r.Directory}
}

// infoStageTable is one trusted descriptor from the literal staging DDL. No
// caller string reaches SQL: table and columns are fixed source schema, indexed
// by the exact family number of the CREATE TABLE order. assetAt is the index in
// the row values of an asset token column, or -1 when the family is not
// asset-scoped.
type infoStageTable struct {
	table   string
	columns string
	assetAt int
}

var infoStageTables = [infoInputFamilies + 1]infoStageTable{
	1:  {table: "catalog_info_new_stream_facts", assetAt: 1, columns: "input_ordinal,asset_id,revision,size,modified_ns,fingerprint,detail_version"},
	2:  {table: "catalog_info_new_streams", assetAt: 1, columns: "input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json"},
	3:  {table: "catalog_info_new_stream_breadth", assetAt: 1, columns: "input_ordinal,asset_id,stream_index,audio_description,audio_variant,chroma_subsampling,fps_numerator,fps_denominator,frame_rate_mode,interlace_analysis,telecine,dovi_rpu,dovi_base_layer,dovi_metadata_known,dovi_enhancement_type,dovi_cm_version"},
	4:  {table: "catalog_info_new_chapter_facts", assetAt: 1, columns: "input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint"},
	5:  {table: "catalog_info_new_chapters", assetAt: 1, columns: "input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds"},
	6:  {table: "catalog_info_new_subtitle_facts", assetAt: 1, columns: "input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint"},
	7:  {table: "catalog_info_new_subtitle_sidecars", assetAt: -1, columns: "input_ordinal,id,path,size,modified_ns,digest"},
	8:  {table: "catalog_info_new_subtitles", assetAt: 2, columns: "input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired"},
	9:  {table: "catalog_info_new_subtitle_inventory_evidence", assetAt: 1, columns: "input_ordinal,asset_id,revision,evidence"},
	10: {table: "catalog_info_new_disc_facts", assetAt: 2, columns: "input_ordinal,object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision,media_title_key,media_stream_revision,media_chapter_revision"},
	11: {table: "catalog_info_new_disc_titles", assetAt: -1, columns: "input_ordinal,object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist,dvd_title,vts,pgc"},
	12: {table: "catalog_info_new_disc_clips", assetAt: -1, columns: "input_ordinal,object_id,title_key,sequence,file,in_time_90k,out_time_90k"},
	13: {table: "catalog_info_new_disc_streams", assetAt: -1, columns: "input_ordinal,object_id,title_key,stream_order,clip_sequence,pid,kind,coding,language,dv_enhancement,dv_enhancement_known,dv_base_pid,video_role"},
	14: {table: "catalog_info_new_disc_chapters", assetAt: -1, columns: "input_ordinal,object_id,title_key,chapter_order,start_ms"},
	15: {table: "catalog_info_new_disc_media_members", assetAt: -1, columns: "input_ordinal,object_id,path,object_identity,size,modified_ns,required,directory"},
	16: {table: "catalog_info_new_disc_title_media", assetAt: -1, columns: "input_ordinal,object_id,title_key,source_revision,fingerprint,revision,container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known"},
	17: {table: "catalog_info_new_disc_title_media_streams", assetAt: -1, columns: "input_ordinal,object_id,title_key,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json,breadth_json"},
	18: {table: "catalog_info_new_disc_title_media_members", assetAt: -1, columns: "input_ordinal,object_id,title_key,path,object_identity,size,modified_ns,required,directory"},
}

func infoStageInsertSQL(table infoStageTable, rows int) string {
	columns := strings.Count(table.columns, ",") + 2 // change_id plus the source columns
	group := "(" + strings.TrimSuffix(strings.Repeat("?,", columns), ",") + ")"
	groups := make([]string, rows)
	for i := range groups {
		groups[i] = group
	}
	return "INSERT INTO " + table.table + "(change_id," + table.columns + ") VALUES " + strings.Join(groups, ",")
}

package writer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

const uoReadingChildMaxBytes int64 = 51 + 16 + 512 + 64 + 4 + 4096 + 1024 + 128
const uoReadingReserveBytes int64 = usermutation.ReceiptChargeBytes + uoReadingChildMaxBytes

// Existing public p251 identity and typed F child, NOT a lifetime-root receipt
// or a new wire envelope. MP owns canonical request digest, source/permission
// proof, reading_revision CAS, event and final command dispatch. Only 770/771,
// ordinary budget (never caller-selected reserved-stop quota), local interactive
// proof, and one original per frame are admitted by this private first instance.
type uoPublicReadingRequest struct {
	kind      CommandID
	key, root string
	request   RequestDigest
}
type uoPublicReadingResult struct {
	publicID                                          [16]byte
	asset, page                                       sql.NullInt64
	sourceToken, sourceRevision, format, cfi, section sql.NullString
	progression                                       sql.NullFloat64
	readingRevision, personalRevision, observedMS     int64
	accepted, stale, cleared                          bool
	reason                                            string
}
type uoPublicReadingOriginal struct {
	receipt usermutation.Receipt
	reading uoPublicReadingResult
}
type uoPublicReadingCheck struct {
	reservation *uoPublicReadingReservation
	original    *uoPublicReadingOriginal
}
type uoPublicReadingReservation struct {
	turn                                    *commandTurn
	authority                               *usCurrentSessionAuthorization
	request                                 uoPublicReadingRequest
	scope, issued, globalCount, globalBytes int64
	done, replay                            bool
}

func (uoPublicReadingReservation) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "uoPublicReadingReservation")
}
func (uoPublicReadingRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "uoPublicReadingRequest")
}

func (q uoPublicReadingRequest) operation() (string, error) {
	switch q.kind {
	case 770:
		return "mp_reading_intent", nil
	case 771:
		return "mp_reading_clear", nil
	default:
		return "", ErrInvalid
	}
}
func (q uoPublicReadingRequest) validate(now int64) (int64, error) {
	if _, err := q.operation(); err != nil {
		return 0, err
	}
	if q.request == (RequestDigest{}) || q.root != "" && !usermutation.ValidRootID(q.root) {
		return 0, ErrInvalid
	}
	return usermutation.IssuedMs(q.key, now)
}

func uoPublicReadingPoison(turn *commandTurn, err error) {
	if err != nil && turn != nil && turn.transaction != nil && !turn.transaction.done && turn.transaction.cancel != nil {
		turn.transaction.cancel()
	}
}

// Root state is a comparison against the independently CURRENT proof identity.
// A root is never synthesized here. Rootless public domain receipts stay
// rootless, with empty installation binding, as store_lifetime.go specifies.
func checkUOPublicReadingRoot(ctx context.Context, turn *commandTurn, a *usCurrentSessionAuthorization, q uoPublicReadingRequest, fresh bool) error {
	if err := a.recheck(ctx, turn); err != nil {
		return err
	}
	if q.root == "" {
		return nil
	}
	i := a.proof.witness.issuer
	row, err := wc008DraftQuery(ctx, turn, uoPublicReadingRoot, q.root, i.account, i.profile, i.memberIncarnation, i.profileLifetime, i.family, i.installation)
	if err != nil {
		return err
	}
	var state string
	if err = row.Scan(&state); err != nil {
		return errors.Join(usermutation.ErrRootGone, err)
	}
	if state != "active" && state != "draining" {
		return usermutation.ErrRootGone
	}
	if fresh && state != "active" {
		return ErrConflict
	}

	return nil
}

// Check classifies the ORIGINAL before reserving or applying an MP effect.
// A fresh reservation and all source/result/ledger effects share this exact
// frame. The physical terminal refuses an unsaved reservation even if its caller
// ignores an error. Historical replay returns the immutable saved typed child.
func checkUOPublicReadingReceipt(ctx context.Context, turn *commandTurn, a *usCurrentSessionAuthorization, q uoPublicReadingRequest) (out uoPublicReadingCheck, err error) {
	defer func() { uoPublicReadingPoison(turn, err) }()
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || a == nil || a.turn != turn || turn.transaction.publicReading != nil || q.kind != turn.transaction.descriptor.ID {
		return out, ErrInvalid
	}
	now := turn.transaction.writer.now().UnixMilli()
	issued, err := q.validate(now)
	if err != nil {
		return out, err
	}
	if err = checkUOPublicReadingRoot(ctx, turn, a, q, false); err != nil {
		return out, err
	}
	i := a.proof.witness.issuer
	var scope, accountCut, accountCount int64
	row, err := wc008DraftQuery(ctx, turn, uoPublicReadingAccount, i.account, i.account)
	if err != nil {
		return out, err
	}
	err = row.Scan(&scope, &accountCut, &accountCount)
	accountAbsent := errors.Is(err, sql.ErrNoRows)
	if err != nil && !accountAbsent {
		return out, err
	}
	var globalCut, globalCount, globalBytes int64
	row, err = wc008DraftQuery(ctx, turn, uoPublicReadingGlobal)
	if err != nil {
		return out, err
	}
	err = row.Scan(&globalCut, &globalCount, &globalBytes)
	globalAbsent := errors.Is(err, sql.ErrNoRows)
	if err != nil && !globalAbsent {
		return out, err
	}
	if globalAbsent {
		if !accountAbsent {
			return out, ErrConflict
		}
		globalBytes = usermutation.BaseChargeBytes
	}
	cut := max(globalCut, accountCut, now-usermutation.RetentionMs)
	if issued < cut {
		return out, usermutation.ErrExpired
	}
	operation, _ := q.operation()
	digest := hex.EncodeToString(q.request[:])
	installation := ""
	if q.root != "" {
		installation = i.installation
	}
	row, err = wc008DraftQuery(ctx, turn, uoPublicReadingHeader, q.key)
	if err != nil {
		return out, err
	}
	var storedScope, storedIssued, storedStop int64
	var storedProfile, storedFamily, storedLifetime, storedInstallation, storedRoot, storedOperation, storedDigest string
	var original uoPublicReadingOriginal
	err = row.Scan(&storedScope, &storedProfile, &storedFamily, &storedLifetime, &storedInstallation, &storedRoot, &storedOperation, &storedDigest, &storedIssued, &storedStop, &original.receipt.Status)
	headerAbsent := errors.Is(err, sql.ErrNoRows)
	if err != nil && !headerAbsent {
		return out, err
	}
	r := &uoPublicReadingReservation{turn: turn, authority: a, request: q, scope: scope, issued: issued}
	if !headerAbsent {
		if storedScope != scope || storedProfile != i.profile || storedFamily != i.family || storedLifetime != i.profileLifetime || storedInstallation != installation || storedRoot != q.root || storedOperation != operation || storedDigest != digest || storedIssued != issued || storedStop != 0 || original.receipt.Status != "applied" {
			return out, ErrConflict
		}
		row, err = wc008DraftQuery(ctx, turn, uoPublicReadingHeaderOriginal, q.key, scope, digest)
		if err != nil {
			return out, err
		}
		if err = row.Scan(&original.receipt.Status, &original.receipt.TargetID, &original.receipt.Revision, &original.receipt.Generation, &original.receipt.PositionMs, &original.receipt.PositionStatus, &original.receipt.CommittedMs); err != nil {
			return out, err
		}
		if err = readUOPublicReadingResult(ctx, turn, q, &original.reading); err != nil {
			return out, err
		}
		if original.receipt.Revision != original.reading.personalRevision || original.receipt.Generation != 0 || original.receipt.PositionMs != 0 || original.receipt.PositionStatus != "" {
			return out, ErrConflict
		}
		original.receipt.Key = q.key
		original.receipt.Operation = operation
		original.receipt.ExpiresMs = issued + usermutation.RetentionMs
		if err = checkUOPublicReadingRoot(ctx, turn, a, q, false); err != nil {
			return out, err
		}
		r.done = true
		r.replay = true
		turn.transaction.publicReading = r
		return uoPublicReadingCheck{original: &original}, nil
	}
	if err = checkUOPublicReadingRoot(ctx, turn, a, q, true); err != nil {
		return out, err
	}
	// Check quota BEFORE allocating the account or reservation. Retained headers
	// keep the existing conservative fixed charge; typed child variable octets
	// are separately reserved at their legal maximum, then reconciled on Save.
	delta := uoReadingReserveBytes
	if accountAbsent {
		delta += usermutation.AccountChargeBytes
	}
	if accountCount >= 8192 || globalCount >= 65536 || globalBytes > usermutation.OrdinaryByteLimit-delta {
		return out, usermutation.ErrCapacity
	}
	if globalAbsent {
		if _, err = wc008DraftInteger(ctx, turn, uoPublicReadingGlobalBirth, int64(1)); err != nil {
			return out, err
		}
	}
	if accountAbsent {
		scope, err = wc008DraftInteger(ctx, turn, uoPublicReadingAccountBirth, "local", i.account, i.account, int64(0))
		if err != nil {
			return out, err
		}
		r.scope = scope
	}
	if _, err = wc008DraftInteger(ctx, turn, uoPublicReadingAccountReserve, scope, accountCount); err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, turn, uoPublicReadingGlobalReserve, globalCount+1, globalBytes+delta, max(globalCut, now-usermutation.RetentionMs), globalCount, globalBytes, globalCut); err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, turn, uoPublicReadingHeaderBirth, q.key, scope, i.profile, i.family, i.profileLifetime, installation, q.root, operation, digest, issued, int64(0), "reserved"); err != nil {
		return out, err
	}
	r.globalCount = globalCount + 1
	r.globalBytes = globalBytes + delta
	turn.transaction.publicReading = r
	if err = checkUOPublicReadingRoot(ctx, turn, a, q, true); err != nil {
		return out, err
	}
	return uoPublicReadingCheck{reservation: r}, nil
}

func saveUOPublicReadingReceipt(ctx context.Context, turn *commandTurn, r *uoPublicReadingReservation, h usermutation.Result, v uoPublicReadingResult) (err error) {
	defer func() {
		_, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err)
		uoPublicReadingPoison(turn, err)
	}()
	if turn == nil || turn.transaction == nil || r == nil || r.turn != turn || r.done || turn.transaction.publicReading != r {
		return ErrInvalid
	}
	if err = checkUOPublicReadingRoot(ctx, turn, r.authority, r.request, true); err != nil {
		return err
	}
	if err = checkUOPublicReadingWindow(ctx, turn, r); err != nil {
		return err
	}
	if !validUOPublicReadingResult(v) || h.Status != "applied" || len(h.TargetID) > 128 || strings.IndexByte(h.TargetID, 0) >= 0 || !utf8.ValidString(h.TargetID) || h.Revision != v.personalRevision || h.Generation != 0 || h.PositionMs != 0 || h.PositionStatus != "" {
		return ErrInvalid
	}
	if r.request.kind == 771 && !v.cleared || r.request.kind == 770 && v.cleared {
		return ErrInvalid
	}
	size, err := wc008DraftInteger(ctx, turn, uoPublicReadingResultBirth, r.request.key, int64(r.request.kind), int64(1), v.publicID[:], uoReadingNullInt(v.asset), uoReadingNullText(v.sourceToken), uoReadingNullText(v.sourceRevision), uoReadingNullText(v.format), uoReadingNullText(v.cfi), uoReadingNullText(v.section), uoReadingNullInt(v.page), uoReadingNullReal(v.progression), v.readingRevision, v.personalRevision, v.observedMS, uoReadingBool(v.accepted), uoReadingBool(v.stale), uoReadingBool(v.cleared), v.reason)
	if err != nil {
		return err
	}
	if size < 0 || size > uoReadingChildMaxBytes {
		return ErrConflict
	}
	if _, err = wc008DraftInteger(ctx, turn, uoPublicReadingGlobalReconcile, r.globalBytes-(uoReadingChildMaxBytes-size), r.globalCount, r.globalBytes); err != nil {
		return err
	}
	now := turn.transaction.writer.now().UnixMilli()
	issuer := r.authority.proof.witness.issuer
	installation := ""
	if r.request.root != "" {
		installation = issuer.installation
	}
	operation, _ := r.request.operation()
	if _, err = wc008DraftInteger(ctx, turn, uoPublicReadingHeaderSave, h.Status, h.TargetID, h.Revision, int64(h.Generation), h.PositionMs, h.PositionStatus, now, r.request.key, r.scope, hex.EncodeToString(r.request.request[:]), operation, issuer.profile, issuer.family, issuer.profileLifetime, r.request.root, installation); err != nil {
		return err
	}
	if err = checkUOPublicReadingRoot(ctx, turn, r.authority, r.request, true); err != nil {
		return err
	}
	r.done = true
	return nil
}

func validUOPublicReadingResult(v uoPublicReadingResult) bool {
	if v.publicID == [16]byte{} || v.readingRevision < 0 || v.readingRevision > 9007199254740991 || v.personalRevision < 0 || v.personalRevision > 9007199254740991 || v.observedMS < 0 || v.observedMS > 9999999999999 {
		return false
	}
	for _, x := range []struct {
		s string
		n int
	}{{v.reason, 128}, {v.sourceToken.String, 512}, {v.sourceRevision.String, 64}, {v.format.String, 4}, {v.cfi.String, 4096}, {v.section.String, 1024}} {
		if len(x.s) > x.n || strings.IndexByte(x.s, 0) >= 0 || !utf8.ValidString(x.s) {
			return false
		}
	}
	if !v.format.Valid {
		return v.asset == (sql.NullInt64{}) && v.page == (sql.NullInt64{}) && v.sourceToken == (sql.NullString{}) && v.sourceRevision == (sql.NullString{}) && v.format == (sql.NullString{}) && v.cfi == (sql.NullString{}) && v.section == (sql.NullString{}) && v.progression == (sql.NullFloat64{})
	}
	if !v.asset.Valid || v.asset.Int64 <= 0 || !v.sourceToken.Valid || v.sourceToken.String == "" || !v.sourceRevision.Valid || !usHex(v.sourceRevision.String, 64) || !v.progression.Valid || math.IsNaN(v.progression.Float64) || v.progression.Float64 < 0 || v.progression.Float64 > 1 {
		return false
	}
	if v.format.String == "epub" {
		return v.cfi.Valid && v.cfi.String != "" && v.page == (sql.NullInt64{}) && (!v.section.Valid && v.section.String == "" || v.section.Valid)
	}
	switch v.format.String {
	case "pdf", "cbz", "cb7", "cbr":
		return v.cfi == (sql.NullString{}) && v.section == (sql.NullString{}) && v.page.Valid && v.page.Int64 >= 0 && v.page.Int64 <= 1000000
	}
	return false
}

func readUOPublicReadingResult(ctx context.Context, turn *commandTurn, q uoPublicReadingRequest, out *uoPublicReadingResult) error {
	size, err := wc008DraftInteger(ctx, turn, uoPublicReadingResultBytes, q.key, int64(q.kind))
	if err != nil {
		return err
	}
	if size < 0 || size > uoReadingChildMaxBytes {
		return ErrConflict
	}
	row, err := wc008DraftQuery(ctx, turn, uoPublicReadingResultRead, q.key, int64(q.kind), size)
	if err != nil {
		return err
	}
	var public []byte
	err = row.Scan(&public, &out.asset, &out.sourceToken, &out.sourceRevision, &out.format, &out.cfi, &out.section, &out.page, &out.progression, &out.readingRevision, &out.personalRevision, &out.observedMS, &out.accepted, &out.stale, &out.cleared, &out.reason)
	if err != nil {
		return err
	}
	if len(public) != 16 {
		return ErrConflict
	}
	copy(out.publicID[:], public)
	if !validUOPublicReadingResult(*out) {
		return ErrConflict
	}
	return nil
}

// One closed terminal participant, not a callback/issuer registry. Never let an
// ignored Check/Save error or unsaved reservation publish a partial MP prefix.
func (t *transaction) checkUOPublicReadingCommit() error {
	r := t.publicReading
	if r == nil {
		return nil
	}
	if !r.done || r.turn == nil || r.turn.transaction != t {
		return ErrConflict
	}
	if err := checkUOPublicReadingRoot(t.ctx, r.turn, r.authority, r.request, !r.replay); err != nil {
		return err
	}
	return checkUOPublicReadingWindow(t.ctx, r.turn, r)
}

func uoReadingNullInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
func uoReadingNullText(v sql.NullString) any {
	if v.Valid {
		return v.String
	}
	return nil
}
func uoReadingNullReal(v sql.NullFloat64) any {
	if v.Valid {
		return v.Float64
	}
	return nil
}
func uoReadingBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func checkUOPublicReadingWindow(ctx context.Context, turn *commandTurn, r *uoPublicReadingReservation) error {
	now := turn.transaction.writer.now().UnixMilli()
	issued, err := r.request.validate(now)
	if err != nil {
		return err
	}
	if issued != r.issued {
		return ErrConflict
	}
	issuer := r.authority.proof.witness.issuer
	row, err := wc008DraftQuery(ctx, turn, uoPublicReadingAccount, issuer.account, issuer.account)
	if err != nil {
		return err
	}
	var scope, cut, count int64
	if err = row.Scan(&scope, &cut, &count); err != nil {
		return err
	}
	if scope != r.scope {
		return ErrConflict
	}
	row, err = wc008DraftQuery(ctx, turn, uoPublicReadingGlobal)
	if err != nil {
		return err
	}
	var globalCut, globalCount, bytes int64
	if err = row.Scan(&globalCut, &globalCount, &bytes); err != nil {
		return err
	}
	if issued < max(cut, globalCut, now-usermutation.RetentionMs) {
		return usermutation.ErrExpired
	}
	return turn.checkLifetimes(ctx)
}
func (uoPublicReadingResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "uoPublicReadingResult")
}
func (uoPublicReadingOriginal) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "uoPublicReadingOriginal")
}
func (uoPublicReadingCheck) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "uoPublicReadingCheck")
}

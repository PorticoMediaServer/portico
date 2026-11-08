package writer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// One shared notice publication under the exact authorized inbox. The
// dispatcher supplies current producer/scope authority BEFORE receipt
// disclosure and calls this only for a fresh command; it owns the saved
// receipt, quota, original replay classification and physical terminal.
type uoNoticePublish struct {
	view                                                uoNoticeView
	id, severity, source, category, title, body, dedupe string
	arguments                                           map[string]string
	actions                                             []notify.Action
	nowMS, expiresMS, authorityRevision                 int64
	// Zero creates. A positive pair restates exactly this current record.
	expectedRevision, expectedAuthorityRevision int64
	operation                                   uoNoticeOperation
}

// Withdraw is scoped to the same authorized inbox. expectedRevision and
// authorityRevision are the caller's exact current witness; a mismatch is a
// conflict, never a guessed target.
type uoNoticeWithdraw struct {
	view                                       uoNoticeView
	id                                         string
	expectedRevision, authorityRevision, nowMS int64
}

// The complete original notice result. The dispatcher persists exactly these
// fields with its receipt; replay serves them and never re-enters the native
// effects, so no replay allocates a revision, writes an inbox head or appends
// a second hint.
type uoNoticeEffect struct {
	id                                    string
	sequence, revision, authorityRevision int64
	inboxRevision                         int64
	created, withdrawn, changed           bool
	event                                 wcAPIEventEffect
}

// ErrNoticeRootUnavailable is the named seam to UO-01's private lifetime-root
// receipt projection (and UO-03's dispatcher). This build cannot mint a root
// receipt, payload commitment or Applied result from a native effect; a caller
// that needs the root-bound result must refuse rather than fabricate custody.
// Remove this refusal only with the actual merged root projection.
var ErrNoticeRootUnavailable = errors.New("writer: notice root receipt projection unavailable")

// uoNoticeRootBinding is the closed root-bound result shape the dispatcher
// needs. It is inert data, not a capability, and cannot be constructed here.
type uoNoticeRootBinding struct {
	rootRevision int64
	commitment   [32]byte
}

// bindUONoticeRoot is intentionally a refusal in this build: UO-01/UO-03 are
// separate tasks and their files are not available to this one.
func bindUONoticeRoot(uoNoticeEffect) (uoNoticeRootBinding, error) {
	return uoNoticeRootBinding{}, ErrNoticeRootUnavailable
}

func (q uoNoticePublish) validate() error {
	if !q.view.valid() || !uoNoticeIdentifier(q.id, uoNoticeMaxIDBytes) || !uoNoticeIdentifier(q.source, uoNoticeMaxSourceBytes) ||
		!uoNoticeIdentifier(q.category, uoNoticeMaxCategoryBytes) || !uoNoticeIdentifier(q.dedupe, uoNoticeMaxDedupeBytes) ||
		!uoNoticeText(q.title, uoNoticeMaxTitleBytes, true) || !uoNoticeText(q.body, uoNoticeMaxBodyBytes, false) ||
		q.nowMS <= 0 || q.expiresMS <= q.nowMS || q.authorityRevision <= 0 || len(q.arguments) > uoNoticeMaxArguments {
		return ErrInvalid
	}
	if q.severity != notify.SeverityInfo && q.severity != notify.SeverityWarning && q.severity != notify.SeverityCritical {
		return ErrInvalid
	}
	if (q.expectedRevision == 0) != (q.expectedAuthorityRevision == 0) || q.expectedRevision < 0 || q.expectedAuthorityRevision < 0 {
		return ErrInvalid
	}
	for k, v := range q.arguments {
		if !uoNoticeIdentifier(k, 48) || !uoNoticeText(v, uoNoticeMaxArgumentBytes, true) {
			return ErrInvalid
		}
	}
	if err := notify.ValidateActions(q.actions); err != nil {
		return ErrInvalid
	}
	if q.operation != (uoNoticeOperation{}) && !q.operation.valid(q.nowMS) {
		return ErrInvalid
	}
	return nil
}

// An exact producer retry of the same source fact is not a new hint. Comparing
// the canonical payload bytes, expiry and accepted authority revision keeps a
// retry a no-op while any real change becomes a restatement.
func (q uoNoticePublish) identical(stored uoNoticeRecordState, arguments, actions string) bool {
	return !stored.withdrawn.Valid && q.id == stored.id && q.severity == stored.severity && q.category == stored.category &&
		q.title == stored.title && q.body == stored.body && arguments == stored.arguments && actions == stored.actions &&
		q.dedupe == stored.dedupe && q.expiresMS == stored.expiresMS && q.authorityRevision == stored.authorityRevision
}

func (q uoNoticeWithdraw) validate() error {
	if !q.view.valid() || !uoNoticeIdentifier(q.id, uoNoticeMaxIDBytes) || q.expectedRevision <= 0 || q.authorityRevision <= 0 || q.nowMS <= 0 {
		return ErrInvalid
	}
	return nil
}

func reserveUONoticeOrdinals(ctx context.Context, turn *commandTurn, count int) (first int64, err error) {
	defer func() { first, err = finishCommand(ctx, commandLifetime(turn), first, err) }()
	if count < 1 || count > 8 {
		return 0, ErrInvalid
	}
	last, err := wc008DraftInteger(ctx, turn, uoNoticeReserve, int64(count), int64(count), int64(math.MaxInt64-int64(count)))
	if err != nil {
		return 0, err
	}
	if last < int64(count) {
		return 0, ErrResolveRequired
	}
	return last - int64(count) + 1, nil
}

// The shared event channel for one inbox. #1267's sole append/oldest-delete
// protocol bounds the ring; this only supplies the already-sealed audience.
func uoNoticeEventAudience(view uoNoticeView) string {
	switch view.audience {
	case uoNoticeAudienceProfile:
		return apievents.InboxAudience(view.scope, uoNoticeAudienceProfile)
	case uoNoticeAudienceAccount:
		return apievents.InboxAudience(view.scope, uoNoticeAudienceAccount)
	default:
		return apievents.AdminAudience
	}
}

func appendUONoticeHint(ctx context.Context, turn *commandTurn, view uoNoticeView, revision, nowMS int64) (out wcAPIEventEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	return appendWCAPIEvent(ctx, turn, wcAPIEvent{
		audience:     uoNoticeEventAudience(view),
		eventType:    "notification.changed",
		resourceKind: "inbox",
		resourceID:   "inbox",
		revision:     strconv.FormatInt(revision, 10),
		data:         "{}",
		atMS:         nowMS,
	})
}

// Fresh-only component after current authority and receipt classification.
// Every error requires whole-turn rollback. One (scope,audience,source,dedupe)
// identity yields exactly one record and one inbox head, never per-admin rows.
func applyUONoticePublish(ctx context.Context, turn *commandTurn, q uoNoticePublish) (out uoNoticeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = q.validate(); err != nil {
		return out, err
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	arguments, err := uoNoticeEncodedArguments(q.arguments)
	if err != nil {
		return out, err
	}
	actions, err := uoNoticeEncodedActions(q.actions)
	if err != nil {
		return out, err
	}
	if uoNoticeRecordBytes(q.view, q.id, q.severity, q.source, q.category, q.title, q.body, q.dedupe, arguments, actions) > uoNoticeMaxRecordBytes {
		return out, ErrInvalid
	}
	stored, err := loadUONoticeSourceRecord(ctx, turn, q.view, q.source, q.dedupe)
	if err != nil {
		return out, err
	}
	if stored.exists {
		if q.identical(stored, arguments, actions) {
			out = uoNoticeEffect{id: stored.id, sequence: stored.sequence, revision: stored.revision, authorityRevision: stored.authorityRevision}
			return out, turn.checkLifetimes(ctx)
		}
		if stored.id != q.id || stored.revision != q.expectedRevision || stored.authorityRevision != q.expectedAuthorityRevision {
			return out, ErrConflict
		}
	} else if q.expectedRevision != 0 || q.expectedAuthorityRevision != 0 {
		return out, ErrConflict
	}
	ordinal, err := reserveUONoticeOrdinals(ctx, turn, 1)
	if err != nil {
		return out, err
	}
	if stored.exists && ordinal <= stored.revision {
		return out, ErrResolveRequired
	}
	out.revision, out.authorityRevision = ordinal, q.authorityRevision
	out.inboxRevision, err = wc008DraftInteger(ctx, turn, uoNoticeInboxUpsert, q.view.scope, q.view.audience, ordinal, q.nowMS)
	if err != nil {
		return out, err
	}
	if stored.exists {
		out.id, out.sequence = stored.id, stored.sequence
		_, err = wc008DraftInteger(ctx, turn, uoNoticeRestate, q.severity, q.category, q.title, q.body, arguments, actions,
			ordinal, q.nowMS, q.expiresMS, q.authorityRevision, stored.sequence, stored.revision, stored.authorityRevision)
	} else {
		out.id = q.id
		out.sequence, err = wc008DraftInteger(ctx, turn, uoNoticeInsert, q.id, q.view.scope, q.view.audience, q.severity, q.source,
			q.category, q.title, q.body, arguments, actions, q.dedupe, ordinal, q.nowMS, q.nowMS, q.expiresMS, q.authorityRevision)
		out.created = true
	}
	if err != nil {
		return out, err
	}
	// One shared marker per publication, never one per recipient. A
	// restatement of the same operation keeps its original shared row.
	if q.operation != (uoNoticeOperation{}) && !stored.exists {
		ids, _ := json.Marshal([]string{out.id})
		if _, err = wc008DraftInteger(ctx, turn, uoNoticeOperationInsert, q.view.scope, q.operation.id, hex.EncodeToString(q.operation.digest[:]),
			q.view.audience, uoNoticeBool(out.created), uoNoticeBool(!out.created), ordinal, string(ids), q.operation.expiresMS); err != nil {
			return out, err
		}
	}
	out.event, err = appendUONoticeHint(ctx, turn, q.view, ordinal, q.nowMS)
	if err != nil {
		return out, err
	}
	out.changed = true
	return out, turn.checkLifetimes(ctx)
}

// Withdrawal is idempotent: a notice already withdrawn at the caller's exact
// revision and authority returns unchanged with no new revision, inbox write or
// hint. It never resurrects a record and never touches another inbox.
func applyUONoticeWithdraw(ctx context.Context, turn *commandTurn, q uoNoticeWithdraw) (out uoNoticeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = q.validate(); err != nil {
		return out, err
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	target, err := loadUONoticeWithdrawTarget(ctx, turn, q.view, q.id)
	if err != nil {
		return out, err
	}
	out.id, out.sequence, out.withdrawn = target.id, target.sequence, target.withdrawn.Valid
	out.revision, out.authorityRevision = target.revision, target.authorityRevision
	if target.revision != q.expectedRevision || target.authorityRevision != q.authorityRevision {
		return out, ErrConflict
	}
	if target.withdrawn.Valid {
		return out, turn.checkLifetimes(ctx)
	}
	ordinal, err := reserveUONoticeOrdinals(ctx, turn, 1)
	if err != nil {
		return out, err
	}
	if ordinal <= target.revision {
		return out, ErrResolveRequired
	}
	if _, err = wc008DraftInteger(ctx, turn, uoNoticeWithdrawUpdate, ordinal, q.nowMS, q.nowMS, target.sequence, q.expectedRevision, q.authorityRevision); err != nil {
		return out, err
	}
	out.inboxRevision, err = wc008DraftInteger(ctx, turn, uoNoticeInboxUpsert, q.view.scope, q.view.audience, ordinal, q.nowMS)
	if err != nil {
		return out, err
	}
	out.revision = ordinal
	out.event, err = appendUONoticeHint(ctx, turn, q.view, ordinal, q.nowMS)
	if err != nil {
		return out, err
	}
	out.withdrawn, out.changed = true, true
	return out, turn.checkLifetimes(ctx)
}

func uoNoticeBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

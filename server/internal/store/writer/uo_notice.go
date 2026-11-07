package writer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// DRAFT fresh-effects only. W supplies current producer/principal/scope
// authority BEFORE receipt disclosure, and calls these only for fresh commands.
// W owns receipt codecs, quota/control accounting, forecasts and physical
// commit/rollback. Every error requires whole-turn rollback. No result here is
// an Applied claim, an authority capability, or a public command API.
type uoNoticeView struct{ scope, audience string }
type uoNoticeOperation struct {
	id        string
	digest    [32]byte
	expiresMS int64
}
type uoNoticePublish struct {
	view                                                uoNoticeView
	id, severity, source, category, title, body, dedupe string
	arguments                                           map[string]string
	actions                                             []notify.Action
	nowMS, expiresMS, authorityRevision                 int64
	// Zero creates; positive restates precisely this prior record revision.
	expectedRevision int64
	operation        uoNoticeOperation
}
type uoNoticeEffect struct {
	id                                    string
	sequence, revision, authorityRevision int64
	created, changed                      bool
	event                                 wcAPIEventEffect
}
type uoNoticeWithdraw struct {
	view                                       uoNoticeView
	id                                         string
	expectedRevision, authorityRevision, nowMS int64
}

func uoNoticeText(s string, limit int, required bool) bool {
	if !utf8.ValidString(s) || len(s) > limit || (required || s != "") && strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return false
		}
	}
	return true
}
func uoNoticeIdentifier(s string, limit int) bool {
	if len(s) == 0 || len(s) > limit {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:-", r)) {
			return false
		}
	}
	return true
}
func (v uoNoticeView) valid() bool {
	return uoNoticeText(v.scope, 400, true) && (v.audience == "profile" || v.audience == "account-admin" || v.audience == "server-admin")
}
func (q uoNoticePublish) validate() error {
	if !q.view.valid() || !uoNoticeIdentifier(q.id, 160) || !uoNoticeIdentifier(q.source, 40) || !uoNoticeIdentifier(q.category, 60) || !uoNoticeIdentifier(q.dedupe, 200) || !uoNoticeText(q.title, 200, true) || !uoNoticeText(q.body, 1000, false) || q.nowMS <= 0 || q.expiresMS <= q.nowMS || q.authorityRevision <= 0 || q.expectedRevision < 0 || len(q.arguments) > 12 {
		return ErrInvalid
	}
	if q.severity != "info" && q.severity != "warning" && q.severity != "critical" {
		return ErrInvalid
	}
	for k, v := range q.arguments {
		if !uoNoticeIdentifier(k, 48) || !uoNoticeText(v, 300, true) {
			return ErrInvalid
		}
	}
	if err := notify.ValidateActions(q.actions); err != nil {
		return ErrInvalid
	}
	if q.operation != (uoNoticeOperation{}) && (!uoNoticeIdentifier(q.operation.id, 160) || q.operation.digest == [32]byte{} || q.operation.expiresMS <= q.nowMS) {
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
func uoNoticeEvent(ctx context.Context, turn *commandTurn, audience, resource string, revision, now int64) (wcAPIEventEffect, error) {
	return appendWCAPIEvent(ctx, turn, wcAPIEvent{audience: audience, eventType: "notifications.changed", resourceKind: "inbox", resourceID: resource, revision: strconv.FormatInt(revision, 10), data: "{}", atMS: now})
}
func applyUONoticePublish(ctx context.Context, turn *commandTurn, q uoNoticePublish) (out uoNoticeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = q.validate(); err != nil {
		return out, err
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoNoticeDedupe, q.view.scope, q.view.audience, q.source, q.dedupe)
	if err != nil {
		return out, err
	}
	var oldRevision, oldAuthority int64
	err = row.Scan(&out.id, &out.sequence, &oldRevision, &oldAuthority)
	absent := errors.Is(err, sql.ErrNoRows)
	if !absent && err != nil {
		return out, err
	}
	if absent && q.expectedRevision != 0 || !absent && (oldRevision != q.expectedRevision || out.id != q.id || oldAuthority != q.authorityRevision) {
		return out, ErrConflict
	}
	ordinal, err := reserveUONoticeOrdinals(ctx, turn, 1)
	if err != nil {
		return out, err
	}
	if !absent && ordinal <= oldRevision {
		return out, ErrResolveRequired
	}
	out.revision, out.authorityRevision = ordinal, q.authorityRevision
	if _, err = wc008DraftInteger(ctx, turn, uoNoticeInboxUpsert, q.view.scope, q.view.audience, ordinal, q.nowMS); err != nil {
		return out, err
	}
	arguments, err := json.Marshal(q.arguments)
	if err != nil {
		return out, err
	}
	if q.arguments == nil {
		arguments = []byte("{}")
	}
	actions, err := json.Marshal(q.actions)
	if err != nil {
		return out, err
	}
	if q.actions == nil {
		actions = []byte("[]")
	}
	if absent {
		out.id = q.id
		out.sequence, err = wc008DraftInteger(ctx, turn, uoNoticeInsert, q.id, q.view.scope, q.view.audience, q.severity, q.source, q.category, q.title, q.body, string(arguments), string(actions), q.dedupe, ordinal, q.nowMS, q.nowMS, q.expiresMS, q.authorityRevision)
		out.created = true
	} else {
		_, err = wc008DraftInteger(ctx, turn, uoNoticeRestate, q.severity, q.category, q.title, q.body, string(arguments), string(actions), ordinal, q.nowMS, q.expiresMS, q.authorityRevision, out.sequence, oldRevision, oldAuthority)
	}
	if err != nil {
		return out, err
	}
	if q.operation != (uoNoticeOperation{}) {
		ids, _ := json.Marshal([]string{out.id})
		_, err = wc008DraftInteger(ctx, turn, uoNoticeOperationInsert, q.view.scope, q.operation.id, hex.EncodeToString(q.operation.digest[:]), q.view.audience, uoNoticeBool(out.created), uoNoticeBool(!out.created), ordinal, string(ids), q.operation.expiresMS)
		if err != nil {
			return out, err
		}
	}
	out.event, err = uoNoticeEvent(ctx, turn, q.view.scope, q.view.audience, ordinal, q.nowMS)
	if err != nil {
		return out, err
	}
	out.changed = true
	return out, turn.checkLifetimes(ctx)
}
func applyUONoticeWithdraw(ctx context.Context, turn *commandTurn, q uoNoticeWithdraw) (out uoNoticeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.view.valid() || !uoNoticeIdentifier(q.id, 160) || q.expectedRevision <= 0 || q.authorityRevision <= 0 || q.nowMS <= 0 {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoNoticeTarget, q.id, q.view.scope, q.view.audience)
	if err != nil {
		return out, err
	}
	var expires int64
	var withdrawn sql.NullInt64
	if err = row.Scan(&out.sequence, &out.revision, &out.authorityRevision, &expires, &withdrawn); err != nil {
		return out, err
	}
	if out.revision != q.expectedRevision || out.authorityRevision != q.authorityRevision {
		return out, ErrConflict
	}
	out.id = q.id
	if withdrawn.Valid {
		return out, nil
	}
	ordinal, err := reserveUONoticeOrdinals(ctx, turn, 1)
	if err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, turn, uoNoticeWithdrawUpdate, ordinal, q.nowMS, q.nowMS, out.sequence, q.expectedRevision, q.authorityRevision); err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, turn, uoNoticeInboxUpsert, q.view.scope, q.view.audience, ordinal, q.nowMS); err != nil {
		return out, err
	}
	if ordinal <= q.expectedRevision {
		return out, ErrResolveRequired
	}
	out.revision = ordinal
	out.event, err = uoNoticeEvent(ctx, turn, q.view.scope, q.view.audience, ordinal, q.nowMS)
	if err != nil {
		return out, err
	}
	out.changed = true
	return out, turn.checkLifetimes(ctx)
}

// One operation's targets share an ordinal, while the <=8 public operations
// reserve their ORDERED ordinal range exactly once per fresh transaction.
// The parent splits/admits at most200 point targets, never scans recipients.
type uoNoticeAction uint8

const (
	uoNoticeRead      uoNoticeAction = 1
	uoNoticeUnread    uoNoticeAction = 2
	uoNoticeArchive   uoNoticeAction = 3
	uoNoticeUnarchive uoNoticeAction = 4
	uoNoticeReadAll   uoNoticeAction = 5
)

type uoNoticeAckTarget struct {
	id                          string
	revision, authorityRevision int64
}
type uoNoticeAckOperation struct {
	action  uoNoticeAction
	targets []uoNoticeAckTarget
}
type uoNoticeAckInput struct {
	view       uoNoticeView
	principal  string
	nowMS      int64
	operations []uoNoticeAckOperation
}
type uoNoticeAckOutcome struct {
	action             uoNoticeAction
	id                 string
	changed, notFound  bool
	revision           int64
	readMS, archivedMS sql.NullInt64
}
type uoNoticeAckEffect struct {
	revision int64
	changed  int
	outcomes []uoNoticeAckOutcome
	event    wcAPIEventEffect
}
type uoNoticeAckState struct {
	exists             bool
	revision           int64
	readMS, archivedMS sql.NullInt64
}
type uoNoticeBulkState struct{ sequence, revision, readMS int64 }

func (q uoNoticeAckInput) validate() error {
	if !q.view.valid() || !uoNoticeText(q.principal, 512, true) || q.nowMS <= 0 || len(q.operations) < 1 || len(q.operations) > 8 {
		return ErrInvalid
	}
	n := 0
	for _, op := range q.operations {
		if op.action < uoNoticeRead || op.action > uoNoticeReadAll || op.action == uoNoticeReadAll && len(op.targets) != 0 || op.action != uoNoticeReadAll && len(op.targets) == 0 {
			return ErrInvalid
		}
		n += len(op.targets)
		if n > 200 {
			return ErrInvalid
		}
		for _, target := range op.targets {
			if !uoNoticeIdentifier(target.id, 160) || target.revision <= 0 || target.authorityRevision <= 0 {
				return ErrInvalid
			}
		}
	}
	return nil
}

// Same rule as read/uo_notice_state, kept native and values-only across the
// package boundary. A raw sequence0 head is no bulk coverage. Archived unread
// suppresses read-all, including a later bulk ordinal. Positive coverage with
// zero time cannot synthesize epoch readAt; the parent must refuse/resync.
func resolveUONativeAck(sequence, revision int64, a uoNoticeAckState, b uoNoticeBulkState) (read, archive sql.NullInt64, err error) {
	if sequence <= 0 || revision <= 0 || b.sequence < 0 || b.sequence > 0 && (b.revision <= 0 || b.readMS <= 0) || a.exists && (a.revision <= 0 || a.readMS.Valid && a.readMS.Int64 < 0 || a.archivedMS.Valid && a.archivedMS.Int64 < 0) {
		return read, archive, ErrResolveRequired
	}
	current := a.exists && a.revision >= revision
	covered := b.sequence >= sequence && b.revision >= revision
	if current {
		archive = a.archivedMS
		if a.readMS.Valid {
			return a.readMS, archive, nil
		}
		if archive.Valid || !covered || a.revision > b.revision {
			return read, archive, nil
		}
	}
	if covered {
		read = sql.NullInt64{Int64: b.readMS, Valid: true}
	}
	return read, archive, nil
}
func loadUONoticeBulk(ctx context.Context, turn *commandTurn, principal string) (b uoNoticeBulkState, err error) {
	row, err := wc008DraftQuery(ctx, turn, uoNoticeBulkRead, principal)
	if err != nil {
		return b, err
	}
	var archive int64
	err = row.Scan(&b.sequence, &archive, &b.revision, &b.readMS)
	if errors.Is(err, sql.ErrNoRows) {
		return uoNoticeBulkState{}, nil
	}
	if err != nil {
		return b, err
	}
	// archive-all has no API; never silently interpret unsupported head state.
	if archive != 0 || b.sequence < 0 || b.revision <= 0 || b.readMS < 0 || b.sequence > 0 && b.readMS == 0 {
		return b, ErrResolveRequired
	}
	return b, nil
}
func applyUONoticeAcks(ctx context.Context, turn *commandTurn, q uoNoticeAckInput) (out uoNoticeAckEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = q.validate(); err != nil {
		return out, err
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	bulk, err := loadUONoticeBulk(ctx, turn, q.principal)
	if err != nil {
		return out, err
	}
	first, err := reserveUONoticeOrdinals(ctx, turn, len(q.operations))
	if err != nil {
		return out, err
	}
	if bulk.sequence > 0 && first <= bulk.revision {
		return out, ErrResolveRequired
	}
	for i, op := range q.operations {
		ordinal := first + int64(i)
		if op.action == uoNoticeReadAll {
			// Highest sequence is a bounded indexed seek, including withdrawn/expired
			// positions. Current visibility and producer revision remain read fences.
			row, e := wc008DraftQuery(ctx, turn, uoNoticeUpper, q.view.scope, q.view.audience)
			if e != nil {
				return out, e
			}
			var upper int64
			e = row.Scan(&upper)
			if errors.Is(e, sql.ErrNoRows) {
				upper = 0
			} else if e != nil {
				return out, e
			}
			result := uoNoticeAckOutcome{action: op.action, revision: ordinal}
			if upper > 0 {
				if _, e = wc008DraftInteger(ctx, turn, uoNoticeBulkUpsert, q.principal, upper, ordinal, q.nowMS); e != nil {
					return out, e
				}
				bulk = uoNoticeBulkState{upper, ordinal, q.nowMS}
				out.changed++
				result.changed = true
			}
			out.outcomes = append(out.outcomes, result)
			continue
		}
		for _, target := range op.targets {
			result := uoNoticeAckOutcome{action: op.action, id: target.id, revision: ordinal}
			row, e := wc008DraftQuery(ctx, turn, uoNoticeTarget, target.id, q.view.scope, q.view.audience)
			if e != nil {
				return out, e
			}
			var sequence, revision, authority, expires int64
			var withdrawn sql.NullInt64
			e = row.Scan(&sequence, &revision, &authority, &expires, &withdrawn)
			if errors.Is(e, sql.ErrNoRows) || e == nil && (withdrawn.Valid || expires <= q.nowMS) {
				result.notFound = true
				out.outcomes = append(out.outcomes, result)
				continue
			}
			if e != nil {
				return out, e
			}
			if ordinal <= revision {
				return out, ErrResolveRequired
			}
			if revision != target.revision || authority != target.authorityRevision {
				return out, ErrConflict
			}
			row, e = wc008DraftQuery(ctx, turn, uoNoticeAckRead, q.principal, sequence)
			if e != nil {
				return out, e
			}
			var before uoNoticeAckState
			e = row.Scan(&before.revision, &before.readMS, &before.archivedMS)
			if e == nil {
				before.exists = true
			} else if !errors.Is(e, sql.ErrNoRows) {
				return out, e
			}
			read, archive, e := resolveUONativeAck(sequence, revision, before, bulk)
			if e != nil {
				return out, e
			}
			nextRead, nextArchive := read, archive
			switch op.action {
			case uoNoticeRead:
				if !read.Valid {
					nextRead = sql.NullInt64{Int64: q.nowMS, Valid: true}
				}
			case uoNoticeUnread:
				nextRead = sql.NullInt64{}
			case uoNoticeArchive:
				if !archive.Valid {
					nextArchive = sql.NullInt64{Int64: q.nowMS, Valid: true}
					if !read.Valid {
						nextRead = sql.NullInt64{Int64: q.nowMS, Valid: true}
					}
				}
			case uoNoticeUnarchive:
				nextArchive = sql.NullInt64{}
			}
			result.readMS, result.archivedMS = nextRead, nextArchive
			// No-op actions must not freeze a bulk-only timestamp into a sparse read.
			if nextRead != read || nextArchive != archive {
				if _, e = wc008DraftInteger(ctx, turn, uoNoticeAckUpsert, q.principal, sequence, uoNoticeNullable(nextRead), uoNoticeNullable(nextArchive), ordinal); e != nil {
					return out, e
				}
				result.changed = true
				out.changed++
			}
			out.outcomes = append(out.outcomes, result)
		}
	}
	out.revision = first + int64(len(q.operations)) - 1
	if out.changed > 0 {
		out.event, err = uoNoticeEvent(ctx, turn, q.principal, q.view.audience, out.revision, q.nowMS)
		if err != nil {
			return out, err
		}
	}
	return out, turn.checkLifetimes(ctx)
}
func uoNoticeNullable(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

func uoNoticeBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

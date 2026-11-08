package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

// Native source identity and admission bounds for ONE shared notice record.
// These are private facts from a freshly authorized writer turn, never a
// capability, a command receipt or a public input shape. The independent
// dispatcher still owns current authority, receipt classification, quota and
// disclosure; this file only decides what one source may write.
const (
	uoNoticeMaxScopeBytes     = 400
	uoNoticeMaxIDBytes        = 160
	uoNoticeMaxSourceBytes    = 40
	uoNoticeMaxCategoryBytes  = 60
	uoNoticeMaxDedupeBytes    = 200
	uoNoticeMaxTitleBytes     = 200
	uoNoticeMaxBodyBytes      = 1000
	uoNoticeMaxArgumentBytes  = 300
	uoNoticeMaxArguments      = 12
	uoNoticeMaxRecordBytes    = 8 << 10
	uoNoticeAudienceProfile   = "profile"
	uoNoticeAudienceAccount   = "account-admin"
	uoNoticeAudienceServerAdm = "server-admin"
)

// The durable inbox key. scope is the authorized server-bound key (never a bare
// profile identifier) and audience names which shared record set it is.
type uoNoticeView struct{ scope, audience string }

// One shared operation marker for a producer or broadcast. It never expands
// into per-recipient rows; it only lets the owning operation be advanced or
// reclaimed later by its own bounded command.
type uoNoticeOperation struct {
	id        string
	digest    [32]byte
	expiresMS int64
}

// The exact current source row this native publish may restate or withdraw.
// Absence is a fact, not an error. No other inbox is ever selected.
type uoNoticeRecordState struct {
	exists                                           bool
	id                                               string
	sequence, revision, authorityRevision, expiresMS int64
	severity, category, title, body                  string
	arguments, actions, dedupe                       string
	withdrawn                                        sql.NullInt64
}

// Same allowlists as the merged notify seam, kept native and values-only so the
// writer never imports a caller instruction or a client command.
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
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == ':' || r == '-') {
			return false
		}
	}
	return true
}

func (v uoNoticeView) valid() bool {
	return uoNoticeText(v.scope, uoNoticeMaxScopeBytes, true) &&
		(v.audience == uoNoticeAudienceProfile || v.audience == uoNoticeAudienceAccount || v.audience == uoNoticeAudienceServerAdm)
}

func (o uoNoticeOperation) valid(nowMS int64) bool {
	return uoNoticeIdentifier(o.id, uoNoticeMaxIDBytes) && o.digest != [32]byte{} && o.expiresMS > nowMS
}

// Canonical JSON for one record. Byte comparison of this exact encoding is what
// makes an identical producer retry a no-op rather than a new hint.
func uoNoticeEncodedArguments(arguments map[string]string) (string, error) {
	if arguments == nil {
		return "{}", nil
	}
	raw, err := json.Marshal(arguments)
	if err != nil {
		return "", ErrInvalid
	}
	return string(raw), nil
}

func uoNoticeEncodedActions(actions []notify.Action) (string, error) {
	if actions == nil {
		return "[]", nil
	}
	raw, err := json.Marshal(actions)
	if err != nil {
		return "", ErrInvalid
	}
	return string(raw), nil
}

// Variable TEXT bytes one record would store. A joint budget is required: the
// per-field capsules alone let arguments and actions add up past a page budget,
// so one producer record must stay inside this bound before any write.
func uoNoticeRecordBytes(view uoNoticeView, id, severity, source, category, title, body, dedupe, arguments, actions string) int64 {
	return int64(len(view.scope) + len(view.audience) + len(id) + len(severity) + len(source) + len(category) + len(title) + len(body) + len(dedupe) + len(arguments) + len(actions))
}

// One indexed source lookup by the declared unique identity. It cannot see the
// same dedupe key in another scope/audience, and it never enumerates records.
func loadUONoticeSourceRecord(ctx context.Context, turn *commandTurn, view uoNoticeView, source, dedupe string) (out uoNoticeRecordState, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, uoNoticeSourceRecord, view.scope, view.audience, source, dedupe)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.id, &out.sequence, &out.revision, &out.authorityRevision, &out.severity, &out.category, &out.title, &out.body, &out.arguments, &out.actions, &out.dedupe, &out.expiresMS, &out.withdrawn)
	if errors.Is(err, sql.ErrNoRows) {
		return uoNoticeRecordState{}, nil
	}
	if err != nil {
		return out, err
	}
	if out.id == "" || out.sequence <= 0 || out.revision <= 0 || out.authorityRevision <= 0 || out.dedupe != dedupe {
		return uoNoticeRecordState{}, ErrResolveRequired
	}
	out.exists = true
	return out, nil
}

// One scoped point lookup by identifier for withdrawal. A record outside this
// exact authorized inbox is indistinguishable from an absent one.
func loadUONoticeWithdrawTarget(ctx context.Context, turn *commandTurn, view uoNoticeView, id string) (out uoNoticeRecordState, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, uoNoticeWithdrawTarget, id, view.scope, view.audience)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.sequence, &out.revision, &out.authorityRevision, &out.expiresMS, &out.withdrawn)
	if errors.Is(err, sql.ErrNoRows) {
		return uoNoticeRecordState{}, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.sequence <= 0 || out.revision <= 0 || out.authorityRevision <= 0 {
		return uoNoticeRecordState{}, ErrResolveRequired
	}
	out.id, out.exists = id, true
	return out, nil
}

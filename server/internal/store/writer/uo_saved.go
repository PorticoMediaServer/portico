package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

// Mandatory parent-owned same-turn boundaries. These checks must use US current
// lifetimes, restrictions and publication/revoke fences, never retained grants.
// Security actions (including create with server visibility) require FULL custody.
// RB validates a closed typed query and returns its canonical representation;
// no arbitrary SQL or caller-provided generic filter map is accepted here.
type uoSavedAuthority interface {
	checkSaved(context.Context, *commandTurn, uoSavedInput, string) error
	checkSavedItem(context.Context, *commandTurn, uoSavedAddition) error
	reserveSavedBytes(context.Context, *commandTurn, int64) error
}
type uoSavedViewValidator interface {
	validateSavedView(context.Context, *commandTurn, uoSavedView) (string, error)
}
type uoSavedView struct {
	kind, sort string
	descending bool
	library    string
}
type uoSavedAction uint8

const (
	uoSavedCreate uoSavedAction = iota + 1
	uoSavedEdit
	uoSavedDelete
	uoSavedEntries
	uoSavedUndo
	uoSavedReorder
	uoSavedShare
	uoSavedUnshare
)

type uoSavedInput struct {
	action                                                uoSavedAction
	id, kind, owner, authority, account, profile, created string
	revision                                              int64
	name, summary, visibility                             *string
	view                                                  *uoSavedView
	security                                              uoSavedAuthority
	validator                                             uoSavedViewValidator
	additions                                             []uoSavedAddition
	removals, order                                       []string
	after                                                 *string
	token                                                 string
	nowMS                                                 int64
	targetAuthority, targetAccount, targetProfile, role   string
}
type uoSavedOutcome struct{ input, id, status string }
type uoSavedEffect struct {
	reclaimedRows, reclaimedBytes int64
	selectionRevision             int64
	id                            string
	revision, generation          int64
	deleted                       bool
	outcomes                      []uoSavedOutcome
	undoToken                     string
	undoUntil                     int64
}

func uoSavedText(s string, n int) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0) && utf8.RuneCountInString(s) <= n
}
func applyUOSaved(ctx context.Context, t *commandTurn, q uoSavedInput) (out uoSavedEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !uoPersonalText(q.id, 128) || q.security == nil || q.revision < 0 || q.revision == math.MaxInt64 || q.nowMS < 0 || q.nowMS > math.MaxInt64-30000 {
		return out, ErrInvalid
	}
	if q.name != nil && (!uoSavedText(*q.name, 200) || strings.TrimSpace(*q.name) == "") || q.summary != nil && !uoSavedText(*q.summary, 4000) || q.visibility != nil && *q.visibility != "private" && *q.visibility != "server" {
		return out, ErrInvalid
	}
	if q.action < uoSavedCreate || q.action > uoSavedUnshare {
		return out, ErrInvalid
	}
	if q.action != uoSavedShare && q.action != uoSavedUnshare && (q.targetAuthority != "" || q.targetAccount != "" || q.targetProfile != "" || q.role != "") {
		return out, ErrInvalid
	}
	if q.action != uoSavedCreate && q.kind != "" {
		return out, ErrInvalid
	}
	kind := q.kind
	if q.action != uoSavedCreate {
		row, e := wc008DraftQuery(ctx, t, "uo.saved.head", q.id)
		if e != nil {
			return out, e
		}
		var rev int64
		if e = row.Scan(&kind, &rev); e != nil {
			return out, e
		}
		if rev != q.revision {
			return out, ErrConflict
		}
	} else if q.revision != 0 || q.name == nil || (kind != "collection" && kind != "view") {
		return out, ErrInvalid
	}
	if (q.visibility != nil || q.action >= uoSavedEntries) && kind != "collection" {
		return out, ErrInvalid
	}
	if q.view != nil && kind != "view" || q.action == uoSavedCreate && kind == "view" && q.view == nil {
		return out, ErrInvalid
	}
	if q.action != uoSavedCreate && q.action != uoSavedEdit && (q.name != nil || q.summary != nil || q.view != nil || q.visibility != nil) {
		return out, ErrInvalid
	}
	if q.action != uoSavedEntries && (len(q.additions) > 0 || len(q.removals) > 0) || q.action != uoSavedReorder && (len(q.order) > 0 || q.after != nil) || q.action != uoSavedUndo && q.action != uoSavedEntries && q.token != "" {
		return out, ErrInvalid
	}
	if q.visibility != nil || q.action == uoSavedShare || q.action == uoSavedUnshare {
		if t == nil || t.transaction == nil || t.transaction.descriptor.Durability != Full {
			return out, ErrInvalid
		}
	}
	if err = q.security.checkSaved(ctx, t, q, kind); err != nil {
		return out, err
	}
	var definition *string
	if q.view != nil {
		if q.validator == nil {
			return out, ErrInvalid
		}
		v, e := q.validator.validateSavedView(ctx, t, *q.view)
		if e != nil {
			return out, e
		}
		if !uoSavedText(v, 16384) || v == "" {
			return out, ErrInvalid
		}
		definition = &v
	}
	out.id = q.id
	switch q.action {
	case uoSavedCreate:
		for _, s := range []string{q.owner, q.authority, q.account, q.profile, q.created} {
			if !uoPersonalText(s, 256) {
				return out, ErrInvalid
			}
		}
		summary, visibility, def := "", "private", "{}"
		if q.summary != nil {
			summary = *q.summary
		}
		if q.visibility != nil {
			visibility = *q.visibility
		}
		if definition != nil {
			def = *definition
		}
		out.revision, err = wc008DraftInteger(ctx, t, "uo.saved.create", q.id, kind, q.owner, q.authority, q.account, q.profile, *q.name, summary, def, visibility, q.created)
	case uoSavedEdit:
		if q.name == nil && q.summary == nil && definition == nil && q.visibility == nil {
			return out, ErrInvalid
		}
		out.revision, err = wc008DraftInteger(ctx, t, "uo.saved.edit", uoSavedOptional(q.name), uoSavedOptional(q.summary), uoSavedOptional(definition), uoSavedOptional(q.visibility), q.id, q.revision)
	case uoSavedDelete:
		out.revision, err = wc008DraftInteger(ctx, t, "uo.saved.delete", q.id, q.revision)
		out.deleted = true
	case uoSavedEntries, uoSavedUndo, uoSavedReorder:
		out, err = applyUOSavedEntries(ctx, t, q)
		if err == nil {
			out.revision, err = wc008DraftInteger(ctx, t, "uo.saved.advance", q.id, q.revision)
		}
	case uoSavedShare, uoSavedUnshare:
		for _, s := range []string{q.targetAuthority, q.targetAccount, q.targetProfile} {
			if !uoPersonalText(s, 128) {
				return out, ErrInvalid
			}
		}
		if q.action == uoSavedShare {
			if q.role != "viewer" && q.role != "editor" {
				return out, ErrInvalid
			}
			var n int64
			n, err = wc008DraftInteger(ctx, t, "uo.saved.share_count", q.id, q.targetAuthority, q.targetAccount, q.targetProfile)
			if err != nil {
				return out, err
			}
			if n >= 100 {
				return out, ErrInvalid
			}
			_, err = wc008DraftInteger(ctx, t, "uo.saved.share", q.id, q.targetAuthority, q.targetAccount, q.targetProfile, q.role)
		} else {
			_, err = wc008DraftInteger(ctx, t, "uo.saved.unshare", q.id, q.targetAuthority, q.targetAccount, q.targetProfile)
			if errors.Is(err, ErrConflict) {
				err = nil
			}
		}
		if err == nil {
			out.revision, err = wc008DraftInteger(ctx, t, "uo.saved.advance", q.id, q.revision)
		}
	default:
		return out, ErrInvalid
	}
	if err != nil {
		return out, err
	}
	out.generation, err = wc008DraftInteger(ctx, t, "uo.saved.generation")
	if err != nil {
		return out, err
	}
	out.selectionRevision, err = wc008DraftInteger(ctx, t, "uo.saved.selection")
	return out, err
}

// Source entry points can be absent without converting partial outcomes into a
// failed turn. All other failures leave rollback to W; never report Applied.
func uoSavedAbsent(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func uoSavedOptional(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

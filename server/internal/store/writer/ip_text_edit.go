package writer

import (
	"context"
	"strings"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The native edit/restore components share the lock receipt protocol. Frozen
// before-values and title inputs are ORIGINAL request inputs, never reloaded
// from a mutable head for replay. The outer writer still owns admission,
// current administrator disclosure, downstream search/event composition and
// COMMIT proof. No component returns public Applied.
type ipTextProjection struct {
	Title, SortTitle, Language                                   string
	SortKey, SortHead, SortKeyKeepArticles, SortHeadKeepArticles string
}

func newIPTextEditCommand(i CommandIdentity, q ipTextLockRequest, edit fieldmeta.TextEdit) (ipTextLockCommand, error) {
	kind := IPFieldEdit
	if edit.Action == fieldmeta.RestoreAutomatic {
		kind = IPFieldRestore
	}
	return newIPTextMutationCommand(i, q, kind, edit)
}
func ipTextCommandName(k CommandID) string {
	switch k {
	case IPFieldLock:
		return "field_lock"
	case IPFieldEdit:
		return "field_edit"
	case IPFieldRestore:
		return "field_restore"
	}
	return ""
}
func ipTextCloneProjection(p ipTextProjection) ipTextProjection {
	return ipTextProjection{strings.Clone(p.Title), strings.Clone(p.SortTitle), strings.Clone(p.Language), strings.Clone(p.SortKey), strings.Clone(p.SortHead), strings.Clone(p.SortKeyKeepArticles), strings.Clone(p.SortHeadKeepArticles)}
}
func ipTextKeyField(f fieldmeta.TextField) bool {
	return f == fieldmeta.Title || f == fieldmeta.SortTitle || f == fieldmeta.MetadataLanguage
}
func ipTextPrepareProjection(q ipTextLockRequest, change fieldmeta.TextChange) (ipTextProjection, error) {
	p := q.Projection
	if !change.EffectiveChanged {
		if p != (ipTextProjection{}) {
			return p, ErrInvalid
		}
		return p, nil
	}
	switch q.Field {
	case fieldmeta.Title, fieldmeta.SortTitle, fieldmeta.MetadataLanguage:
		if _, err := CheckSealControlStrings([]string{p.Title, p.SortTitle, p.Language, p.SortKey, p.SortHead, p.SortKeyKeepArticles, p.SortHeadKeepArticles}); err != nil {
			return p, err
		}
		old, err := ProjectFilenameMetadata(p.Title, p.SortTitle, p.Language)
		if err != nil || old.SortKey != p.SortKey || old.SortHead != p.SortHead || old.SortKeyKeepArticles != p.SortKeyKeepArticles || old.SortHeadKeepArticles != p.SortHeadKeepArticles {
			return p, ErrInvalid
		}
		before := p.Title
		if q.Field == fieldmeta.SortTitle {
			before = p.SortTitle
		}
		if q.Field == fieldmeta.MetadataLanguage {
			before = p.Language
		}
		if before != q.Before.Effective.Value {
			return p, ErrInvalid
		}
		switch q.Field {
		case fieldmeta.Title:
			p.Title = change.State.Effective.Value
		case fieldmeta.SortTitle:
			p.SortTitle = change.State.Effective.Value
		case fieldmeta.MetadataLanguage:
			p.Language = change.State.Effective.Value
		}
		next, err := ProjectFilenameMetadata(p.Title, p.SortTitle, p.Language)
		if err != nil {
			return p, err
		}
		return ipTextProjection{next.Title, next.SortTitle, next.MetadataLanguage, next.SortKey, next.SortHead, next.SortKeyKeepArticles, next.SortHeadKeepArticles}, nil
	case fieldmeta.Description, fieldmeta.OriginalTitle, fieldmeta.Tagline, fieldmeta.Studio, fieldmeta.Network, fieldmeta.Country:
		if p != (ipTextProjection{}) {
			return p, ErrInvalid
		}
		return p, nil
	default:
		// Edition and content rating require their own identity/admission effects;
		// a descriptive body must never partially publish those projections.
		return p, ErrInvalid
	}
}
func ipTextCheckProjection(ctx context.Context, t *commandTurn, q ipTextLockRequest, _ ipTextProjection) error {
	if ipTextKeyField(q.Field) {
		p := q.Projection
		return ipTextExpectOne(ctx, t, ipTextProjectionCurrent, q.EntityID, p.Title, p.SortTitle, p.Language, p.SortKey, p.SortHead, p.SortKeyKeepArticles, p.SortHeadKeepArticles)
	}
	return ipTextExpectOne(ctx, t, ipTextScalarCurrent, q.EntityID, string(q.Field), q.Before.Effective.Value)
}
func ipTextApplyProjection(ctx context.Context, t *commandTurn, q ipTextLockRequest, p ipTextProjection, value string) error {
	if ipTextKeyField(q.Field) {
		return ipTextExpectOne(ctx, t, ipTextProjectionUpdate, p.Title, p.SortTitle, p.Language, p.SortKey, p.SortHead, p.SortKeyKeepArticles, p.SortHeadKeepArticles, q.EntityID)
	}
	return ipTextExpectOne(ctx, t, ipTextScalarUpdate, string(q.Field), value, string(q.Field), value, string(q.Field), value, string(q.Field), value, string(q.Field), value, string(q.Field), value, q.EntityID)
}

const (
	ipTextFieldUpdate       stmt.ID = "ip.text_edit.field_update"
	ipTextProjectionCurrent stmt.ID = "ip.text_edit.projection_current"
	ipTextProjectionUpdate  stmt.ID = "ip.text_edit.projection_update"
	ipTextScalarCurrent     stmt.ID = "ip.text_edit.scalar_current"
	ipTextScalarUpdate      stmt.ID = "ip.text_edit.scalar_update"
)

func ipTextEditDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	return []stmt.Definition{
		{ID: ipTextFieldUpdate, Kind: stmt.Write, SQL: `UPDATE metadata_owner_fields SET value=?,value_present=?,locked=?,actor=?,observed_at=? WHERE kind=? AND entity_id=? AND field=? AND locked=? RETURNING 1`, Params: []stmt.Param{s("value"), i("value_present"), i("locked"), s("actor"), s("at"), s("kind"), i("entity"), s("field"), i("old_locked")}},
		{ID: ipTextProjectionCurrent, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_entities WHERE id=? AND title=? AND sort_title=? AND metadata_language=? AND sort_key=? COLLATE BINARY AND sort_head=? AND sort_key_keep_articles=? COLLATE BINARY AND sort_head_keep_articles=?`, Params: []stmt.Param{i("entity"), s("title"), s("sort_title"), s("language"), s("key"), s("head"), s("keep_key"), s("keep_head")}},
		{ID: ipTextProjectionUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET title=?,sort_title=?,metadata_language=?,sort_key=?,sort_head=?,sort_key_keep_articles=?,sort_head_keep_articles=? WHERE id=? RETURNING 1`, Params: []stmt.Param{s("title"), s("sort_title"), s("language"), s("key"), s("head"), s("keep_key"), s("keep_head"), i("entity")}},
		{ID: ipTextScalarCurrent, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_entities WHERE id=? AND CASE ? WHEN 'description' THEN overview WHEN 'originalTitle' THEN original_title WHEN 'tagline' THEN tagline WHEN 'studio' THEN studio WHEN 'network' THEN network WHEN 'country' THEN country ELSE NULL END=?`, Params: []stmt.Param{i("entity"), s("field"), s("before")}},
		{ID: ipTextScalarUpdate, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET overview=CASE WHEN ?='description' THEN ? ELSE overview END,original_title=CASE WHEN ?='originalTitle' THEN ? ELSE original_title END,tagline=CASE WHEN ?='tagline' THEN ? ELSE tagline END,studio=CASE WHEN ?='studio' THEN ? ELSE studio END,network=CASE WHEN ?='network' THEN ? ELSE network END,country=CASE WHEN ?='country' THEN ? ELSE country END WHERE id=? RETURNING 1`, Params: []stmt.Param{s("f1"), s("v1"), s("f2"), s("v2"), s("f3"), s("v3"), s("f4"), s("v4"), s("f5"), s("v5"), s("f6"), s("v6"), i("entity")}},
	}
}

type ipTextMutationSavedResult struct {
	Kind             CommandID
	Value            ipTextLockResult
	EffectiveChanged bool
}

func ipTextMutationResultDigest(k CommandID, i CommandIdentity, q RequestDigest, v ipTextLockResult, effective bool) (ResultDigest, error) {
	if k == IPFieldLock {
		if effective {
			return ResultDigest{}, ErrInvalid
		}
		return ipTextLockResultDigest(i, q, v)
	}
	return canonicalResultDigest(i, q, ipTextMutationSavedResult{k, v, effective})
}
func preflightIPTextMutationResult(v ipTextMutationSavedResult) error {
	if (v.Kind != IPFieldEdit && v.Kind != IPFieldRestore) || v.EffectiveChanged && !v.Value.Changed {
		return ErrInvalid
	}
	return preflightIPTextLockResult(v.Value)
}
func writeIPTextMutationResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipTextMutationSavedResult) error {
	writeIPResultIdentity(e, ipTextCommandName(v.Kind), i, q)
	e.flag(v.EffectiveChanged)
	return writeIPTextResultPayload(e, v.Value)
}

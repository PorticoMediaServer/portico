package writer

import (
	"context"
	"database/sql"
	"errors"

	fieldmeta "github.com/PorticoMediaServer/portico/server/internal/store/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func ipTextBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func ipTextFlag(n int64) (bool, bool) {
	switch n {
	case 0:
		return false, true
	case 1:
		return true, true
	}
	return false, false
}

func ipTextRows(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (*commandRows, error) {
	step, ok := t.steps[id]
	if !ok {
		return nil, ErrInvalid
	}
	return t.query(ctx, step.statementStep, args...)
}

func ipTextOne(rows *commandRows, scan func() error) (err error) {
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if e := rows.Err(); e != nil {
			return e
		}
		return sql.ErrNoRows
	}
	if err = scan(); err != nil {
		return err
	}
	if rows.Next() {
		return ErrResolveRequired
	}
	return rows.Err()
}

func ipTextExpectOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	rows, err := ipTextRows(ctx, t, id, args...)
	if err != nil {
		return err
	}
	var n int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&n) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func ipTextExecOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, ok := t.steps[id]
	if !ok {
		return ErrInvalid
	}
	result, err := t.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

func ipTextEditNamespaces(s ipTextEditSource) (entity, person any) {
	if s.EntityID > 0 {
		entity = s.EntityID
	} else {
		person = s.PersonID
	}
	return
}

func ipTextEditCatalogValue(value string, present bool) fieldmeta.Text {
	return fieldmeta.Text{Value: value, Present: present}
}

// ipTextEditFieldState reads the owner-field cohort member. Absence is a real
// state (no row), not an empty value: automatic and lock must also be absent.
func ipTextEditFieldState(ctx context.Context, t *commandTurn, s ipTextEditSource, field fieldmeta.TextField) (state fieldmeta.TextState, found bool, err error) {
	rows, err := ipTextRows(ctx, t, ipTextEditFieldRow, s.Kind, ipTextEditTarget(s), string(field))
	if err != nil {
		return state, false, err
	}
	var value, automatic string
	var valuePresent, automaticPresent, locked int64
	err = ipTextOne(rows, func() error {
		return rows.Scan(&value, &automatic, &valuePresent, &automaticPresent, &locked)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	vp, ok1 := ipTextFlag(valuePresent)
	ap, ok2 := ipTextFlag(automaticPresent)
	lk, ok3 := ipTextFlag(locked)
	if !ok1 || !ok2 || !ok3 {
		return state, false, ErrResolveRequired
	}
	state = fieldmeta.TextState{Effective: fieldmeta.Text{Value: value, Present: vp},
		Automatic: fieldmeta.Text{Value: automatic, Present: ap}, Locked: lk}
	return state, true, nil
}

type ipTextEditAuthorFacts struct {
	Author  string
	Reading sql.NullString
	Key     sql.NullString
	Source  sql.NullString
}

func ipTextEditReadAuthor(ctx context.Context, t *commandTurn, entity int64) (out ipTextEditAuthorFacts, err error) {
	rows, err := ipTextRows(ctx, t, ipTextEditAuthor, entity)
	if err != nil {
		return out, err
	}
	err = ipTextOne(rows, func() error {
		return rows.Scan(&out.Author, &out.Reading, &out.Key, &out.Source)
	})
	if err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	return out, nil
}

func ipTextEditReadText(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (string, error) {
	rows, err := ipTextRows(ctx, t, id, args...)
	if err != nil {
		return "", err
	}
	var value string
	if err = ipTextOne(rows, func() error { return rows.Scan(&value) }); err != nil {
		return "", errors.Join(ErrConflict, err)
	}
	return value, nil
}

func ipTextEditReadScalar(ctx context.Context, t *commandTurn, entity int64, field fieldmeta.TextField) (string, error) {
	rows, err := ipTextRows(ctx, t, ipTextEditScalar, string(field), entity)
	if err != nil {
		return "", err
	}
	var value sql.NullString
	if err = ipTextOne(rows, func() error { return rows.Scan(&value) }); err != nil {
		return "", errors.Join(ErrConflict, err)
	}
	if !value.Valid {
		return "", ErrConflict
	}
	return value.String, nil
}

// ipTextEditCatalogState is the current accepted effective value for a field,
// derived from the same catalog column the edit will CAS. Presence cannot be
// recovered from a NOT NULL text column: a non-empty value is present, and an
// empty value is present only when the author reading trio says so.
func ipTextEditCatalogState(ctx context.Context, t *commandTurn, s ipTextEditSource, field fieldmeta.TextField) (fieldmeta.Text, error) {
	if s.PersonID > 0 {
		value, err := ipTextEditReadText(ctx, t, ipTextEditPersonCurrent, s.PersonID)
		if err != nil {
			return fieldmeta.Text{}, err
		}
		return ipTextEditCatalogValue(value, value != ""), nil
	}
	switch {
	case field == fieldmeta.Title:
		return ipTextEditCatalogValue(s.Projection.Title, s.Projection.Title != ""), nil
	case field == fieldmeta.SortTitle:
		return ipTextEditCatalogValue(s.Projection.SortTitle, s.Projection.SortTitle != ""), nil
	case field == fieldmeta.MetadataLanguage:
		return ipTextEditCatalogValue(s.Projection.Language, s.Projection.Language != ""), nil
	case field == ipTextAuthorField:
		facts, err := ipTextEditReadAuthor(ctx, t, s.EntityID)
		if err != nil {
			return fieldmeta.Text{}, err
		}
		return ipTextEditCatalogValue(facts.Author, facts.Reading.Valid || facts.Author != ""), nil
	default:
		value, err := ipTextEditReadScalar(ctx, t, s.EntityID, field)
		if err != nil {
			return fieldmeta.Text{}, err
		}
		return ipTextEditCatalogValue(value, value != ""), nil
	}
}

// ipTextEditBeforeMatches proves the ORIGINAL frozen before cohort against the
// current owner row, or, when no row exists, against the catalog column. An
// absent row cannot carry an automatic value or a lock.
func ipTextEditBeforeMatches(before, current fieldmeta.TextState, found bool, catalog fieldmeta.Text) bool {
	if found {
		return before == current
	}
	if before.Automatic.Present || before.Automatic.Value != "" || before.Locked {
		return false
	}
	return before.Effective.Value == catalog.Value && before.Effective.Present == catalog.Present
}

type ipTextEditApplied struct {
	value  ipTextEditResult
	digest ResultDigest
}

func (c *ipTextEditCommand) resultFields() []ipTextEditFieldResult {
	out := make([]ipTextEditFieldResult, len(c.after))
	for j, change := range c.after {
		out[j] = ipTextEditFieldResult{Field: c.request.Fields[j].Field, Change: change}
	}
	return out
}

func (c *ipTextEditCommand) resultValue(ownerID, revision, subject, appliedMS int64, history EvidenceInteger) ipTextEditResult {
	return ipTextEditResult{OwnerID: ownerID, EntityID: c.request.Source.EntityID, PersonID: c.request.Source.PersonID,
		Revision: revision, SubjectRevision: subject, AppliedMS: appliedMS, History: history, Fields: c.resultFields()}
}

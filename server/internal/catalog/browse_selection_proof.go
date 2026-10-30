package catalog

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
	"regexp"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const BrowseSelectionConversionLimit = 200

const SelectionBasisExpression = `^[a-f0-9]{64}$`

var selectionBasisPattern = regexp.MustCompile(SelectionBasisExpression)

// ValidateSelectionBasis accepts an omitted legacy proof, never an invalid held proof.
func ValidateSelectionBasis(basis string) error {
	if basis != "" && !selectionBasisPattern.MatchString(basis) {
		return browseIssue("selectionBasis", "refresh the selected view")
	}
	return nil
}

// ValidateSelectionBasisJSON distinguishes omission from an explicit null/empty proof.
func ValidateSelectionBasisJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if value, present := fields["selectionBasis"]; present {
		var basis string
		if json.Unmarshal(value, &basis) != nil || !selectionBasisPattern.MatchString(basis) {
			return browseIssue("selectionBasis", "refresh the selected view")
		}
	}
	return nil
}

func (q *JobQuery) UnmarshalJSON(raw []byte) error {
	type wire JobQuery
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := ValidateSelectionBasisJSON(raw); err != nil {
		return err
	}
	*q = JobQuery(value)
	return nil
}

// checkBrowseSelectionBasis is called inside the carried admission/page snapshot.
// It deliberately does not alter ordinary page/cursor revisions.
func (s *Service) checkBrowseSelectionBasis(v Viewer, library, basis string) error {
	if err := ValidateSelectionBasis(basis); err != nil {
		return err
	}
	if basis == "" {
		return nil
	}
	if library == AcrossLibraries || !v.AllowsLibrary(library) {
		return ErrStaleContinuation
	}
	if tx := s.readTx; tx != nil && dbwork.Snapshot(s.Context()) == nil {
		ctx, err := dbwork.BindSnapshot(s.Context(), tx)
		if err != nil {
			return err
		}
		clone := *s.WithContext(ctx)
		clone.readTx = tx
		s = &clone
	}
	if dbwork.Snapshot(s.Context()) == nil {
		return sql.ErrTxDone
	}
	current, err := s.browseSelectionWitness(library, v.Profile, v.EffectiveRestrictions())
	if err != nil {
		return err
	}
	if current != basis {
		return ErrStaleContinuation
	}
	return nil
}

func (JobQuery) JSONSchema() map[string]any {
	type wire JobQuery
	shape, _ := apikit.Schema(reflect.TypeFor[wire]())
	shape["properties"].(map[string]any)["selectionBasis"].(map[string]any)["pattern"] = SelectionBasisExpression
	return shape
}

// The callback owns only bounded intent/policy and no admission transaction.
func (s *Service) selectionBasisCheck(v Viewer, library, basis string) func(context.Context, *sql.Tx) error {
	if basis == "" {
		return nil
	}
	root := Service{db: s.db, state: s.state, Clock: s.Clock}
	return func(ctx context.Context, tx *sql.Tx) error {
		bound, err := dbwork.BindSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		read := *root.WithContext(bound)
		read.readTx = tx
		return read.checkBrowseSelectionBasis(v, library, basis)
	}
}

// CheckBrowseSelectionBasisTx validates a held proof against this exact fresh
// capture/commit transaction, with the caller's live effective content policy.
func (s *Service) CheckBrowseSelectionBasisTx(ctx context.Context, tx *sql.Tx, viewer Viewer, library, basis string) error {
	bound, err := dbwork.BindSnapshot(ctx, tx)
	if err != nil {
		return err
	}
	read := *s.WithContext(bound)
	read.readTx = tx
	return read.checkBrowseSelectionBasis(viewer, library, basis)
}

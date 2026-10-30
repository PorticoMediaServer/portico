package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
)

// MissingScope is resolved inside the transaction that reads or removes rows.
// A nil Libraries slice is the explicit owner/system bypass. A non-nil empty
// slice admits no libraries. VisibilitySQL is a trusted predicate over the
// alias missing_candidate.id, compiled by the canonical content-access helper.
type MissingScope struct {
	Libraries      []string
	VisibilitySQL  string
	VisibilityArgs []any
}

type MissingAuthority func(context.Context, *sql.Tx) (MissingScope, error)

func resolveMissingScope(ctx context.Context, tx *sql.Tx, authority MissingAuthority) (MissingScope, error) {
	if authority == nil {
		return MissingScope{VisibilitySQL: "1"}, nil
	}
	scope, err := authority(ctx, tx)
	if err == nil && scope.VisibilitySQL == "" {
		err = ErrAdminQuery
	}
	return scope, err
}

func (s MissingScope) librarySQL(expression string) (string, []any) {
	if s.Libraries == nil {
		return "1", nil
	}
	raw, _ := json.Marshal(s.Libraries)
	return expression + ` IN(SELECT value FROM json_each(?))`, []any{string(raw)}
}

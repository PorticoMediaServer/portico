package writer

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	inventoryDisclosureJob     stmt.ID = "scan.inventory_accept.disclosure_job"
	inventoryDisclosureLibrary stmt.ID = "scan.inventory_accept.disclosure_library"
)

// These are current native scope facts, not source/lease eligibility or a
// principal factory. W must validate its live opaque internal ScanDriver FIRST,
// then check these facts before the original receipt header/cohort. No source,
// root, job status, run revision, cursor or lease predicate may hide history.
func inventoryDisclosureDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	return []stmt.Definition{
		{ID: inventoryDisclosureJob, Owner: "store/writer Inventory disclosure", CallSites: []string{"store/writer/inventory_disclosure.go"}, Kind: stmt.Read,
			SQL:    `SELECT id,library_id FROM jobs WHERE id=? AND library_id=?`,
			Params: []stmt.Param{text("run_id"), text("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "scope_hit", Bindings: []any{"scan-hit", "scan-library"}},
				{Name: "missing_job", Bindings: []any{"absent", "scan-library"}},
				{Name: "wrong_library_key", Bindings: []any{"scan-hit", "other"}},
			}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_jobs_1"}, Seeks: []string{"id=?"}}},
		{ID: inventoryDisclosureLibrary, Owner: "store/writer Inventory disclosure", CallSites: []string{"store/writer/inventory_disclosure.go"}, Kind: stmt.Read,
			SQL:    `SELECT id,library_id FROM catalog_libraries WHERE id=? AND library_id=?`,
			Params: []stmt.Param{integer("library_id"), text("library_key")},
			Cohorts: []stmt.Cohort{
				{Name: "scope_hit", Bindings: []any{int64(7), "scan-library"}},
				{Name: "missing_library", Bindings: []any{int64(9), "scan-library"}},
				{Name: "wrong_library_key", Bindings: []any{int64(8), "scan-library"}},
			}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}},
	}
}

// checkInventoryNativeScope is one private part of W's disclosure bridge. A
// nil error proves ONLY these two native relationships; it confers no command,
// principal or receipt authority. The sole dispatcher still owns opaque current
// permission, original owner/receipt resolution and final revocation checking.
func checkInventoryNativeScope(ctx context.Context, turn *commandTurn, request ScanReceiptRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	if request.RunID == "" || request.LibraryID <= 0 || request.LibraryKey == "" ||
		len(request.RunID) > maxCanonicalRequestBytes || len(request.LibraryKey) > maxCanonicalRequestBytes-len(request.RunID) ||
		!utf8.ValidString(request.RunID) || !utf8.ValidString(request.LibraryKey) {
		return ErrDisclosureDenied
	}
	for _, id := range []stmt.ID{inventoryDisclosureJob, inventoryDisclosureLibrary} {
		if step, ok := turn.steps[id]; !ok || step.kind != stmt.Read {
			return invalid("inventory_disclosure_steps")
		}
	}
	for _, point := range []struct {
		id   stmt.ID
		args []any
	}{{inventoryDisclosureJob, []any{request.RunID, request.LibraryKey}},
		{inventoryDisclosureLibrary, []any{request.LibraryID, request.LibraryKey}}} {
		rows, err := turn.query(ctx, turn.steps[point.id].statementStep, point.args...)
		if err != nil {
			return err
		}
		// The SELECTs return only the exact already-budgeted key bytes, never
		// an uncharged root/title/private source string. Verify both values and
		// full cardinality even though the actual indexed predicates are unique.
		readErr := func() error {
			if !rows.Next() {
				if err := rows.Err(); err != nil {
					return err
				}
				return ErrDisclosureDenied
			}
			var key, run string
			var library int64
			if point.id == inventoryDisclosureJob {
				if err := rows.Scan(&run, &key); err != nil {
					return err
				}
				if run != request.RunID || key != request.LibraryKey {
					return ErrDisclosureDenied
				}
			} else {
				if err := rows.Scan(&library, &key); err != nil {
					return err
				}
				if library != request.LibraryID || key != request.LibraryKey {
					return ErrDisclosureDenied
				}
			}
			if rows.Next() {
				return ErrResolveRequired
			}
			return rows.Err()
		}()
		if err := errors.Join(readErr, rows.Close()); err != nil {
			return err
		}
	}
	return turn.checkLifetimes(ctx)
}

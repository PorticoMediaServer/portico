package writer

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const scanInventoryObject stmt.ID = "scan.inventory_accept.insert_object"

// These are SC-private, POST-allocation effect values, not an original request,
// source proof, result receipt or authorization. The fresh caller must FIRST
// prove source/path/lease/final shape and prepare the exact observation under
// the complete admitted memory/work budget. In particular objectIdentity is
// the derived stable identity (possibly empty); evidenceJSON has the separate
// stable-or-actual-path fallback. This primitive does not prepare either one.
type inventoryObjectFacts struct {
	objectID, sourceID, assetToken, incarnation, relativePath string
	objectIdentity, revision, evidenceJSON                    string
	size, modifiedNS                                          int64
	runID, directoryPath                                      string
}

// scanObjectDefinitions is a separate finite provider so the previously
// reviewed 16 statements/47 cohorts stay unchanged. W alone composes this new
// ID into the complete Inventory registry/recipe; there is no registration API.
func scanObjectDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	return []stmt.Definition{{
		ID: scanInventoryObject, Kind: stmt.Write,
		// Omit retired: the adopted schema's DEFAULT 0 makes EVERY insert
		// write the unretired folder index. Still verify native RETURNING=0.
		SQL: `INSERT INTO inventory_objects(
id,source_id,asset_id,root_incarnation,relative_path,object_identity,
revision,evidence_json,size,modified_ns,seen_job,directory_path,
state,analysis_state,analysis_revision,has_analysis_probe_bindings)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
RETURNING id,source_id,asset_id,root_incarnation,relative_path,object_identity,
revision,evidence_json,size,modified_ns,seen_job,directory_path,
state,retired,analysis_state,analysis_revision,has_analysis_probe_bindings`,
		Params: []stmt.Param{text("object_id"), text("source_id"), text("asset_token"), text("root_incarnation"),
			text("relative_path"), text("object_identity"), text("revision"), text("evidence_json"), integer("size"), integer("modified_ns"),
			text("run_id"), text("directory_path"), text("state"), text("analysis_state"), text("analysis_revision"), integer("has_analysis_probe_bindings")},
		Owner: "store/writer scan owner adapter", CallSites: []string{"store/writer/scan_object.go"},
		Plan: stmt.Plan{Hot: true},
		Cohorts: []stmt.Cohort{{Name: "new_ordinary_observation", Bindings: []any{
			"scan-object-new", "scan-object-source", "scan-object-asset", "source-incarnation", "Arrival.mkv", "ino:31",
			"opaque:observation:019", `{"kind":"local","scope":"scan-object-source:source-incarnation","object":"ino:31","size":42,"modifiedNs":-11,"changeToken":"change:29"}`,
			int64(42), int64(-11), "scan-object-run", ".", "available", "pending", "", int64(0),
		}}},
	}}
}

// insertInventoryObject publishes only this native row on the SAME protected
// turn. No source admission, historical resolution, generated IDs, clock,
// transaction/registry construction, commit, receipt or Applied result here.
// The caller must roll back the WHOLE command on every error. Merely calling
// this helper does not make an incomplete InventoryAccept safe to expose.
func insertInventoryObject(ctx context.Context, turn *commandTurn, in inventoryObjectFacts) (out inventoryObjectFacts, err error) {
	if ctx == nil || turn == nil || turn.transaction == nil {
		return out, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	step, ok := turn.steps[scanInventoryObject]
	if !ok {
		return out, invalid("unregistered_inventory_object_step")
	}
	// Shape only. Exact stable identity/JSON/revision and native containment are
	// the fresh caller's required prepared proof, never inferred from UTF-8.
	if in.objectID == "" || in.sourceID == "" || in.assetToken == "" || in.incarnation == "" || in.revision == "" ||
		in.evidenceJSON == "" || in.runID == "" || in.size < 0 || !inventoryPath(in.relativePath, false) ||
		!inventoryPath(in.directoryPath, true) || path.Dir(in.relativePath) != in.directoryPath {
		return out, invalid("inventory_object_values")
	}
	for _, value := range [...]string{in.objectID, in.sourceID, in.assetToken, in.incarnation, in.relativePath,
		in.objectIdentity, in.revision, in.evidenceJSON, in.runID, in.directoryPath} {
		if !utf8.ValidString(value) {
			return out, invalid("inventory_object_text")
		}
	}
	rows, err := turn.query(ctx, step.statementStep, in.objectID, in.sourceID, in.assetToken, in.incarnation,
		in.relativePath, in.objectIdentity, in.revision, in.evidenceJSON, in.size, in.modifiedNS, in.runID, in.directoryPath,
		"available", "pending", "", int64(0))
	if err != nil {
		return out, err
	}
	defer func() {
		err = errors.Join(err, rows.Close())
		if err != nil {
			out = inventoryObjectFacts{}
		}
	}()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return out, err
		}
		return out, sql.ErrNoRows
	}
	var state, analysisState, analysisRevision string
	var retired, probeBindings int64
	if err := rows.Scan(&out.objectID, &out.sourceID, &out.assetToken, &out.incarnation, &out.relativePath,
		&out.objectIdentity, &out.revision, &out.evidenceJSON, &out.size, &out.modifiedNS, &out.runID, &out.directoryPath,
		&state, &retired, &analysisState, &analysisRevision, &probeBindings); err != nil {
		return out, err
	}
	if rows.Next() {
		return out, invalid("inventory_object_returning_count")
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if out != in || state != "available" || retired != 0 || analysisState != "pending" || analysisRevision != "" || probeBindings != 0 {
		return out, invalid("inventory_object_returning_values")
	}
	return out, nil
}

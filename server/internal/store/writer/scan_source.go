package writer

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	scanSourceLengths      stmt.ID = "scan.issuer.source_lengths"
	scanSourcePayload      stmt.ID = "scan.issuer.source"
	scanSourcePolicy       stmt.ID = "scan.issuer.policy"
	scanLibraryRootLengths stmt.ID = "scan.issuer.library_root_lengths"
	scanLibraryRoot        stmt.ID = "scan.issuer.library_root"
)

func scanSourceDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	params := []stmt.Param{text("source_id"), text("library_key")}
	cohorts := []stmt.Cohort{{Name: "current_local", Bindings: []any{"lifecycle-new-source", "lifecycle-library"}},
		{Name: "missing", Bindings: []any{"no-source", "lifecycle-library"}},
		{Name: "wrong_library", Bindings: []any{"lifecycle-new-source", "wrong-library"}}}
	sourcePlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, Seeks: []string{"id=?"}}
	defs := []stmt.Definition{
		{ID: scanSourceLengths, Kind: stmt.Read, SQL: `SELECT octet_length(id),octet_length(library_id),octet_length(kind),octet_length(root),octet_length(incarnation),octet_length(root_identity),octet_length(health),octet_length(classification) FROM library_sources WHERE id=? AND library_id=?`, Params: params, Cohorts: cohorts, Plan: sourcePlan},
		{ID: scanSourcePayload, Kind: stmt.Read, SQL: `SELECT id,library_id,kind,root,incarnation,root_identity,health,classification,generation,identity_confirmed,enabled,follow_symlinks FROM library_sources WHERE id=? AND library_id=?`, Params: params, Cohorts: cohorts, Plan: sourcePlan},
		{ID: scanSourcePolicy, Kind: stmt.Read, SQL: `SELECT revision FROM library_scan_policies WHERE library_id=?`, Params: []stmt.Param{text("library_key")}, Cohorts: []stmt.Cohort{{Name: "current_policy", Bindings: []any{"lifecycle-library"}}, {Name: "missing", Bindings: []any{"no-library"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_scan_policies_1"}, Seeks: []string{"library_id=?"}}},
	}
	for _, point := range []struct {
		id  stmt.ID
		sql string
	}{
		{scanLibraryRootLengths, `SELECT octet_length(root) FROM catalog_libraries WHERE id=? AND library_id=?`},
		{scanLibraryRoot, `SELECT root FROM catalog_libraries WHERE id=? AND library_id=?`},
	} {
		defs = append(defs, stmt.Definition{ID: point.id, Kind: stmt.Read, SQL: point.sql,
			Params:  []stmt.Param{{Name: "library_id", Type: stmt.Integer}, text("library_key")},
			Cohorts: []stmt.Cohort{{Name: "current_library", Bindings: []any{int64(7), "lifecycle-library"}}, {Name: "missing", Bindings: []any{int64(9), "lifecycle-library"}}, {Name: "wrong_key", Bindings: []any{int64(7), "wrong-library"}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}})
	}
	for i := range defs {
		defs[i].Owner = "store/writer scan issuer"
		defs[i].CallSites = []string{"store/writer/scan_source.go"}
	}
	return append(defs, inventoryFreshLibraryDefinition())
}

func captureScanSource(ctx context.Context, turn *commandTurn, libraryID int64, libraryKey, sourceID string) (value scanSourceFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if turn == nil || turn.transaction == nil || libraryID <= 0 || libraryKey == "" || sourceID == "" ||
		len(libraryKey) > maxCanonicalRequestBytes || len(sourceID) > maxCanonicalRequestBytes-len(libraryKey) ||
		!utf8.ValidString(libraryKey) || !utf8.ValidString(sourceID) {
		return value, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return value, err
	}
	switch turn.transaction.descriptor.ID {
	case InventoryCreate, InventoryClaim, InventoryAccept, InventoryDirectoryDiscover, InventoryDirectoryEOF:
	default:
		return value, invalid("scan_source_purpose")
	}
	for _, id := range []stmt.ID{scanSourceLengths, scanSourcePayload, scanSourcePolicy, scanLibraryRootLengths, scanLibraryRoot, inventoryFreshLibrary} {
		if _, ok := turn.steps[id]; !ok {
			return value, invalid("scan_source_steps")
		}
	}
	if err = checkInventoryFreshLibrary(ctx, turn, ScanReceiptRequest{LibraryID: libraryID, LibraryKey: libraryKey}); err != nil {
		return value, err
	}
	rows, err := turn.query(ctx, turn.steps[scanSourceLengths].statementStep, sourceID, libraryKey)
	if err != nil {
		return value, err
	}
	var lengths [8]int64
	err = scanSourcePoint(rows, &lengths[0], &lengths[1], &lengths[2], &lengths[3], &lengths[4], &lengths[5], &lengths[6], &lengths[7])
	if err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	total := int64(0)
	for _, n := range lengths {
		if n < 0 || n > int64(maxCanonicalRequestBytes)-total {
			return value, &Refusal{Code: RefusalCapacity}
		}
		total += n
	}
	rows, err = turn.query(ctx, turn.steps[scanLibraryRootLengths].statementStep, libraryID, libraryKey)
	if err != nil {
		return value, err
	}
	var identityRootBytes int64
	if err = scanSourcePoint(rows, &identityRootBytes); err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	if identityRootBytes < 0 || identityRootBytes > maxCanonicalRequestBytes {
		return value, &Refusal{Code: RefusalCapacity}
	}
	// Bounds close BEFORE SQLite copies the native TEXT payload or W allocates
	// the retained raw root. BEGIN IMMEDIATE preserves this source snapshot.
	required := uint64(64<<10) + uint64(total+identityRootBytes)*32
	switch turn.transaction.descriptor.ID {
	case InventoryCreate, InventoryClaim:
		err = turn.requireScanLifecycleMemory(ctx, required)
	case InventoryAccept:
		err = turn.requireInventoryMemory(ctx, required)
	case InventoryDirectoryDiscover, InventoryDirectoryEOF:
		err = turn.requireCommandMemory(ctx, turn.transaction.descriptor.ID, required, "directory_memory_partition")
	default:
		err = invalid("scan_source_purpose")
	}
	if err != nil {
		return value, err
	}
	rows, err = turn.query(ctx, turn.steps[scanSourcePayload].statementStep, sourceID, libraryKey)
	if err != nil {
		return value, err
	}
	var texts [8]string
	var generation, confirmed, enabled, follow int64
	err = scanSourcePoint(rows, &texts[0], &texts[1], &texts[2], &texts[3], &texts[4], &texts[5], &texts[6], &texts[7], &generation, &confirmed, &enabled, &follow)
	if err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	for i, s := range texts {
		if int64(len(s)) != lengths[i] || !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
			return value, invalid("scan_source_native_text")
		}
	}
	rows, err = turn.query(ctx, turn.steps[scanLibraryRoot].statementStep, libraryID, libraryKey)
	if err != nil {
		return value, err
	}
	var identityRoot string
	if err = scanSourcePoint(rows, &identityRoot); err != nil {
		return value, errors.Join(ErrConflict, err)
	}
	if int64(len(identityRoot)) != identityRootBytes || !utf8.ValidString(identityRoot) || strings.IndexByte(identityRoot, 0) >= 0 {
		return value, invalid("scan_library_root_native_text")
	}
	if texts[0] != sourceID || texts[1] != libraryKey || texts[2] != "local" || texts[3] == "" || texts[4] == "" || texts[5] == "" ||
		texts[6] != "ready" || (texts[7] != "local" && texts[7] != "network") || generation <= 0 || confirmed != 1 || enabled != 1 || (follow != 0 && follow != 1) {
		return value, ErrConflict
	}
	rows, err = turn.query(ctx, turn.steps[scanSourcePolicy].statementStep, libraryKey)
	if err != nil {
		return value, err
	}
	var policy int64
	if err = scanSourcePoint(rows, &policy); err != nil || policy <= 0 {
		return value, errors.Join(ErrConflict, err)
	}
	return scanSourceFacts{libraryID: libraryID, libraryKey: strings.Clone(texts[1]), policyRevision: policy, identityRoot: []byte(identityRoot),
		source: inventorySource{id: strings.Clone(texts[0]), resolvedRoot: []byte(texts[3]), incarnation: strings.Clone(texts[4]), rootIdentity: strings.Clone(texts[5]), generation: generation, followSymlinks: follow == 1}}, nil
}

func scanSourcePoint(rows *commandRows, values ...any) (err error) {
	if rows == nil {
		return ErrInvalid
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		return errors.Join(ErrConflict, rows.Err())
	}
	if err = rows.Scan(values...); err != nil {
		return err
	}
	if rows.Next() {
		return ErrResolveRequired
	}
	return rows.Err()
}

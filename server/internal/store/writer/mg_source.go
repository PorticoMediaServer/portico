package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	MGConfigureTuner        CommandID = 808
	MGSetSourceEnabled      CommandID = 809
	MGRetireSource          CommandID = 810
	MGSetSourceViewerAccess CommandID = 818
)

func mgSourceDescriptors() []Descriptor {
	return []Descriptor{{MGConfigureTuner, "mg.source.configure", Normal}, {MGSetSourceEnabled, "mg.source.enabled", Full}, {MGRetireSource, "mg.source.retire", Full}, {MGSetSourceViewerAccess, "mg.source.viewer-access", Full}}
}

// Native facts only. Actual owner/admin/member/source authority, public mutation
// lifetime/receipt, admission and uncertain COMMIT resolution are parent-owned.
// A captured snapshot, revision, request ID or viewer-access string is no grant.
type mgSourceFacts struct {
	id, name, state, generation, published, viewerAccess string
	revision, tuners, physical, ownerLimit               int64
	removed                                              int64
	removalRequest, removedAt                            sql.NullString
	removalRevision                                      sql.NullInt64
}
type mgSourceResult struct {
	source    mgSourceFacts
	appliedMS int64
}
type mgSourceConfiguration struct {
	name                 string
	physical, ownerLimit int64
	usePhysical          bool
}

const (
	mgSourceRead     stmt.ID = "mg.source.read"
	mgSourceUpdate   stmt.ID = "mg.source.update"
	mgSourceSettings stmt.ID = "mg.source.settings"
	mgSourceAccess   stmt.ID = "mg.source.access"
	mgSourceRemoval  stmt.ID = "mg.source.removal"
)

func mgSourceDefinitions() []stmt.Definition {
	txt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	defs := []stmt.Definition{
		{ID: mgSourceRead, Kind: stmt.Read, SQL: `SELECT s.id,s.name,s.revision,s.state,s.tuner_count,s.active_generation,s.published_at,x.settings_physical_count,x.settings_owner_limit,x.settings_viewer_access,x.has_live_source_removals,x.removal_request_id,x.removal_revision,x.removal_removed_at FROM live_sources AS s INDEXED BY sqlite_autoindex_live_sources_1 CROSS JOIN live_source_identities AS x INDEXED BY sqlite_autoindex_live_source_identities_1 ON x.id=s.id WHERE s.id=?`, Params: []stmt.Param{txt("source")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1", "sqlite_autoindex_live_source_identities_1"}}},
		{ID: mgSourceUpdate, Kind: stmt.Write, SQL: `UPDATE live_sources INDEXED BY sqlite_autoindex_live_sources_1 SET name=?,revision=?,state=?,tuner_count=? WHERE id=? AND revision=? AND state=? AND active_generation=? AND name=? AND tuner_count=?`, Params: []stmt.Param{txt("name"), i("revision"), txt("state"), i("tuners"), txt("source"), i("expected_revision"), txt("expected_state"), txt("expected_generation"), txt("expected_name"), i("expected_tuners")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_sources_1"}}},
		{ID: mgSourceSettings, Kind: stmt.Write, SQL: `UPDATE live_source_identities INDEXED BY sqlite_autoindex_live_source_identities_1 SET settings_physical_count=?,settings_owner_limit=? WHERE id=? AND has_live_source_removals=0 AND settings_physical_count=? AND settings_owner_limit=? AND settings_viewer_access=?`, Params: []stmt.Param{i("physical"), i("limit"), txt("source"), i("expected_physical"), i("expected_limit"), txt("expected_access")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_source_identities_1"}}},
		{ID: mgSourceAccess, Kind: stmt.Write, SQL: `UPDATE live_source_identities INDEXED BY sqlite_autoindex_live_source_identities_1 SET settings_viewer_access=? WHERE id=? AND has_live_source_removals=0 AND settings_viewer_access=?`, Params: []stmt.Param{txt("access"), txt("source"), txt("expected_access")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_source_identities_1"}}},
		{ID: mgSourceRemoval, Kind: stmt.Write, SQL: `UPDATE live_source_identities INDEXED BY sqlite_autoindex_live_source_identities_1 SET has_live_source_removals=1,removal_request_id=?,removal_revision=?,removal_removed_at=? WHERE id=? AND has_live_source_removals=0`, Params: []stmt.Param{txt("request"), i("revision"), txt("removed"), txt("source")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_source_identities_1"}}},
	}
	for j := range defs {
		d := &defs[j]
		d.Owner = "writer.mg.source"
		d.CallSites = []string{"writer/mg_source.go"}
		for _, idx := range d.Plan.Indexes {
			table := "live_sources"
			if idx == "sqlite_autoindex_live_source_identities_1" {
				table = "live_source_identities"
			}
			d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, stmt.AutoIndexBinding{Table: table, Name: idx, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}})
		}
		args := make([]any, len(d.Params))
		for k, p := range d.Params {
			if p.Type == stmt.Text {
				args[k] = "missing"
			} else {
				args[k] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-native-keys", Bindings: args}}
	}
	return defs
}
func mgSourceText(s string, required bool, max int) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func mgSourceTime(ctx context.Context, t *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil {
		return 0, ErrInvalid
	}
	tx := t.transaction
	switch tx.descriptor.ID {
	case MGConfigureTuner, MGSetSourceEnabled, MGRetireSource, MGSetSourceViewerAccess:
	default:
		return 0, ErrInvalid
	}
	if tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || tx.conn == nil || tx.quantum == nil || tx.custody == nil {
		return 0, ErrInvalid
	}
	if tx.done || !tx.begun {
		return 0, ErrClosed
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok {
		return 0, ErrInvalid
	}
	defer tx.guardPanic()
	now := tx.writer.now()
	if now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if err = tx.observeTime(now); err != nil {
		return 0, err
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return now.UnixMilli(), nil
}
func mgSourcePurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose || !t.transaction.policyVerified {
		return ErrInvalid
	}
	if purpose != MGConfigureTuner && (t.transaction.descriptor.Durability != Full || t.transaction.settings.Synchronous != int(sqliteconn.Full)) {
		return ErrInvalid
	}
	return nil
}
func mgSourceStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgSourceRead, mgSourceUpdate, mgSourceSettings, mgSourceAccess, mgSourceRemoval:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}
func mgReadSource(ctx context.Context, t *commandTurn, id string) (out mgSourceFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	step, err := mgSourceStep(t, mgSourceRead)
	if err != nil {
		return out, err
	}
	row, err := t.queryRow(ctx, step, id)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.id, &out.name, &out.revision, &out.state, &out.tuners, &out.generation, &out.published, &out.physical, &out.ownerLimit, &out.viewerAccess, &out.removed, &out.removalRequest, &out.removalRevision, &out.removedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if out.id != id || !mgSourceText(out.name, true, 256) || out.revision <= 0 || out.tuners < 0 || out.tuners > 256 || out.physical < 0 || out.physical > 256 || out.ownerLimit < 0 || out.ownerLimit > 256 || (out.state != "active" && out.state != "disabled") || (out.viewerAccess != "owner-only" && out.viewerAccess != "server-members") || (out.removed != 0 && out.removed != 1) {
		return out, ErrInvalid
	}
	if out.removed == 1 && (!out.removalRequest.Valid || out.removalRequest.String == "" || !out.removalRevision.Valid || out.removalRevision.Int64 <= 0 || !out.removedAt.Valid || out.removedAt.String == "") {
		return out, ErrInvalid
	}
	return out, nil
}
func mgSourceChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgSourceStep(t, id)
	if err != nil {
		return err
	}
	result, err := t.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}
func mgSourceBefore(ctx context.Context, t *commandTurn, before mgSourceFacts) error {
	if before.removed != 0 || before.revision <= 0 || before.revision == math.MaxInt64 {
		return ErrConflict
	}
	actual, err := mgReadSource(ctx, t, before.id)
	if err != nil {
		return err
	}
	if actual != before {
		return ErrConflict
	}
	return nil
}
func mgSourceAdvance(ctx context.Context, t *commandTurn, before, after mgSourceFacts) (out mgSourceResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgSourceChange(ctx, t, mgSourceUpdate, after.name, after.revision, after.state, after.tuners, before.id, before.revision, before.state, before.generation, before.name, before.tuners); err != nil {
		return out, err
	}
	post, err := mgReadSource(ctx, t, before.id)
	if err != nil {
		return out, err
	}
	if post != after {
		return out, ErrConflict
	}
	now, err := mgSourceTime(ctx, t)
	if err != nil {
		return out, err
	}
	return mgSourceResult{post, now}, nil
}
func mgConfigureSource(ctx context.Context, t *commandTurn, before mgSourceFacts, c mgSourceConfiguration) (out mgSourceResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgSourcePurpose(t, MGConfigureTuner); err != nil {
		return out, err
	}

	if !mgSourceText(c.name, true, 256) || c.physical < 0 || c.physical > 256 || c.ownerLimit < 0 || c.ownerLimit > 256 {
		return out, ErrInvalid
	}
	if err = mgSourceBefore(ctx, t, before); err != nil {
		return out, err
	}
	after := before
	after.name = c.name
	after.physical = c.physical
	after.ownerLimit = c.ownerLimit
	after.revision++
	// Preserve the established unknown-capacity value 0; its display estimate
	// remains 1, never a fabricated physical tuner. The limit narrows discovery.
	after.tuners = c.ownerLimit
	if c.usePhysical && c.physical > 0 && (c.ownerLimit == 0 || c.physical < c.ownerLimit) {
		after.tuners = c.physical
	}
	if err = mgSourceChange(ctx, t, mgSourceSettings, c.physical, c.ownerLimit, before.id, before.physical, before.ownerLimit, before.viewerAccess); err != nil {
		return out, err
	}
	return mgSourceAdvance(ctx, t, before, after)
}
func mgSetSourceEnabled(ctx context.Context, t *commandTurn, before mgSourceFacts, enabled bool) (out mgSourceResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgSourcePurpose(t, MGSetSourceEnabled); err != nil {
		return out, err
	}

	if err = mgSourceBefore(ctx, t, before); err != nil {
		return out, err
	}
	after := before
	after.revision++
	after.state = "disabled"
	if enabled {
		after.state = "active"
	}
	return mgSourceAdvance(ctx, t, before, after)
}
func mgSetSourceAccess(ctx context.Context, t *commandTurn, before mgSourceFacts, access string) (out mgSourceResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgSourcePurpose(t, MGSetSourceViewerAccess); err != nil {
		return out, err
	}

	if access != "owner-only" && access != "server-members" {
		return out, ErrInvalid
	}
	if err = mgSourceBefore(ctx, t, before); err != nil {
		return out, err
	}
	after := before
	after.revision++
	after.viewerAccess = access
	if err = mgSourceChange(ctx, t, mgSourceAccess, access, before.id, before.viewerAccess); err != nil {
		return out, err
	}
	return mgSourceAdvance(ctx, t, before, after)
}
func mgRetireSource(ctx context.Context, t *commandTurn, before mgSourceFacts, request string) (out mgSourceResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgSourcePurpose(t, MGRetireSource); err != nil {
		return out, err
	}

	if !mgSourceText(request, true, 256) {
		return out, ErrInvalid
	}
	if err = mgSourceBefore(ctx, t, before); err != nil {
		return out, err
	}
	now, err := mgSourceTime(ctx, t)
	if err != nil {
		return out, err
	}
	after := before
	after.revision++
	after.state = "disabled"
	after.removed = 1
	after.removalRequest = sql.NullString{String: request, Valid: true}
	after.removalRevision = sql.NullInt64{Int64: after.revision, Valid: true}
	after.removedAt = sql.NullString{String: time.UnixMilli(now).UTC().Format("2006-01-02T15:04:05.000Z"), Valid: true}
	if err = mgSourceChange(ctx, t, mgSourceRemoval, request, after.revision, after.removedAt.String, before.id); err != nil {
		return out, err
	}
	// Identity removal is an immediate deny fence. The source/head/generations,
	// allocations and captures are retained until their real owners retire them.
	return mgSourceAdvance(ctx, t, before, after)
}

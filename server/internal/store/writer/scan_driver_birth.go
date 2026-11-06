package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	scanBirthRun       stmt.ID = "scan.issuer.birth_run"
	scanBirthJob       stmt.ID = "scan.issuer.birth_job"
	scanBirthOperation stmt.ID = "scan.issuer.birth_operation"
	scanBirthActive    stmt.ID = "scan.issuer.birth_active"
)

// No caller flag/UUID confers birth. This one original is allocated by the
// actual driver, bound to this opening and funded preparation, and lost across
// restart. Native absence is checked only on its exact protected create turn.
type scanBirth struct {
	driver      *ScanDriver
	runID       string
	identity    CommandIdentity
	command     *inventoryCreateCommand
	preparation *scanPreparation
}

func (d *ScanDriver) allocateBirth(ctx context.Context) (out *scanBirth, err error) {
	lifetime := ctx
	if d != nil && d.self == d {
		lifetime = d.lifetime
	}
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if d.birth != nil || d.work != nil {
		return nil, ErrBusy
	}
	var raw [48]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return nil, err
	}
	b := &scanBirth{driver: d, runID: hex.EncodeToString(raw[:16])}
	copy(b.identity.Incarnation[:], raw[16:32])
	copy(b.identity.OperationID[:], raw[32:])
	b.identity.Sequence = 1
	d.birth = b
	return b, nil
}

// Source values are inert until the same protected create turn revalidates
// them. The root DIRECTORY identity/revision come only from W's private actual
// supervised observation, never from caller values or source.root_identity.
func (d *ScanDriver) prepareBirth(ctx context.Context, b *scanBirth, p *scanPreparation, source scanSourceFacts, mode string) (out *inventoryCreateCommand, err error) {
	lifetime := ctx
	if d != nil && d.self == d {
		lifetime = d.lifetime
	}
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if err = d.current(); err != nil {
		return nil, err
	}
	if b == nil || d.birth != b || b.driver != d || b.command != nil || p == nil || d.preparation != p || p.self != p || p.opening != d.opening || p.runtime != d.writer.effective ||
		p.expected.Key.Domain != uint16(InventoryCreate) || p.expected.Key.ID != b.runID || len(p.expected.Footprint) != 1 || p.expected.Footprint[0].Library != source.libraryID {
		return nil, ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err = p.current(); err != nil {
		return nil, err
	}
	if p.failed || p.closed || p.observing || p.observed == nil {
		return nil, ErrClosed
	}
	page := p.observed
	if string(p.request.RelativePath) != "." || p.request.Cursor != "" || p.request.DirectoryRevision != "" ||
		!bytes.Equal(p.request.Root, source.source.resolvedRoot) || p.request.FollowSymlinks != source.source.followSymlinks ||
		page.Version != 1 || page.ErrorCode != "" || page.Unstable || len(page.Issues) != 0 || page.RootIdentity != source.source.rootIdentity ||
		page.DirectoryIdentity == "" || page.DirectoryRevision == "" {
		return nil, ErrConflict
	}
	out, err = newInventoryCreateCommand(b.identity, inventoryCreateRequest{
		Scope: inventoryScope{runID: b.runID, libraryID: source.libraryID, libraryKey: source.libraryKey}, Source: source.source,
		PolicyRevision: source.policyRevision, ScanMode: mode, RootIdentity: page.DirectoryIdentity, RootRevision: page.DirectoryRevision})
	if err != nil {
		return nil, err
	}
	b.command, b.preparation = out, p
	return out, nil
}

func scanBirthDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	return []stmt.Definition{
		{ID: scanBirthRun, Owner: "store/writer scan birth", CallSites: []string{"store/writer/scan_driver_birth.go"}, Kind: stmt.Read,
			SQL: `SELECT job_id FROM inventory_runs WHERE job_id=?`, Params: []stmt.Param{text("run_id")},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{"lifecycle-new"}}, {Name: "existing", Bindings: []any{"lifecycle-run"}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_runs_1"}, Seeks: []string{"job_id=?"}}},
		{ID: scanBirthJob, Owner: "store/writer scan birth", CallSites: []string{"store/writer/scan_driver_birth.go"}, Kind: stmt.Read,
			SQL: `SELECT id FROM jobs WHERE id=?`, Params: []stmt.Param{text("run_id")},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{"lifecycle-new"}}, {Name: "existing", Bindings: []any{"lifecycle-run"}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_jobs_1"}, Seeks: []string{"id=?"}}},
		{ID: scanBirthOperation, Owner: "store/writer scan birth", CallSites: []string{"store/writer/scan_driver_birth.go"}, Kind: stmt.Read,
			SQL: `SELECT create_operation_id FROM inventory_runs INDEXED BY inventory_runs_create_operation WHERE create_operation_id=?`, Params: []stmt.Param{{Name: "create_operation", Type: stmt.Blob}},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{bytes.Repeat([]byte{5}, 16)}}, {Name: "existing", Bindings: []any{append([]byte{2}, make([]byte, 15)...)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"inventory_runs_create_operation"}, Seeks: []string{"create_operation_id=?"}}},
		{ID: scanBirthActive, Owner: "store/writer scan birth", CallSites: []string{"store/writer/scan_driver_birth.go"}, Kind: stmt.Read,
			SQL: `SELECT source_id FROM inventory_source_active WHERE source_id=?`, Params: []stmt.Param{text("source_id")},
			Cohorts: []stmt.Cohort{{Name: "absent", Bindings: []any{"lifecycle-new-source"}}, {Name: "existing", Bindings: []any{"lifecycle-source"}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_source_active_1"}, Seeks: []string{"source_id=?"}}},
	}
}

func (d *ScanDriver) checkBirthAbsence(ctx context.Context, turn *commandTurn, b *scanBirth) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = d.current(); err != nil {
		return err
	}
	if b == nil || d.birth != b || b.driver != d || b.command == nil || b.preparation == nil || turn == nil || turn.transaction == nil ||
		turn.transaction.writer != d.opening.executor || turn.transaction.descriptor.ID != InventoryCreate {
		return ErrInvalid
	}
	w := d.work
	if w == nil || w.purpose != InventoryCreate || w.submission != scanFrameBound || w.frame != turn.transaction ||
		w.original.Receipt != (ReceiptReference{b.command.identity, b.command.digest}) || w.original.RunID != b.runID || w.preparation != b.preparation {
		return ErrDisclosureDenied
	}
	if err = d.retention.check(turn.transaction.writer, turn.registry); err != nil {
		return err
	}
	if d.writer.effective.registry != turn.registry {
		return missingScanOwnerRetention("scan_owner_retention_registry")
	}
	if err = b.preparation.current(); err != nil {
		return err
	}
	for _, point := range []struct {
		id  stmt.ID
		arg any
	}{
		{scanBirthRun, b.runID}, {scanBirthJob, b.runID}, {scanBirthOperation, b.identity.OperationID[:]}, {scanBirthActive, b.command.request.Source.id},
	} {
		step, ok := turn.steps[point.id]
		if !ok || step.kind != stmt.Read {
			return invalid("scan_birth_steps")
		}
		rows, e := turn.query(ctx, step.statementStep, point.arg)
		if e != nil {
			return e
		}
		exists := rows.Next()
		e = errors.Join(rows.Err(), rows.Close())
		if e != nil {
			return e
		}
		// Even a matching original is history, not absence. Missing one part of a
		// partial owner cannot be repaired or replaced by this exception.
		if exists {
			return ErrResolveRequired
		}
	}
	return nil
}

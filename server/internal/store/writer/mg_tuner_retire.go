package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	MGTunerRetireAllocation     CommandID = 816
	MGTunerReconcileAllocations CommandID = 817
)

func mgTunerRetireDescriptors() []Descriptor {
	return []Descriptor{
		{MGTunerRetireAllocation, "mg.tuner.retire", Normal},
		{MGTunerReconcileAllocations, "mg.tuner.reconcile", Normal},
	}
}

const (
	mgTunerRetire         stmt.ID = "writer.mg.tuner.retire"
	mgTunerReconcileNext  stmt.ID = "writer.mg.tuner.reconcile-next"
	mgTunerReconcileLimit int64   = 8
)

// A retire observation is the MP trusted bridge's already-joined physical root,
// never a caller boolean, lease deadline, cancellation or ACK. The allocation
// tuple is the captured held row; the physical key must be the same resource and
// execution token so an exited stream cannot retire another stream's row.
type mgTunerRetireRequest struct {
	allocation  mgTunerAllocation
	key         mpPhysicalKey
	observation *mpPhysicalObservation
}

// release is the sealed MP refund token. It is valid only after this turn
// commits, and only for the exact observation that proved the physical exit.
// Replay of an already released tuple carries no token and no effects.
type mgTunerRetireResult struct {
	allocation mgTunerAllocation
	release    *mpPhysicalRelease
}

// One bounded debt row. Expired lease is debt, never release proof: the row
// keeps its state and capacity until a joined physical exit retires it.
type mgTunerDebt struct {
	id, channel, state, token, account, profile string
	generation, lease                           int64
}

// page is a bounded prefix over the (channel,id) reconcile stream. more reports
// that at least one further held row exists; cursor is the last row returned and
// is unchanged when the page is empty, so a dropped wake cannot skip debt.
type mgTunerReconcileResult struct {
	debt          []mgTunerDebt
	cursorChannel string
	cursorID      string
	more          bool
	expired       int64
	applied       int64
}

// One bounded sweep request. The cursor is the exact last (channel,id) already
// reported; an empty cursor starts the page.
type mgTunerReconcileRequest struct {
	source, afterChannel, afterID string
	limit                         int64
}

// Maintenance custody for retire/reconcile. It is deliberately separate from
// mgSourceAcquisition: the source head, enabled state and viewer access are not
// re-granted by a cleanup stamp, and a denied viewer cannot launder retirement
// through it. Only a retained claim plus the captured owner tuple can retire,
// and only a joined MP physical observation proves the exit.
type mgTunerMaintenance struct {
	self  *mgTunerMaintenance
	turn  *commandTurn
	frame *mgSourceFrame
}

func mgTunerRetireDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	pk := stmt.AutoIndexBinding{Table: "live_allocations", Name: "sqlite_autoindex_live_allocations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	defs := []stmt.Definition{
		// The whole captured tuple is the CAS: owner, token, generation, state and
		// lease must still be exactly the held row being retired.
		{ID: mgTunerRetire, Kind: stmt.Write, SQL: `UPDATE live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 SET state='released' WHERE id=? AND source_id=? AND channel_id=? AND resource_id=? AND kind=? AND authority=? AND account_id=? AND profile_id=? AND token=? AND generation=? AND state=? AND lease_until_ms=? AND created_ms=? AND preempt_at_ms=? RETURNING ` + mgTunerColumns,
			Params: []stmt.Param{text("id"), text("source"), text("channel"), text("resource"), text("kind"), text("authority"), text("account"), text("profile"), text("token"), integer("generation"), text("state"), integer("lease"), integer("created"), integer("preempt")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_allocations_1"}, AutoIndexes: []stmt.AutoIndexBinding{pk}}},
		// One state stream of the existing (source,state,channel,id) index. The
		// caller merges the two held states and stops at the bounded page; the
		// ORDER BY is the index suffix, never a temporary B-tree.
		{ID: mgTunerReconcileNext, Kind: stmt.Read, SQL: `SELECT id,channel_id,state,lease_until_ms,token,generation,account_id,profile_id FROM live_allocations INDEXED BY live_allocations_source_state_channel WHERE source_id=? AND state=? AND channel_id>=? AND (channel_id>? OR id>?) ORDER BY channel_id,id LIMIT 1`,
			Params: []stmt.Param{text("source"), text("state"), text("after_channel"), text("after_channel_floor"), text("after_id")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"live_allocations_source_state_channel"}}},
	}
	for i := range defs {
		d := &defs[i]
		d.Owner = "writer.mg.tuner.retire"
		d.CallSites = []string{"writer/mg_tuner_retire.go"}
		args := make([]any, len(d.Params))
		for j, p := range d.Params {
			if p.Type == stmt.Text {
				args[j] = "missing"
			} else {
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "captured-held-tuple", Bindings: args}}
	}
	return defs
}

func openMGTunerMaintenance(ctx context.Context, t *commandTurn, p *mgActorProof, life context.Context, source string, b mgAuthorityBudget) (out *mgTunerMaintenance, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !b.maintenance || p == nil || life == nil || life.Err() != nil {
		return nil, ErrDisclosureDenied
	}
	switch tID(t) {
	case MGTunerRetireAllocation, MGTunerReconcileAllocations:
	default:
		return nil, ErrInvalid
	}
	f, err := openMGSourceFrame(ctx, t, p, life, source, b)
	if err != nil {
		return nil, err
	}
	out = &mgTunerMaintenance{turn: t, frame: f}
	out.self = out
	return out, nil
}

func (m *mgTunerMaintenance) recheck(ctx context.Context, t *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if m == nil || m.self != m || m.turn != t || m.frame == nil {
		return ErrDisclosureDenied
	}
	return m.frame.recheck(ctx, t)
}

// The captured tuple's own account/profile is the owner. It is compared with the
// independently re-prepared current actor witness, never with a caller fact
// struct. A source head or viewer-access change is irrelevant here; a revoked or
// replaced principal is not.
func mgTunerCapturedOwner(m *mgTunerMaintenance, a mgTunerAllocation) bool {
	if m == nil || m.self != m || m.frame == nil || m.frame.actor == nil {
		return false
	}
	i := m.frame.actor.proof.native.witness.issuer
	return a.authority == "local" && a.account == i.account && a.profile == i.profile
}

func freezeMGTunerRetire(r mgTunerRetireRequest) (mgTunerRetireRequest, error) {
	a := r.allocation
	if !a.valid() {
		return mgTunerRetireRequest{}, ErrInvalid
	}
	if a.state == "released" {
		return r, nil
	}
	if a.state != "active" && a.state != "preempting" {
		return mgTunerRetireRequest{}, ErrInvalid
	}
	if !r.key.valid() || r.key.media != a.resource || r.key.execution != a.token {
		return mgTunerRetireRequest{}, ErrInvalid
	}
	return r, nil
}

// Retires one exactly captured allocation, and refunds its physical exclusion
// only when the MP bridge proves the joined process exit in this same turn. A
// lease deadline, cancellation or ACK alone proves nothing. The original result
// is the released tuple; a repeat for the same released tuple is a read-only
// replay and is never a second refund.
func retireMGTuner(ctx context.Context, m *mgTunerMaintenance, s *mpPhysicalSupervisor, r mgTunerRetireRequest) (out mgTunerRetireResult, err error) {
	var t *commandTurn
	if m != nil && m.self == m {
		t = m.turn
	}
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if m == nil || m.self != m || m.turn == nil || m.frame == nil || t == nil {
		return out, ErrDisclosureDenied
	}
	if err = mgTunerPurpose(t, MGTunerRetireAllocation); err != nil {
		return out, err
	}
	if err = m.recheck(ctx, t); err != nil {
		return out, err
	}
	if r, err = freezeMGTunerRetire(r); err != nil {
		return out, err
	}
	a := r.allocation
	if a.source != m.frame.source {
		return out, ErrConflict
	}
	if !mgTunerCapturedOwner(m, a) {
		return out, ErrDisclosureDenied
	}
	// Retired replay: the stored original is released only while the exact tuple
	// is still the row and the captured owner is current.
	if a.state == "released" {
		got, e := readMGTuner(ctx, t, mgTunerRead, a.id)
		if e != nil {
			return out, mgTunerMissing(e)
		}
		if got != a {
			return out, ErrConflict
		}
		return mgTunerRetireResult{allocation: got}, nil
	}
	if s == nil || r.observation == nil {
		return out, ErrDisclosureDenied
	}
	// A stale release observation never releases a live row: the captured tuple
	// must still be the exact native row and its kind/resource must still name
	// only this allocation.
	before, err := readMGTuner(ctx, t, mgTunerRead, a.id)
	if err != nil {
		return out, mgTunerMissing(err)
	}
	if before != a {
		return out, ErrConflict
	}
	shared, err := readMGTuner(ctx, t, mgTunerResource, a.kind, a.resource)
	if err != nil {
		return out, mgTunerMissing(err)
	}
	if shared != a {
		return out, ErrConflict
	}
	// The MP bridge binds this turn, this command, this joined execution and the
	// current native stop/exit row. It is the only physical exit proof.
	v, err := consumeMPPhysicalObservation(ctx, t, s, r.observation, r.key)
	if err != nil {
		return out, err
	}
	row, err := mgTunerRow(ctx, t, mgTunerRetire, a.values()...)
	if err != nil {
		return out, err
	}
	changed, err := scanMGTuner(row)
	if err != nil {
		return out, mgTunerMissing(err)
	}
	want := a
	want.state = "released"
	if changed != want {
		return out, ErrConflict
	}
	got, err := readMGTuner(ctx, t, mgTunerRead, a.id)
	if err != nil {
		return out, mgTunerMissing(err)
	}
	if got != want {
		return out, ErrConflict
	}
	release, err := sealMPPhysicalRelease(ctx, t, v)
	if err != nil {
		return out, err
	}
	// The original result is only released while the captured owner is current.
	if err = m.recheck(ctx, t); err != nil {
		return out, err
	}
	return mgTunerRetireResult{allocation: want, release: release}, nil
}

func freezeMGTunerReconcile(r mgTunerReconcileRequest) (mgTunerReconcileRequest, error) {
	if !mgSourceText(r.source, true, 256) || r.limit <= 0 || r.limit > mgTunerReconcileLimit {
		return mgTunerReconcileRequest{}, ErrInvalid
	}
	if r.afterChannel == "" && r.afterID != "" {
		return mgTunerReconcileRequest{}, ErrInvalid
	}
	if r.afterChannel != "" && (!mgSourceText(r.afterChannel, true, 256) || !mgSourceText(r.afterID, true, 256)) {
		return mgTunerReconcileRequest{}, ErrInvalid
	}
	return r, nil
}

// One state stream at a cursor. Absence is sql.ErrNoRows. A legacy empty
// channel key sorts first and cannot be skipped as though it were reclaimable,
// so it refuses instead of silently hiding debt.
func nextMGTunerDebt(ctx context.Context, t *commandTurn, source, state, channel, id string) (out mgTunerDebt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mgTunerRow(ctx, t, mgTunerReconcileNext, source, state, channel, channel, id)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.id, &out.channel, &out.state, &out.lease, &out.token, &out.generation, &out.account, &out.profile); err != nil {
		return out, err
	}
	if !mgSourceText(out.id, true, 256) || !mgSourceText(out.channel, true, 256) || out.state != state || out.state == "" ||
		!mgSourceText(out.token, true, 256) || !mgSourceText(out.account, true, 256) || !mgSourceText(out.profile, true, 256) ||
		out.generation <= 0 || out.lease < 0 {
		return mgTunerDebt{}, ErrConflict
	}
	if out.channel < channel || out.channel == channel && out.id <= id {
		return mgTunerDebt{}, ErrInvalid
	}
	return out, nil
}

// Reports a bounded, ordered page of still-held tuner debt for one source. It
// never releases or refunds: an expired lease stays held until a joined physical
// exit retires it. The cursor is the last row returned, so a dropped wake can be
// repeated without skipping or duplicating rows.
func reconcileMGTuners(ctx context.Context, m *mgTunerMaintenance, r mgTunerReconcileRequest) (out mgTunerReconcileResult, err error) {
	var t *commandTurn
	if m != nil && m.self == m {
		t = m.turn
	}
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if m == nil || m.self != m || m.turn == nil || m.frame == nil || t == nil {
		return out, ErrDisclosureDenied
	}
	if err = mgTunerPurpose(t, MGTunerReconcileAllocations); err != nil {
		return out, err
	}
	if err = m.recheck(ctx, t); err != nil {
		return out, err
	}
	if r, err = freezeMGTunerReconcile(r); err != nil {
		return out, err
	}
	if r.source != m.frame.source {
		return out, ErrConflict
	}
	// A sweep reads other viewers' captured owners; a plain viewer may not.
	switch m.frame.actor.proof.kind {
	case mgActorOwner, mgActorAdminViewer:
	default:
		return out, ErrDisclosureDenied
	}
	now, err := mgTunerTime(ctx, t)
	if err != nil {
		return out, err
	}
	out.applied = now
	out.cursorChannel, out.cursorID = strings.Clone(r.afterChannel), strings.Clone(r.afterID)
	full := false
	for int64(len(out.debt)) < r.limit {
		var next mgTunerDebt
		found := false
		for _, state := range []string{"active", "preempting"} {
			row, e := nextMGTunerDebt(ctx, t, r.source, state, out.cursorChannel, out.cursorID)
			if errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return out, e
			}
			if !found || row.channel < next.channel || row.channel == next.channel && row.id < next.id {
				next, found = row, true
			}
		}
		if !found {
			break
		}
		out.debt = append(out.debt, next)
		out.cursorChannel, out.cursorID = strings.Clone(next.channel), strings.Clone(next.id)
		full = int64(len(out.debt)) == r.limit
	}
	// A full page is a lower bound. Probe once past the cursor so pending is
	// reported only when a further held row really exists.
	if full {
		for _, state := range []string{"active", "preempting"} {
			_, e := nextMGTunerDebt(ctx, t, r.source, state, out.cursorChannel, out.cursorID)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return out, e
			}
			out.more = out.more || e == nil
		}
	}
	for _, d := range out.debt {
		if d.lease <= now {
			out.expired++
		}
	}
	if err = m.recheck(ctx, t); err != nil {
		return out, err
	}
	return out, nil
}

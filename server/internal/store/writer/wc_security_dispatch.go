package writer

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const wcSecurityHeaderBytes stmt.ID = "wc.security.header_bytes"
const wcSecurityCurrentDeny stmt.ID = "wc.security.current_deny"

func wcSecurityCommandDefinitions() []stmt.Definition {
	defs := append(wcSecurityDefinitions(), wcSecurityProtocolDefinitions()...)
	defs = append(defs, wcAPIEventDefinitions()...)
	for _, d := range wcProgressProtocolDefinitions() {
		switch d.ID {
		case wcProgressNativeScope, wcProgressNativeProfileScope, wcProgressNativeToken, wcProgressNativeEvent:
			defs = append(defs, d)
		}
	}
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	for _, d := range []stmt.Definition{
		{ID: wcSecurityHeaderBytes, Kind: stmt.Read, SQL: `SELECT control_variable_bytes FROM authorization_session_families WHERE id=? AND server_id=? AND authority='local'`, Params: []stmt.Param{text("family"), text("server")}, Cohorts: []stmt.Cohort{{Name: "before_header_copy", Bindings: []any{"target", "server"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}}},
		{ID: wcSecurityCurrentDeny, Kind: stmt.Read, SQL: `SELECT revoked_reason,revoked_at,teardown_due_ms FROM authorization_session_families WHERE id=? AND server_id=? AND authority='local'`, Params: []stmt.Param{text("family"), text("server")}, Cohorts: []stmt.Cohort{{Name: "current_deny_fence", Bindings: []any{"target", "server"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}}},
	} {
		d.Owner = "store/writer WC security composition"
		d.CallSites = []string{"store/writer/wc_security_dispatch.go"}
		defs = append(defs, d)
	}
	return defs
}

func wcSecurityMemoryBytes(v *wcSecurityInvocation) uint64 {
	n := uint64(64 << 10)
	s := v.disclosure.driver.scope
	for _, x := range []string{s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.familyID, s.serverID, s.tokenHash, s.memberLibraries, s.profileLibraries.String, v.request.FamilyID} {
		n += uint64(len(x)) * 32
	}
	return n
}

// The native actor query is the same closed ordinary-family proof used by
// progress. Security additionally requires the current primary OWNER/ADMIN;
// it does not borrow progress's library permission as an administration grant.
func checkWCSecurityNativeDisclosure(ctx context.Context, t *commandTurn, v *wcSecurityInvocation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if err = checkWCSecurityDisclosure(v.disclosure.driver.writer, v); err != nil {
		return err
	}
	base := wcSecurityMemoryBytes(v)
	if err = t.requireCommandMemory(ctx, SecurityAccept, base, "wc_security_memory"); err != nil {
		return err
	}
	s := v.disclosure.driver.scope
	n, e := wc008DraftInteger(ctx, t, wcSecurityHeaderBytes, v.request.FamilyID, s.serverID)
	if e != nil || n <= 0 {
		return errors.Join(ErrOwnerUnavailable, e)
	}
	if uint64(n) > (^uint64(0)-base)/32 {
		return ErrCap
	}
	if err = t.requireCommandMemory(ctx, SecurityAccept, base+uint64(n)*32, "wc_security_memory"); err != nil {
		return err
	}
	id := wcProgressNativeScope
	args := []any{s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.memberLibraries, s.memberRevision, s.profileRevision, s.membershipRole, s.primary, s.primaryProfile}
	if s.profileLibraries.Valid {
		id = wcProgressNativeProfileScope
		args = append(args, s.profileLibraries.String)
	}
	row, e := wc008DraftQuery(ctx, t, id, args...)
	if e != nil {
		return e
	}
	var epoch, primary int64
	if e = row.Scan(&epoch, &primary); e != nil {
		return errors.Join(ErrDisclosureDenied, e)
	}
	if epoch != s.epoch || primary != 1 || s.role != s.membershipRole || (s.role != "owner" && s.role != "admin") {
		return ErrDisclosureDenied
	}
	row, e = wc008DraftQuery(ctx, t, wcProgressNativeToken, s.tokenHash, s.familyID, s.serverID, s.accountID, s.profileID, s.role, s.epoch, s.tokenGeneration, s.memberBirth, s.profileBirth, s.horizon, s.expires)
	if e != nil {
		return e
	}
	var generation int64
	if e = row.Scan(&generation); e != nil {
		return errors.Join(ErrDisclosureDenied, e)
	}
	horizon, e := time.Parse(time.RFC3339Nano, s.horizon)
	if e != nil {
		return ErrDisclosureDenied
	}
	expires, e := time.Parse(time.RFC3339Nano, s.expires)
	if e != nil {
		return ErrDisclosureDenied
	}
	deadline, ok := t.transaction.ctx.Deadline()
	if !ok || generation != s.tokenGeneration || deadline.After(horizon) || deadline.After(expires) {
		return ErrDisclosureDenied
	}
	target, e := readWCSecurityOwner(ctx, t, v.request.FamilyID, s.serverID)
	if e != nil {
		return e
	}
	tier, e := readWCSecurityTargetTier(ctx, t, target)
	if e != nil {
		return e
	}
	if !wcSecurityTargetTierAllowed(s.role, tier) {
		return ErrDisclosureDenied
	}
	v.current = wcSecurityCurrentActor{wc008IssuerScope{s.accountID, s.profileID, s.familyID, s.memberBirth, s.profileBirth}, s.role}
	return t.checkLifetimes(ctx)
}

type wcSecurityCompletion struct {
	out        SecurityResult
	ack        AckResult
	historical bool
}

func executeWCSecurityInvocation(ctx context.Context, t *commandTurn, v *wcSecurityInvocation, ack bool) (out SecurityResult, floor AckResult, historical bool, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), wcSecurityCompletion{out, floor, historical}, err)
		out, floor, historical, err = x.out, x.ack, x.historical, e
	}()
	if v.snapshot {
		if ack || v.input != nil {
			return out, floor, false, ErrInvalid
		}
		s, e := readWCSecurityOwner(ctx, t, v.request.FamilyID, v.disclosure.driver.scope.serverID)
		if e != nil {
			return out, floor, false, e
		}
		out.Snapshot = SecuritySnapshot{FamilyID: s.family, Last: s.last, Acknowledged: s.acknowledged, Revoked: s.revoked, ControlBytes: s.controlBytes, source: s}
		row, e := wc008DraftQuery(ctx, t, wcSecurityCurrentDeny, s.family, s.server)
		if e != nil {
			return out, floor, false, e
		}
		if e = row.Scan(&out.Snapshot.Reason, &out.Snapshot.RevokedAt, &out.Snapshot.TeardownDue); e != nil {
			return out, floor, false, e
		}
		row, e = wc008DraftQuery(ctx, t, wcProgressNativeEvent, apievents.AdminAudience, int64(1))
		if e != nil {
			return out, floor, false, e
		}
		var event int64
		e = row.Scan(&event)
		if errors.Is(e, sql.ErrNoRows) {
			e = nil
		} else if e == nil {
			out.Snapshot.LastEvent = sql.NullInt64{Int64: event, Valid: true}
		}
		return out, floor, true, e
	}
	lookup, e := lookupWCSecurity(ctx, t, v.request, v.disclosure.driver.scope.serverID)
	if e != nil {
		return out, floor, false, e
	}
	if ack {
		floor.AcknowledgedThrough, err = acknowledgeWCSecurity(ctx, t, v.request, lookup)
		return out, floor, true, err
	}
	out.Outcome.Reference = v.request.Receipt
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, floor, true, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.value, lookup.digest
		return out, floor, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, floor, false, ErrResolveRequired
	}
	if v.input == nil {
		return out, floor, false, nil
	}
	q, e := securityReference(v.input)
	if e != nil || q != v.request || v.input.driver != v.disclosure.driver {
		return out, floor, false, errors.Join(ErrConflict, e)
	}
	now, e := t.commandTimeMS(ctx, SecurityAccept, 0)
	if e != nil {
		return out, floor, false, e
	}
	applied, e := applyWCSecurity(ctx, t, v.input.prepared, v.current, now)
	if e != nil {
		return out, floor, false, e
	}
	out.Value, out.Digest = applied.value, applied.digest
	return out, floor, false, nil
}

func wcSecurityStatementMaxUses(id stmt.ID) int {
	switch id {
	case wcSecurityNativeOwner:
		return 3
	case wcSecurityNativeResult, wcSecurityNativeTarget:
		return 2
	default:
		return 1
	}
}

// One-time private composition; supplied plans must carry the complete native
// receipt and all forecasts. These component figures never qualify a profile.
func composeWCSecurity(ctx context.Context, r *effectiveRuntime, p effectivePlan) (err error) {
	var runtimeLocked, executorLocked, installed bool
	var oldRegistry *stmt.Registry
	defer func() {
		_, err = finishCommand(ctx, ctx, struct{}{}, err)
		if installed && err != nil {
			r.registry, r.wcSecurityPlan = oldRegistry, nil
		}
		if executorLocked {
			r.executor.mu.Unlock()
		}
		if runtimeLocked {
			r.mu.Unlock()
		}
	}()
	defer func() { _, err = finishCommand(ctx, ctx, struct{}{}, err) }()
	if ctx == nil || r == nil || r.executor == nil {
		return ErrInvalid
	}
	if !r.mu.TryLock() {
		return ErrBusy
	}
	runtimeLocked = true
	if !r.executor.mu.TryLock() {
		return ErrBusy
	}
	executorLocked = true
	if r.fenced || r.executor.closed || r.executor.fenced || r.executor.ambiguous {
		return ErrClosed
	}
	if r.wcSecurityPlan != nil {
		return ErrBusy
	}
	defs := wcSecurityCommandDefinitions()
	p, err = freezeCommandPlan(r.executor, p, defs, SecurityAccept)
	if err != nil {
		return err
	}
	if p.wcSecurityMemory == (commandMemoryBudget{}) {
		return &Refusal{Code: RefusalUnqualified}
	}
	all := []stmt.Definition{}
	for _, e := range r.registry.Manifest() {
		all = append(all, e.Definition)
	}
	// Shared scope/event points are compiled only once and must be identical.
	for _, d := range defs {
		found := false
		for _, old := range all {
			if old.ID == d.ID {
				if !reflect.DeepEqual(old, d) {
					return ErrConflict
				}
				found = true
				break
			}
		}
		if !found {
			all = append(all, d)
		}
	}
	receipts := []stmt.Receipt{r.plan.registration, p.registration}
	for _, p := range []*effectivePlan{r.inventoryPlan, r.wcProgressPlan, r.wcProgressBirthPlan, r.lifecyclePlans[0], r.lifecyclePlans[1], r.issuerPlans[0], r.issuerPlans[1]} {
		if p != nil {
			receipts = append(receipts, p.registration)
		}
	}
	registry, e := stmt.New(all, receipts...)
	if e != nil {
		return e
	}
	if err = errors.Join(ctx.Err(), r.authority.VerifyComposition(r.governor, r.wal)); err != nil {
		return err
	}
	oldRegistry = r.registry
	r.registry, r.wcSecurityPlan = registry, &p
	installed = true
	if err = ctx.Err(); err != nil {
		r.registry, r.wcSecurityPlan = oldRegistry, nil
	}
	return err
}

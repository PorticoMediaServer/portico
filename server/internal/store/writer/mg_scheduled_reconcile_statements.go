package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MG-12 bounded scheduled-intent reconciliation. One keyset window on the
// existing dvr_recordings_rule(rule_id,start_ms,id) index (at most eight
// complete not-yet-launched intents plus one lookahead row), exact per-row CAS,
// the matching reservation disable and any still-owned source dependency. No
// provider, tuner, capture, allocation, artifact or filesystem statement
// appears here. The state set is the legacy future-intent cohort MG-11's delete
// detaches for keep; this frame is the bounded cancel/keep reconciler for a
// rule that is already retired.
const (
	mgScheduledIntentWindow      stmt.ID = "writer.mg.scheduled.window"
	mgScheduledIntentRead        stmt.ID = "writer.mg.scheduled.intent-read"
	mgScheduledIntentDetach      stmt.ID = "writer.mg.scheduled.intent-detach"
	mgScheduledIntentCancel      stmt.ID = "writer.mg.scheduled.intent-cancel"
	mgScheduledReservationRead   stmt.ID = "writer.mg.scheduled.reservation-read"
	mgScheduledReservationOff    stmt.ID = "writer.mg.scheduled.reservation-disable"
	mgScheduledDependencyDelete  stmt.ID = "writer.mg.scheduled.dependency-delete"
	mgScheduledOwnerRevisionRead stmt.ID = "writer.mg.scheduled.owner-revision-read"
	mgScheduledOwnerRevisionAdd  stmt.ID = "writer.mg.scheduled.owner-revision-insert"
	mgScheduledOwnerRevisionCAS  stmt.ID = "writer.mg.scheduled.owner-revision-update"
)

const mgScheduledIntentStates = `state IN('scheduled','conflicted','waiting-source','waiting-guide')`

const mgScheduledIntentColumns = `id,owner_key,authority,account_id,profile_id,source_id,revision,state,rule_id,rule_revision,manual,start_ms,end_ms,priority,cancel_generation,claim_token,claim_generation,owner_instance,lease_until_ms,artifact_id,item_id,updated_ms`

// Every mutation re-checks the complete logical row version AND that no capture,
// share or launch custody has appeared since the window read. This is logical
// intent detachment, never a physical claim, cancel or release.
const mgScheduledIntentFence = `id=? AND owner_key=? AND authority='local' AND account_id=? AND profile_id=? AND source_id=? AND revision=? AND state=? AND rule_id=? AND rule_revision=? AND manual=? AND start_ms=? AND end_ms=? AND priority=? AND cancel_generation=? AND claim_token=? AND claim_generation=? AND owner_instance=? AND lease_until_ms=? AND capture_launch_id IS NULL AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL`

func mgScheduledIntentParams() []stmt.Param {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return []stmt.Param{t("id"), t("owner"), t("account"), t("profile"), t("source"), i("revision"), t("state"), t("rule"), i("rule_revision"), i("manual"), i("start"), i("end"), i("priority"), i("cancel_generation"), t("claim_token"), i("claim_generation"), t("owner_instance"), i("lease_until")}
}

func mgScheduledReconcileDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	fence := mgScheduledIntentParams()
	defs := []stmt.Definition{
		{ID: mgScheduledIntentWindow, Kind: stmt.Read,
			SQL: `SELECT ` + mgScheduledIntentColumns + ` FROM dvr_recordings INDEXED BY dvr_recordings_rule
WHERE rule_id=? AND ` + mgScheduledIntentStates + ` AND capture_launch_id IS NULL AND has_dvr_recording_shares=0 AND share_capture_recording_id IS NULL
AND (start_ms>? OR (start_ms=? AND id>?))
ORDER BY start_ms,id LIMIT ?`,
			Params: []stmt.Param{t("rule"), i("cursor_from"), i("cursor_lo"), t("cursor_id"), i("row_limit")}},
		{ID: mgScheduledIntentRead, Kind: stmt.Read,
			SQL:    `SELECT ` + mgScheduledIntentColumns + ` FROM dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 WHERE id=?`,
			Params: []stmt.Param{t("id")}},
		{ID: mgScheduledIntentDetach, Kind: stmt.Write,
			SQL:    `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET manual=1,rule_id='',rule_revision=0,revision=revision+1,updated_ms=? WHERE ` + mgScheduledIntentFence,
			Params: append([]stmt.Param{i("updated")}, fence...)},
		{ID: mgScheduledIntentCancel, Kind: stmt.Write,
			SQL:    `UPDATE dvr_recordings INDEXED BY sqlite_autoindex_dvr_recordings_1 SET state='cancelled',reason=?,cancel_generation=cancel_generation+1,revision=revision+1,updated_ms=?,finished_ms=? WHERE ` + mgScheduledIntentFence,
			Params: append([]stmt.Param{t("reason"), i("updated"), i("finished")}, fence...)},
		{ID: mgScheduledReservationRead, Kind: stmt.Read,
			SQL:    `SELECT source_id,start_ms,end_ms,priority,enabled FROM live_reservations INDEXED BY sqlite_autoindex_live_reservations_1 WHERE id=?`,
			Params: []stmt.Param{t("id")}},
		{ID: mgScheduledReservationOff, Kind: stmt.Write,
			SQL:    `UPDATE live_reservations INDEXED BY sqlite_autoindex_live_reservations_1 SET enabled=0 WHERE id=? AND source_id=? AND start_ms=? AND end_ms=? AND priority=? AND enabled=?`,
			Params: []stmt.Param{t("id"), t("source"), i("start"), i("end"), i("priority"), i("enabled")}},
		{ID: mgScheduledDependencyDelete, Kind: stmt.Write,
			SQL:    `DELETE FROM live_source_dependencies INDEXED BY sqlite_autoindex_live_source_dependencies_1 WHERE source_id=? AND kind='recording' AND id=?`,
			Params: []stmt.Param{t("source"), t("id")}},
		{ID: mgScheduledOwnerRevisionRead, Kind: stmt.Read,
			SQL:    `SELECT revision FROM dvr_owner_revisions INDEXED BY sqlite_autoindex_dvr_owner_revisions_1 WHERE owner_key=?`,
			Params: []stmt.Param{t("owner")}},
		{ID: mgScheduledOwnerRevisionAdd, Kind: stmt.Write,
			SQL:    `INSERT INTO dvr_owner_revisions(owner_key,revision) VALUES(?,?)`,
			Params: []stmt.Param{t("owner"), i("revision")}},
		{ID: mgScheduledOwnerRevisionCAS, Kind: stmt.Write,
			SQL:    `UPDATE dvr_owner_revisions INDEXED BY sqlite_autoindex_dvr_owner_revisions_1 SET revision=revision+1 WHERE owner_key=? AND revision=?`,
			Params: []stmt.Param{t("owner"), i("revision")}},
	}
	pin := func(table, column string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: "sqlite_autoindex_" + table + "_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}}
	}
	for n := range defs {
		d := &defs[n]
		d.Owner = "writer.mg.scheduled"
		d.CallSites = []string{"writer/mg_scheduled_reconcile.go"}
		d.Plan.Hot = true
		switch d.ID {
		case mgScheduledIntentWindow:
			d.Plan.Indexes = []string{"dvr_recordings_rule"}
			d.Plan.Seeks = []string{"rule_id=? AND start_ms>?"}
		case mgScheduledIntentRead, mgScheduledIntentDetach, mgScheduledIntentCancel:
			d.Plan.Indexes = []string{"sqlite_autoindex_dvr_recordings_1"}
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("dvr_recordings", "id")}
			d.Plan.Seeks = []string{"id=?"}
		case mgScheduledReservationRead, mgScheduledReservationOff:
			d.Plan.Indexes = []string{"sqlite_autoindex_live_reservations_1"}
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("live_reservations", "id")}
			d.Plan.Seeks = []string{"id=?"}
		case mgScheduledDependencyDelete:
			d.Plan.Indexes = []string{"sqlite_autoindex_live_source_dependencies_1"}
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("live_source_dependencies", "source_id")}
			d.Plan.Seeks = []string{"source_id=? AND kind=? AND id=?"}
		case mgScheduledOwnerRevisionRead, mgScheduledOwnerRevisionCAS:
			d.Plan.Indexes = []string{"sqlite_autoindex_dvr_owner_revisions_1"}
			d.Plan.AutoIndexes = []stmt.AutoIndexBinding{pin("dvr_owner_revisions", "owner_key")}
			d.Plan.Seeks = []string{"owner_key=?"}
		}
		if d.ID == mgScheduledOwnerRevisionAdd {
			d.Plan = stmt.Plan{}
		}
		args := make([]any, len(d.Params))
		for j, p := range d.Params {
			if p.Type == stmt.Text {
				args[j] = "missing"
			} else {
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-scheduled-key", Bindings: args}}
	}
	return defs
}

func mgScheduledStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgScheduledIntentWindow, mgScheduledIntentRead, mgScheduledIntentDetach, mgScheduledIntentCancel,
		mgScheduledReservationRead, mgScheduledReservationOff, mgScheduledDependencyDelete,
		mgScheduledOwnerRevisionRead, mgScheduledOwnerRevisionAdd, mgScheduledOwnerRevisionCAS:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}

func (f mgScheduledIntentFacts) values() []any {
	return []any{f.id, f.owner, f.account, f.profile, f.source, f.revision, f.state, f.rule, f.ruleRevision,
		f.manual, f.start, f.end, f.priority, f.cancelGeneration, f.claimToken, f.claimGeneration, f.ownerInstance, f.leaseUntil}
}

func mgScheduledIntentScan(row commandRow) (out mgScheduledIntentFacts, err error) {
	err = row.Scan(&out.id, &out.owner, &out.authority, &out.account, &out.profile, &out.source, &out.revision,
		&out.state, &out.rule, &out.ruleRevision, &out.manual, &out.start, &out.end, &out.priority,
		&out.cancelGeneration, &out.claimToken, &out.claimGeneration, &out.ownerInstance, &out.leaseUntil,
		&out.artifact, &out.item, &out.updated)
	if err != nil {
		return mgScheduledIntentFacts{}, err
	}
	if !out.stored() {
		return mgScheduledIntentFacts{}, ErrInvalid
	}
	return out, nil
}

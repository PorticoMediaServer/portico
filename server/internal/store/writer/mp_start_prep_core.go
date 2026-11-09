package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MP start-preparation birth/claim/seal (packet MP-07).
//
// This file owns ONLY the MP half of commands 700-702: bounded source/plan
// validation, the preparation/lifecycle/execution rows, exact claim and seal
// CAS, and the MP start receipt root. Accept, retire, timeout and cleanup are
// MP-08 (703-705) and are deliberately absent here; helper work never starts
// from this frame.
//
// The UO side of 700 is UO-23 (DESK-B) and does not exist yet: UO-01's root
// binding is under a blocking review, so this frame never reads
// uo_root_binding.go, never mints a user root or a second root graph, and never
// fabricates an Applied birth driver. mp_start_prep_births.go names the one
// private seam where UO hands MP its freshly admitted root, and refuses when
// that witness is absent.
//
// 701 writes one receipt on the preparation owner. The adopted F schema allows
// at most ONE unacknowledged receipt per owner, so 702 needs the 701 receipt
// acknowledged first; command 780 (MP-29) owns that ACK and is not written yet.
// The tests simulate the ACK floor directly, exactly as the landed MP-04 frame
// does for its own receipt root. Nothing here mints Applied outside W's commit.

const (
	MPStartPrepBegin CommandID = 700
	MPStartPrepClaim CommandID = 701
	MPStartPrepSeal  CommandID = 702
)

type mpPrepActor struct {
	server, authority, account, profile, device, family, grant, sessionHash string
	epoch, tokenGeneration, horizonMS                                       int64
}

func (a mpPrepActor) valid(now int64) bool {
	return mpPrepTexts(a.server, a.authority, a.account, a.profile, a.device, a.family, a.grant) &&
		lowerHex(a.sessionHash, 64) && a.epoch >= 0 && a.tokenGeneration > 0 && a.horizonMS > now
}

func (a mpPrepActor) args() []any {
	return []any{a.server, a.authority, a.account, a.profile, a.device, a.family, a.grant, a.sessionHash, a.epoch, a.tokenGeneration, a.horizonMS}
}

// Complete selected inventory/configuration witness. Fresh turns supply the
// current effective policy/authority witnesses; a saved preparation never
// reconstructs them and native object/source/asset CAS is checked as well.
type mpPrepSource struct {
	library, source, asset, object, revision, root, incarnation, remote, configuration, policy, authority string
	libraryGeneration, configurationGeneration, profileRevision, size, modifiedNS                         int64
}

func (s mpPrepSource) valid() bool {
	return mpPrepTexts(s.library, s.source, s.asset, s.object, s.revision, s.root, s.incarnation, s.configuration, s.policy, s.authority) &&
		mpPrepText(s.remote, true) && s.libraryGeneration > 0 && s.configurationGeneration > 0 &&
		s.profileRevision >= 0 && s.size > 0 && s.modifiedNS >= 0
}

func (s mpPrepSource) args() []any {
	return []any{s.library, s.source, s.asset, s.object, s.revision, s.root, s.incarnation, s.remote,
		s.configuration, s.policy, s.authority, s.libraryGeneration, s.configurationGeneration,
		s.profileRevision, s.size, s.modifiedNS}
}

type mpPrepQueue struct {
	id, entry, ownerKind, ownerID string
	revision, custody             int64
}

func (q mpPrepQueue) valid() bool {
	if q.id == "" {
		return q == (mpPrepQueue{})
	}
	return mpPrepTexts(q.id, q.entry, q.ownerKind, q.ownerID) && q.revision > 0 && q.custody >= 0
}

func (q mpPrepQueue) args() []any {
	return []any{q.id, q.entry, q.ownerKind, q.ownerID, q.revision, q.custody}
}

type mpStartPrepStableIDs struct {
	session, legacySession, snapshot string
	legacyGeneration                 int64
}

func (v mpStartPrepStableIDs) valid() bool {
	return mpPrepTexts(v.session, v.legacySession, v.snapshot) && v.legacyGeneration > 0
}

// mpStartPrepFence is the exact fresh CAS witness for claim/seal. It carries no
// saved result, authority or exit proof: the current disclosure reads every
// stored row again and the native actor/source/item/queue proofs run in the
// SAME turn.
type mpStartPrepFence struct {
	id, execution   string
	revision        int64
	actor           mpPrepActor
	source          mpPrepSource
	queue           mpPrepQueue
	sourceWitnessed bool
}

func (f mpStartPrepFence) valid() bool {
	return mpPrepTexts(f.id) && mpPrepRevision(f.revision) && f.actor.valid(0) && f.source.valid() && f.queue.valid() && f.sourceWitnessed
}

type mpStartPrepCompletion struct {
	execution, resultDigest string
	stagedRows, stagedBytes int64
}

func (c mpStartPrepCompletion) valid() bool {
	return mpPrepTexts(c.execution) && lowerHex(c.resultDigest, 64) &&
		c.stagedRows > 0 && c.stagedRows <= 256 && c.stagedBytes > 0 && c.stagedBytes <= 1<<20
}

// mpStartPrepValue is the complete immutable result of one MP start-preparation
// command. PhysicalPending is never set here: no helper exists before claim,
// and no exit is proved by this frame.
type mpStartPrepValue struct {
	Preparation, State, Execution, Session, LegacySession, Snapshot string
	Revision, DeadlineMS, LegacyGeneration, AppliedMS               int64
	Sealed                                                          bool
}

func (v mpStartPrepValue) valid(kind CommandID) bool {
	if !mpPrepTexts(v.Preparation, v.State) || !mpPrepText(v.Execution, true) ||
		!mpPrepRevision(v.Revision) || v.DeadlineMS <= 0 || v.AppliedMS <= 0 {
		return false
	}
	if v.Sealed {
		if v.Execution == "" {
			return false
		}
	} else if v.Execution != "" || v.State != "pending" {
		return false
	}
	switch kind {
	case MPStartPrepBegin:
		return v.State == "pending" && v.Revision == 1 && !v.Sealed && v.Execution == ""
	case MPStartPrepClaim:
		return v.State == "preparing" && v.Sealed && v.Execution != ""
	case MPStartPrepSeal:
		return v.State == "prepared" && v.Sealed && v.Execution != ""
	}
	return false
}

func mpPrepText(s string, empty bool) bool {
	if (!empty && s == "") || len(s) > 4096 || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func mpPrepTexts(v ...string) bool {
	for _, s := range v {
		if !mpPrepText(s, false) {
			return false
		}
	}
	return true
}

func mpPrepRevision(v int64) bool { return v > 0 && v < math.MaxInt64 }

func mpPrepTurn(t *commandTurn, kind CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != kind {
		return ErrInvalid
	}
	return nil
}

func mpPrepRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if t == nil {
		return commandRow{}, ErrInvalid
	}
	s, ok := t.steps[id]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return t.queryRow(ctx, s.statementStep, args...)
}

func mpPrepOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	r, e := mpPrepRow(ctx, t, id, args...)
	if e != nil {
		return e
	}
	var one int64
	if e = r.Scan(&one); errors.Is(e, sql.ErrNoRows) {
		return ErrConflict
	}
	if e != nil {
		return e
	}
	if one != 1 {
		return ErrResolveRequired
	}
	return nil
}

// Native relationship supplement to W's current opaque disclosure, never a
// replacement for membership/profile/item restriction and horizon checks.
func checkMPPrepActor(ctx context.Context, t *commandTurn, a mpPrepActor, now int64) error {
	if !a.valid(now) {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.start.actor_current", a.sessionHash, a.family, a.server, a.authority, a.account, a.profile, a.device, a.epoch, a.tokenGeneration)
}

func checkMPPrepSource(ctx context.Context, t *commandTurn, s mpPrepSource) error {
	if !s.valid() {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.start.source_current", s.object, s.source, s.asset, s.revision, s.incarnation, s.size, s.modifiedNS, s.library, s.root, s.libraryGeneration, s.remote)
}

func checkMPPrepPublicItem(ctx context.Context, t *commandTurn, item string, part int64, s mpPrepSource) error {
	if part < 0 || !mpPrepTexts(item) {
		return ErrInvalid
	}
	raw, ok := entityid.Decode(item)
	if !ok || len(raw) != 16 {
		return ErrInvalid
	}
	return mpPrepOne(ctx, t, "mp.start.public_item_current", raw, s.library, s.asset, part)
}

func checkMPPrepQueue(ctx context.Context, t *commandTurn, q mpPrepQueue, a mpPrepActor) error {
	if !q.valid() {
		return ErrInvalid
	}
	if q.id == "" {
		return nil
	}
	return mpPrepOne(ctx, t, "mp.start.queue_current", q.id, q.ownerKind, q.ownerID, q.revision, q.custody, q.entry, a.authority, a.account, a.profile)
}

// Closed SQL catalog helper. Callers supply compiled SQL and native parameter
// types, never runtime identifiers or generic transition strings.
func mpPrepDefinition(id, sqlText, types string, kind stmt.Kind) stmt.Definition {
	p := make([]stmt.Param, len(types))
	b := make([]any, len(types))
	for i, c := range types {
		p[i].Name = "p" + strconv.Itoa(i)
		switch c {
		case 's':
			p[i].Type = stmt.Text
			b[i] = "fixture"
		case 'i':
			p[i].Type = stmt.Integer
			b[i] = int64(1)
		case 'b':
			p[i].Type = stmt.Blob
			b[i] = make([]byte, 16)
		}
	}
	return stmt.Definition{ID: stmt.ID(id), SQL: sqlText, Kind: kind, Params: p, Owner: "store/writer MP start preparation",
		CallSites: []string{"store/writer/mp_start_prep_core.go", "store/writer/mp_start_prep_receipts.go"},
		Cohorts:   []stmt.Cohort{{Name: "bounded-start-input", Bindings: b}}, Plan: stmt.Plan{Hot: true}}
}

func mpPrepCommonDefinitions() []stmt.Definition {
	out := []stmt.Definition{
		mpPrepDefinition("mp.start.public_item_current", `SELECT 1 FROM catalog_entities AS e CROSS JOIN catalog_libraries AS c ON c.id=e.library_id CROSS JOIN catalog_assets AS a ON a.library_id=e.library_id CROSS JOIN catalog_asset_links AS l ON l.entity_id=e.id AND l.asset_id=a.id AND l.library_id=e.library_id WHERE e.public_id=? AND c.library_id=? AND a.token=? AND l.part_index=? AND e.retired=0 AND c.retired=0 AND a.available=1 AND l.available=1 AND l.info_pending_change IS NULL`, "bssi", stmt.Read),
		mpPrepDefinition("mp.start.actor_current", `SELECT 1 FROM authorization_family_tokens AS t INNER JOIN authorization_session_families AS f ON f.id=t.family_id WHERE t.token_hash=? AND f.id=? AND f.server_id=? AND f.authority=? AND f.account_id=? AND f.profile_id=? AND f.device_id=? AND f.epoch=? AND t.generation=? AND t.generation=f.current_generation AND t.retired=0 AND f.revoked=0 AND f.pin_gate=0`, "sssssssii", stmt.Read),
		mpPrepDefinition("mp.start.source_current", `SELECT 1 FROM inventory_objects AS o INNER JOIN library_sources AS s ON s.id=o.source_id INNER JOIN catalog_assets AS a ON a.token=o.asset_id WHERE o.id=? AND o.source_id=? AND o.asset_id=? AND o.revision=? AND o.root_incarnation=? AND o.size=? AND o.modified_ns=? AND o.retired=0 AND o.state='available' AND s.library_id=? AND s.root_identity=? AND s.generation=? AND coalesce(s.remote_source_id,'')=? AND s.incarnation=o.root_incarnation AND s.enabled=1 AND s.identity_confirmed=1 AND s.health='ready' AND a.available=1 AND a.info_publication_pending=0 AND a.size=o.size AND a.modified_ns=o.modified_ns`, "sssssiissis", stmt.Read),
		mpPrepDefinition("mp.start.queue_current", `SELECT 1 FROM queues_v1 WHERE id=? AND owner_kind=? AND owner_id=? AND revision=? AND custody_generation=? AND current_entry_id=? AND authority=? AND account_id=? AND profile_id=?`, "sssiissss", stmt.Read),
		mpPrepDefinition("mp.start.replacement", `SELECT 1 FROM playback_v1_sessions WHERE id=? AND authority=? AND account_id=? AND profile_id=? AND device_id=? AND generation=? AND ended_ms=0`, "sssssi", stmt.Read),
	}
	out[0].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_entities_1"}, Seeks: []string{"public_id=?"}}
	out[1].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_family_tokens_1"}}
	out[2].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_assets_1"}}
	out[3].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_queues_v1_1"}}
	out[4].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_v1_sessions_1"}}
	return out
}

// mpStartPrepBeginFresh proves the current disclosure for a birth. The UO birth
// witness is admitted only for the MP owner incarnation minted in this turn.
func mpStartPrepBeginFresh(ctx context.Context, t *commandTurn, owner [16]byte, q mpStartPrepBeginInput, now int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if !q.valid() || owner == ([16]byte{}) || now <= 0 {
		return ErrInvalid
	}
	if q.deadlineMS <= now || q.tokenExpiresMS < q.deadlineMS {
		return ErrConflict
	}
	if !q.actor.valid(now) {
		return ErrDisclosureDenied
	}
	if err = requireMPStartPrepUOBirth(q.birth, owner, now); err != nil {
		return err
	}
	if err = checkMPPrepActor(ctx, t, q.actor, now); err != nil {
		return err
	}
	if err = checkMPPrepSource(ctx, t, q.source); err != nil {
		return err
	}
	if err = checkMPPrepPublicItem(ctx, t, q.item, q.part, q.source); err != nil {
		return err
	}
	if err = checkMPPrepQueue(ctx, t, q.queue, q.actor); err != nil {
		return err
	}
	if q.replaces != "" {
		if err = mpPrepOne(ctx, t, "mp.start.replacement", q.replaces, q.actor.authority, q.actor.account, q.actor.profile, q.actor.device, q.replacesGeneration); err != nil {
			return err
		}
	}
	return nil
}

// applyMPStartPrepBegin stages exactly the MP-owned preparation rows after the
// fresh disclosure and duplicate lookup already ran in this SAME turn.
func applyMPStartPrepBegin(ctx context.Context, t *commandTurn, owner [16]byte, q mpStartPrepBeginInput, now int64) (out mpStartPrepValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepBegin); err != nil {
		return
	}
	if !q.valid() || owner == ([16]byte{}) || now <= 0 || q.birth.incarnation != owner {
		return out, ErrInvalid
	}
	// A duplicate device/key arrives only after the dispatcher's original
	// key/body replay lookup. A changed duplicate is a conflict, never a
	// second preparation.
	if err = mpPrepOne(ctx, t, "mp.start.begin", q.id, q.actor.device, q.keyDigest, q.bodyDigest, q.item, q.source.asset, q.part, q.replaces, q.replacesGeneration, q.tokenExpiresMS, now, q.deadlineMS, owner[:], q.birth.birth.root, q.birth.birth.member, q.birth.birth.profileLifetime); err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.start.actor_insert", append([]any{q.id}, q.actor.args()...)...); err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.start.source_insert", append([]any{q.id}, q.source.args()...)...); err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.start.queue_insert", append([]any{q.id, q.id, q.keyDigest, q.bodyDigest}, q.queue.args()...)...); err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.start.results_insert", q.id, q.results.session, q.results.legacySession, q.results.legacyGeneration, q.results.snapshot); err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.start.lifecycle_insert", q.id); err != nil {
		return out, err
	}
	return mpStartPrepValue{Preparation: q.id, State: "pending", Revision: 1, DeadlineMS: q.deadlineMS, Session: q.results.session, LegacySession: q.results.legacySession, LegacyGeneration: q.results.legacyGeneration, Snapshot: q.results.snapshot, AppliedMS: now}, nil
}

// readMPStartPrepFresh reads every stored row again and proves CURRENT
// disclosure. It deliberately makes no revision/state CAS decision: a saved
// original must be classified and served before any fresh-state comparison.
func readMPStartPrepFresh(ctx context.Context, t *commandTurn, kind CommandID, f mpStartPrepFence) (out mpStartPrepOwner, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !f.valid() {
		return out, ErrInvalid
	}
	now, err := t.commandTimeMS(ctx, kind, 0)
	if err != nil {
		return out, err
	}
	if !f.actor.valid(now) {
		return out, ErrDisclosureDenied
	}
	if err = checkMPPrepActor(ctx, t, f.actor, now); err != nil {
		return out, err
	}
	if err = readMPStartPrepOwner(ctx, t, f.id, f.actor.device, &out); err != nil {
		return out, err
	}
	if out.actor != f.actor {
		return out, ErrDisclosureDenied
	}
	if out.deadlineMS <= now {
		return out, ErrConflict
	}
	if out.source != f.source || out.queue != f.queue {
		return out, ErrConflict
	}
	out.nowMS = now
	if err = checkMPPrepPublicItem(ctx, t, out.item, out.part, out.source); err != nil {
		return out, err
	}
	if err = checkMPPrepSource(ctx, t, out.source); err != nil {
		return out, err
	}
	if err = checkMPPrepQueue(ctx, t, out.queue, out.actor); err != nil {
		return out, err
	}
	return out, nil
}

// captureMPStartPrepOwner adds the exact lifecycle revision/state CAS required
// only after the receipt lookup reports a fresh command.
func captureMPStartPrepOwner(ctx context.Context, t *commandTurn, kind CommandID, f mpStartPrepFence) (out mpStartPrepOwner, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if out, err = readMPStartPrepFresh(ctx, t, kind, f); err != nil {
		return out, err
	}
	if out.state != "pending" && out.state != "preparing" {
		return out, ErrConflict
	}
	if out.revision != f.revision {
		return out, ErrConflict
	}
	return out, nil
}

// Claim assigns one immutable execution and seals the staged input marker. It
// starts no helper and proves no physical exit.
func applyMPStartPrepClaim(ctx context.Context, t *commandTurn, f mpStartPrepFence, execution string) (out mpStartPrepValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepClaim); err != nil {
		return
	}
	if !mpPrepTexts(execution) || f.execution != "" {
		return out, ErrInvalid
	}
	owner, err := captureMPStartPrepOwner(ctx, t, MPStartPrepClaim, f)
	if err != nil {
		return out, err
	}
	if owner.state != "pending" || owner.execution != "" || owner.sealed {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.start.claim", execution, f.id, f.revision); err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.start.execution_insert", f.id, execution); err != nil {
		return out, err
	}
	return mpStartPrepValue{Preparation: f.id, State: "preparing", Execution: execution, Revision: f.revision + 1,
		DeadlineMS: owner.deadlineMS, Session: owner.ids.session, LegacySession: owner.ids.legacySession,
		LegacyGeneration: owner.ids.legacyGeneration, Snapshot: owner.ids.snapshot, Sealed: true, AppliedMS: owner.nowMS}, nil
}

// Sealing publishes only the verified bounded commitment selected by the
// external preparer. No external work or manifest traversal occurs here, and
// zero staged totals mean the stage was interrupted, not complete.
func applyMPStartPrepSeal(ctx context.Context, t *commandTurn, f mpStartPrepFence, c mpStartPrepCompletion) (out mpStartPrepValue, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, MPStartPrepSeal); err != nil {
		return
	}
	if !c.valid() || c.execution != f.execution || f.execution == "" {
		return out, ErrInvalid
	}
	owner, err := captureMPStartPrepOwner(ctx, t, MPStartPrepSeal, f)
	if err != nil {
		return out, err
	}
	if owner.state != "preparing" || owner.execution != c.execution || !owner.sealed {
		return out, ErrConflict
	}
	if err = mpPrepOne(ctx, t, "mp.start.completion", c.resultDigest, c.stagedRows, c.stagedBytes, f.id, c.execution); err != nil {
		return out, err
	}
	if err = mpPrepOne(ctx, t, "mp.start.seal", f.id, f.revision, c.execution); err != nil {
		return out, err
	}
	return mpStartPrepValue{Preparation: f.id, State: "prepared", Execution: c.execution, Revision: f.revision + 1,
		DeadlineMS: owner.deadlineMS, Session: owner.ids.session, LegacySession: owner.ids.legacySession,
		LegacyGeneration: owner.ids.legacyGeneration, Snapshot: owner.ids.snapshot, Sealed: true, AppliedMS: owner.nowMS}, nil
}

func mpStartPrepDefinitions() []stmt.Definition {
	defs := mpPrepCommonDefinitions()
	defs = append(defs, mpStartPrepReceiptDefinitions()...)
	defs = append(defs,
		mpPrepDefinition("mp.start.begin_key", `SELECT id,start_body_digest FROM playback_start_preparations WHERE device_id=? AND start_key_digest=?`, "ss", stmt.Read),
		mpPrepDefinition("mp.start.owner", `SELECT mp_command_incarnation,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_owner_revision,mp_receipt_rows,mp_receipt_variable_bytes,mp_control_variable_bytes,mp_retirement_state,device_id,item_id,part_index,deadline_ms,token_expires_ms,replaces_session_id,replaces_generation,created_ms,coalesce(mp_user_root_id,'') FROM playback_start_preparations WHERE id=? AND device_id=?`, "ss", stmt.Read),
		mpPrepDefinition("mp.start.lifecycle_row", `SELECT state,revision,execution_id,sealed FROM playback_start_preparation_lifecycle WHERE operation_id=?`, "s", stmt.Read),
		mpPrepDefinition("mp.start.actor_row", `SELECT server_id,authority,account_id,profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms FROM playback_start_preparation_actor WHERE operation_id=?`, "s", stmt.Read),
		mpPrepDefinition("mp.start.source_row", `SELECT library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns FROM playback_start_preparation_source WHERE operation_id=?`, "s", stmt.Read),
		mpPrepDefinition("mp.start.queue_row", `SELECT queue_id,queue_entry_id,queue_owner_kind,queue_owner_id,queue_revision,queue_custody_generation FROM playback_start_preparation_queue WHERE operation_id=?`, "s", stmt.Read),
		mpPrepDefinition("mp.start.results_row", `SELECT session_id,legacy_session_id,legacy_generation,snapshot_id FROM playback_start_preparation_results WHERE operation_id=?`, "s", stmt.Read),
		mpPrepDefinition("mp.start.execution_row", `SELECT execution_id,result_digest,staged_rows,staged_bytes,physical_exited,cleaned,cleanup_cursor FROM mp_start_execution WHERE operation_id=?`, "s", stmt.Read),
		mpPrepDefinition("mp.start.result_native", `SELECT l.state,l.revision,l.execution_id,l.sealed,p.deadline_ms,coalesce(p.mp_user_root_id,''),r.session_id,r.legacy_session_id,r.legacy_generation,r.snapshot_id,p.created_ms FROM playback_start_preparations AS p INNER JOIN playback_start_preparation_lifecycle AS l ON l.operation_id=p.id INNER JOIN playback_start_preparation_results AS r ON r.operation_id=p.id WHERE p.id=?`, "s", stmt.Read),
		mpPrepDefinition("mp.start.begin", `INSERT INTO playback_start_preparations(id,device_id,start_key_digest,start_body_digest,item_id,asset_id,part_index,replaces_session_id,replaces_generation,token_expires_ms,created_ms,deadline_ms,mp_command_incarnation,mp_user_root_id,mp_owner_member_incarnation,mp_owner_profile_lifetime) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "ssssssisiiiibsss", stmt.Write),
		mpPrepDefinition("mp.start.actor_insert", `INSERT INTO playback_start_preparation_actor(operation_id,server_id,authority,account_id,profile_id,device_id,family_id,grant_id,session_hash,epoch,token_generation,original_horizon_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "sssssssssiii", stmt.Write),
		mpPrepDefinition("mp.start.source_insert", `INSERT INTO playback_start_preparation_source(operation_id,library_id,library_source_id,asset_id,inventory_object_id,inventory_revision,root_identity,incarnation,remote_source_id,effective_configuration,read_policy_witness,read_authority_witness,library_generation,raw_configuration_generation,profile_revision,size,modified_ns) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "ssssssssssssiiiii", stmt.Write),
		mpPrepDefinition("mp.start.queue_insert", `INSERT INTO playback_start_preparation_queue(operation_id,legacy_request_id,start_key_digest,start_body_digest,queue_id,queue_entry_id,queue_owner_kind,queue_owner_id,queue_revision,queue_custody_generation) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING 1`, "ssssssssii", stmt.Write),
		mpPrepDefinition("mp.start.results_insert", `INSERT INTO playback_start_preparation_results(operation_id,session_id,legacy_session_id,legacy_generation,snapshot_id) VALUES(?,?,?,?,?) RETURNING 1`, "sssis", stmt.Write),
		mpPrepDefinition("mp.start.lifecycle_insert", `INSERT INTO playback_start_preparation_lifecycle(operation_id,state,revision,execution_id,sealed) VALUES(?,'pending',1,'',0) RETURNING 1`, "s", stmt.Write),
		mpPrepDefinition("mp.start.claim", `UPDATE playback_start_preparation_lifecycle SET state='preparing',execution_id=?,sealed=1,revision=revision+1 WHERE operation_id=? AND revision=? AND state='pending' AND execution_id='' AND sealed=0 RETURNING 1`, "ssi", stmt.Write),
		mpPrepDefinition("mp.start.execution_insert", `INSERT INTO mp_start_execution(operation_id,execution_id,physical_exited,cleaned,exit_digest,result_digest,staged_rows,staged_bytes,cleanup_cursor) VALUES(?,?,0,0,'','',0,0,0) RETURNING 1`, "ss", stmt.Write),
		mpPrepDefinition("mp.start.completion", `UPDATE mp_start_execution SET result_digest=?,staged_rows=?,staged_bytes=? WHERE operation_id=? AND execution_id=? AND result_digest='' AND cleaned=0 AND physical_exited=0 RETURNING 1`, "siiss", stmt.Write),
		mpPrepDefinition("mp.start.seal", `UPDATE playback_start_preparation_lifecycle SET state='prepared',revision=revision+1 WHERE operation_id=? AND revision=? AND execution_id=? AND state='preparing' AND sealed=1 RETURNING 1`, "sis", stmt.Write),
	)
	// Point plans: every single-table read/write seeks its TEXT primary key.
	for name := range defs {
		switch defs[name].ID {
		case "mp.start.begin_key":
			defs[name].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_playback_start_preparations_2"}, Seeks: []string{"device_id=?"}}
		case "mp.start.owner":
			defs[name].Plan = mpPrepTablePointPlan("playback_start_preparations", "id")
		case "mp.start.lifecycle_row":
			defs[name].Plan = mpPrepTablePointPlan("playback_start_preparation_lifecycle", "operation_id")
		case "mp.start.actor_row":
			defs[name].Plan = mpPrepTablePointPlan("playback_start_preparation_actor", "operation_id")
		case "mp.start.source_row":
			defs[name].Plan = mpPrepTablePointPlan("playback_start_preparation_source", "operation_id")
		case "mp.start.queue_row":
			defs[name].Plan = mpPrepTablePointPlan("playback_start_preparation_queue", "operation_id")
		case "mp.start.results_row":
			defs[name].Plan = mpPrepTablePointPlan("playback_start_preparation_results", "operation_id")
		case "mp.start.result_native":
			defs[name].Plan = mpPrepTablePointPlan("playback_start_preparations", "id")
		case "mp.start.execution_row":
			defs[name].Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"mp_start_execution"}}
		case "mp.start.completion":
			defs[name].Plan = stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_mp_start_execution_2"}, Seeks: []string{"execution_id=?"}}
		case "mp.start.claim", "mp.start.seal":
			defs[name].Plan = mpPrepTablePointPlan("playback_start_preparation_lifecycle", "operation_id")
		case "mp.start.debt":
			defs[name].Plan = stmt.Plan{Hot: true, Indexes: []string{"mp_start_command_owner"}}
		}
	}
	return defs
}

func mpPrepTablePointPlan(table, key string) stmt.Plan {
	return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_" + table + "_1"}, Seeks: []string{key + "=?"}}
}

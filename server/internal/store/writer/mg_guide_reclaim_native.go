package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private guide retirement effects on the caller's one admitted NORMAL turn.
// These helpers neither authorize disclosure nor select admission, events,
// receipt ACK or COMMIT. Every read names an exact cohort and every DELETE is a
// keyset range whose affected-row count must equal the rows just observed.
const (
	mgGuideReclaimOperation  stmt.ID = "mg.guide.reclaim.operation"
	mgGuideReclaimCandidate  stmt.ID = "mg.guide.reclaim.candidate"
	mgGuideReclaimProgramme  stmt.ID = "mg.guide.reclaim.programmes"
	mgGuideReclaimChannel    stmt.ID = "mg.guide.reclaim.channels"
	mgGuideReclaimChoice     stmt.ID = "mg.guide.reclaim.choices"
	mgGuideReclaimReminder   stmt.ID = "mg.guide.reclaim.pin.reminder"
	mgGuideReclaimRecording  stmt.ID = "mg.guide.reclaim.pin.recording"
	mgGuideReclaimOperationP stmt.ID = "mg.guide.reclaim.pin.operation"
	mgGuideReclaimGenScope   stmt.ID = "mg.guide.reclaim.generation"

	mgGuideDiscardOperation      stmt.ID = "mg.guide.discard.operation"
	mgGuideReclaimAdvance        stmt.ID = "mg.guide.reclaim.advance"
	mgGuideReclaimDeletePrograms stmt.ID = "mg.guide.reclaim.delete_programmes"
	mgGuideReclaimDeleteChannels stmt.ID = "mg.guide.reclaim.delete_channels"
	mgGuideReclaimDeleteChoices  stmt.ID = "mg.guide.reclaim.delete_choices"
)

// Actual persisted native bytes of one child row, never a caller's estimate.
// Fixed row/header costs are the caller's separate admission overhead.
const (
	mgGuideProgrammeBytes = `length(CAST(id AS BLOB))+length(CAST(channel_id AS BLOB))+length(CAST(provider_key AS BLOB))+length(CAST(title AS BLOB))+length(CAST(start_utc AS BLOB))+length(CAST(end_utc AS BLOB))+length(CAST(lineage AS BLOB))+coalesce(length(CAST(metadata_series_id AS BLOB)),0)+coalesce(length(CAST(metadata_episode_id AS BLOB)),0)+coalesce(length(CAST(metadata_new_evidence AS BLOB)),0)+coalesce(length(CAST(metadata_description AS BLOB)),0)+coalesce(length(CAST(metadata_facts AS BLOB)),0)+coalesce(length(CAST(icon_url AS BLOB)),0)`
	mgGuideChannelBytes   = `length(CAST(generation_id AS BLOB))+length(CAST(channel_id AS BLOB))+length(CAST(provider_key AS BLOB))+length(CAST(name AS BLOB))+length(CAST(number AS BLOB))+coalesce(length(CAST(source_number AS BLOB)),0)+length(CAST(group_name AS BLOB))+length(locator_sealed)+length(CAST(available_start AS BLOB))+length(CAST(available_end AS BLOB))+coalesce(length(CAST(attribute_country AS BLOB)),0)+coalesce(length(CAST(attribute_catchup AS BLOB)),0)+coalesce(length(CAST(attribute_catchup_source AS BLOB)),0)+coalesce(length(CAST(attribute_catchup_days AS BLOB)),0)+coalesce(length(CAST(logo_url AS BLOB)),0)`
	mgGuideChoiceBytes    = `length(CAST(generation_id AS BLOB))+length(CAST(channel_key AS BLOB))+length(CAST(name AS BLOB))+length(CAST(group_name AS BLOB))`
)

func mgGuideReclaimDefinitions() []stmt.Definition {
	txt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	args := func(params []stmt.Param) []any {
		out := make([]any, len(params))
		for k, p := range params {
			switch p.Type {
			case stmt.Text:
				out[k] = "missing"
			case stmt.Integer:
				out[k] = int64(1)
			case stmt.Blob:
				out[k] = make([]byte, 32)
			}
		}
		return out
	}
	cohort := func(name string, params []stmt.Param) []stmt.Cohort {
		return []stmt.Cohort{{Name: name, Bindings: args(params)}}
	}
	operationsPK := stmt.AutoIndexBinding{Table: "live_operations", Name: "sqlite_autoindex_live_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "request_id", Collation: "BINARY"}}}
	programmesPK := stmt.AutoIndexBinding{Table: "live_programmes", Name: "sqlite_autoindex_live_programmes_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "generation_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}}
	channelsPK := stmt.AutoIndexBinding{Table: "live_channel_versions", Name: "sqlite_autoindex_live_channel_versions_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "generation_id", Collation: "BINARY"}, {Name: "channel_id", Collation: "BINARY"}}}
	choicesPK := stmt.AutoIndexBinding{Table: "live_channel_choices", Name: "sqlite_autoindex_live_channel_choices_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "generation_id", Collation: "BINARY"}, {Name: "channel_key", Collation: "BINARY"}}}

	opParams := []stmt.Param{txt("request")}
	candidateParams := []stmt.Param{txt("after"), txt("source")}
	programmeParams := []stmt.Param{txt("generation"), txt("after")}
	rangeParams := []stmt.Param{txt("generation"), txt("after"), txt("through")}
	generationParams := []stmt.Param{txt("generation"), txt("source")}
	discardParams := []stmt.Param{integer("next_sequence"), integer("next_revision"), txt("request"), blob("incarnation"), integer("sequence"), integer("acknowledged"), integer("revision")}
	advanceParams := []stmt.Param{integer("next_sequence"), integer("next_revision"), txt("family"), txt("cursor"), txt("request"), blob("incarnation"), integer("sequence"), integer("acknowledged"), integer("revision"), txt("before_family"), txt("before_cursor")}

	defs := []stmt.Definition{
		{ID: mgGuideReclaimOperation, Kind: stmt.Read, SQL: `SELECT status,command_incarnation,last_command_sequence,acknowledged_command_sequence,stage_revision,reclaim_family,reclaim_cursor,source_id,generation_id
FROM live_operations INDEXED BY sqlite_autoindex_live_operations_1 WHERE request_id=?`, Params: opParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, Seeks: []string{"request_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{operationsPK}},
			Cohorts: cohort("staged", opParams)},
		{ID: mgGuideReclaimCandidate, Kind: stmt.Read, SQL: `SELECT request_id FROM live_operations INDEXED BY sqlite_autoindex_live_operations_1
WHERE request_id>? AND source_id=? AND status='superseded' AND reclaim_family<>'complete'
ORDER BY request_id LIMIT 1`, Params: candidateParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, Seeks: []string{"request_id>?"}, AutoIndexes: []stmt.AutoIndexBinding{operationsPK}},
			Cohorts: cohort("after", candidateParams)},
		{ID: mgGuideReclaimProgramme, Kind: stmt.Read, SQL: `SELECT id,` + mgGuideProgrammeBytes + `
FROM live_programmes INDEXED BY sqlite_autoindex_live_programmes_1 WHERE generation_id=? AND id>? ORDER BY id LIMIT 9`, Params: programmeParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_programmes_1"}, Seeks: []string{"generation_id=?", "id>?"}, AutoIndexes: []stmt.AutoIndexBinding{programmesPK}},
			Cohorts: cohort("page", programmeParams)},
		{ID: mgGuideReclaimChannel, Kind: stmt.Read, SQL: `SELECT channel_id,` + mgGuideChannelBytes + `
FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id>? ORDER BY channel_id LIMIT 9`, Params: programmeParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_channel_versions_1"}, Seeks: []string{"generation_id=?", "channel_id>?"}, AutoIndexes: []stmt.AutoIndexBinding{channelsPK}},
			Cohorts: cohort("page", programmeParams)},
		{ID: mgGuideReclaimChoice, Kind: stmt.Read, SQL: `SELECT channel_key,` + mgGuideChoiceBytes + `
FROM live_channel_choices INDEXED BY sqlite_autoindex_live_channel_choices_1 WHERE generation_id=? AND channel_key>? ORDER BY channel_key LIMIT 9`, Params: programmeParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_channel_choices_1"}, Seeks: []string{"generation_id=?", "channel_key>?"}, AutoIndexes: []stmt.AutoIndexBinding{choicesPK}},
			Cohorts: cohort("page", programmeParams)},
		{ID: mgGuideReclaimReminder, Kind: stmt.Read, SQL: `SELECT 1 FROM live_reminders INDEXED BY live_reminders_external_generation WHERE external_generation_ref=? AND source_id=? LIMIT 1`, Params: generationParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"live_reminders_external_generation"}, Seeks: []string{"external_generation_ref=?", "source_id=?"}},
			Cohorts: cohort("pin", generationParams)},
		{ID: mgGuideReclaimRecording, Kind: stmt.Read, SQL: `SELECT 1 FROM dvr_recordings INDEXED BY dvr_recordings_guide_pin WHERE guide_pin_generation=? AND source_id=? LIMIT 1`, Params: generationParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"dvr_recordings_guide_pin"}, Seeks: []string{"guide_pin_generation=?", "source_id=?"}},
			Cohorts: cohort("pin", generationParams)},
		{ID: mgGuideReclaimOperationP, Kind: stmt.Read, SQL: `SELECT 1 FROM live_operations INDEXED BY live_operations_generation_id_fk WHERE generation_id=? AND request_id<>? LIMIT 1`, Params: []stmt.Param{txt("generation"), txt("request")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"live_operations_generation_id_fk"}, Seeks: []string{"generation_id=?"}},
			Cohorts: cohort("pin", []stmt.Param{txt("generation"), txt("request")})},
		{ID: mgGuideReclaimGenScope, Kind: stmt.Read, SQL: `SELECT 1 FROM live_generations INDEXED BY live_generations_source_scope WHERE id=? AND source_id=?`, Params: generationParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"live_generations_source_scope"}, Seeks: []string{"id=?", "source_id=?"}},
			Cohorts: cohort("scope", generationParams)},
		{ID: mgGuideDiscardOperation, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1
SET status='superseded',last_command_sequence=?,stage_revision=?,reclaim_family='programmes',reclaim_cursor=''
WHERE request_id=? AND command_incarnation=? AND status='staging' AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND reclaim_family='none' AND reclaim_cursor=''`, Params: discardParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, Seeks: []string{"request_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{operationsPK}},
			Cohorts: cohort("staging", discardParams)},
		{ID: mgGuideReclaimAdvance, Kind: stmt.Write, SQL: `UPDATE live_operations INDEXED BY sqlite_autoindex_live_operations_1
SET last_command_sequence=?,stage_revision=?,reclaim_family=?,reclaim_cursor=?
WHERE request_id=? AND command_incarnation=? AND status='superseded' AND last_command_sequence=? AND acknowledged_command_sequence=? AND stage_revision=? AND reclaim_family=? AND reclaim_cursor=?`, Params: advanceParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_operations_1"}, Seeks: []string{"request_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{operationsPK}},
			Cohorts: cohort("slice", advanceParams)},
		{ID: mgGuideReclaimDeletePrograms, Kind: stmt.Write, SQL: `DELETE FROM live_programmes INDEXED BY sqlite_autoindex_live_programmes_1 WHERE generation_id=? AND id>? AND id<=?`, Params: rangeParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_programmes_1"}, Seeks: []string{"generation_id=?", "id>?", "id<=?"}, AutoIndexes: []stmt.AutoIndexBinding{programmesPK}},
			Cohorts: cohort("page", rangeParams)},
		{ID: mgGuideReclaimDeleteChannels, Kind: stmt.Write, SQL: `DELETE FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id>? AND channel_id<=?`, Params: rangeParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_channel_versions_1"}, Seeks: []string{"generation_id=?", "channel_id>?", "channel_id<=?"}, AutoIndexes: []stmt.AutoIndexBinding{channelsPK}},
			Cohorts: cohort("page", rangeParams)},
		{ID: mgGuideReclaimDeleteChoices, Kind: stmt.Write, SQL: `DELETE FROM live_channel_choices INDEXED BY sqlite_autoindex_live_channel_choices_1 WHERE generation_id=? AND channel_key>? AND channel_key<=?`, Params: rangeParams,
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_live_channel_choices_1"}, Seeks: []string{"generation_id=?", "channel_key>?", "channel_key<=?"}, AutoIndexes: []stmt.AutoIndexBinding{choicesPK}},
			Cohorts: cohort("page", rangeParams)},
	}
	for j := range defs {
		defs[j].Owner = "writer.mg.guide.reclaim"
		defs[j].CallSites = []string{"writer/mg_guide_reclaim_native.go"}
	}
	return defs
}

// Exact statement for each child family. A probe can never borrow another
// family's SQL under a matching family label.
func mgGuideReclaimStatementFor(f mgGuideReclaimFamily) stmt.ID {
	switch f {
	case mgGuideReclaimProgrammes:
		return mgGuideReclaimProgramme
	case mgGuideReclaimChannels:
		return mgGuideReclaimChannel
	case mgGuideReclaimChoices:
		return mgGuideReclaimChoice
	}
	return ""
}

func mgGuideReclaimStatementStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgGuideReclaimOperation, mgGuideReclaimCandidate, mgGuideReclaimProgramme,
		mgGuideReclaimChannel, mgGuideReclaimChoice, mgGuideReclaimReminder, mgGuideReclaimRecording,
		mgGuideReclaimOperationP, mgGuideReclaimGenScope, mgGuideDiscardOperation, mgGuideReclaimAdvance,
		mgGuideReclaimDeletePrograms, mgGuideReclaimDeleteChannels, mgGuideReclaimDeleteChoices:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}

func mgGuideReclaimQuery(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgGuideReclaimStatementStep(t, id)
	if err != nil {
		return commandRow{}, err
	}
	return t.queryRow(ctx, step, args...)
}

func mgGuideReclaimChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgGuideReclaimStatementStep(t, id)
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

// The retirement columns and the accepted operation header are two reads of the
// SAME turn; any disagreement means the caller prepared facts from another row.
func mgGuideReclaimMatchesNative(state mgGuideReclaimState, op mgGuideNative) bool {
	p := op.progress
	m := p.manifest
	return state.request == op.request && state.source == m.source && state.generation == m.generation &&
		state.incarnation == op.incarnation && state.sequence == p.sequence && state.revision == p.revision &&
		state.acknowledged == op.acknowledged
}

// mgReadGuideReclaimNative reads the retirement cursor and its exact lineage.
// It is not a pin proof, current-head proof or disclosure decision.
func mgReadGuideReclaimNative(ctx context.Context, t *commandTurn, request string) (out mgGuideReclaimState, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgGuideText(request, true, 256) {
		return out, ErrInvalid
	}
	row, err := mgGuideReclaimQuery(ctx, t, mgGuideReclaimOperation, request)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	var family, cursor string
	if err = row.Scan(&out.status, &incarnation, &out.sequence, &out.acknowledged, &out.revision, &family, &cursor, &out.source, &out.generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
	}
	if len(incarnation) != 16 {
		return out, ErrInvalid
	}
	copy(out.incarnation[:], incarnation)
	out.request, out.family, out.cursor = request, mgGuideReclaimFamily(family), cursor
	if !out.valid() {
		return out, ErrInvalid
	}
	return out, nil
}

// The turn's applied time for retirement purposes. No real-clock assertion or
// latency claim: this is a native observation on the admitted frame.
func mgGuideReclaimTime(ctx context.Context, t *commandTurn) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.writer == nil || t.transaction.writer.now == nil {
		return 0, ErrInvalid
	}
	switch t.transaction.descriptor.ID {
	case MGGuideDiscard, MGGuideReclaim, MGGuideAdvanceCleanup:
	default:
		return 0, ErrInvalid
	}
	if t.transaction.descriptor.Durability != Normal {
		return 0, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	defer t.transaction.guardPanic()
	now := t.transaction.writer.now()
	if now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if err = t.transaction.observeTime(now); err != nil {
		return 0, err
	}
	return now.UnixMilli(), nil
}

// Observed current authority for the owner of one source. The caller supplies
// the witness it observed before admission; a mismatch is stale authority, never
// a re-read that silently authorizes a different head.
type mgGuideReclaimWitness struct {
	source, head string
	revision     int64
	removed      bool
}

func (w mgGuideReclaimWitness) equal(h mgGuideHead) bool {
	return w.source == h.source && w.head == h.generation && w.revision == h.revision && w.removed == h.removed
}

type mgGuideDiscardInput struct {
	request  string
	identity CommandIdentity
	witness  mgGuideReclaimWitness
}

type mgGuideReclaimInput struct {
	request  string
	identity CommandIdentity
	witness  mgGuideReclaimWitness
}

// Result of one retirement step. Removed/Bytes are actual native effects on this
// turn; Blocked is a truthful effect-free reason. Neither is Applied/COMMIT
// evidence and neither is result-receipt disclosure.
type mgGuideReclaimResult struct {
	operation mgGuideNative
	head      mgGuideHead
	state     mgGuideReclaimState
	appliedMS int64
	removed   int64
	bytes     int64
	blocked   string
}

// Logical withdrawal of one staging generation. Rows, pins and the serving head
// are retained; only the operation leaves staging and gains a durable cleanup
// intent. External generation deletion is not enabled or implied here.
func mgDiscardGuideNative(ctx context.Context, t *commandTurn, input mgGuideDiscardInput) (out mgGuideNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuideReclaimPurpose(t, MGGuideDiscard); err != nil {
		return out, err
	}
	if !mgGuideText(input.request, true, 256) {
		return out, ErrInvalid
	}
	before, err := mgReadGuideNative(ctx, t, input.request)
	if err != nil {
		return out, err
	}
	state, err := mgReadGuideReclaimNative(ctx, t, input.request)
	if err != nil {
		return out, err
	}
	head, err := mgGuideHeadFacts(ctx, t, before.progress.manifest.source)
	if err != nil {
		return out, err
	}
	if !mgGuideReclaimMatchesNative(state, before) || !input.witness.equal(head) {
		return out, ErrConflict
	}
	next, err := planMGGuideDiscard(state, input.identity, head.generation)
	if err != nil {
		return out, err
	}
	floor, err := mgReadGuideRetirementFloor(ctx, t, input.request)
	if err != nil {
		return out, err
	}
	if floor.found {
		return out, ErrConflict
	}
	applied, err := mgGuideReclaimTime(ctx, t)
	if err != nil {
		return out, err
	}
	if err = mgGuideReclaimChange(ctx, t, mgGuideDiscardOperation, next.sequence, next.revision, input.request, state.incarnation[:], state.sequence, state.acknowledged, state.revision); err != nil {
		return out, err
	}
	want := before
	want.status = "superseded"
	want.progress.sequence = next.sequence
	want.progress.revision = next.revision
	post, err := mgReadGuideNative(ctx, t, input.request)
	if err != nil {
		return out, err
	}
	if post != want {
		return out, ErrConflict
	}
	postState, err := mgReadGuideReclaimNative(ctx, t, input.request)
	if err != nil {
		return out, err
	}
	if postState.family != next.family || postState.cursor != next.cursor || postState.status != "superseded" {
		return out, ErrConflict
	}
	return mgGuideNativeResult{post, head, applied}, nil
}

// One bounded retirement slice for the requested operation. Every head/pin
// decision is a point read or an exact reverse-key probe on this same turn; a
// refusal performs no effect and returns its truthful reason.
func mgReclaimGuideNative(ctx context.Context, t *commandTurn, input mgGuideReclaimInput) (out mgGuideReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuideReclaimPurpose(t, MGGuideReclaim); err != nil {
		return out, err
	}
	if !mgGuideText(input.request, true, 256) {
		return out, ErrInvalid
	}
	return mgReclaimGuideSlice(ctx, t, input.request, input.identity, input.witness)
}

// mgReclaimGuideSlice is shared by 807 (requested operation) and 852 (bounded
// per-source cleanup). It mutates at most one child page and never the
// generation row.
func mgReclaimGuideSlice(ctx context.Context, t *commandTurn, request string, identity CommandIdentity, witness mgGuideReclaimWitness) (out mgGuideReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	before, err := mgReadGuideNative(ctx, t, request)
	if err != nil {
		return out, err
	}
	state, err := mgReadGuideReclaimNative(ctx, t, request)
	if err != nil {
		return out, err
	}
	head, err := mgGuideHeadFacts(ctx, t, state.source)
	if err != nil {
		return out, err
	}
	out.operation, out.head, out.state = before, head, state
	if !mgGuideReclaimMatchesNative(state, before) || state.status != "superseded" || state.family == mgGuideReclaimNone || state.family == mgGuideReclaimComplete {
		return out, ErrConflict
	}
	if !witness.equal(head) || witness.source != state.source {
		return out, ErrConflict
	}
	if head.removed {
		return out, ErrDisclosureDenied
	}
	if head.generation == state.generation {
		out.blocked = mgGuideReclaimBlockedHead
		return out, nil
	}
	var present int64
	row, err := mgGuideReclaimQuery(ctx, t, mgGuideReclaimGenScope, state.generation, state.source)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&present); err != nil {
		return out, usAuthorityReadError(err)
	}
	if present != 1 {
		return out, ErrConflict
	}
	floor, err := mgReadGuideRetirementFloor(ctx, t, request)
	if err != nil {
		return out, err
	}
	if floor.found {
		return out, ErrConflict
	}
	if state.family != mgGuideReclaimGeneration {
		if pinned, reason, err := mgGuideGenerationPinned(ctx, t, state); err != nil {
			return out, err
		} else if pinned {
			out.blocked = reason
			return out, nil
		}
	}
	applied, err := mgGuideReclaimTime(ctx, t)
	if err != nil {
		return out, err
	}
	out.appliedMS = applied
	switch state.family {
	case mgGuideReclaimProgrammes:
		page, err := mgReadGuideReclaimKeys(ctx, t, mgGuideReclaimProgramme, state)
		if err != nil {
			return out, err
		}
		next, err := planMGGuideReclaimStep(state, identity, page)
		if err != nil {
			return out, err
		}
		if page.rows > 0 {
			if err = mgDeleteGuideReclaimRange(ctx, t, mgGuideReclaimDeletePrograms, state.generation, page.after, page.last, page.rows); err != nil {
				return out, err
			}
		}
		out.removed, out.bytes = page.rows, page.bytes
		if err = mgAdvanceGuideReclaim(ctx, t, state, next); err != nil {
			return out, err
		}
	case mgGuideReclaimChannels:
		page, err := mgReadGuideReclaimKeys(ctx, t, mgGuideReclaimChannel, state)
		if err != nil {
			return out, err
		}
		next, err := planMGGuideReclaimStep(state, identity, page)
		if err != nil {
			return out, err
		}
		if page.rows > 0 {
			if err = mgDeleteGuideReclaimRange(ctx, t, mgGuideReclaimDeleteChannels, state.generation, page.after, page.last, page.rows); err != nil {
				return out, err
			}
		}
		out.removed, out.bytes = page.rows, page.bytes
		if err = mgAdvanceGuideReclaim(ctx, t, state, next); err != nil {
			return out, err
		}
	case mgGuideReclaimChoices:
		page, err := mgReadGuideReclaimKeys(ctx, t, mgGuideReclaimChoice, state)
		if err != nil {
			return out, err
		}
		next, err := planMGGuideReclaimStep(state, identity, page)
		if err != nil {
			return out, err
		}
		if page.rows > 0 {
			if err = mgDeleteGuideReclaimRange(ctx, t, mgGuideReclaimDeleteChoices, state.generation, page.after, page.last, page.rows); err != nil {
				return out, err
			}
		}
		out.removed, out.bytes = page.rows, page.bytes
		if err = mgAdvanceGuideReclaim(ctx, t, state, next); err != nil {
			return out, err
		}
	case mgGuideReclaimGeneration:
		children, err := mgGuideGenerationChildren(ctx, t, state)
		if err != nil {
			return out, err
		}
		blocked, err := planMGGuideReclaimGeneration(state, identity, children)
		if err != nil {
			return out, err
		}
		out.blocked = blocked
		return out, nil
	default:
		return out, ErrConflict
	}
	post, err := mgReadGuideReclaimNative(ctx, t, request)
	if err != nil {
		return out, err
	}
	out.state = post
	return out, nil
}

// Typed generation pins on the same turn: a reminder, a recording custody pin or
// another operation still names this immutable generation. The single private
// writer connection means no pin can be inserted between these probes and the
// bounded DELETE below.
func mgGuideGenerationPinned(ctx context.Context, t *commandTurn, state mgGuideReclaimState) (pinned bool, reason string, err error) {
	for _, probe := range []struct {
		id     stmt.ID
		args   []any
		reason string
	}{
		{mgGuideReclaimReminder, []any{state.generation, state.source}, mgGuideReclaimBlockedReminder},
		{mgGuideReclaimRecording, []any{state.generation, state.source}, mgGuideReclaimBlockedRecording},
		{mgGuideReclaimOperationP, []any{state.generation, state.request}, mgGuideReclaimBlockedOperation},
	} {
		row, err := mgGuideReclaimQuery(ctx, t, probe.id, probe.args...)
		if err != nil {
			return false, "", err
		}
		var unit int64
		err = row.Scan(&unit)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, "", err
		}
		if unit != 1 {
			return false, "", ErrConflict
		}
		return true, probe.reason, nil
	}
	return false, "", nil
}

// One bounded PK page with a single lookahead row. `page.last` is always a row
// this caller will delete, never the unselected lookahead key.
func mgReadGuideReclaimKeys(ctx context.Context, t *commandTurn, id stmt.ID, state mgGuideReclaimState) (out mgGuideReclaimPage, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if id != mgGuideReclaimStatementFor(state.family) {
		return out, ErrInvalid
	}
	step, err := mgGuideReclaimStatementStep(t, id)
	if err != nil {
		return out, err
	}
	rows, err := t.query(ctx, step, state.generation, state.cursor)
	if err != nil {
		return out, err
	}
	out.family, out.after = state.family, state.cursor
	for rows.Next() {
		var key string
		var bytes int64
		if err = rows.Scan(&key, &bytes); err != nil {
			return out, errors.Join(err, rows.Close())
		}
		if !mgGuideText(key, true, mgGuideReclaimCursor) || bytes < 0 || key <= out.last {
			return out, errors.Join(ErrConflict, rows.Close())
		}
		// One lookahead row proves `more` without COUNTing the family. It is
		// never the deleted cursor: only admitted rows advance `last`.
		if out.rows == mgGuideReclaimRows {
			out.more = true
			break
		}
		out.last = strings.Clone(key)
		out.rows++
		out.bytes += bytes
		if out.bytes > mgGuideReclaimBytes {
			return out, errors.Join(ErrResolveRequired, rows.Close())
		}
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	if out.rows == 0 {
		out.last = ""
	}
	if !out.valid(state) {
		return out, ErrConflict
	}
	return out, nil
}

func mgDeleteGuideReclaimRange(ctx context.Context, t *commandTurn, id stmt.ID, generation, after, through string, want int64) error {
	if want < 0 || want > mgGuideReclaimRows || through <= after {
		return ErrInvalid
	}
	step, err := mgGuideReclaimStatementStep(t, id)
	if err != nil {
		return err
	}
	result, err := t.exec(ctx, step, generation, after, through)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != want {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}

// mgAdvanceGuideReclaim writes the exact next cursor and proves it by a native
// reread on the same turn. A deleted child page is committed only within this
// frame; any mismatch rolls the whole frame back.
func mgAdvanceGuideReclaim(ctx context.Context, t *commandTurn, before mgGuideReclaimState, next mgGuideReclaimTransition) error {
	if !next.family.valid() || next.sequence != before.sequence+1 || next.revision != before.revision+1 ||
		!mgGuideText(next.cursor, false, mgGuideReclaimCursor) {
		return ErrInvalid
	}
	if err := mgGuideReclaimChange(ctx, t, mgGuideReclaimAdvance, next.sequence, next.revision, string(next.family), next.cursor,
		before.request, before.incarnation[:], before.sequence, before.acknowledged, before.revision, string(before.family), before.cursor); err != nil {
		return err
	}
	post, err := mgReadGuideReclaimNative(ctx, t, before.request)
	if err != nil {
		return err
	}
	want := before
	want.family, want.cursor, want.sequence, want.revision = next.family, next.cursor, next.sequence, next.revision
	if post != want {
		return ErrConflict
	}
	return nil
}

// mgGuideGenerationChildren proves the child families are empty without a final
// COUNT over the generation: three bounded keyset probes.
func mgGuideGenerationChildren(ctx context.Context, t *commandTurn, state mgGuideReclaimState) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	empty := state
	empty.cursor = ""
	for j, id := range []stmt.ID{mgGuideReclaimProgramme, mgGuideReclaimChannel, mgGuideReclaimChoice} {
		probe := empty
		probe.family = []mgGuideReclaimFamily{mgGuideReclaimProgrammes, mgGuideReclaimChannels, mgGuideReclaimChoices}[j]
		page, err := mgReadGuideReclaimKeys(ctx, t, id, probe)
		if err != nil {
			return 0, err
		}
		out += page.rows
	}
	return out, nil
}

// A bounded per-source cleanup candidate from the PK keyset. No scan and no
// aggregated generation count: the caller passes the previous request key.
type mgGuideCleanupCandidate struct {
	request, source, generation string
	incarnation                 [16]byte
	family                      mgGuideReclaimFamily
	cursor                      string
	sequence                    int64
	head                        mgGuideHead
	found                       bool
}

func mgNextGuideCleanupNative(ctx context.Context, t *commandTurn, source, after string) (out mgGuideCleanupCandidate, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgGuideText(source, true, 256) || !mgGuideText(after, false, mgGuideReclaimCursor) {
		return out, ErrInvalid
	}
	head, err := mgGuideHeadFacts(ctx, t, source)
	if err != nil {
		return out, err
	}
	if head.removed {
		return out, ErrDisclosureDenied
	}
	row, err := mgGuideReclaimQuery(ctx, t, mgGuideReclaimCandidate, after, source)
	if err != nil {
		return out, err
	}
	var request string
	if err = row.Scan(&request); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil
		}
		return out, err
	}
	state, err := mgReadGuideReclaimNative(ctx, t, request)
	if err != nil {
		return out, err
	}
	if state.status != "superseded" || state.source != source || state.family == mgGuideReclaimNone ||
		state.family == mgGuideReclaimComplete || state.generation == head.generation {
		return out, ErrConflict
	}
	return mgGuideCleanupCandidate{
		request: state.request, source: state.source, generation: state.generation,
		incarnation: state.incarnation, family: state.family, cursor: state.cursor,
		sequence: state.sequence, head: head, found: true,
	}, nil
}

type mgGuideCleanupInput struct {
	source, after string
	request       string
	identity      CommandIdentity
	witness       mgGuideReclaimWitness
}

// AdvanceGuideCleanup advances exactly one bounded slice for the next superseded
// operation of one source. A stale candidate, another source's head or a forged
// identity is a conflict with no effect.
func mgAdvanceGuideCleanupNative(ctx context.Context, t *commandTurn, input mgGuideCleanupInput) (out mgGuideReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mgGuideReclaimPurpose(t, MGGuideAdvanceCleanup); err != nil {
		return out, err
	}
	if !mgGuideText(input.source, true, 256) || !mgGuideText(input.request, true, 256) || input.witness.source != input.source {
		return out, ErrInvalid
	}
	candidate, err := mgNextGuideCleanupNative(ctx, t, input.source, input.after)
	if err != nil {
		return out, err
	}
	if !candidate.found || candidate.request != input.request || !input.witness.equal(candidate.head) ||
		candidate.incarnation != input.identity.Incarnation || candidate.sequence+1 != input.identity.Sequence {
		return out, ErrConflict
	}
	return mgReclaimGuideSlice(ctx, t, candidate.request, input.identity, input.witness)
}

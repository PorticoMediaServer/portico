package writer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The source is immutable INPUT, frozen before the candidate body. No returned
// effect, production predicate, reducer or receipt decoder supplies this model.
// Digests are opaque fixture inputs: this is not a common-codec proof.
type progressCrashSource struct {
	Operation string
	Initial   effectiveRawJournal
	Snapshot  map[string]effectiveRawCell
	Identity  CommandIdentity
	Request   RequestDigest
	Result    ResultDigest
	Event     *progressCrashEvent
}

type progressCrashEvent struct {
	Audience, Type, ResourceKind, ResourceID, Revision, Data string
	AtMS                                                     int64
}

var progressCrashTables = []string{
	"playback_personal_claims", "playback_claim_command_receipts", "personal_items", "personal_snapshots",
	"personal_activity_revisions", "viewer_revisions", "play_history", "listening_time_spans",
	"listening_item_buckets", "listening_profile_buckets", "listening_play_instants",
	"playback_personal_fences", "playback_personal_profile_fences", "catalog_subjects", "catalog_libraries", "catalog_kinds",
	"search_revision", "sqlite_sequence", "api_events",
}

var progressSnapshotColumns = strings.Fields(`occurred_at event_kind account_key profile_id playback_id occurrence_generation accepted_sequence
item_public_id library_public_id item_kind item_title parent_title season_number episode_number item_year app location live_channel_key
device_key platform delivery_kind position unit played_ms duration_ms completed converted chapter_index chapter_start_ms chapter_end_ms
source_revision cause subject_id activity_generation started_ms time_bucket_ms user_name profile_name device_name membership_incarnation profile_lifetime`)

var progressHistoryByteColumns = strings.Fields(`occurred_at event_kind account_key profile_id playback_id item_public_id library_public_id
item_title parent_title app location live_channel_key device_key platform delivery_kind source_revision cause user_name profile_name device_name
membership_incarnation profile_lifetime`)

var progressClaimByteColumns = strings.Fields(`playback_id profile_key pending_time_clock_incarnation command_authority command_account_id
command_profile_id command_member_incarnation command_profile_lifetime command_incarnation reclaim_operation_id reclaim_request_digest reclaim_command_kind reclaim_result_digest`)

var progressReceiptByteColumns = strings.Fields(`playback_id command_incarnation operation_id request_digest command_kind source_clock_incarnation result_digest`)

func progressRequire(ok bool, message string) {
	if !ok {
		panic(fmt.Errorf("unsupported progress input/model: %s", message))
	}
}

func progressSet(row map[string]effectiveRawCell, key string, value any) {
	_, exists := row[key]
	progressRequire(exists, "unknown column "+key)
	cell, err := effectiveCell(value)
	progressRequire(err == nil, "unsupported cell "+key)
	row[key] = cell
}

func progressSingle(j effectiveRawJournal, name string) map[string]effectiveRawCell {
	table, ok := j[name]
	progressRequire(ok && len(table.Rows) == 1, "expected exactly one "+name)
	return table.Rows[0]
}

func progressBytes(row map[string]effectiveRawCell, names []string) int64 {
	var n int64
	for _, name := range names {
		v, exists := row[name]
		progressRequire(exists, "missing byte-accounted column "+name)
		switch v.Kind {
		case "text":
			n += int64(len(v.Text)) // UTF-8 bytes, not rune count
		case "blob":
			n += int64(len(v.Blob))
		case "null":
		default:
			panic(fmt.Errorf("unsupported byte-accounted type %s.%s", name, v.Kind))
		}
	}
	return n
}

func progressAppend(j effectiveRawJournal, table string, row map[string]effectiveRawCell) {
	t, exists := j[table]
	progressRequire(exists && len(t.Columns) == len(row), "complete column inventory "+table)
	for _, name := range t.Columns {
		_, exists := row[name]
		progressRequire(exists, "unmodeled new column "+table+"."+name)
	}
	t.Rows = append(t.Rows, row)
	sortEffectiveRows(&t)
	j[table] = t
}

func progressNextID(j effectiveRawJournal, name string) int64 {
	var previous int64
	for _, row := range j["sqlite_sequence"].Rows {
		if row["name"].Text == name {
			progressRequire(row["seq"].Kind == "integer" && row["seq"].Int >= 0, "sequence type")
			previous = row["seq"].Int
			progressSet(row, "seq", previous+1)
			return previous + 1
		}
	}
	progressAppend(j, "sqlite_sequence", effectiveModelRow(map[string]any{"name": name, "seq": int64(1)}))
	return 1
}

func progressReceiptRow(source progressCrashSource, start, personal, viewer, observation, history, until, consumed int64) map[string]effectiveRawCell {
	capture := source.Operation == "capture"
	lastAccepted, kind := int64(17), "drain_piece"
	if capture {
		lastAccepted, kind = -1, "capture_report"
	}
	row := effectiveModelRow(map[string]any{
		"playback_id": "playback-音", "command_incarnation": source.Identity.Incarnation[:], "command_sequence": int64(1),
		"operation_id": source.Identity.OperationID[:], "request_digest": source.Request[:], "command_kind": kind,
		"result_version": int64(1), "applied_ms": progressAppliedMS(source.Operation), "source_accepted_ordinal": int64(71), "source_last_accepted_sequence": lastAccepted,
		"source_report_sequence": int64(13), "source_occurrence_generation": int64(5), "source_activity_generation": int64(1),
		"source_history_policy_generation": int64(1), "source_clock_incarnation": []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"source_start_event_id": nil, "source_pending_from_ms": nil, "source_pending_until_ms": nil, "source_pending_cursor_ms": nil,
		"result_start_event_id": start, "result_report_sequence": lastAccepted, "result_pending_sequence": int64(13),
		"result_pending_from_ms": nil, "result_pending_until_ms": nil, "result_pending_cursor_ms": nil,
		"result_viewer_revision": viewer, "result_observation_revision": observation, "result_history_revision": history, "result_personal_revision": personal,
		"capture_created_start": nil, "consumed_from_ms": nil, "consumed_until_ms": nil, "result_next_cursor_ms": nil,
		"result_remaining_until_ms": nil, "finished": nil, "credited_ms": nil, "result_count": int64(1), "result_digest": source.Result[:], "variable_bytes": int64(0),
	})
	if capture {
		progressSet(row, "capture_created_start", int64(1))
		progressSet(row, "result_report_sequence", int64(13))
	} else {
		progressSet(row, "source_start_event_id", start)
		progressSet(row, "source_pending_from_ms", int64(0))
		progressSet(row, "source_pending_until_ms", until)
		progressSet(row, "source_pending_cursor_ms", int64(0))
		progressSet(row, "consumed_from_ms", int64(0))
		progressSet(row, "consumed_until_ms", consumed)
		progressSet(row, "result_next_cursor_ms", consumed)
		progressSet(row, "credited_ms", consumed)
		finished := int64(1)
		if consumed < until {
			finished = 0
			progressSet(row, "result_remaining_until_ms", until)
		}
		progressSet(row, "finished", finished)
	}
	if capture || consumed < until {
		progressSet(row, "result_pending_from_ms", int64(0))
		progressSet(row, "result_pending_until_ms", until)
		progressSet(row, "result_pending_cursor_ms", consumed)
	}
	progressSet(row, "variable_bytes", progressBytes(row, progressReceiptByteColumns))
	return row
}

func progressAppliedMS(operation string) int64 {
	if operation == "drain_pending" {
		return 900001
	}
	return 1000
}

func expectedProgress(source progressCrashSource) (expected effectiveRawJournal, capture wc008CaptureEffect, drain wc008ProgressEffect, err error) {
	if strings.HasPrefix(source.Operation, "ack_") || strings.HasPrefix(source.Operation, "ring_") {
		expected, _, _, err = expectedProgressAux(source)
		return
	}
	defer func() {
		if value := recover(); value != nil {
			expected, capture, drain, err = nil, wc008CaptureEffect{}, wc008ProgressEffect{}, fmt.Errorf("progress oracle: %v", value)
		}
	}()
	progressRequire(source.Operation == "capture" || source.Operation == "drain" || source.Operation == "drain_pending", "operation")
	progressRequire(source.Identity == (CommandIdentity{Incarnation: [16]byte{1}, Sequence: 1, OperationID: [16]byte{2}}), "original identity")
	progressRequire(source.Request != (RequestDigest{}) && source.Result != (ResultDigest{}), "opaque input digests")
	progressRequire(len(source.Initial) == len(progressCrashTables) && len(source.Snapshot) == 43, "original checkpoint/snapshot inventory")
	data, err := json.Marshal(source.Initial)
	progressRequire(err == nil, "clone original")
	progressRequire(json.Unmarshal(data, &expected) == nil, "clone typed checkpoint")
	claim := progressSingle(expected, "playback_personal_claims")
	leaf := progressSingle(expected, "personal_items")
	viewer := progressSingle(expected, "viewer_revisions")
	activity := progressSingle(expected, "personal_activity_revisions")
	progressRequire(claim["command_last_sequence"].Int == 0 && claim["command_acknowledged_sequence"].Int == 0 && claim["receipt_rows"].Int == 0 && claim["receipt_variable_bytes"].Int == 0, "fresh original fixture command slot")
	progressRequire(leaf["revision"].Int == 19 && leaf["played_ms"].Int == 0 && viewer["revision"].Int == 23 && viewer["observation_revision"].Int == 41 && activity["revision"].Int == 31, "original scalar revisions")
	until, consumed := int64(1000), int64(1000)
	if source.Operation == "drain_pending" {
		until, consumed = 900001, 900000
	}
	isCapture := source.Operation == "capture"
	historyRow := map[string]effectiveRawCell{}
	for key, value := range source.Snapshot {
		historyRow[key] = value
	}
	progressSet(historyRow, "id", progressNextID(expected, "play_history"))
	progressSet(historyRow, "occurred_at", time.UnixMilli(progressAppliedMS(source.Operation)).UTC().Format(time.RFC3339Nano))
	progressSet(historyRow, "accepted_sequence", int64(13))
	startID, personalRevision, viewerRevision := int64(1), int64(19), int64(23)
	if isCapture {
		startID, personalRevision, viewerRevision = historyRow["id"].Int, 20, 24
		progressSet(historyRow, "event_kind", "started")
		progressSet(historyRow, "played_ms", int64(0))
		progressSet(historyRow, "started_ms", int64(1000))
		progressSet(historyRow, "time_bucket_ms", nil)
		for key, value := range map[string]any{
			"revision": int64(20), "watched": int64(0), "watched_at": nil, "started_count": int64(1), "play_count": int64(0),
			"last_played_at": "1970-01-01T00:00:01Z", "last_started_at": "1970-01-01T00:00:01Z", "last_completed_at": nil,
			"resume_position": int64(1000), "resume_unit": int64(0), "resume_completed": int64(0), "resume_playback_id": "playback-音",
			"resume_source_revision": "original snapshot", "resume_item_public_id": []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"activity_at": "1970-01-01T00:00:01Z", "activity_state": "playing",
		} {
			progressSet(leaf, key, value)
		}
		state := map[string]any{"revision": int64(20), "watched": false, "favorite": true, "watchlisted": false, "progressSeconds": 1,
			"lastPlayedAt": "1970-01-01T00:00:01Z", "status": "current", "conflicts": []any{}, "rating": nil}
		raw, e := json.Marshal(state)
		progressRequire(e == nil, "independent snapshot encoding")
		progressAppend(expected, "personal_snapshots", effectiveModelRow(map[string]any{"profile_id": "viewer-one", "item_id": int64(11), "revision": int64(20), "state": string(raw), "created_at": "1970-01-01T00:00:01Z"}))
		progressAppend(expected, "listening_play_instants", effectiveModelRow(map[string]any{"profile_id": "viewer-one", "activity_generation": int64(1), "started_ms": int64(1000), "subject_id": int64(11), "started_count": int64(1), "completed_count": int64(0)}))
		for key, value := range map[string]any{"last_accepted_sequence": int64(13), "start_counted": int64(1), "completion_counted": int64(0), "occurrence_start_event_id": startID,
			"pending_time_from_ms": int64(0), "pending_time_until_ms": until, "pending_time_cursor_ms": int64(0), "pending_time_activity_generation": int64(1),
			"pending_time_remember_generation": int64(1), "pending_time_clock_incarnation": []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			"pending_time_sequence": int64(13), "pending_time_start_event_id": startID, "pending_time_occurrence_generation": int64(5)} {
			progressSet(claim, key, value)
		}
	} else {
		progressSet(historyRow, "event_kind", "time")
		progressSet(historyRow, "played_ms", consumed)
		progressSet(historyRow, "time_bucket_ms", int64(0))
		progressSet(leaf, "played_ms", consumed)
		progressAppend(expected, "listening_time_spans", effectiveModelRow(map[string]any{"id": progressNextID(expected, "listening_time_spans"), "profile_id": "viewer-one", "activity_generation": int64(1), "subject_id": int64(11), "from_ms": int64(0), "until_ms": consumed, "copies": int64(1)}))
		if consumed == until {
			for _, key := range strings.Fields(`pending_time_from_ms pending_time_until_ms pending_time_cursor_ms pending_time_activity_generation pending_time_remember_generation pending_time_clock_incarnation pending_time_start_event_id pending_time_occurrence_generation`) {
				progressSet(claim, key, nil)
			}
		} else {
			progressSet(claim, "pending_time_cursor_ms", consumed)
		}
	}
	progressSet(historyRow, "control_variable_bytes", progressBytes(historyRow, progressHistoryByteColumns))
	progressAppend(expected, "play_history", historyRow)
	for _, grain := range []string{"quarter", "day", "month"} {
		started, credit := int64(0), consumed
		if isCapture {
			started, credit = 1, 0
		}
		for _, item := range []bool{true, false} {
			row := map[string]any{"profile_id": "viewer-one", "activity_generation": int64(1), "grain": grain, "bucket_ms": int64(0), "started_count": started, "completed_count": int64(0), "played_ms": credit}
			table := "listening_profile_buckets"
			if item {
				table, row["subject_id"] = "listening_item_buckets", int64(11)
			}
			progressAppend(expected, table, effectiveModelRow(row))
		}
	}
	progressSet(viewer, "revision", viewerRevision)
	progressSet(viewer, "observation_revision", int64(42))
	progressSet(activity, "revision", int64(32))
	receipt := progressReceiptRow(source, startID, personalRevision, viewerRevision, 42, 32, until, consumed)
	if isCapture {
		progressSet(receipt, "result_pending_cursor_ms", int64(0))
	}
	progressAppend(expected, "playback_claim_command_receipts", receipt)
	progressSet(claim, "command_last_sequence", int64(1))
	progressSet(claim, "receipt_rows", int64(1))
	progressSet(claim, "receipt_variable_bytes", receipt["variable_bytes"].Int)
	progressSet(claim, "control_variable_bytes", progressBytes(claim, progressClaimByteColumns))
	if isCapture {
		capture = wc008CaptureEffect{createdStart: true, startEventID: startID, startVariableBytes: historyRow["control_variable_bytes"].Int,
			viewerRevision: 24, observationRevision: 42, historyRevision: 32, personalRevision: 20, controlVariableBytes: claim["control_variable_bytes"].Int}
	} else {
		drain = wc008ProgressEffect{consumedFrom: 0, consumedUntil: consumed, creditedMS: consumed, nextCursor: consumed, finished: consumed == until,
			observationRevision: 42, historyRevision: 32, viewerRevision: 23, personalRevision: 19,
			controlVariableBytes: claim["control_variable_bytes"].Int, adminVariableBytes: historyRow["control_variable_bytes"].Int}
		if consumed < until {
			drain.remainingUntil.Int64, drain.remainingUntil.Valid = until, true
		}
	}
	for name, table := range expected {
		sortEffectiveRows(&table)
		expected[name] = table
	}
	return expected, capture, drain, nil
}

// These are independent state transitions over a previously checked raw
// checkpoint. No ACK helper, event byte calculator or receipt decoder is used.
func expectedProgressAux(source progressCrashSource) (expected effectiveRawJournal, floor int64, event wcAPIEventEffect, err error) {
	defer func() {
		if value := recover(); value != nil {
			expected, floor, event, err = nil, 0, wcAPIEventEffect{}, fmt.Errorf("progress auxiliary oracle: %v", value)
		}
	}()
	data, err := json.Marshal(source.Initial)
	progressRequire(err == nil && json.Unmarshal(data, &expected) == nil, "clone auxiliary checkpoint")
	progressRequire(len(expected) == len(progressCrashTables), "auxiliary table inventory")
	switch source.Operation {
	case "ack_empty", "ack_due", "ack_retry", "ack_capture":
		claim := progressSingle(expected, "playback_personal_claims")
		receipt := progressSingle(expected, "playback_claim_command_receipts")
		progressRequire(source.Identity.Sequence == 1 && claim["command_last_sequence"].Int == 1 && claim["receipt_rows"].Int == 1 && claim["receipt_variable_bytes"].Int == receipt["variable_bytes"].Int && receipt["command_sequence"].Int == 1, "original retained ACK child/debt")
		if source.Operation == "ack_capture" {
			progressRequire(receipt["command_kind"].Text == "capture_report" && receipt["credited_ms"].Kind == "null" && receipt["result_pending_sequence"].Int == 13 && receipt["result_start_event_id"].Int == 2, "real original capture receipt")
			progressRequire(claim["pending_time_sequence"].Int == 13 && claim["pending_time_from_ms"].Int == 0 && claim["pending_time_cursor_ms"].Int == 0 && claim["pending_time_until_ms"].Int == 1000 && len(expected["listening_time_spans"].Rows) == 0 && progressSingle(expected, "personal_items")["played_ms"].Int == 0, "capture ACK retains undrained obligation")
		} else {
			progressRequire(receipt["command_kind"].Text == "drain_piece" && receipt["credited_ms"].Int == 1000, "real original drain receipt")
		}
		progressRequire(claim["reclaim_command_sequence"].Int == 1 && claim["reclaim_result_count"].Int == 1, "retained cleanup slot")
		floor = 1
		if source.Operation == "ack_retry" {
			progressRequire(claim["command_acknowledged_sequence"].Int == 1 && claim["receipt_reclaim_due_ms"].Int == 2000, "retired original floor")
			break
		}
		progressRequire(claim["command_acknowledged_sequence"].Int == 0, "original ACK floor")
		due := int64(2000)
		if source.Operation == "ack_due" {
			progressRequire(claim["receipt_reclaim_due_ms"].Kind == "integer" && claim["receipt_reclaim_due_ms"].Int == 17, "existing earlier due")
			due = 17
		} else {
			progressRequire(claim["receipt_reclaim_due_ms"].Kind == "null", "empty original due")
		}
		progressSet(claim, "command_acknowledged_sequence", floor)
		progressSet(claim, "receipt_reclaim_due_ms", due)
	case "ring_gap", "ring_empty", "ring_capacity":
		q := source.Event
		progressRequire(q != nil && q.Audience != "" && q.Type != "" && q.AtMS == 2000, "original event input")
		progressRequire(len(expected["api_events"].Rows) <= 20000, "owned ring capacity precondition")
		id := progressNextID(expected, "api_events")
		row := effectiveModelRow(map[string]any{"id": id, "audience": q.Audience, "type": q.Type, "resource_kind": q.ResourceKind, "resource_id": q.ResourceID, "revision": q.Revision, "data": q.Data, "at_ms": q.AtMS})
		event.insertedID, event.insertedVariableBytes = id, progressBytes(row, strings.Fields("audience type resource_kind resource_id revision data"))
		var victim map[string]effectiveRawCell
		for _, old := range expected["api_events"].Rows {
			oldID := old["id"].Int
			if oldID <= id-20000 && (victim == nil || oldID < victim["id"].Int) {
				victim = old
			}
		}
		if victim != nil {
			event.removed, event.removedID = true, victim["id"].Int
			event.removedVariableBytes = progressBytes(victim, strings.Fields("audience type resource_kind resource_id revision data"))
			table := expected["api_events"]
			rows := table.Rows[:0]
			for _, old := range table.Rows {
				if old["id"].Int != event.removedID {
					rows = append(rows, old)
				}
			}
			table.Rows = rows
			expected["api_events"] = table
		}
		progressAppend(expected, "api_events", row)
	default:
		progressRequire(false, "unknown auxiliary operation")
	}
	for name, table := range expected {
		sortEffectiveRows(&table)
		expected[name] = table
	}
	return expected, floor, event, nil
}

func TestProgressAuxOracleUsesOriginalRowsAndOneVictim(t *testing.T) {
	initial := effectiveRawJournal{}
	for _, name := range progressCrashTables {
		initial[name] = effectiveRawTable{}
	}
	initial["api_events"] = effectiveRawTable{Columns: strings.Fields("id audience type resource_kind resource_id revision data at_ms")}
	for _, id := range []int64{1, 2, 100} {
		progressAppend(initial, "api_events", effectiveModelRow(map[string]any{"id": id, "audience": "admin", "type": "old", "resource_kind": "library", "resource_id": "音", "revision": "1", "data": "retain", "at_ms": int64(0)}))
	}
	initial["sqlite_sequence"] = effectiveRawTable{Columns: []string{"name", "seq"}, Rows: []map[string]effectiveRawCell{effectiveModelRow(map[string]any{"name": "api_events", "seq": int64(40000)})}}
	source := progressCrashSource{Operation: "ring_gap", Initial: initial, Event: &progressCrashEvent{Audience: "音", Type: "new", AtMS: 2000}}
	want, floor, event, err := expectedProgressAux(source)
	if err != nil || floor != 0 || !event.removed || event.removedID != 1 || event.insertedID != 40001 || event.insertedVariableBytes != 6 || len(want["api_events"].Rows) != 3 {
		t.Fatal(event, floor, err)
	}
	if len(initial["api_events"].Rows) != 3 || initial["sqlite_sequence"].Rows[0]["seq"].Int != 40000 {
		t.Fatal("oracle mutated immutable source")
	}
	for _, id := range []int64{2, 100, 40001} {
		found := false
		for _, row := range want["api_events"].Rows {
			found = found || row["id"].Int == id
		}
		if !found {
			t.Fatal("lost retained row", id)
		}
	}
	source.Operation = "ring_future"
	if _, _, _, err := expectedProgressAux(source); err == nil {
		t.Fatal("unsupported ring accepted")
	}
}

func TestProgressCrashOracleRejectsUnsupportedAndCountsUTF8(t *testing.T) {
	if _, _, _, err := expectedProgress(progressCrashSource{Operation: "future_ack"}); err == nil {
		t.Fatal("missing ACK implementation acquired an empty expectation")
	}
	row := effectiveModelRow(map[string]any{"text": "音", "blob": []byte{0, 1}, "empty": nil})
	if got := progressBytes(row, []string{"text", "blob", "empty"}); got != 5 {
		t.Fatal("native UTF-8 byte debt changed", got)
	}
	if len(progressSnapshotColumns) != 41 || len(progressHistoryByteColumns) != 22 {
		t.Fatal("explicit native snapshot/byte inventory changed")
	}
}

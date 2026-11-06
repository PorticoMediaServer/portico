package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const wc008ProgressStartSnapshot stmt.ID = "wc.progress.start_snapshot"
const wc008HistoryAppend stmt.ID = "wc.progress.history_append"

// Fixed native snapshot, never a receipt body, authority or arbitrary table copier.
// Every position is the named typed column in the literal definition below.
type wc008HistorySnapshot struct{ values [41]any }

func wc008HistoryDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{ID: wc008ProgressStartSnapshot, Owner: "store/writer WC progress", Kind: stmt.Read, CallSites: []string{"store/writer/wc_progress_history.go"}, SQL: `SELECT occurred_at,event_kind,account_key,profile_id,playback_id,occurrence_generation,accepted_sequence,item_public_id,library_public_id,item_kind,item_title,parent_title,season_number,episode_number,item_year,app,location,live_channel_key,device_key,platform,delivery_kind,position,unit,played_ms,duration_ms,completed,converted,chapter_index,chapter_start_ms,chapter_end_ms,source_revision,cause,subject_id,activity_generation,started_ms,time_bucket_ms,user_name,profile_name,device_name,membership_incarnation,profile_lifetime FROM play_history WHERE id=? AND event_kind='started' AND account_key=? AND profile_id=? AND playback_id=? AND occurrence_generation=? AND subject_id=? AND membership_incarnation=? AND profile_lifetime=? AND item_public_id=? AND library_public_id=? AND started_ms IS NOT NULL`, Params: []stmt.Param{{Name: "id", Type: stmt.Integer}, {Name: "account", Type: stmt.Text}, {Name: "profile", Type: stmt.Text}, {Name: "playback", Type: stmt.Text}, {Name: "occurrence", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "member_birth", Type: stmt.Text}, {Name: "profile_birth", Type: stmt.Text}, {Name: "public_id", Type: stmt.Blob}, {Name: "library", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{int64(1), "account-one", "profile-one", "playback-音", int64(5), int64(11), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "library-one"}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"play_history"}}},
		{ID: wc008HistoryAppend, Owner: "store/writer WC progress", Kind: stmt.Write, CallSites: []string{"store/writer/wc_progress_history.go"}, SQL: `INSERT INTO play_history(occurred_at,event_kind,account_key,profile_id,playback_id,occurrence_generation,accepted_sequence,item_public_id,library_public_id,item_kind,item_title,parent_title,season_number,episode_number,item_year,app,location,live_channel_key,device_key,platform,delivery_kind,position,unit,played_ms,duration_ms,completed,converted,chapter_index,chapter_start_ms,chapter_end_ms,source_revision,cause,subject_id,activity_generation,started_ms,time_bucket_ms,user_name,profile_name,device_name,membership_incarnation,profile_lifetime) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id,control_variable_bytes`, Params: []stmt.Param{{Name: "occurred_at", Type: stmt.Text},
			{Name: "event_kind", Type: stmt.Text},
			{Name: "account_key", Type: stmt.Text},
			{Name: "profile_id", Type: stmt.Text},
			{Name: "playback_id", Type: stmt.Text},
			{Name: "occurrence_generation", Type: stmt.Integer},
			{Name: "accepted_sequence", Type: stmt.Integer},
			{Name: "item_public_id", Type: stmt.Blob, Nullable: true},
			{Name: "library_public_id", Type: stmt.Text, Nullable: true},
			{Name: "item_kind", Type: stmt.Integer, Nullable: true},
			{Name: "item_title", Type: stmt.Text, Nullable: true},
			{Name: "parent_title", Type: stmt.Text, Nullable: true},
			{Name: "season_number", Type: stmt.Integer, Nullable: true},
			{Name: "episode_number", Type: stmt.Integer, Nullable: true},
			{Name: "item_year", Type: stmt.Integer, Nullable: true},
			{Name: "app", Type: stmt.Text, Nullable: true},
			{Name: "location", Type: stmt.Text, Nullable: true},
			{Name: "live_channel_key", Type: stmt.Text, Nullable: true},
			{Name: "device_key", Type: stmt.Text, Nullable: true},
			{Name: "platform", Type: stmt.Text, Nullable: true},
			{Name: "delivery_kind", Type: stmt.Text, Nullable: true},
			{Name: "position", Type: stmt.Integer},
			{Name: "unit", Type: stmt.Integer},
			{Name: "played_ms", Type: stmt.Integer},
			{Name: "duration_ms", Type: stmt.Integer, Nullable: true},
			{Name: "completed", Type: stmt.Integer},
			{Name: "converted", Type: stmt.Integer},
			{Name: "chapter_index", Type: stmt.Integer, Nullable: true},
			{Name: "chapter_start_ms", Type: stmt.Integer, Nullable: true},
			{Name: "chapter_end_ms", Type: stmt.Integer, Nullable: true},
			{Name: "source_revision", Type: stmt.Text, Nullable: true},
			{Name: "cause", Type: stmt.Text, Nullable: true},
			{Name: "subject_id", Type: stmt.Integer, Nullable: true},
			{Name: "activity_generation", Type: stmt.Integer},
			{Name: "started_ms", Type: stmt.Integer, Nullable: true},
			{Name: "time_bucket_ms", Type: stmt.Integer, Nullable: true},
			{Name: "user_name", Type: stmt.Text, Nullable: true},
			{Name: "profile_name", Type: stmt.Text, Nullable: true},
			{Name: "device_name", Type: stmt.Text, Nullable: true},
			{Name: "membership_incarnation", Type: stmt.Text, Nullable: true},
			{Name: "profile_lifetime", Type: stmt.Text, Nullable: true}}, Cohorts: []stmt.Cohort{{Name: "owned_point", Bindings: []any{"1970-01-01T00:00:01Z", "time", "account-one", "profile-one", "playback-音", int64(5), int64(13), []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "library-one", int64(1), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, int64(1000), int64(0), int64(1000), int64(100000), int64(0), int64(0), nil, nil, nil, nil, nil, int64(11), int64(1), int64(0), int64(0), nil, nil, nil, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}, Plan: stmt.Plan{Hot: true}},
	}
}

func readWC008Start(ctx context.Context, turn *commandTurn, q wc008ProgressCommand) (out wc008HistorySnapshot, err error) {
	row, err := wc008DraftQuery(ctx, turn, wc008ProgressStartSnapshot, q.startEventID, q.accountKey, q.profileID, q.proof.playbackID, q.proof.occurrenceGeneration, q.subjectID, q.memberIncarnation, q.profileLifetime, q.subjectPublicID[:], q.libraryKey)
	if err != nil {
		return out, err
	}
	var targets [41]any
	for i := range targets {
		targets[i] = &out.values[i]
	}
	if err = row.Scan(targets[:]...); errors.Is(err, sql.ErrNoRows) {
		return wc008HistorySnapshot{}, ErrConflict
	} else if err != nil {
		return wc008HistorySnapshot{}, err
	}
	public, ok := out.values[7].([]byte)
	if !ok || !bytes.Equal(public, q.subjectPublicID[:]) {
		return wc008HistorySnapshot{}, ErrResolveRequired
	}
	out.values[7] = bytes.Clone(public)
	return out, turn.checkLifetimes(ctx)
}

func appendWC008History(ctx context.Context, turn *commandTurn, s wc008HistorySnapshot) (id, variableBytes int64, err error) {
	row, err := wc008DraftQuery(ctx, turn, wc008HistoryAppend, s.values[:]...)
	if err != nil {
		return 0, 0, err
	}
	if err = row.Scan(&id, &variableBytes); err != nil {
		return 0, 0, err
	}
	if id <= 0 || variableBytes <= 0 {
		return 0, 0, ErrResolveRequired
	}
	return id, variableBytes, turn.checkLifetimes(ctx)
}

func appendWC008Time(ctx context.Context, turn *commandTurn, q wc008ProgressCommand, credit, quarter int64) (int64, error) {
	s, err := readWC008Start(ctx, turn, q)
	if err != nil {
		return 0, err
	}
	s.values[0], s.values[1], s.values[6] = q.appliedAt, "time", q.proof.reportSequence
	s.values[23], s.values[35] = credit, quarter
	_, n, err := appendWC008History(ctx, turn, s)
	return n, err
}

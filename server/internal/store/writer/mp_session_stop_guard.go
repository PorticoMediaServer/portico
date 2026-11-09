package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const mpSessionDrained stmt.ID = "mp.session_retire.parent_exit"

// Only the owning retirement participant may call this under its current
// library/source/account fence and existing W admission. This is a guard, never
// a deletion capability or source witness. In particular, an asset's presence
// in a source does not establish this session's original source after children
// have drained. Source-scoped retirement remains held without that witness.
func requireMPSessionParentDrained(ctx context.Context, turn *commandTurn, sessionID string) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = mpSessionRetirementTurn(turn); err != nil {
		return err
	}
	if !mpWorkerID(sessionID) {
		return ErrInvalid
	}
	if _, err = wc008DraftInteger(ctx, turn, mpSessionDrained, sessionID); err != nil {
		return err
	}
	for _, p := range mpSessionCustodyPoints {
		row, e := wc008DraftQuery(ctx, turn, p.id, sessionID)
		if e != nil {
			return e
		}
		var present int64
		e = row.Scan(&present)
		if e == nil {
			return ErrConflict
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	return turn.checkLifetimes(ctx)
}
func mpSessionRetirementTurn(t *commandTurn) error {
	if t == nil || t.transaction == nil || !t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch t.transaction.descriptor.ID {
	case 715, 716, 718, 719, IARetireLibrary, IARetireSource, IARetireStep:
		return nil
	default:
		return ErrInvalid
	}
}

type mpSessionCustodyPoint struct {
	id         stmt.ID
	table, key string
}

// Complete incoming physical-parent graph, including inherited CASCADE edges:
// even those children must drain before a parent can erase retained evidence.
// Additional logical media references have no FK but still block reclamation.
var mpSessionCustodyPoints = []mpSessionCustodyPoint{
	{"mp.session_retire.child.lyric_selections", "lyric_selections", "session_id"},
	{"mp.session_retire.child.playback_airplay_presentations", "playback_airplay_presentations", "media_session_id"},
	{"mp.session_retire.child.playback_audio_plans", "playback_audio_plans", "session_id"},
	{"mp.session_retire.child.playback_conversion_observations", "playback_conversion_observations", "session_id"},
	{"mp.session_retire.child.playback_delivery_plans", "playback_delivery_plans", "session_id"},
	{"mp.session_retire.child.playback_disc_pins", "playback_disc_pins", "session_id"},
	{"mp.session_retire.child.playback_disc_snapshots", "playback_disc_snapshots", "session_id"},
	{"mp.session_retire.child.playback_hls_demand", "playback_hls_demand", "session_id"},
	{"mp.session_retire.child.playback_hls_stages", "playback_hls_stages", "session_id"},
	{"mp.session_retire.child.playback_hls_windows", "playback_hls_windows", "session_id"},
	{"mp.session_retire.child.playback_manifest_subtitles", "playback_manifest_subtitles", "session_id"},
	{"mp.session_retire.child.playback_observations", "playback_observations", "session_id"},
	{"mp.session_retire.child.playback_physical_source_pins", "playback_physical_source_pins", "session_id"},
	{"mp.session_retire.child.playback_private_presentations", "playback_private_presentations", "session_id"},
	{"mp.session_retire.child.playback_source_pins", "playback_source_pins", "session_id"},
	{"mp.session_retire.child.playback_subtitle_pins", "playback_subtitle_pins", "session_id"},
	{"mp.session_retire.child.playback_subtitle_presentations", "playback_subtitle_presentations", "session_id"},
	{"mp.session_retire.child.playback_subtitle_state", "playback_subtitle_state", "session_id"},
	{"mp.session_retire.child.playback_vod_plans", "playback_vod_plans", "session_id"},
	{"mp.session_retire.child.prepared_media_session_pins", "prepared_media_session_pins", "session_id"},
	{"mp.session_retire.child.remote_playback", "remote_playback", "session_id"},
	{"mp.session_retire.child.subtitle_remote_sessions", "subtitle_remote_sessions", "session_id"},
	{"mp.session_retire.child.subtitle_selection_operations", "subtitle_selection_operations", "session_id"},
	{"mp.session_retire.child.mp_physical_command_receipts", "mp_physical_command_receipts", "owner_id"},
	{"mp.session_retire.child.mp_queue_stop_obligations", "mp_queue_stop_obligations", "media_session_id"},
	{"mp.session_retire.child.mp_next_stop_obligations", "mp_next_stop_obligations", "media_session_id"},
	{"mp.session_retire.child.mp_media_inputs", "mp_media_inputs", "media_session_id"},
	{"mp.session_retire.child.mp_session_replacements", "mp_session_replacements", "old_media_id"},
	{"mp.session_retire.child.mp_remote_delivery_inputs", "mp_remote_delivery_inputs", "media_session_id"},
	{"mp.session_retire.child.mp_direct_delivery", "mp_direct_delivery", "session_id"},
	{"mp.session_retire.child.mp_media_read_pins", "mp_media_read_pins", "media_session_id"},
	{"mp.session_retire.child.mp_hls_plans", "mp_hls_plans", "session_id"},
	{"mp.session_retire.child.mp_external_guide_pins", "mp_external_guide_pins", "media_session_id"},
	{"mp.session_retire.child.logical", "playback_v1_sessions", "media_session_id"},
}

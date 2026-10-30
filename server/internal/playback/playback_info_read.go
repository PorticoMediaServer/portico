package playback

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// ReadPlaybackInfoTx reads only the committed VOD media generation. The caller
// must authorize its V1 session and title in this SAME snapshot. This reader
// grants no media access and never opens or refreshes the source.
func ReadPlaybackInfoTx(ctx context.Context, tx *sql.Tx, session string, generation int, viewer identity.Principal) (*PlaybackInfo, error) {
	if tx == nil || session == "" || generation < 1 {
		return nil, nil
	}
	var current int
	err := tx.QueryRowContext(ctx, `SELECT generation FROM playback_sessions WHERE id=? AND generation=? AND state NOT IN('stopped','ended','failed')`, session, generation).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	plan, err := loadDeliveryPlan(ctx, tx, session)
	if errors.Is(err, ErrStaleOffer) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Listening has its own signal model, and a source without a stored video
	// snapshot cannot supply the requested comparison.
	if plan == nil || plan.Trace == nil || plan.Trace.Video == nil {
		return nil, nil
	}
	subtitle, err := readPlaybackSubtitleFacts(ctx, tx, session, current, plan.SourceID, subtitles.ViewerKey(viewer.Authority, viewer.AccountID, viewer.ProfileID, viewer.ServerID))
	if err != nil {
		return nil, err
	}
	return PlaybackInfoForPlan(*plan, subtitle), nil
}

func readPlaybackSubtitleFacts(ctx context.Context, tx *sql.Tx, session string, generation int, asset, viewer string) (*PlaybackSubtitleFacts, error) {
	var resource, renderer string
	out := &PlaybackSubtitleFacts{}
	err := tx.QueryRowContext(ctx, `SELECT state.resource_id,state.renderer,COALESCE(rev.format,''),COALESCE(rev.language,''),COALESCE(rev.title,''),COALESCE(rev.origin,'')
 FROM playback_subtitle_state state
 LEFT JOIN subtitle_resources resource ON resource.id=state.resource_id AND resource.source_id=? AND resource.deleted=0 AND (resource.scope='shared' OR resource.owner=?)
 LEFT JOIN subtitle_revisions rev ON rev.resource_id=resource.id AND rev.revision=state.resource_revision
 WHERE state.session_id=? AND state.generation=?`, asset, viewer, session, generation).Scan(&resource, &renderer, &out.Format, &out.Language, &out.Title, &out.Origin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if resource == "" {
		return &PlaybackSubtitleFacts{Rendering: "off"}, nil
	}
	switch renderer {
	case "client":
		out.Rendering = "drawnByDevice"
	case "external_text":
		out.Rendering = "externalText"
	case "burn_in":
		out.Rendering = "burnedIn"
	}
	return out, nil
}

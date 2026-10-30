package dvr

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

type recordingChannelRef struct {
	source  string
	channel *RecordingChannel
}

func recordingChannelRefs(recordings []Recording) []recordingChannelRef {
	refs := make([]recordingChannelRef, 0, len(recordings))
	for i := range recordings {
		r := &recordings[i]
		refs = append(refs, recordingChannelRef{r.Occurrence.SourceID, &r.Channel})
		for j := range r.Overlaps {
			refs = append(refs, recordingChannelRef{r.Occurrence.SourceID, &r.Overlaps[j].Channel})
		}
	}
	return refs
}

// DVR's HTTP authority is source-level, as in recordingSourcesTx. Marks come
// from the complete CURRENT source lineup in this same snapshot,
// never from the recording page, a programme title or the retained old guide.
func (s *Store) recordingChannelMarksTx(ctx context.Context, tx *sql.Tx, authority livechannels.Authority, owner livechannels.Owner, refs []recordingChannelRef) error {
	for _, ref := range refs {
		ref.channel.Mark = nil
	}
	if len(refs) == 0 {
		return nil
	}
	if s.live == nil || tx == nil || authority == nil || len(refs) > 200 {
		return ErrDenied
	}
	channels := make([]livechannels.Channel, len(refs))
	for i, ref := range refs {
		channels[i] = livechannels.Channel{SourceID: ref.source, ID: ref.channel.ID}
	}
	directory := func(ctx context.Context, snapshot *sql.Tx) (livechannels.DirectoryScope, error) {
		fence, allowed, err := authority(ctx, snapshot, false)
		scope := livechannels.DirectoryScope{Fence: fence, Viewer: owner}
		if err != nil {
			return scope, err
		}
		if fence == "" || allowed == nil {
			return scope, ErrDenied
		}
		rows, err := snapshot.QueryContext(ctx, `SELECT id FROM live_sources WHERE state='active' ORDER BY id`)
		if err != nil {
			return scope, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				return scope, err
			}
			if allowed(id, "") {
				scope.AllowedLiveSources = append(scope.AllowedLiveSources, id)
				if len(scope.AllowedLiveSources) > livechannels.MaxSources {
					return scope, ErrDenied
				}
			}
		}
		return scope, rows.Err()
	}
	if err := s.live.ChannelMarksTx(ctx, tx, directory, channels); err != nil {
		return err
	}
	for i, ref := range refs {
		ref.channel.Mark = channels[i].Mark
	}
	return nil
}

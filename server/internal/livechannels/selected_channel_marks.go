package livechannels

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ChannelMarksTx enriches a bounded selected set using the same complete
// source collision domain as the guide. It joins the caller's read
// transaction; it neither opens another snapshot nor widens directory authority.
// Unknown or unauthorized references receive no mark. Hidden status affects
// listing, not an already selected recording/channel's mark.
func (s *Store) ChannelMarksTx(ctx context.Context, tx *sql.Tx, authorize DirectoryAuthority, channels []Channel) error {
	clearMarks := func() {
		for i := range channels {
			channels[i].Mark = nil
		}
	}
	clearMarks()
	if tx == nil || authorize == nil || len(channels) > 200 {
		return ErrDenied
	}
	if len(channels) == 0 {
		return nil
	}
	scope, err := authorize(ctx, tx)
	if err != nil {
		return err
	}
	if scope.Fence == "" || len(scope.AllowedLiveSources) > MaxSources || len(scope.LibraryRows) > 64 {
		return ErrDenied
	}
	wanted := map[string]bool{}
	for _, c := range channels {
		if c.SourceID != "" && c.ID != "" {
			wanted[c.SourceID] = true
		}
	}
	// Authorize the selected references separately from the full source domain.
	selected := scope
	selected.AllowedLiveSources = nil
	selected.LibraryRows = nil
	for _, source := range scope.AllowedLiveSources {
		if wanted[source] {
			selected.AllowedLiveSources = append(selected.AllowedLiveSources, source)
		}
	}
	for _, row := range scope.LibraryRows {
		if wanted[row.Channel.SourceID] {
			selected.LibraryRows = append(selected.LibraryRows, row)
		}
	}
	eligible := map[string]bool{}
	for _, row := range selected.LibraryRows {
		eligible[row.Channel.SourceID+"\x00"+row.Channel.ID] = true
	}
	for _, source := range selected.AllowedLiveSources {
		ids := []string{}
		for _, c := range channels {
			if c.SourceID == source {
				ids = append(ids, c.ID)
			}
		}
		raw, _ := json.Marshal(ids)
		rows, e := tx.QueryContext(ctx, `SELECT v.channel_id FROM live_sources s JOIN live_channel_versions v ON v.generation_id=s.active_generation WHERE s.id=? AND s.state='active' AND v.channel_id IN (SELECT value FROM json_each(?))`, source, string(raw))
		if e != nil {
			return e
		}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				break
			}
			eligible[source+"\x00"+id] = true
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
	}
	visible := []Channel{}
	for _, c := range channels {
		if eligible[c.SourceID+"\x00"+c.ID] {
			visible = append(visible, c)
		}
	}
	if err = s.directoryMarks(ctx, tx, selected, visible); err != nil {
		clearMarks()
		return err
	}
	current, err := authorize(ctx, tx)
	if err != nil || current.Fence != scope.Fence {
		clearMarks()
		if err != nil {
			return err
		}
		return ErrDenied
	}
	byID := map[string]*ChannelMark{}
	for _, c := range visible {
		byID[c.SourceID+"\x00"+c.ID] = c.Mark
	}
	for i := range channels {
		channels[i].Mark = byID[channels[i].SourceID+"\x00"+channels[i].ID]
	}
	return nil
}

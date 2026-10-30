package librarychannels

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// DirectoryRowsTx projects at most MaxChannels configured library channels in
// the caller's snapshot. It never reads schedule entries or programme facts.
func (s *Store) DirectoryRowsTx(ctx context.Context, tx *sql.Tx, a Authority, viewer livechannels.Owner) ([]livechannels.DirectoryRow, string, error) {
	scope, err := auth(ctx, tx, a, false)
	if err != nil {
		return nil, "", err
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.config_json,c.revision,c.active_generation,c.state,g.published_ms,g.start_ms,g.end_ms,COALESCE(p.favorite,0),0,COALESCE(p.revision,0)
 FROM lc_channels c JOIN lc_generations g ON g.id=c.active_generation AND g.status='published'
 LEFT JOIN live_channel_preferences p ON p.authority=? AND p.account_id=? AND p.profile_id=? AND p.source_id='library:'||c.id AND p.channel_id=c.id
 WHERE c.enabled=1 AND c.removed=0 ORDER BY c.position,c.id`, viewer.Authority, viewer.AccountID, viewer.ProfileID)
	if err != nil {
		return nil, "", unavailable(err)
	}
	result := []livechannels.DirectoryRow{}
	for rows.Next() {
		var raw string
		var c Config
		var v livechannels.DirectoryRow
		var published, start, end int64
		if err = rows.Scan(&raw, &v.Revision, &v.Channel.Generation, &v.Source.RefreshState, &published, &start, &end, &v.Channel.Favorite, &v.Channel.Hidden, &v.Channel.PreferenceRevision); err != nil {
			rows.Close()
			return nil, "", unavailable(err)
		}
		if err = json.Unmarshal([]byte(raw), &c); err != nil {
			rows.Close()
			return nil, "", unavailable(err)
		}
		// Older library channels predate this shared-viewer field, as readChannel does.
		if c.ViewerAccess != "server-members" {
			c.ViewerAccess = "server-members"
		}
		if !scope.permits(c) {
			continue
		}
		v.Channel.ID = c.ID
		v.Channel.SourceID = c.ID
		v.Channel.Provenance = livechannels.LibraryChannel
		v.Channel.Name = c.Name
		v.Channel.Number = fmt.Sprint(c.Position + 1)
		v.Channel.Group = "Library Channels"
		v.Channel.Programmes = []livechannels.Programme{}
		v.Channel.TuneUnavailableReason = "delivery-unavailable"
		v.Channel.RecordUnavailableReason = "not-recordable"
		v.Source.ID = c.ID
		v.Source.Name = c.Name
		v.Source.Generation = v.Channel.Generation
		v.Source.Provenance = livechannels.LibraryChannel
		v.Source.PublishedAt = time.UnixMilli(published).UTC().Format(time.RFC3339)
		v.Source.AvailableStart = time.UnixMilli(start).UTC().Format(time.RFC3339Nano)
		v.Source.AvailableEnd = time.UnixMilli(end).UTC().Format(time.RFC3339Nano)
		v.LogoItemID = c.LogoItemID
		v.Position = c.Position
		result = append(result, v)
		if len(result) > MaxChannels {
			rows.Close()
			return nil, "", ErrUnavailable
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", unavailable(err)
	}
	// Logos are media identities and must obey the profile's content fence too.
	for i := range result {
		if id := result[i].LogoItemID; id != "" {
			var library string
			if tx.QueryRowContext(ctx, `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?) AND e.retired=0 AND cl.retired=0`, id).Scan(&library) == nil && scope.AllowsLibrary(library) && contentaccess.VisibleItemTx(ctx, tx, scope.Principal, id) == nil {
				result[i].Channel.LogoPath = "/v1/items/" + id + "/art/poster"
			}
		}
		result[i].LogoItemID = ""
	}
	return result, scope.Fence, nil
}

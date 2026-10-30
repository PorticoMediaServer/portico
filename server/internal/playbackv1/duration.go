package playbackv1

import (
	"context"
	"database/sql"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The length of what a session is playing, from the catalogue's own file facts
// (the same sum Options gives a version), so an administrator's position
// control has an end. A channel has none; an unknown length is 0.

// versionDuration is the length of version (the version's first file, as
// Options names it) among assets, or of the version played when none is named.
func versionDuration(assets []assetRow, version string) int64 {
	for i, group := range versionsOf(assets) {
		if version != "" && group[0].id != version || version == "" && i > 0 {
			continue
		}
		var total int64
		for _, a := range group {
			total += int64(a.duration * 1000)
		}
		return total
	}
	return 0
}

// sessionDuration reads the length of one session's version.
func (s *Service) sessionDuration(ctx context.Context, r row) (int64, error) {
	if r.item == "" || r.kind == "live" || r.kind == "channel" {
		return 0, nil
	}
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	defer done()
	assets, err := readAssets(ctx, tx, r.item)
	if err != nil {
		return 0, err
	}
	return versionDuration(assets, r.version), nil
}

type durationKey struct {
	entity  int64
	version string
}

// durationsOf reads the lengths of a page of sessions' versions in one bounded
// query (one page of items), not one read per session.
func durationsOf(ctx context.Context, tx *sql.Tx, wanted []durationKey) (map[durationKey]int64, error) {
	out := map[durationKey]int64{}
	seen := map[int64]bool{}
	args := make([]any, 0, len(wanted))
	for _, k := range wanted {
		if k.entity != 0 && !seen[k.entity] {
			seen[k.entity] = true
			args = append(args, k.entity)
		}
	}
	if len(args) == 0 {
		return out, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT link.entity_id,a.token,a.container,a.video_codec,a.size,a.duration,a.width,a.height,link.part_index,link.edition
 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id
 WHERE link.entity_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`) AND a.available=1
 ORDER BY link.entity_id,`+compactcatalog.BestVersionOrder("link", "a"), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assets := map[int64][]assetRow{}
	for rows.Next() {
		var entity int64
		var a assetRow
		if err = rows.Scan(&entity, &a.id, &a.container, &a.videoCodec, &a.size, &a.duration, &a.width, &a.height, &a.part, &a.edition); err != nil {
			return nil, err
		}
		assets[entity] = append(assets[entity], a)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, k := range wanted {
		out[k] = versionDuration(assets[k.entity], k.version)
	}
	return out, nil
}

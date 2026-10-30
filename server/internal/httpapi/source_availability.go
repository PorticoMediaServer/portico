package httpapi

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// sourceAvailability is an advisory projection over current admitted locations,
// not a source-selection or playback grant. Never expose a provider locator.
func (d Dependencies) sourceAvailability(ctx context.Context, item string) (string, error) {
	type location struct{ path, container string }
	locations := []location{}
	err := dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
		q := dbwork.ReadHandle(ctx, d.DB)
		rows, err := q.QueryContext(ctx, `
 SELECT s.root,o.relative_path,a.container FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id
 CROSS JOIN catalog_asset_links l ON l.entity_id=e.id CROSS JOIN catalog_assets a ON a.id=l.asset_id
 CROSS JOIN inventory_objects o INDEXED BY inventory_objects_asset ON o.asset_id=a.token JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id
 WHERE e.public_id=pid_blob(?) AND o.retired=0 AND o.state='available' AND o.unsupported_reason=''
 AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing') AND o.root_incarnation=s.incarnation
 UNION ALL
 SELECT '',a.path,a.container FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id
 CROSS JOIN catalog_asset_links l ON l.entity_id=e.id CROSS JOIN catalog_assets a ON a.id=l.asset_id
 WHERE e.public_id=pid_blob(?) AND EXISTS(SELECT 1 FROM inventory_item_availability av WHERE av.item_id=l.entity_id AND av.asset_id=a.token AND av.available=1)
 AND NOT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources s ON s.id=o.source_id WHERE o.asset_id=a.token AND s.library_id=cl.library_id)
 LIMIT 129`, item, item)
		if err != nil {
			return err
		}
		for rows.Next() {
			var root, relative, container string
			if err = rows.Scan(&root, &relative, &container); err != nil {
				rows.Close()
				return err
			}
			path := relative
			if root != "" {
				if !filepath.IsLocal(relative) || relative == "." {
					continue
				}
				path = filepath.Join(root, relative)
			}
			locations = append(locations, location{path, container})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	// Release the sole SQL connection before adapter health reads.
	state := "unavailable"
	for _, location := range locations {
		current, e := d.RemoteSources.ViewerStatus(ctx, location.path)
		if e != nil {
			return "", e
		}
		if current == "ready" {
			if location.container != "strm" {
				return "ready", nil
			}
			state = "resolve_on_play"
		} else if state != "resolve_on_play" && (current == "changed" || current == "unsupported" && state == "unavailable") {
			state = current
		}
	}
	return state, nil
}

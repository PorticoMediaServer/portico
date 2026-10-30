package subtitles

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/medialimits"
	"strconv"
)

// PersistDiscResourcesTx accepts only tracks measured from the exact cached
// title. Its caller holds the actual inventory job/source/policy publication
// fence. IDs contain no stream/playlist/path identity that clients can choose.
func PersistDiscResourcesTx(ctx context.Context, tx *sql.Tx, object, key, sourceRevision, fingerprint, factsRevision string, policyRevision, origin int64, timingKnown bool, tracks []Track) error {
	if len(tracks) > MaxTracks || !validID(factsRevision) {
		return ErrCapacity
	}
	if len(tracks) > medialimits.SubtitleTracks() {
		tracks = tracks[:medialimits.SubtitleTracks()]
	}
	// A missing measured title clock is unknown, not a zero origin assumption.
	if !timingKnown {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT link.entity_id,a.token,a.size,a.modified_ns,COALESCE(st.revision,0) FROM inventory_disc_facts d JOIN catalog_assets a ON a.token=d.asset_id JOIN catalog_asset_links link ON link.asset_id=a.id LEFT JOIN asset_stream_facts st ON st.asset_id=a.token AND st.size=a.size AND st.modified_ns=a.modified_ns WHERE d.object_id=? AND d.source_revision=? AND d.fingerprint=? AND d.encryption='none' AND a.available=1`, object, sourceRevision, fingerprint)
	if err != nil {
		return err
	}
	type linked struct {
		item, size, modified, facts int64
		asset                       string
	}
	items := []linked{}
	for rows.Next() {
		var v linked
		if err = rows.Scan(&v.item, &v.asset, &v.size, &v.modified, &v.facts); err != nil {
			break
		}
		items = append(items, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range items {
		changed := false
		for _, track := range tracks {
			if track.StreamIndex < 0 || track.Reason != "" || !validFormat(track.Format) {
				continue
			}
			var measured bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_disc_title_media m JOIN inventory_disc_title_media_streams st ON st.object_id=m.object_id AND st.title_key=m.title_key WHERE m.object_id=? AND m.title_key=? AND m.revision=? AND m.source_revision=? AND m.fingerprint=? AND st.stream_index=? AND st.type='subtitle')`, object, key, factsRevision, sourceRevision, fingerprint, track.StreamIndex).Scan(&measured); err != nil {
				return err
			}
			if !measured {
				return ErrConflict
			}
			id := stable("disc-subtitle-v1:" + strconv.FormatInt(v.item, 10) + ":" + v.asset + ":" + object + ":" + key + ":" + factsRevision + ":" + strconv.FormatInt(policyRevision, 10) + ":" + strconv.Itoa(track.StreamIndex))
			language, err := Language(track.Language)
			if err != nil {
				language = "und"
			}
			title := safeTitle(track.Title)
			if track.HearingImpaired && !hearingImpairedTitle(title) {
				title += " SDH"
			}
			renderer := "external_text"
			if !textFormat(track.Format) || track.Format == "ass" || track.Format == "ssa" {
				renderer = "burn_in"
			}
			result, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,discovery_id,discovery_revision,discovered) VALUES(?,?,?,'shared','',1,'',1,0)`, id, v.item, v.asset)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			changed = changed || n > 0
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) VALUES(?,1,'',0,?,?,?,'embedded','','',?,?,?,strftime('%Y-%m-%dT%H:%M:%SZ','now'))`, id, track.Format, language, title, v.size, v.modified, v.facts); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_render_revisions(resource_id,revision,renderer,is_default,is_forced,source_evidence,hearing_impaired) VALUES(?,1,?,?,?,'',?)`, id, renderer, track.Default, track.Forced, track.HearingImpaired); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_disc_resources(resource_id,object_id,title_key,title_facts_revision,stream_index,source_revision,fingerprint,policy_revision,origin_us,timing_known) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, object, key, factsRevision, track.StreamIndex, sourceRevision, fingerprint, policyRevision, origin, timingKnown); err != nil {
				return err
			}
		}
		if changed {
			if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_collections(item_id,revision) VALUES(?,2) ON CONFLICT(item_id) DO UPDATE SET revision=revision+1`, v.item); err != nil {
				return err
			}
		}
	}
	return nil
}

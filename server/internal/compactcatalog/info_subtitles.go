package compactcatalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/subtitleoffer"
)

// projectInfoSubtitles writes at most one raw subtitle or resource per turn.
// The holder remains blocked until both finite passes finish. Header fields
// are source-publication cursors, never reader or per-query work.
func projectInfoSubtitles(ctx context.Context, tx *sql.Tx, c infoChange, item int64) (bool, error) {
	var current, after int64
	var active, phase int
	var key, upper string
	err := tx.QueryRowContext(ctx, `SELECT copy_family,copy_id,copy_phase,copy_aux,copy_key,copy_key3 FROM catalog_info_changes WHERE id=?`, c.id).Scan(&active, &current, &phase, &after, &key, &upper)
	if err != nil {
		return false, err
	}
	if active == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=1,copy_id=?,copy_phase=0,copy_aux=0,copy_key='',copy_key2='',copy_key3='' WHERE id=?`, item, c.id)
		return false, err
	}
	if active != 1 || current != item {
		return false, ErrInfoInvariant
	}
	if phase < 2 {
		var id, origin, format, lang, title, reason string
		var def, forced, hearing bool
		q := `SELECT id,origin,format,language,title,is_default,is_forced,reason,hearing_impaired FROM asset_subtitles WHERE asset_id=?`
		args := []any{c.token}
		if phase == 1 {
			q += ` AND id>?`
			args = append(args, key)
		}
		q += ` ORDER BY id LIMIT 1`
		err = tx.QueryRowContext(ctx, q, args...).Scan(&id, &origin, &format, &lang, &title, &def, &forced, &reason, &hearing)
		if errors.Is(err, sql.ErrNoRows) {
			// All normal discovered resources are shared/owner-empty. The other
			// producers create discovered=0 (disc/private/upload resources), preserved.
			var fence int64
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT rowid FROM subtitle_resources INDEXED BY subtitle_resources_item WHERE item_id=? AND source_id=? AND deleted=0 AND scope='shared' AND owner='' ORDER BY rowid DESC LIMIT 1),0)`, item, c.token).Scan(&fence); err != nil {
				return false, err
			}
			_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_phase=2,copy_aux=0,copy_key='',copy_key3=? WHERE id=?`, strconv.FormatInt(fence, 10), c.id)
			return false, err
		}
		if err != nil {
			return false, err
		}
		if reason == "" && subtitleoffer.ValidFormat(format) {
			var revision, size, modified, facts int64
			var evidence string
			err = tx.QueryRowContext(ctx, `SELECT f.revision,f.size,f.modified_ns,COALESCE(st.revision,0),COALESCE(ev.evidence,'') FROM asset_subtitle_facts f LEFT JOIN asset_stream_facts st ON st.asset_id=f.asset_id AND st.size=f.size AND st.modified_ns=f.modified_ns LEFT JOIN subtitle_inventory_evidence ev ON ev.asset_id=f.asset_id AND ev.revision=f.revision WHERE f.asset_id=?`, c.token).Scan(&revision, &size, &modified, &facts, &evidence)
			if err != nil {
				return false, err
			}
			sum := sha256.Sum256([]byte("discovered:" + strconv.FormatInt(item, 10) + ":" + id))
			resource := hex.EncodeToString(sum[:])
			p := subtitleoffer.Present(format, lang, title, hearing)
			if _, err = tx.ExecContext(ctx, `INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,discovery_id,discovery_revision,discovered) VALUES(?,?,?,'shared','',1,?,?,1) ON CONFLICT(id) DO UPDATE SET current_revision=current_revision+CASE WHEN discovery_revision!=excluded.discovery_revision THEN 1 ELSE 0 END,discovery_revision=excluded.discovery_revision,deleted=0`, resource, item, c.token, id, revision); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) SELECT id,current_revision,'',0,?,?,?,?,'','',?,?,?,strftime('%Y-%m-%dT%H:%M:%SZ','now') FROM subtitle_resources WHERE id=?`, format, p.Language, p.Title, origin, size, modified, facts, resource); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO subtitle_render_revisions(resource_id,revision,renderer,is_default,is_forced,source_evidence,hearing_impaired) SELECT id,current_revision,?,?,?,?,? FROM subtitle_resources WHERE id=?`, p.Renderer, def, forced, evidence, hearing, resource); err != nil {
				return false, err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_phase=1,copy_key=? WHERE id=?`, id, c.id)
		return false, err
	}
	if phase != 2 && phase != 3 {
		return false, ErrInfoInvariant
	}
	fence, err := strconv.ParseInt(upper, 10, 64)
	if err != nil {
		return false, ErrInfoInvariant
	}
	q := `SELECT rowid,id,discovery_id,discovered FROM subtitle_resources INDEXED BY subtitle_resources_item WHERE item_id=? AND source_id=? AND deleted=0 AND scope='shared' AND owner='' AND rowid<=?`
	args := []any{item, c.token, fence}
	if phase == 3 {
		q += ` AND rowid>?`
		args = append(args, after)
	}
	q += ` ORDER BY rowid LIMIT 1`
	var row int64
	var resource, discovery string
	var discovered bool
	err = tx.QueryRowContext(ctx, q, args...).Scan(&row, &resource, &discovery, &discovered)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=0,copy_id=0,copy_phase=0,copy_aux=0,copy_key='',copy_key2='',copy_key3='' WHERE id=?`, c.id)
		return err == nil, err
	}
	if err != nil {
		return false, err
	}
	if discovered {
		var keep bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM asset_subtitles WHERE id=? AND asset_id=? AND reason='') OR EXISTS(SELECT 1 FROM subtitle_disc_resources WHERE resource_id=?)`, discovery, c.token, resource).Scan(&keep); err != nil {
			return false, err
		}
		if !keep {
			if _, err = tx.ExecContext(ctx, `UPDATE subtitle_resources SET deleted=1 WHERE id=? AND deleted=0`, resource); err != nil {
				return false, err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_phase=3,copy_aux=? WHERE id=?`, row, c.id)
	return false, err
}

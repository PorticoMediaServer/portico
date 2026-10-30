package subtitles

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

func stable(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func Persist(tx *sql.Tx, asset string, size, modified int64, f *Inventory) error {
	if f == nil {
		return nil
	}
	if len(f.Tracks) > MaxTracks {
		return ErrCapacity
	}
	if f.Status != "known" {
		var oldSize, oldModified int64
		if err := tx.QueryRow(`SELECT size,modified_ns FROM asset_subtitle_facts WHERE asset_id=?`, asset).Scan(&oldSize, &oldModified); err == nil && oldSize == size && oldModified == modified {
			// An incomplete directory cannot prove a previously seen subtitle
			// disappeared. The caller still records the current scan warning.
			return nil
		}
	}

	raw, e := json.Marshal(f)
	if e != nil {
		return e
	}
	fingerprint := stable(string(raw)) // Fingerprint only; domain rows below are normalized.
	_, e = tx.Exec(`INSERT INTO asset_subtitle_facts VALUES(?,1,?,?,?,?,?,?) ON CONFLICT(asset_id) DO UPDATE SET revision=asset_subtitle_facts.revision+CASE WHEN fingerprint!=excluded.fingerprint OR size!=excluded.size OR modified_ns!=excluded.modified_ns THEN 1 ELSE 0 END,size=excluded.size,modified_ns=excluded.modified_ns,origin_us=excluded.origin_us,timing_known=excluded.timing_known,status=excluded.status,fingerprint=excluded.fingerprint`, asset, size, modified, f.OriginUS, f.TimingKnown, f.Status, fingerprint)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(`INSERT INTO subtitle_inventory_evidence(asset_id,revision,evidence) SELECT asset_id,revision,? FROM asset_subtitle_facts WHERE asset_id=? ON CONFLICT(asset_id) DO UPDATE SET revision=excluded.revision,evidence=excluded.evidence`, f.SourceEvidence, asset); e != nil {
		return e
	}
	if _, e = tx.Exec(`DELETE FROM asset_subtitles WHERE asset_id=?`, asset); e != nil {
		return e
	}
	for _, t := range f.Tracks {
		var sidecar any
		locator := t.Locator
		if t.Origin == "embedded" {
			locator = strconv.Itoa(t.StreamIndex)
		} else {
			sidecar = stable("sidecar:" + locator)
			if _, e = tx.Exec(`INSERT INTO subtitle_sidecars VALUES(?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET size=excluded.size,modified_ns=excluded.modified_ns,digest=excluded.digest`, sidecar, locator, t.Size, t.ModifiedNS, t.Digest); e != nil {
				return e
			}
		}
		id := stable(asset + ":" + t.Origin + ":" + locator)
		if _, e = tx.Exec(`INSERT INTO asset_subtitles VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, asset, t.Origin, locator, t.StreamIndex, sidecar, t.Format, t.Language, t.Title, t.Default, t.Forced, t.Reason, t.HearingImpaired); e != nil {
			return e
		}
		if t.Reason == "" && validFormat(t.Format) {
			language, err := Language(t.Language)
			if err != nil {
				language = "und"
			}
			title := safeTitle(t.Title)
			if t.HearingImpaired && !hearingImpairedTitle(title) {
				title += " SDH"
			}
			renderer := "external_text"
			if !textFormat(t.Format) || t.Format == "ass" || t.Format == "ssa" {
				renderer = "burn_in"
			}
			// Discovery is selectable immediately. Empty bytes mean an internal
			// cache miss, never a viewer-facing preparation state.
			rows, err := tx.Query(`SELECT link.entity_id,COALESCE(st.revision,0) FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id LEFT JOIN asset_stream_facts st ON st.asset_id=a.token AND st.size=a.size AND st.modified_ns=a.modified_ns WHERE a.token=?`, asset)
			if err != nil {
				return err
			}
			type linked struct{ item, facts int64 }
			var items []linked
			for rows.Next() {
				var v linked
				if err = rows.Scan(&v.item, &v.facts); err != nil {
					rows.Close()
					return err
				}
				items = append(items, v)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			var discoveryRevision int64
			if err = tx.QueryRow(`SELECT revision FROM asset_subtitle_facts WHERE asset_id=?`, asset).Scan(&discoveryRevision); err != nil {
				return err
			}
			for _, v := range items {
				resource := stable("discovered:" + strconv.FormatInt(v.item, 10) + ":" + id)
				if _, err = tx.Exec(`INSERT INTO subtitle_resources(id,item_id,source_id,scope,owner,current_revision,discovery_id,discovery_revision,discovered) VALUES(?,?,?,'shared','',1,?,?,1) ON CONFLICT(id) DO UPDATE SET current_revision=current_revision+CASE WHEN discovery_revision!=excluded.discovery_revision THEN 1 ELSE 0 END,discovery_revision=excluded.discovery_revision,deleted=0`, resource, v.item, asset, id, discoveryRevision); err != nil {
					return err
				}
				if _, err = tx.Exec(`INSERT OR IGNORE INTO subtitle_revisions(resource_id,revision,digest,size,format,language,title,origin,rights,original_digest,source_size,source_modified_ns,source_facts_revision,created_at) SELECT id,current_revision,'',0,?,?,?,?,'', '',?,?,?,strftime('%Y-%m-%dT%H:%M:%SZ','now') FROM subtitle_resources WHERE id=?`, t.Format, language, title, t.Origin, size, modified, v.facts, resource); err != nil {
					return err
				}
				if _, err = tx.Exec(`INSERT OR IGNORE INTO subtitle_render_revisions(resource_id,revision,renderer,is_default,is_forced,source_evidence,hearing_impaired) SELECT id,current_revision,?,?,?,?,? FROM subtitle_resources WHERE id=?`, renderer, t.Default, t.Forced, f.SourceEvidence, t.HearingImpaired, resource); err != nil {
					return err
				}
			}
		}

	}
	_, e = tx.Exec(`UPDATE subtitle_resources SET deleted=1 WHERE source_id=? AND discovered=1 AND NOT EXISTS(SELECT 1 FROM subtitle_disc_resources dr WHERE dr.resource_id=subtitle_resources.id) AND discovery_id NOT IN(SELECT id FROM asset_subtitles WHERE asset_id=? AND reason='')`, asset, asset)
	if e != nil {
		return e
	}

	return nil
}

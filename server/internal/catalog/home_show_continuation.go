package catalog

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"strconv"
)

// A profile contributes at most one episode per started show. The index is
// updated with personal activity, so a request begins with started shows and
// never discovers them by scanning the whole library. The candidate checks
// apply restrictions before selecting the one episode: a hidden episode must
// not consume the show's slot.
// showActivityKey is a position in a profile's started shows, newest first:
// the walk continues strictly after it.
type showActivityKey struct {
	lastActivity string
	showID       int64
}

// nextEpisodeWindow is how many of a show's later episodes Continue Watching
// tests for the next one the viewer may watch.
const nextEpisodeWindow = 64

func homeShowContinuation(r HomeRequest, cutoffs, premieres string, window int, showID string, after *showActivityKey, detail ...bool) (string, []any) {
	page := len(detail) > 0 && detail[0]
	partialDismissal := ` AND NOT EXISTS(SELECT 1 FROM continue_dismissals d WHERE d.profile_id=p.profile_id AND d.item_id=p.item_id AND d.playback_id=p.playback_id)`
	nextDismissal := ` AND NOT EXISTS(SELECT 1 FROM continue_dismissals d WHERE d.profile_id=w.profile_id AND d.item_id=i2.id AND d.playback_id=COALESCE((SELECT p.playback_id FROM progress p WHERE p.profile_id=d.profile_id AND p.item_id=d.item_id),''))`
	showDismissal := ` AND NOT EXISTS(SELECT 1 FROM continue_show_dismissals d WHERE d.profile_id=w.profile_id AND d.show_id=w.show_id AND d.last_activity>=w.last_activity AND d.playback_id=COALESCE((SELECT p.playback_id FROM progress p WHERE p.profile_id=d.profile_id AND p.item_id=d.item_id),'')))`
	if page {
		partialDismissal = ""
		nextDismissal = ""
		showDismissal = ")"
	}
	partialVisibility, partialArgs := ItemRestrictionSQL("i2.id", r.Restrictions)
	nextVisibility, nextArgs := ItemRestrictionSQL("i2.id", r.Restrictions)
	premiereVisibility, premiereArgs := ItemRestrictionSQL("early_item.id", r.Restrictions)
	libraries, _ := json.Marshal(r.Libraries)
	showFilter := ""
	if showID != "" {
		showFilter = ` AND show_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`
	}
	if after != nil {
		showFilter += ` AND (last_activity<? OR last_activity=? AND show_id>?)`
	}
	// profile_show_activity already names the newest activity item. When it is
	// still partial, it is necessarily the show's newest partial episode. Try
	// that primary-key lookup before scanning the profile's older progress for
	// a fallback. A profile with thousands of shows otherwise re-walks every
	// progress row once per show.
	partial := func(latest bool) string {
		item := ""
		if latest {
			item = ` AND pa.item_id=CAST(w.last_item_id AS INTEGER)`
		}
		source := `progress_activity pa JOIN catalog_episodes e2 ON e2.entity_id=pa.item_id JOIN catalog_entities i2 ON i2.id=pa.item_id`
		if !latest {
			// A denied/completed latest item must not scan the profile's whole
			// history again for each show, nor the show's every episode (a
			// restricted viewer is often refused the latest one): begin with
			// the profile's unfinished episodes of this show, newest first,
			// which the progress writes keep (profile_show_partials).
			source = `profile_show_partials ps INDEXED BY profile_show_partials_show CROSS JOIN progress_activity pa ON pa.profile_id=ps.profile_id AND pa.item_id=ps.item_id
   CROSS JOIN catalog_episodes e2 ON e2.entity_id=ps.item_id CROSS JOIN catalog_entities i2 ON i2.id=ps.item_id`
			item = ` AND ps.profile_id=w.profile_id AND ps.show_id=w.show_id`
		}
		return `(SELECT pid(i2.public_id) FROM ` + source + `
   CROSS JOIN progress p ON p.profile_id=pa.profile_id AND p.item_id=pa.item_id
   WHERE pa.profile_id=w.profile_id AND e2.show_id=w.show_id AND EXISTS(SELECT 1 FROM catalog_libraries cl WHERE cl.id=i2.library_id AND cl.library_id=w.library_id)
    ` + item + `
	    AND pa.state!='ended' AND p.position>0 AND ` + homeVisible("i2.id") + `
    AND p.position<(SELECT COALESCE(max(NULLIF(COALESCE(ia.end_seconds,a.duration)-ia.start_seconds,0))*1000-3000,9223372036854775807) FROM catalog_asset_links ia JOIN catalog_assets a ON a.id=ia.asset_id WHERE ia.entity_id=i2.id AND a.available=1 AND ia.available=1)
    AND ` + personalstate.CompactSQL("pa.profile_id", "i2.id") + `=0
    ` + partialDismissal + `
    AND ` + partialVisibility + ` AND pa.updated_at>=(SELECT value FROM json_each(?) WHERE key=w.library_id)
   ORDER BY pa.updated_at DESC,pa.item_id DESC LIMIT 1)`
	}
	// The next-episode and premiere checks join the compact episode and
	// season tables by integer id: SQLite cannot flatten a LEFT JOIN onto a
	// view that is itself a join, and would materialise every episode of
	// every library once per statement (NEW-47).
	//
	// The next episode is found in two steps: the later episodes are put in
	// order by their numbers alone, then tested for availability, watched
	// state and the viewer's restriction in that order, stopping at the first
	// that passes. Testing every later episode before ordering them cost a
	// whole show for each started show, mostly for a restricted viewer whose
	// next allowed episode is near. The ordered list keeps its LIMIT so SQLite
	// neither flattens it nor drops its order; it holds the next
	// nextEpisodeWindow episodes, so a show the viewer may see none of soon
	// costs that many tests, not the rest of the show.
	//
	// Most often the next episode is later in the same season: the season's
	// own index reads those first, so a long show is not ordered whole for
	// each started show. The general step below still decides when the
	// season has none the viewer may watch.
	sameSeason := `(SELECT pid(i2.public_id) FROM (SELECT e2.entity_id AS id FROM catalog_episodes previous
   CROSS JOIN catalog_episodes e2 INDEXED BY catalog_episodes_season ON e2.season_id=previous.season_id AND e2.number>=previous.number
   WHERE previous.entity_id=CAST(w.last_item_id AS INTEGER) AND previous.season_id IS NOT NULL AND previous.numbering!='absolute' AND previous.show_id=w.show_id
    AND e2.numbering!='absolute' AND e2.show_id=w.show_id AND (e2.entity_id=previous.entity_id OR e2.number>previous.number)
   ORDER BY e2.number,e2.entity_id LIMIT ` + strconv.Itoa(nextEpisodeWindow) + `) next
   CROSS JOIN catalog_entities i2 ON i2.id=next.id
   WHERE EXISTS(SELECT 1 FROM catalog_libraries cl WHERE cl.id=i2.library_id AND cl.library_id=w.library_id) AND ` + homeVisible("i2.id") + `
    AND ` + personalstate.CompactSQL("w.profile_id", "i2.id") + `=0
    AND COALESCE((SELECT p.position FROM progress p WHERE p.profile_id=w.profile_id AND p.item_id=i2.id),0)=0
    ` + nextDismissal + `
    AND ` + nextVisibility + `
   LIMIT 1)`
	visibleShow := ""
	if r.visibleShows != "" {
		visibleShow = " AND " + r.visibleShows
	}
	base := `SELECT chosen.id,chosen.ord FROM (SELECT COALESCE(
  ` + partial(true) + `,
  ` + partial(false) + `,
  ` + sameSeason + `,
  (SELECT pid(i2.public_id) FROM (SELECT e2.entity_id AS id FROM catalog_entities pe JOIN catalog_episodes previous ON previous.entity_id=pe.id
   LEFT JOIN catalog_seasons ps ON ps.entity_id=previous.season_id AND (SELECT retired FROM catalog_entities WHERE id=previous.season_id)=0
   JOIN catalog_entities sh ON sh.id=w.show_id JOIN catalog_episodes e2 ON e2.show_id=sh.id
   LEFT JOIN catalog_seasons s2 ON s2.entity_id=e2.season_id AND (SELECT retired FROM catalog_entities WHERE id=e2.season_id)=0
	   WHERE pe.id=CAST(w.last_item_id AS INTEGER)
    AND (e2.entity_id=previous.entity_id OR e2.numbering='absolute' AND previous.numbering='absolute' AND e2.number>previous.number
      OR e2.numbering!='absolute' AND previous.numbering!='absolute' AND
       (COALESCE(s2.number,0)>COALESCE(ps.number,0) OR COALESCE(s2.number,0)=COALESCE(ps.number,0) AND e2.number>previous.number))
    AND (COALESCE(s2.number,0)>0 OR COALESCE(ps.number,0)=0)
   ORDER BY COALESCE(s2.number,0),e2.number,e2.entity_id LIMIT ` + strconv.Itoa(nextEpisodeWindow) + `) next
   CROSS JOIN catalog_entities i2 ON i2.id=next.id
   WHERE EXISTS(SELECT 1 FROM catalog_libraries cl WHERE cl.id=i2.library_id AND cl.library_id=w.library_id) AND ` + homeVisible("i2.id") + `
    AND ` + personalstate.CompactSQL("w.profile_id", "i2.id") + `=0
    AND COALESCE((SELECT p.position FROM progress p WHERE p.profile_id=w.profile_id AND p.item_id=i2.id),0)=0
    ` + nextDismissal + `
    AND ` + nextVisibility + `
   LIMIT 1)
	 ) AS id,w.last_activity AS ord,w.library_id AS library_id,w.last_item_id AS last_item_id FROM
	 (SELECT * FROM profile_show_activity WHERE profile_id=? AND library_id IN(SELECT value FROM json_each(?))` + showFilter + ` ORDER BY last_activity DESC,show_id LIMIT ` + strconv.Itoa(window) + `) w
	 WHERE w.profile_id=? AND w.library_id IN(SELECT value FROM json_each(?))` + visibleShow + `
	 ` + showDismissal + ` chosen
	 WHERE chosen.id IS NOT NULL AND (chosen.ord>=(SELECT value FROM json_each(?) WHERE key=chosen.library_id)
	 OR (EXISTS(SELECT 1 FROM json_each(?) WHERE key=chosen.library_id AND value=1)
	 AND EXISTS(SELECT 1 FROM catalog_entities ce JOIN catalog_episodes e ON e.entity_id=ce.id JOIN catalog_seasons se ON se.entity_id=e.season_id AND (SELECT retired FROM catalog_entities WHERE id=e.season_id)=0
	  JOIN catalog_entities pe ON pe.id=CAST(chosen.last_item_id AS INTEGER) JOIN catalog_episodes prior ON prior.entity_id=pe.id
	  LEFT JOIN catalog_seasons sp ON sp.entity_id=prior.season_id AND (SELECT retired FROM catalog_entities WHERE id=prior.season_id)=0
	  WHERE ce.public_id=pid_blob(chosen.id) AND julianday(ce.added_ms/1000.0,'unixepoch')>=julianday((SELECT value FROM json_each(?) WHERE key=chosen.library_id)) AND NOT EXISTS(SELECT 1 FROM catalog_episodes earlier JOIN catalog_entities early_item ON early_item.id=earlier.entity_id WHERE earlier.season_id=e.season_id AND earlier.number<e.number AND ` + homeVisible("early_item.id") + ` AND ` + premiereVisibility + `) AND se.number>COALESCE(sp.number,0))))`
	args := append([]any{}, partialArgs...)
	args = append(args, cutoffs)
	args = append(args, partialArgs...)
	args = append(args, cutoffs)
	args = append(args, nextArgs...)
	args = append(args, nextArgs...)
	args = append(args, r.Profile, string(libraries))
	if showID != "" {
		args = append(args, showID)
	}
	if after != nil {
		args = append(args, after.lastActivity, after.lastActivity, after.showID)
	}
	args = append(args, r.Profile, string(libraries), cutoffs, premieres, cutoffs)
	args = append(args, premiereArgs...)
	return base, args
}

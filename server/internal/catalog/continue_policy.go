package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

// ContinueWatchingSettings belongs to a library's owner configuration. A nil
// premiere choice means an older client did not send the field, not false.
type ContinueWatchingSettings struct {
	Weeks                  int    `json:"weeks"`
	MaximumItems           int    `json:"maximumItems"`
	IncludeSeasonPremieres *bool  `json:"includeSeasonPremieres"`
	VideoPlayedThreshold   int    `json:"videoPlayedThreshold"`
	VideoCompletion        string `json:"videoCompletion"`
}

func DefaultContinueWatchingSettings() ContinueWatchingSettings {
	yes := true
	return ContinueWatchingSettings{Weeks: 16, MaximumItems: 40, IncludeSeasonPremieres: &yes, VideoPlayedThreshold: 95, VideoCompletion: "earliest"}
}

func NormalizeContinueWatchingSettings(v *ContinueWatchingSettings) {
	defaults := DefaultContinueWatchingSettings()
	if v.Weeks == 0 {
		v.Weeks = defaults.Weeks
	}
	if v.MaximumItems == 0 {
		v.MaximumItems = defaults.MaximumItems
	}
	if v.IncludeSeasonPremieres == nil {
		v.IncludeSeasonPremieres = defaults.IncludeSeasonPremieres
	}
	if v.VideoPlayedThreshold == 0 {
		v.VideoPlayedThreshold = defaults.VideoPlayedThreshold
	}
	if v.VideoCompletion == "" {
		v.VideoCompletion = defaults.VideoCompletion
	}
}

func ValidContinueWatchingSettings(v ContinueWatchingSettings) bool {
	return v.Weeks >= 1 && v.Weeks <= 104 && v.MaximumItems >= 1 && v.MaximumItems <= 200 && v.IncludeSeasonPremieres != nil &&
		v.VideoPlayedThreshold >= 50 && v.VideoPlayedThreshold <= 100 &&
		(v.VideoCompletion == "earliest" || v.VideoCompletion == "threshold" || v.VideoCompletion == "credits")
}

func readContinueWatchingSettings(body sql.NullString) ContinueWatchingSettings {
	settings := DefaultContinueWatchingSettings()
	if body.Valid {
		var stored struct {
			ContinueWatching ContinueWatchingSettings `json:"continueWatching"`
		}
		if json.Unmarshal([]byte(body.String), &stored) == nil {
			NormalizeContinueWatchingSettings(&stored.ContinueWatching)
			if ValidContinueWatchingSettings(stored.ContinueWatching) {
				settings = stored.ContinueWatching
			}
		}
	}
	return settings
}

func continueWatchingSettingsTx(tx *sql.Tx, item string) (ContinueWatchingSettings, error) {
	var body sql.NullString
	err := tx.QueryRow(`SELECT d.body FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id LEFT JOIN admin_documents d ON d.scope='library:'||l.library_id WHERE i.public_id=pid_blob(?)`, item).Scan(&body)
	if err != nil {
		return ContinueWatchingSettings{}, err
	}
	return readContinueWatchingSettings(body), nil
}

// The first trusted credits marker in the current source revision can finish
// a video before its percentage threshold. Trusted is what the player may skip
// unattended (segmentmarkers.AutomaticSafe): approved by the owner, or
// confirmed by the show's other episodes while the library's detection is on.
// Single-file guesses cannot mark a title watched. Item boundaries convert a
// multi-episode file's source timeline to the selected episode's own position.
func firstApprovedCreditsTx(tx *sql.Tx, item string) (sql.NullFloat64, error) {
	var start sql.NullFloat64
	policy, err := segmentmarkers.ReadConfidencePolicy(context.Background(), tx)
	if err != nil {
		return start, err
	}
	err = tx.QueryRow(`SELECT min((m.start_us/1000000.0)-ia.start_seconds)
 FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id
 JOIN catalog_asset_links ia ON ia.entity_id=i.id JOIN catalog_assets a ON a.id=ia.asset_id
 JOIN inventory_objects o ON o.asset_id=a.token AND o.retired=0 AND o.state='available'
 JOIN analysis_markers m ON m.object_id=o.id AND m.source_revision=o.revision
 WHERE i.public_id=pid_blob(?) AND m.kind='credits' AND m.deleted=0
 AND (m.approved=1 OR substr(m.provenance,1,?)=? AND m.provenance NOT LIKE '%semantic_ambiguous%' AND m.confidence>=? AND COALESCE((CASE (SELECT json_extract(d.body,'$.segmentDetection') FROM admin_documents d WHERE d.scope='library:'||l.library_id) WHEN 'standard' THEN 'standard' WHEN 'thorough' THEN 'thorough' WHEN 'off' THEN 'off' END),NULLIF((SELECT CASE library.kind WHEN 'tv' THEN settings."segments.tvMode" WHEN 'anime' THEN settings."segments.animeMode" WHEN 'movie' THEN settings."segments.movieMode" END FROM server_settings AS settings JOIN libraries AS library ON library.id=l.library_id WHERE settings.singleton=1),'server'),(SELECT settings."library.segmentDetection" FROM server_settings AS settings WHERE settings.singleton=1),'standard')<>'off')
 AND m.start_us>=ia.start_seconds*1000000.0
 AND m.start_us<COALESCE(ia.end_seconds,a.duration)*1000000.0`, item, len(segmentmarkers.MeasuredFamily+":"), segmentmarkers.MeasuredFamily+":", float64(policy.AutomaticPercent)/100).Scan(&start)
	return start, err
}

func (s *Service) continueWatchingPolicy(libraries []string, now time.Time) (string, string, int, error) {
	cutoffs, premieres := map[string]string{}, map[string]bool{}
	maximum := 0
	if len(libraries) == 0 {
		return "{}", "{}", 40, nil
	}
	raw, _ := json.Marshal(libraries)
	rows, err := s.read().Query(`SELECT l.id,d.body FROM libraries l LEFT JOIN admin_documents d ON d.scope='library:'||l.id WHERE l.id IN(SELECT value FROM json_each(?))`, string(raw))
	if err != nil {
		return "", "", 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var library string
		var body sql.NullString
		if err = rows.Scan(&library, &body); err != nil {
			return "", "", 0, err
		}
		settings := readContinueWatchingSettings(body)
		cutoffs[library] = now.AddDate(0, 0, -7*settings.Weeks).UTC().Format("2006-01-02T15:04:05.000Z")
		premieres[library] = *settings.IncludeSeasonPremieres
		if settings.MaximumItems > maximum {
			maximum = settings.MaximumItems
		}
	}
	if err = rows.Err(); err != nil {
		return "", "", 0, err
	}
	if maximum == 0 {
		maximum = 40
	}
	cutoffJSON, _ := json.Marshal(cutoffs)
	premiereJSON, _ := json.Marshal(premieres)
	return string(cutoffJSON), string(premiereJSON), maximum, nil
}

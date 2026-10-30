package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// Private evidence only. The claim hashes this entire value; fingerprints and
// filesystem/source identifiers are never included in public review responses.
type musicInputs struct {
	Algorithm          string
	PolicyRevision     int64
	LocalMode          string
	AcoustIDEnabled    bool
	ScanRevision       int64
	FingerprintAllowed bool
	Tags               map[string]string
	Duration           int
	FingerprintProblem string
	FingerprintCurrent bool
	Problem            string
}

type AcousticProvider interface {
	Lookup(context.Context, string, int) ([]metadataprovider.AcousticMatch, error)
}

// ConfigureAcoustID installs the server's own application key (the
// environment's). An owner key saved in Server › Metadata replaces it while it
// is set; clearing that key brings this one back.
func (s *Service) ConfigureAcoustID(key string) error {
	key = strings.TrimSpace(key)
	if _, e := metadataprovider.NewAcoustID(key); e != nil {
		return e
	}
	s.acoustidMu.Lock()
	s.acoustidEnvKey = key
	owner := s.acoustidKey != "" && s.acoustidKey != s.acoustidEnvKey
	s.acoustidMu.Unlock()
	if owner {
		return nil
	}
	return s.installAcoustID(key)
}

// useAcoustIDKey applies the owner's saved key ("" for none) over the server's.
func (s *Service) useAcoustIDKey(owner string) error {
	s.acoustidMu.RLock()
	key := s.acoustidEnvKey
	s.acoustidMu.RUnlock()
	if owner != "" {
		key = owner
	}
	return s.installAcoustID(key)
}

func (s *Service) installAcoustID(key string) error {
	s.acoustidMu.Lock()
	defer s.acoustidMu.Unlock()
	if s.acoustid != nil && key == s.acoustidKey {
		return nil
	}
	p, e := metadataprovider.NewAcoustID(key)
	if e != nil {
		// A stored key that no longer parses leaves fingerprint lookup off
		// rather than failing every other provider's settings.
		s.acoustid, s.acoustidConfig, s.acoustidKey = nil, "", key
		return nil
	}
	s.acoustidKey = key
	if p == nil {
		s.acoustid, s.acoustidConfig = nil, ""
		return nil
	}
	digest := sha256.Sum256([]byte(key))
	s.acoustid, s.acoustidConfig = p, hex.EncodeToString(digest[:])
	return nil
}

// acoustidState is the provider in force and its configuration digest.
func (s *Service) acoustidState() (AcousticProvider, string) {
	s.acoustidMu.RLock()
	defer s.acoustidMu.RUnlock()
	return s.acoustid, s.acoustidConfig
}

// acoustidKeySource names which key is in force, for the settings page.
func (s *Service) acoustidKeySource(ownerConfigured bool) string {
	provider, _ := s.acoustidState()
	switch {
	case provider == nil:
		return "none"
	case ownerConfigured:
		return "owner"
	}
	return "environment"
}

func readMusicInputs(ctx context.Context, tx *sql.Tx, kind, id, library string) (musicInputs, error) {
	m := musicInputs{Algorithm: "music-evidence-v1", Tags: map[string]string{}, LocalMode: "prefer"}
	e := tx.QueryRowContext(ctx, `SELECT revision,local_mode,acoustid_enabled FROM audio_metadata_policies WHERE library_id=?`, library).Scan(&m.PolicyRevision, &m.LocalMode, &m.AcoustIDEnabled)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return m, e
	}
	p, e := catalog.ScanPolicyTx(ctx, tx, library)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return m, e
	}
	if e == nil {
		m.ScanRevision = p.Revision
		m.FingerprintAllowed = (p.Tier == "complete" || p.Tier == "custom") && p.Allows("fingerprint")
	}
	where := `al.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`
	fields := `'title','artist','album','album_artist','isrc','date','year','edition','acoustid_fingerprint','fingerprint_algorithm'`
	if kind == "album" {
		where = `al.entity_id IN(SELECT entity_id FROM catalog_songs WHERE album_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)))`
		fields = `'album','album_artist','date','year','barcode','catalognumber','release_country','total_tracks','total_discs','edition'`
	}
	rows, e := tx.QueryContext(ctx, `SELECT DISTINCT t.field,t.value,a.token,a.duration,
  EXISTS(SELECT 1 FROM audio_local_evidence le WHERE le.library_id=t.library_id AND le.asset_id=a.token AND le.field=t.field AND le.value=t.value AND le.size=a.size AND le.modified_ns=a.modified_ns AND NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.asset_id=a.token AND o.retired=0 AND o.revision<>le.source_revision)),
  EXISTS(SELECT 1 FROM audio_local_evidence le WHERE le.library_id=t.library_id AND le.asset_id=a.token),a.available
  FROM audio_tag_evidence t JOIN catalog_assets a ON a.token=t.asset_id JOIN catalog_asset_links al ON al.asset_id=a.id JOIN catalog_entities e ON e.id=al.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id AND cl.library_id=t.library_id
  WHERE `+where+` AND t.field IN(`+fields+`) ORDER BY a.id,t.field,t.value LIMIT 257`, id)
	if e != nil {
		return m, e
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var field, value, asset string
		var duration float64
		var fresh, hasEvidence, available bool
		if e = rows.Scan(&field, &value, &asset, &duration, &fresh, &hasEvidence, &available); e != nil {
			return m, e
		}
		n++
		if n > 256 {
			m.Problem = "evidence_budget"
			break
		}
		if !available || (hasEvidence && !fresh) {
			m.Problem = "source_evidence_changed"
			continue
		}
		// Album-wide fields must agree. Missing values are not contradictions.
		if old := m.Tags[field]; old != "" && !strings.EqualFold(strings.TrimSpace(old), strings.TrimSpace(value)) {
			m.Problem = "conflicting_local_evidence"
		}
		m.Tags[field] = value
		if kind == "song" && duration > 0 && duration <= 86400 && !math.IsNaN(duration) && !math.IsInf(duration, 0) {
			m.Duration = int(math.Round(duration))
		}
		if field == "acoustid_fingerprint" {
			m.FingerprintCurrent = fresh && metadataprovider.ValidFingerprint(value)
		}
	}
	if e = rows.Err(); e != nil {
		return m, e
	}
	rows.Close()
	// Inspect physical membership independently of tag presence. A stitched or
	// bounded item cannot reuse a whole-file fingerprint as recording identity.
	if kind == "song" {
		sources, e := tx.QueryContext(ctx, `SELECT a.duration,al.start_seconds,al.end_seconds,a.available FROM catalog_asset_links al JOIN catalog_assets a ON a.id=al.asset_id WHERE al.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) ORDER BY a.id LIMIT 2`, id)
		if e != nil {
			return m, e
		}
		count := 0
		whole := false
		for sources.Next() {
			var duration, start float64
			var end sql.NullFloat64
			var available bool
			if e = sources.Scan(&duration, &start, &end, &available); e != nil {
				sources.Close()
				return m, e
			}
			count++
			whole = start == 0 && !end.Valid && available
			if end.Valid {
				duration = end.Float64
			}
			duration -= start
			if available && duration > 0 && duration <= 86400 && !math.IsNaN(duration) && !math.IsInf(duration, 0) {
				m.Duration = int(math.Round(duration))
			} else {
				m.Duration = 0
			}
		}
		e = sources.Err()
		sources.Close()
		if e != nil {
			return m, e
		}
		if count != 1 || !whole {
			m.FingerprintCurrent = false
		}
		if count != 1 {
			m.Duration = 0
		}
	}
	if m.Tags["acoustid_fingerprint"] != "" {
		algorithm := m.Tags["fingerprint_algorithm"]
		// The explicitly named embedded AcoustID/Chromaprint tag is a format
		// claim. Generic analysis fingerprints are not read from this channel.
		if algorithm == "" {
			algorithm = "chromaprint"
		}
		code := metadataprovider.FingerprintCompatibility(algorithm, m.Tags["acoustid_fingerprint"])
		if code != "ready" {
			m.FingerprintProblem = code
			m.FingerprintCurrent = false
		}
	}
	return m, nil
}
func musicFingerprintStatus(j mbJob, configured bool) string {
	if j.kind != "song" {
		return "not_applicable"
	}
	if !j.base.Music.AcoustIDEnabled {
		return "disabled"
	}
	if !j.base.Music.FingerprintAllowed {
		return "scan_policy_disallows"
	}
	if !configured {
		return "credential_required"
	}
	if j.base.Music.FingerprintProblem == "unsupported_algorithm" {
		return "unsupported_algorithm"
	}
	if j.base.Music.Tags["acoustid_fingerprint"] == "" {
		return "not_available"
	}
	if !j.base.Music.FingerprintCurrent || j.base.Music.Duration < 1 || j.base.Music.Problem != "" {
		return "stale_or_invalid"
	}
	return "ready"
}

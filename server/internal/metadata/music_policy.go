package metadata

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type MusicPolicy struct {
	Revision           int64  `json:"revision"`
	LocalMode          string `json:"localMode"`
	MusicBrainzEnabled bool   `json:"musicBrainzEnabled"`
	AcoustIDEnabled    bool   `json:"acoustidEnabled"`
	AcoustIDConfigured bool   `json:"acoustidConfigured"`
	FingerprintAllowed bool   `json:"fingerprintAllowed"`
}
type MusicPolicyChange struct {
	ExpectedRevision       int64  `json:"expectedRevision"`
	ExpectedPolicyRevision int64  `json:"expectedPolicyRevision"`
	LocalMode              string `json:"localMode"`
	MusicBrainzEnabled     bool   `json:"musicBrainzEnabled"`
	AcoustIDEnabled        bool   `json:"acoustidEnabled"`
}
type MusicMatchObservation struct {
	FingerprintStatus string  `json:"fingerprintStatus"`
	ProviderError     string  `json:"providerError"`
	Confidence        float64 `json:"confidence"`
	Margin            float64 `json:"margin"`
	StrongSignals     int     `json:"strongSignals"`
	Algorithm         string  `json:"algorithm"`
}

func musicLibrary(tx *sql.Tx, kind, id string) (string, error) {
	q := `SELECT cl.library_id FROM catalog_songs cs JOIN catalog_entities e ON e.id=cs.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`
	if kind == "album" {
		q = `SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`
	} else if kind != "song" {
		return "", sql.ErrNoRows
	}
	var library string
	e := tx.QueryRow(q, id).Scan(&library)
	return library, e
}
func (s *Service) musicPolicy(tx *sql.Tx, library string) (MusicPolicy, error) {
	acoustid, _ := s.acoustidState()
	p := MusicPolicy{AcoustIDConfigured: acoustid != nil}
	e := tx.QueryRow(`SELECT a.revision,a.local_mode,a.acoustid_enabled,m.enabled FROM audio_metadata_policies a JOIN mb_provider_policies m ON m.library_id=a.library_id WHERE a.library_id=?`, library).Scan(&p.Revision, &p.LocalMode, &p.AcoustIDEnabled, &p.MusicBrainzEnabled)
	if e != nil {
		return p, e
	}
	scan, e := catalog.ScanPolicyTx(context.Background(), tx, library)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return p, e
	}
	p.FingerprintAllowed = (scan.Tier == "complete" || scan.Tier == "custom") && scan.Allows("fingerprint")
	return p, nil
}
func (s *Service) UpdateMusicPolicy(kind, id string, m MusicPolicyChange, authorize func(*sql.Tx) error) error {
	if m.ExpectedRevision < 1 || m.ExpectedPolicyRevision < 1 || (m.LocalMode != "prefer" && m.LocalMode != "supplement" && m.LocalMode != "off") {
		return requestinput.New("invalid music metadata policy")
	}
	gated, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return e
		}
	}
	library, e := musicLibrary(tx, kind, id)
	if e != nil {
		return e
	}
	var revision int64
	if e = tx.QueryRow(`SELECT revision FROM mb_jobs WHERE kind=? AND entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, kind, id).Scan(&revision); e != nil {
		return e
	}
	if revision != m.ExpectedRevision {
		return ErrMBConflict
	}
	p, e := s.musicPolicy(tx, library)
	if e != nil {
		return e
	}
	if p.Revision != m.ExpectedPolicyRevision {
		return ErrMBConflict
	}
	// Opt-in is recorded even when no fingerprint exists yet. Real credentials
	// and scan permission are enforced at every acquisition/publication boundary.
	result, e := tx.Exec(`UPDATE audio_metadata_policies SET revision=revision+1,local_mode=?,acoustid_enabled=? WHERE library_id=? AND revision=?`, m.LocalMode, m.AcoustIDEnabled, library, m.ExpectedPolicyRevision)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrMBConflict
	}
	if _, e = tx.Exec(`UPDATE mb_provider_policies SET enabled=? WHERE library_id=? AND enabled<>?`, m.MusicBrainzEnabled, library, m.MusicBrainzEnabled); e != nil {
		return e
	}
	if e = syncLibraryAgentTx(context.Background(), tx, library, m.MusicBrainzEnabled || m.AcoustIDEnabled); e != nil {
		return e
	}
	// Only the selected entity is requeued now. Other in-flight claims are fenced
	// by the library policy revision; library rescans apply descriptive modes.
	if _, e = tx.Exec(`UPDATE mb_jobs SET status='pending',attempts=0,next_attempt='',error='',revision=revision+1,generation=generation+1,lease='',lease_until='' WHERE kind=? AND entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, kind, id); e != nil {
		return e
	}
	return gated.Commit()
}

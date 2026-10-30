package metadata

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// Bounded owner-facing edition/credit evidence. Neither fingerprints nor raw
// provider payloads/paths are projected. The existing selected release ID is
// unchanged, including for the independently owned artwork consumer.
type MusicPublishedDetails struct {
	Status    string                           `json:"status,omitempty"`
	Packaging string                           `json:"packaging,omitempty"`
	Script    string                           `json:"script,omitempty"`
	Language  string                           `json:"language,omitempty"`
	Barcode   string                           `json:"barcode,omitempty"`
	Credits   []metadataprovider.ArtistCredit  `json:"credits"`
	Labels    []metadataprovider.ReleaseLabel  `json:"labels"`
	ISRCs     []string                         `json:"isrcs"`
	Relations []metadataprovider.MusicRelation `json:"relations"`
}

func musicPublishedDetails(tx *sql.Tx, kind, id string) (*MusicPublishedDetails, error) {
	d := &MusicPublishedDetails{Credits: []metadataprovider.ArtistCredit{}, Labels: []metadataprovider.ReleaseLabel{}, ISRCs: []string{}, Relations: []metadataprovider.MusicRelation{}}
	var raw string
	q := `SELECT r.payload FROM mb_recording_evidence r JOIN mb_song_links l ON l.recording_revision=r.revision_id AND l.recording_id=r.provider_id WHERE l.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND r.sealed=1`
	if kind == "album" {
		q = `SELECT r.payload FROM mb_release_evidence r JOIN mb_album_links l ON l.release_revision=r.revision_id AND l.release_id=r.provider_id WHERE l.album_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND r.sealed=1`
	}
	e := tx.QueryRow(q, id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if kind == "album" {
		var r metadataprovider.Release
		if json.Unmarshal([]byte(raw), &r) != nil {
			return nil, errPublicationInput
		}
		if e = validateMusicReleaseDetails(r); e != nil {
			return nil, e
		}
		d.Status = r.Status
		d.Packaging = r.Packaging
		d.Barcode = r.Barcode
		d.Credits = append(d.Credits, r.ArtistCredit...)
		d.Labels = append(d.Labels, r.LabelInfo...)
		if r.TextRepresentation != nil {
			d.Language = r.TextRepresentation.Language
			d.Script = r.TextRepresentation.Script
		}
	} else {
		var r metadataprovider.Recording
		if json.Unmarshal([]byte(raw), &r) != nil {
			return nil, errPublicationInput
		}
		b := mbEvidenceBudget{}
		if e = b.recording(r); e != nil {
			return nil, e
		}
		d.Credits = append(d.Credits, r.ArtistCredit...)
		d.Relations = append(d.Relations, r.Relations...)
		d.ISRCs = append(d.ISRCs, r.ISRCs...)
	}
	return d, nil
}

package metadata

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// Staging validates immutable candidates without authorizing publication.
// The music evidence scorer applies confidence, independent-signal and margin
// requirements; exact typed identity uses the separately fenced lookup path.
type mbCandidateEvidence struct {
	signals                                int
	contradiction                          bool
	id, entityType, title, artist, edition string
	confidence                             float64
	reasons                                []string
	payload                                []byte
}

func mbCandidateReasons(title, artist, queryTitle, queryArtist string) (float64, []string) {
	score := 0.1
	reasons := []string{"provider_name_search", "owner_review_required"}
	if strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(queryTitle)) && strings.TrimSpace(queryTitle) != "" {
		score += 0.2
		reasons = append(reasons, "exact_title")
	}
	if strings.EqualFold(strings.TrimSpace(artist), strings.TrimSpace(queryArtist)) && strings.TrimSpace(queryArtist) != "" {
		score += 0.2
		reasons = append(reasons, "exact_artist")
	}
	return score, reasons
}
func stageMBSongCandidates(values []metadataprovider.Recording, title, artist string) ([]mbCandidateEvidence, error) {
	if len(values) > 25 {
		return nil, fmt.Errorf("MusicBrainz candidate count exceeds budget")
	}
	out := make([]mbCandidateEvidence, 0, len(values))
	seen := map[string]bool{}
	total := 0
	for _, v := range values {
		recording, e := stageMBRecording(metadataprovider.RecordingLookup{RequestedID: v.ID, Recording: v})
		if e != nil {
			return nil, e
		}
		if seen[recording.value.ID] {
			return nil, fmt.Errorf("duplicate MusicBrainz candidate identity")
		}
		seen[recording.value.ID] = true
		if len(recording.payload) > 1<<20 {
			return nil, fmt.Errorf("MusicBrainz candidate exceeds byte budget")
		}
		total += len(recording.payload)
		if total > mbEvidenceBytes {
			return nil, fmt.Errorf("MusicBrainz candidates exceed byte budget")
		}
		r := recording.value
		name := mbArtist(r.ArtistCredit)
		score, reasons := mbCandidateReasons(r.Title, name, title, artist)
		out = append(out, mbCandidateEvidence{id: r.ID, entityType: "recording", title: r.Title, artist: name, edition: r.Disambiguation, confidence: score, reasons: reasons, payload: recording.payload})
	}
	return out, nil
}
func stageMBAlbumCandidates(values []metadataprovider.ReleaseCandidate, title, artist string) ([]mbCandidateEvidence, error) {
	if len(values) > 25 {
		return nil, fmt.Errorf("MusicBrainz candidate count exceeds budget")
	}
	out := make([]mbCandidateEvidence, 0, len(values))
	seen := map[string]bool{}
	total := 0
	for _, v := range values {
		id, e := mbEvidenceID(v.ID)
		if e != nil {
			return nil, e
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate MusicBrainz candidate identity")
		}
		seen[id] = true
		b := mbEvidenceBudget{}
		if v.TrackCount < 0 || v.TrackCount > 10000 {
			return nil, errPublicationInput
		}
		for _, text := range []string{v.Title, v.Disambiguation, v.Date, v.Country, v.Barcode} {
			if e = b.text(text, 4096); e != nil {
				return nil, e
			}
		}
		if e = b.credits(v.ArtistCredit); e != nil {
			return nil, e
		}
		raw, e := json.Marshal(v)
		if e != nil {
			return nil, e
		}
		if len(raw) > 1<<20 {
			return nil, fmt.Errorf("MusicBrainz candidate exceeds byte budget")
		}
		total += len(raw)
		if total > mbEvidenceBytes {
			return nil, fmt.Errorf("MusicBrainz candidates exceed byte budget")
		}
		// Detach credit slices before canonicalizing so the caller's response remains
		// unchanged and later mutation cannot alter the staged payload.
		var frozen metadataprovider.ReleaseCandidate
		if e = json.Unmarshal(raw, &frozen); e != nil {
			return nil, e
		}
		frozen.ID = id
		mbNormalizeCredits(frozen.ArtistCredit)
		raw, e = json.Marshal(frozen)
		if e != nil {
			return nil, e
		}
		name := mbArtist(frozen.ArtistCredit)
		score, reasons := mbCandidateReasons(frozen.Title, name, title, artist)
		out = append(out, mbCandidateEvidence{id: id, entityType: "release", title: frozen.Title, artist: name, edition: strings.TrimSpace(frozen.Date + " " + frozen.Country + " " + frozen.Disambiguation), confidence: score, reasons: reasons, payload: raw})
	}
	return out, nil
}

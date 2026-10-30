package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

const (
	mbEvidenceBytes   = 4 << 20
	mbEvidenceTracks  = 10000
	mbEvidenceMedia   = 200
	mbEvidenceCredits = 128
)

// These values are independently validated, detached acquisition inputs. They
// are not accepted metadata until the publication transaction binds their digest
// and immutable normalized rows to a current operation. Observation time is
// deliberately excluded from content identity.
type mbRecordingEvidence struct {
	requested string
	digest    string
	value     metadataprovider.Recording
	payload   []byte
}
type mbReleaseEvidence struct {
	requested string
	digest    string
	value     metadataprovider.Release
	payload   []byte
}

type mbEvidenceBudget struct{ bytes int }

func (b *mbEvidenceBudget) text(value string, maxBytes int) error {
	if !utf8.ValidString(value) || len(value) > maxBytes || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("invalid MusicBrainz evidence text")
	}
	b.bytes += len(value)
	if b.bytes > mbEvidenceBytes {
		return fmt.Errorf("MusicBrainz evidence exceeds byte budget")
	}
	return nil
}
func mbEvidenceID(value string) (string, error) {
	if !mbIdentifier.MatchString(value) {
		return "", fmt.Errorf("invalid MusicBrainz evidence identifier")
	}
	return strings.ToLower(value), nil
}
func (b *mbEvidenceBudget) credits(values []metadataprovider.ArtistCredit) error {
	if len(values) > mbEvidenceCredits {
		return fmt.Errorf("MusicBrainz artist credits exceed budget")
	}
	for _, v := range values {
		if _, e := mbEvidenceID(v.Artist.ID); e != nil {
			return e
		}
		for _, s := range []string{v.Name, v.Artist.Name, v.Artist.SortName, v.Artist.Disambiguation} {
			if e := b.text(s, 2048); e != nil {
				return e
			}
		}
		if e := b.text(v.JoinPhrase, 256); e != nil {
			return e
		}
	}
	return nil
}
func (b *mbEvidenceBudget) recording(v metadataprovider.Recording) error {
	if _, e := mbEvidenceID(v.ID); e != nil {
		return e
	}
	if e := b.text(v.Title, 4096); e != nil {
		return e
	}
	if e := b.text(v.Disambiguation, 4096); e != nil {
		return e
	}
	if v.LengthMillis != nil && *v.LengthMillis < 0 {
		return fmt.Errorf("negative MusicBrainz recording length")
	}
	if e := b.recordingDetails(v); e != nil {
		return e
	}
	return b.credits(v.ArtistCredit)
}
func mbNormalizeCredits(v []metadataprovider.ArtistCredit) {
	for i := range v {
		v[i].Artist.ID = strings.ToLower(v[i].Artist.ID)
	}
}
func mbNormalizeRecording(v *metadataprovider.Recording) {
	v.ID = strings.ToLower(v.ID)
	normalizeMusicRecordingDetails(v)
	if len(v.ArtistCredit) == 0 {
		v.ArtistCredit = nil
	}
	mbNormalizeCredits(v.ArtistCredit)
}
func mbEvidenceDigest(kind string, raw []byte) string {
	h := sha256.New()
	h.Write([]byte("portico-musicbrainz-evidence-v1\x00" + kind + "\x00"))
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}
func stageMBRecording(result metadataprovider.RecordingLookup) (mbRecordingEvidence, error) {
	out := mbRecordingEvidence{}
	var e error
	if out.requested, e = mbEvidenceID(result.RequestedID); e != nil {
		return out, e
	}
	b := mbEvidenceBudget{}
	if e = b.recording(result.Recording); e != nil {
		return out, e
	}
	raw, e := json.Marshal(result.Recording)
	if e != nil {
		return out, e
	}
	if len(raw) > mbEvidenceBytes {
		return out, fmt.Errorf("MusicBrainz recording exceeds encoded budget")
	}
	if e = json.Unmarshal(raw, &out.value); e != nil {
		return out, e
	}
	mbNormalizeRecording(&out.value)
	out.payload, e = json.Marshal(out.value)
	if e != nil {
		return out, e
	}
	out.digest = mbEvidenceDigest("recording", out.payload)
	return out, nil
}
func stageMBRelease(result metadataprovider.ReleaseLookup) (mbReleaseEvidence, error) {
	out := mbReleaseEvidence{}
	var e error
	if out.requested, e = mbEvidenceID(result.RequestedID); e != nil {
		return out, e
	}
	r := result.Release
	if e = validateMusicReleaseDetails(r); e != nil {
		return out, e
	}
	if _, e = mbEvidenceID(r.ID); e != nil {
		return out, e
	}
	if _, e = mbEvidenceID(r.ReleaseGroup.ID); e != nil {
		return out, e
	}
	if len(r.Media) > mbEvidenceMedia {
		return out, fmt.Errorf("MusicBrainz media exceeds budget")
	}
	b := mbEvidenceBudget{}
	for _, v := range []string{r.Title, r.Disambiguation, r.ReleaseGroup.Title} {
		if e = b.text(v, 4096); e != nil {
			return out, e
		}
	}
	for _, v := range []string{r.Date, r.Country, r.Barcode, r.ReleaseGroup.PrimaryType} {
		if e = b.text(v, 256); e != nil {
			return out, e
		}
	}
	if len(r.ReleaseGroup.SecondaryTypes) > 64 {
		return out, fmt.Errorf("MusicBrainz release types exceed budget")
	}
	for _, v := range r.ReleaseGroup.SecondaryTypes {
		if e = b.text(v, 256); e != nil {
			return out, e
		}
	}
	if e = b.credits(r.ArtistCredit); e != nil {
		return out, e
	}
	media := map[int]bool{}
	tracks := map[string]bool{}
	count := 0
	for _, m := range r.Media {
		if m.Position < 1 || media[m.Position] {
			return out, fmt.Errorf("invalid or duplicate MusicBrainz medium position")
		}
		media[m.Position] = true
		if e = b.text(m.Title, 4096); e != nil {
			return out, e
		}
		if e = b.text(m.Format, 256); e != nil {
			return out, e
		}
		count += len(m.Tracks)
		if count > mbEvidenceTracks {
			return out, fmt.Errorf("MusicBrainz tracks exceed budget")
		}
		positions := map[int]bool{}
		for _, t := range m.Tracks {
			id, e := mbEvidenceID(t.ID)
			if e != nil {
				return out, e
			}
			if tracks[id] || t.Position < 1 || positions[t.Position] {
				return out, fmt.Errorf("duplicate or invalid MusicBrainz track ownership")
			}
			tracks[id] = true
			positions[t.Position] = true
			if e = b.text(t.Title, 4096); e != nil {
				return out, e
			}
			if e = b.text(t.Number, 256); e != nil {
				return out, e
			}
			if t.LengthMillis != nil && *t.LengthMillis < 0 {
				return out, fmt.Errorf("negative MusicBrainz track length")
			}
			if e = b.credits(t.ArtistCredit); e != nil {
				return out, e
			}
			if e = b.recording(t.Recording); e != nil {
				return out, e
			}
		}
	}
	raw, e := json.Marshal(r)
	if e != nil {
		return out, e
	}
	if len(raw) > mbEvidenceBytes {
		return out, fmt.Errorf("MusicBrainz release exceeds encoded budget")
	}
	if e = json.Unmarshal(raw, &out.value); e != nil {
		return out, e
	}
	normalizeMusicReleaseDetails(&out.value)
	out.value.ID = strings.ToLower(out.value.ID)
	out.value.ReleaseGroup.ID = strings.ToLower(out.value.ReleaseGroup.ID)
	if len(out.value.ArtistCredit) == 0 {
		out.value.ArtistCredit = nil
	}
	if len(out.value.Media) == 0 {
		out.value.Media = nil
	}
	if len(out.value.ReleaseGroup.SecondaryTypes) == 0 {
		out.value.ReleaseGroup.SecondaryTypes = nil
	}
	mbNormalizeCredits(out.value.ArtistCredit)
	sort.Slice(out.value.Media, func(i, j int) bool { return out.value.Media[i].Position < out.value.Media[j].Position })
	for i := range out.value.Media {
		m := &out.value.Media[i]
		if len(m.Tracks) == 0 {
			m.Tracks = nil
		}
		sort.Slice(m.Tracks, func(i, j int) bool { return m.Tracks[i].Position < m.Tracks[j].Position })
		for j := range m.Tracks {
			t := &m.Tracks[j]
			t.ID = strings.ToLower(t.ID)
			if len(t.ArtistCredit) == 0 {
				t.ArtistCredit = nil
			}
			mbNormalizeCredits(t.ArtistCredit)
			mbNormalizeRecording(&t.Recording)
		}
	}
	out.payload, e = json.Marshal(out.value)
	if e != nil {
		return out, e
	}
	out.digest = mbEvidenceDigest("release", out.payload)
	return out, nil
}

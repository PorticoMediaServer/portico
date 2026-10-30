package metadata

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

const musicAutomaticConfidence = .85
const musicAutomaticMargin = .12

// Cosmetic normalization only. Live/remix/remaster tokens are deliberately not
// removed: materially different versions must not become an exact title match.
func musicName(v string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, v)
}
func musicSame(a, b string) bool { return strings.TrimSpace(a) != "" && musicName(a) == musicName(b) }
func musicNumber(v string) int {
	v, _, _ = strings.Cut(v, "/")
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	if n < 0 || n > 10000 {
		return 0
	}
	return n
}
func musicYear(v string) string {
	if len(v) >= 4 {
		if n, e := strconv.Atoi(v[:4]); e == nil && n >= 1000 && n <= 9999 {
			return v[:4]
		}
	}
	return ""
}
func rankMusicNames(row *mbCandidateEvidence, title, artist string) {
	row.confidence = .04
	row.reasons = []string{"provider_name_search"}
	row.signals = 0
	row.contradiction = false
	if musicSame(row.title, title) {
		row.confidence += .40
		row.signals++
		row.reasons = append(row.reasons, "exact_title")
	}
	if musicSame(row.artist, artist) && !musicSame(artist, "Unknown artist") {
		row.confidence += .22
		row.signals++
		row.reasons = append(row.reasons, "exact_artist")
	}
}
func rankMusicSong(row *mbCandidateEvidence, j mbJob, recording metadataprovider.Recording, acoustic float64) {
	rankMusicNames(row, j.title, j.artist)
	if recording.LengthMillis != nil && j.base.Music.Duration > 0 {
		distance := math.Abs(float64(*recording.LengthMillis)/1000 - float64(j.base.Music.Duration))
		if distance <= 2 {
			row.confidence += .24
			row.signals++
			row.reasons = append(row.reasons, "duration_match")
		} else if distance > math.Max(4, float64(j.base.Music.Duration)*.03) {
			row.contradiction = true
			row.reasons = append(row.reasons, "duration_conflict")
		}
	}
	if acoustic >= .90 {
		row.confidence += .34
		row.signals++
		row.reasons = append(row.reasons, "acoustid_recording_match")
	}
	isrc := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(j.base.Music.Tags["isrc"]), "-", ""))
	if isrc != "" && len(recording.ISRCs) > 0 {
		found := false
		for _, v := range recording.ISRCs {
			found = found || strings.EqualFold(v, isrc)
		}
		if found {
			row.confidence += .34
			row.signals++
			row.reasons = append(row.reasons, "isrc_match")
		} else {
			row.contradiction = true
			row.reasons = append(row.reasons, "isrc_conflict")
		}
	}
	edition := j.base.Music.Tags["edition"]
	if edition != "" && recording.Disambiguation != "" && !musicSame(edition, recording.Disambiguation) {
		row.contradiction = true
		row.reasons = append(row.reasons, "edition_conflict")
	}
	finishMusicRank(row, j)
}
func rankMusicAlbum(row *mbCandidateEvidence, j mbJob, release metadataprovider.ReleaseCandidate) {
	rankMusicNames(row, j.title, j.artist)
	tags := j.base.Music.Tags
	year := musicYear(tags["date"])
	if year == "" {
		year = musicYear(tags["year"])
	}
	if year != "" && musicYear(release.Date) != "" {
		if year == musicYear(release.Date) {
			row.confidence += .18
			row.signals++
			row.reasons = append(row.reasons, "release_year_match")
		} else {
			row.contradiction = true
			row.reasons = append(row.reasons, "release_year_conflict")
		}
	}
	if tags["barcode"] != "" && release.Barcode != "" {
		if musicSame(tags["barcode"], release.Barcode) {
			row.confidence += .34
			row.signals++
			row.reasons = append(row.reasons, "barcode_match")
		} else {
			row.contradiction = true
			row.reasons = append(row.reasons, "barcode_conflict")
		}
	}
	if country := tags["release_country"]; country != "" && release.Country != "" {
		if strings.EqualFold(country, release.Country) {
			row.confidence += .04
			row.reasons = append(row.reasons, "country_match")
		} else {
			row.contradiction = true
			row.reasons = append(row.reasons, "country_conflict")
		}
	}
	// Counts are supporting release-shape evidence, not a recording identity.
	if n := musicNumber(tags["total_tracks"]); n > 0 && release.TrackCount > 0 {
		if n == release.TrackCount {
			row.confidence += .12
			row.reasons = append(row.reasons, "track_count_match")
		} else {
			row.contradiction = true
			row.reasons = append(row.reasons, "track_count_conflict")
		}
	}
	if edition := tags["edition"]; edition != "" && release.Disambiguation != "" && !musicSame(edition, release.Disambiguation) {
		row.contradiction = true
		row.reasons = append(row.reasons, "edition_conflict")
	}
	finishMusicRank(row, j)
}
func finishMusicRank(row *mbCandidateEvidence, j mbJob) {
	if j.base.Music.Problem != "" {
		row.contradiction = true
		row.reasons = append(row.reasons, "local_evidence_conflict")
	}
	row.confidence = math.Min(1, row.confidence)
	if row.contradiction {
		row.confidence = math.Min(.49, row.confidence)
	}
	// Limit final public reasons to the existing, bounded review contract.
	if len(row.reasons) > 14 {
		row.reasons = row.reasons[:14]
	}
}
func rankMusicCandidates(rows []mbCandidateEvidence, j mbJob) (string, MusicMatchObservation) {
	sort.SliceStable(rows, func(i, k int) bool {
		if rows[i].confidence != rows[k].confidence {
			return rows[i].confidence > rows[k].confidence
		}
		return rows[i].id < rows[k].id
	})
	observation := MusicMatchObservation{Algorithm: "music-evidence-v1", FingerprintStatus: j.fingerprintStatus, ProviderError: j.acousticError}
	if len(rows) == 0 {
		return "", observation
	}
	best := rows[0]
	margin := best.confidence
	if len(rows) > 1 {
		margin -= rows[1].confidence
	}
	observation.Confidence = best.confidence
	observation.Margin = math.Max(0, margin)
	observation.StrongSignals = best.signals
	auto := !j.manual && !j.review && j.base.RecordingID == "" && (j.kind != "album" || j.base.ReleaseID == "") && !best.contradiction && best.confidence+1e-9 >= musicAutomaticConfidence && best.signals >= 2 && margin+1e-9 >= musicAutomaticMargin && !j.incompleteCandidates
	for n := range rows {
		reason := "owner_review_required"
		if n == 0 && auto {
			reason = "automatic_evidence_match"
		}
		filtered := rows[n].reasons[:0]
		for _, previous := range rows[n].reasons {
			if previous != "owner_review_required" && previous != "automatic_evidence_match" {
				filtered = append(filtered, previous)
			}
		}
		rows[n].reasons = append(filtered, reason)
	}
	if !auto {
		return "", observation
	}
	return best.id, observation
}
func musicCandidateRecording(row mbCandidateEvidence) metadataprovider.Recording {
	var v metadataprovider.Recording
	_ = json.Unmarshal(row.payload, &v)
	return v
}

// Search evidence is not a license to publish a conflicting detailed response.
// Re-score the full lookup against the same source and competitors. Provider
// merge redirects require explicit review when they change a search identity.
func musicVerifyRecording(rows []mbCandidateEvidence, j mbJob, id string, v metadataprovider.Recording) bool {
	staged, e := stageMBSongCandidates([]metadataprovider.Recording{v}, j.title, j.artist)
	if e != nil || len(staged) != 1 || staged[0].id != id {
		return false
	}
	for n := range rows {
		if rows[n].id == id {
			acoustic := 0.0
			for _, reason := range rows[n].reasons {
				if reason == "acoustid_recording_match" {
					acoustic = 1
				}
			}
			rankMusicSong(&staged[0], j, v, acoustic)
			rows[n] = staged[0]
		}
	}
	selected, _ := rankMusicCandidates(rows, j)
	return selected == id
}
func musicVerifyRelease(rows []mbCandidateEvidence, j mbJob, id string, v metadataprovider.Release) bool {
	count := 0
	for _, medium := range v.Media {
		count += len(medium.Tracks)
	}
	value := metadataprovider.ReleaseCandidate{ID: v.ID, Title: v.Title, ArtistCredit: v.ArtistCredit, Date: v.Date, Country: v.Country, Barcode: v.Barcode, TrackCount: count, Disambiguation: v.Disambiguation}
	staged, e := stageMBAlbumCandidates([]metadataprovider.ReleaseCandidate{value}, j.title, j.artist)
	if e != nil || len(staged) != 1 || staged[0].id != id {
		return false
	}
	for n := range rows {
		if rows[n].id == id {
			rankMusicAlbum(&staged[0], j, value)
			rows[n] = staged[0]
		}
	}
	selected, _ := rankMusicCandidates(rows, j)
	return selected == id
}

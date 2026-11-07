package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MGScheduleRecording CommandID = 820
	MGUpdateRecording   CommandID = 821
	MGKeepRecording     CommandID = 823
)

func mgRecordingIntentDescriptors() []Descriptor {
	return []Descriptor{{MGScheduleRecording, "mg.recording.schedule", Normal}, {MGUpdateRecording, "mg.recording.update", Normal}, {MGKeepRecording, "mg.recording.keep", Normal}}
}

// Native expected lifetime facts only, never a principal/capability factory.
// Parent owns opaque current authority, rating/source restrictions, original
// public user-mutation receipt/lifetime (cross-1a HELD), events and COMMIT.
type mgRecordingOwner struct {
	account, profile, memberIncarnation, profileLifetime string
	accountEpoch, memberRevision, profileRevision        int64
}

func (o mgRecordingOwner) key() string {
	h := sha256.New()
	for _, v := range []string{"local", o.account, o.profile} {
		_, _ = fmt.Fprintf(h, "%d:%s", len(v), v)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (o mgRecordingOwner) valid() bool {
	return mgRecordingText(o.account, true, 256) && mgRecordingText(o.profile, true, 256) && mgRecordingHex(o.memberIncarnation, 32) && mgRecordingHex(o.profileLifetime, 32) && o.accountEpoch > 0 && o.memberRevision > 0 && o.profileRevision > 0
}
func mgRecordingID(o mgRecordingOwner, source, programme string) string {
	// Existing public DVR occurrence identity, including its original JSON shape.
	raw, _ := json.Marshal([]string{"dvr-occurrence-v1", "local", o.account, o.profile, source, programme})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

type mgRecordingScope struct {
	source, generation, head, channel, programme string
	revision                                     int64
}

func (s mgRecordingScope) valid() bool {
	return mgRecordingHex(s.source, 48) && mgRecordingHex(s.generation, 48) && mgRecordingHex(s.head, 48) && mgRecordingHex(s.channel, 64) && mgRecordingHex(s.programme, 64) && s.revision > 0
}

// Full immutable folded programme row, including nullable native groups. It
// preserves provider fields for exact readback; icon URL is never a public image.
type mgRecordingProgramme struct {
	id, channel, provider, title, start, end, lineage string
	metadata                                          int64
	series, episode, newEvidence, description, facts  mgCaptureNullableText
	hasIcon                                           int64
	icon                                              mgCaptureNullableText
}
type mgRecordingOptions struct {
	BeforeMS      int64 `json:"beforeMs"`
	AfterMS       int64 `json:"afterMs"`
	Priority      int64 `json:"priority"`
	RetentionDays int64 `json:"retentionDays"`
	EpisodeLimit  int64 `json:"episodeLimit"`
}

func (o mgRecordingOptions) valid() bool {
	return o.BeforeMS >= 0 && o.BeforeMS <= 21600000 && o.AfterMS >= 0 && o.AfterMS <= 21600000 && o.Priority >= -1000 && o.Priority <= 1000 && o.RetentionDays >= 0 && o.RetentionDays <= 3650 && o.EpisodeLimit >= 0 && o.EpisodeLimit <= 100000
}

type mgRecordingEpisode struct {
	Season  int64  `json:"season,omitempty"`
	Number  int64  `json:"number,omitempty"`
	Display string `json:"display,omitempty"`
}
type mgRecordingRating struct {
	System string `json:"system"`
	Value  string `json:"value"`
}
type mgRecordingFacts struct {
	Subtitle   string              `json:"subtitle,omitempty"`
	Episode    *mgRecordingEpisode `json:"episode,omitempty"`
	Categories []string            `json:"categories,omitempty"`
	Rating     *mgRecordingRating  `json:"rating,omitempty"`
	Year       int64               `json:"year,omitempty"`
	StarRating string              `json:"starRating,omitempty"`
	Live       bool                `json:"live,omitempty"`
	Premiere   bool                `json:"premiere,omitempty"`
}
type mgRecordingProgrammeCopy struct {
	RecordingID    string              `json:"recordingId,omitempty"`
	RecordingState string              `json:"recordingState,omitempty"`
	ID             string              `json:"id"`
	ChannelID      string              `json:"channelId"`
	Title          string              `json:"title"`
	Start          string              `json:"start"`
	End            string              `json:"end"`
	Lineage        string              `json:"lineage"`
	SeriesID       string              `json:"seriesId,omitempty"`
	EpisodeID      string              `json:"episodeId,omitempty"`
	NewEvidence    string              `json:"newEvidence,omitempty"`
	Description    string              `json:"description,omitempty"`
	Subtitle       string              `json:"subtitle,omitempty"`
	Episode        *mgRecordingEpisode `json:"episode,omitempty"`
	Categories     []string            `json:"categories"`
	Rating         *mgRecordingRating  `json:"rating,omitempty"`
	Year           int64               `json:"year,omitempty"`
	StarRating     string              `json:"starRating,omitempty"`
	Flags          struct {
		Live     bool `json:"live"`
		New      bool `json:"new"`
		Premiere bool `json:"premiere"`
		Repeat   bool `json:"repeat"`
	} `json:"flags"`
	Image string `json:"image,omitempty"`
}

const mgRecordingCopyBytes = 256 << 10

// Owned comparable strings; no external mutable slice/pointer survives freeze.
// programmeJSON/optionsJSON are descriptive compatibility snapshots only. No
// JSON field authorizes an actor, source, claim, publication or physical exit.
type mgRecordingFrozen struct {
	programme                  mgRecordingProgramme
	options                    mgRecordingOptions
	programmeJSON, optionsJSON string
	startMS, endMS             int64
}

func mgRecordingText(s string, required bool, max int) bool {
	return (!required || s != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}
func mgRecordingHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func mgRecordingDecode(raw string, out any) error {
	object := strings.TrimSpace(raw)
	if len(object) < 2 || object[0] != '{' || object[len(object)-1] != '}' {
		return ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrInvalid
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
func freezeMGRecordingIntent(p mgRecordingProgramme, o mgRecordingOptions) (mgRecordingFrozen, error) {
	var zero mgRecordingFrozen
	if !mgRecordingHex(p.id, 64) || !mgRecordingHex(p.channel, 64) || !o.valid() || (p.metadata != 0 && p.metadata != 1) || (p.hasIcon != 0 && p.hasIcon != 1) {
		return zero, ErrInvalid
	}
	total := 0
	for _, s := range []string{p.id, p.channel, p.provider, p.title, p.start, p.end, p.lineage, p.series.text, p.episode.text, p.newEvidence.text, p.description.text, p.facts.text, p.icon.text} {
		if !mgRecordingText(s, false, mgRecordingCopyBytes) {
			return zero, ErrInvalid
		}
		total += len(s)
	}
	if p.title == "" || total > mgRecordingCopyBytes {
		return zero, ErrCap
	}
	for _, v := range []mgCaptureNullableText{p.series, p.episode, p.newEvidence, p.description, p.facts, p.icon} {
		if !v.valid && v.text != "" {
			return zero, ErrInvalid
		}
	}
	if p.metadata == 1 && (!p.series.valid || !p.episode.valid || !p.newEvidence.valid || !p.description.valid || !p.facts.valid) {
		return zero, ErrInvalid
	}
	if p.metadata == 0 && (p.series.valid || p.episode.valid || p.newEvidence.valid || p.description.valid) {
		return zero, ErrInvalid
	}
	if p.hasIcon == 1 && !p.icon.valid || p.hasIcon == 0 && p.icon.valid {
		return zero, ErrInvalid
	}
	a, err := time.Parse(time.RFC3339Nano, p.start)
	if err != nil {
		return zero, ErrInvalid
	}
	b, err := time.Parse(time.RFC3339Nano, p.end)
	if err != nil || !b.After(a) || b.Sub(a) > 7*24*time.Hour {
		return zero, ErrInvalid
	}
	start, end := a.UnixMilli()-o.BeforeMS, b.UnixMilli()+o.AfterMS
	if start < 0 || end <= start {
		return zero, ErrInvalid
	}
	copyProgramme := mgRecordingProgrammeCopy{ID: p.id, ChannelID: p.channel, Title: p.title, Start: p.start, End: p.end, Lineage: p.lineage, NewEvidence: "unknown", Categories: []string{}}
	if p.metadata == 1 {
		if p.newEvidence.text != "new" && p.newEvidence.text != "repeat" && p.newEvidence.text != "unknown" && p.newEvidence.text != "" {
			return zero, ErrInvalid
		}
		copyProgramme.SeriesID, copyProgramme.EpisodeID, copyProgramme.NewEvidence, copyProgramme.Description = p.series.text, p.episode.text, p.newEvidence.text, p.description.text
		var f mgRecordingFacts
		if err := mgRecordingDecode(p.facts.text, &f); err != nil {
			return zero, err
		}
		if len(f.Categories) > 16 || !mgRecordingText(f.Subtitle, false, 512) || !mgRecordingText(f.StarRating, false, 16) || (f.Year != 0 && (f.Year < 1850 || f.Year > 2200)) {
			return zero, ErrInvalid
		}
		for _, c := range f.Categories {
			if !mgRecordingText(c, true, 128) {
				return zero, ErrInvalid
			}
		}
		if f.Episode != nil && (f.Episode.Season < 0 || f.Episode.Season > 9999 || f.Episode.Number < 0 || f.Episode.Number > 9999 || !mgRecordingText(f.Episode.Display, false, 64)) {
			return zero, ErrInvalid
		}
		if f.Rating != nil && (!mgRecordingText(f.Rating.System, false, 64) || !mgRecordingText(f.Rating.Value, true, 32)) {
			return zero, ErrInvalid
		}
		copyProgramme.Subtitle, copyProgramme.Episode, copyProgramme.Rating, copyProgramme.Year, copyProgramme.StarRating = f.Subtitle, f.Episode, f.Rating, f.Year, f.StarRating
		if f.Categories != nil {
			copyProgramme.Categories = f.Categories
		}
		copyProgramme.Flags.Live, copyProgramme.Flags.Premiere = f.Live, f.Premiere
	}
	copyProgramme.Flags.New, copyProgramme.Flags.Repeat = copyProgramme.NewEvidence == "new", copyProgramme.NewEvidence == "repeat"
	raw, err := json.Marshal(copyProgramme)
	if err != nil {
		return zero, err
	}
	options, err := json.Marshal(o)
	if err != nil {
		return zero, err
	}
	if len(raw)+len(options) > mgRecordingCopyBytes {
		return zero, ErrCap
	}
	return mgRecordingFrozen{p, o, string(raw), string(options), start, end}, nil
}
func (f mgRecordingFrozen) valid() bool {
	actual, err := freezeMGRecordingIntent(f.programme, f.options)
	return err == nil && actual == f
}

type mgRecordingBirth struct {
	incarnation, operation mgCaptureID
	digest                 mgCaptureDigest
}

func (b mgRecordingBirth) valid() bool {
	return b.incarnation != (mgCaptureID{}) && b.operation != (mgCaptureID{}) && b.digest != (mgCaptureDigest{})
}

type mgRecordingSnapshot struct {
	capture                           mgCaptureSnapshot
	programmeJSON, optionsJSON        string
	manual, priority, keep, createdMS int64
}

func (s mgRecordingSnapshot) valid() bool {
	if !(s.capture.valid() && mgRecordingHex(s.capture.id, 64) && mgRecordingText(s.programmeJSON, true, mgRecordingCopyBytes) && mgRecordingText(s.optionsJSON, true, 4096) && (s.manual == 0 || s.manual == 1) && (s.keep == 0 || s.keep == 1) && s.priority >= -1000 && s.priority <= 1000 && s.createdMS >= 0) {
		return false
	}
	var p mgRecordingProgrammeCopy
	var o mgRecordingOptions
	return mgRecordingDecode(s.programmeJSON, &p) == nil && mgRecordingDecode(s.optionsJSON, &o) == nil && o.valid() && s.priority == o.Priority && p.ID == s.capture.programmeID && p.ChannelID == s.capture.channelID && mgRecordingCopyValid(p)
}

type mgRecordingReservation struct {
	present                           bool
	source                            string
	startMS, endMS, priority, enabled int64
}
type mgRecordingIntentFacts struct {
	recording     mgRecordingSnapshot
	reservation   mgRecordingReservation
	ownerRevision int64
}

func (s mgRecordingSnapshot) ownerMatches(o mgRecordingOwner) bool {
	n := s.capture
	return n.id == mgRecordingID(o, n.sourceID, n.programmeID) && n.ownerKey == o.key() && n.authority == "local" && n.accountID == o.account && n.profileID == o.profile && n.memberIncarnation == o.memberIncarnation && n.profileLifetime == o.profileLifetime
}
func (s mgRecordingSnapshot) editable() bool {
	n := s.capture
	// No expired/released flag is physical exclusion. Window edits are limited
	// to never-launched, unshared, artifact-free pending intent. Pins survive.
	return (n.state == "scheduled" || n.state == "conflicted" || n.state == "waiting-source" || n.state == "waiting-guide") && n.claimGeneration == 0 && n.cancelGeneration == 0 && n.token == "" && n.ownerInstance == "" && n.leaseMS == 0 && n.allocationID == "" && n.artifactID == "" && n.itemID == 0 && n.recoveryAttempt == 0 && !n.launch.valid && n.allocationGeneration == 0 && n.allocationToken == "" && n.sourceRevision == 0 && n.sourceFence == "" && n.writerRetiredSequence == 0 && n.physicalExitSequence == 0 && n.sharedReadySequence == 0 && n.hasShare == 0 && !n.leaderID.valid && n.guidePin.valid && n.guidePin.text == n.guideGeneration && n.lastSequence == n.ackSequence
}
func mgRecordingRevisionValid(revision int64) bool { return revision > 0 && revision < math.MaxInt64 }

// MG-001 gap/boundary: public mutation lifetime and saved public DTO roots are
// cross-1a HELD and must be composed by the parent; this body creates no shadow
// receipt. Parent must also supply its actual-octet memory/admission profile.
// New births require native capture namespace/lifetimes at sequence/ACK zero.
// Capture commands retain their namespace; intent updates/Keep never advance,
// reset or ACK that worker namespace and never release any guide/artifact debt.

func mgRecordingCopyValid(p mgRecordingProgrammeCopy) bool {
	if !mgRecordingHex(p.ID, 64) || !mgRecordingHex(p.ChannelID, 64) || !mgRecordingText(p.Title, true, mgRecordingCopyBytes) || len(p.Categories) > 16 || !mgRecordingText(p.Subtitle, false, 512) || !mgRecordingText(p.StarRating, false, 16) || (p.Year != 0 && (p.Year < 1850 || p.Year > 2200)) {
		return false
	}
	for _, s := range []string{p.Lineage, p.SeriesID, p.EpisodeID, p.Description, p.Image} {
		if !mgRecordingText(s, false, mgRecordingCopyBytes) {
			return false
		}
	}
	if p.NewEvidence != "" && p.NewEvidence != "unknown" && p.NewEvidence != "new" && p.NewEvidence != "repeat" {
		return false
	}
	if p.Flags.New != (p.NewEvidence == "new") || p.Flags.Repeat != (p.NewEvidence == "repeat") {
		return false
	}
	for _, c := range p.Categories {
		if !mgRecordingText(c, true, 128) {
			return false
		}
	}
	if p.Episode != nil && (p.Episode.Season < 0 || p.Episode.Season > 9999 || p.Episode.Number < 0 || p.Episode.Number > 9999 || !mgRecordingText(p.Episode.Display, false, 64)) {
		return false
	}
	if p.Rating != nil && (!mgRecordingText(p.Rating.System, false, 64) || !mgRecordingText(p.Rating.Value, true, 32)) {
		return false
	}
	a, err := time.Parse(time.RFC3339Nano, p.Start)
	if err != nil {
		return false
	}
	b, err := time.Parse(time.RFC3339Nano, p.End)
	if err != nil || !b.After(a) || b.Sub(a) > 7*24*time.Hour {
		return false
	}
	if p.RecordingID != "" && !mgRecordingHex(p.RecordingID, 64) {
		return false
	}
	if p.RecordingState != "" {
		switch p.RecordingState {
		case "scheduled", "conflicted", "waiting-source", "waiting-guide", "preparing", "recording", "finalizing", "completed", "incomplete-playable", "cancelled", "pending-delete", "deleted", "failed":
		default:
			return false
		}
	}
	return true
}

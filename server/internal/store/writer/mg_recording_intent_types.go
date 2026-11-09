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
)

// MG-10 authored recording intent. 820 schedules one manual external-guide
// occurrence, 821 edits only its padding/options window, 822 cancels the
// logical intent and 823 sets retention. None of these commands starts a
// capture worker, acquires a tuner, performs provider/catalogue/filesystem IO
// or releases physical custody.
const (
	MGScheduleRecording CommandID = 820
	MGUpdateRecording   CommandID = 821
	MGCancelRecording   CommandID = 822
	MGKeepRecording     CommandID = 823
)

func mgRecordingIntentDescriptors() []Descriptor {
	return []Descriptor{
		{MGScheduleRecording, "mg.recording.schedule", Normal},
		{MGUpdateRecording, "mg.recording.update", Normal},
		{MGCancelRecording, "mg.recording.cancel", Normal},
		{MGKeepRecording, "mg.recording.keep", Normal},
	}
}

// Purpose and durability bind these NORMAL authored-intent effects to the exact
// closed command. A FULL, foreign or unverified frame cannot borrow them, and a
// caller-supplied number selects nothing.
func mgRecordingPurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch purpose {
	case MGScheduleRecording, MGUpdateRecording, MGCancelRecording, MGKeepRecording:
		return nil
	}
	return ErrInvalid
}

// The occurrence identity is the existing public DVR one, including its original
// JSON shape. It is an identity, never an authorization or a physical handle.
func mgRecordingID(o mgRecordingOwner, source, programme string) string {
	raw, _ := json.Marshal([]string{"dvr-occurrence-v1", "local", o.account, o.profile, source, programme})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// A frozen source/generation/head/channel/programme observation. revision is the
// current native source revision the parent selected; head must equal the
// generation being read, so a stale head can never be paired with a newer one.
type mgRecordingScope struct {
	source, generation, head, channel, programme string
	revision                                     int64
}

func (s mgRecordingScope) valid() bool {
	return mgRecordingHex(s.source, 48) && mgRecordingHex(s.generation, 48) && mgRecordingHex(s.head, 48) &&
		mgRecordingHex(s.channel, 64) && mgRecordingHex(s.programme, 64) && s.revision > 0
}

// Full immutable folded programme row, including nullable native groups. It
// preserves provider fields for exact comparison; icon URL is never a public
// image and never authorizes artwork storage.
type mgRecordingProgramme struct {
	id, channel, provider, title, start, end, lineage string
	metadata                                          int64
	series, episode, newEvidence, description, facts  mgRecordingNullableText
	hasIcon                                           int64
	icon                                              mgRecordingNullableText
}

type mgRecordingOptions struct {
	BeforeMS      int64 `json:"beforeMs"`
	AfterMS       int64 `json:"afterMs"`
	Priority      int64 `json:"priority"`
	RetentionDays int64 `json:"retentionDays"`
	EpisodeLimit  int64 `json:"episodeLimit"`
}

func (o mgRecordingOptions) valid() bool {
	return o.BeforeMS >= 0 && o.BeforeMS <= 21600000 && o.AfterMS >= 0 && o.AfterMS <= 21600000 &&
		o.Priority >= -1000 && o.Priority <= 1000 && o.RetentionDays >= 0 && o.RetentionDays <= 3650 &&
		o.EpisodeLimit >= 0 && o.EpisodeLimit <= 100000
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
	if !mgRecordingHex(p.id, 64) || !mgRecordingHex(p.channel, 64) || !o.valid() ||
		(p.metadata != 0 && p.metadata != 1) || (p.hasIcon != 0 && p.hasIcon != 1) {
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
	for _, v := range []mgRecordingNullableText{p.series, p.episode, p.newEvidence, p.description, p.facts, p.icon} {
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

// The birth namespace is the ORIGINAL command identity and request digest, never
// a caller-supplied value: the row's command incarnation, create operation and
// birth digest are derived from the accepted frame itself.
type mgRecordingBirth struct {
	incarnation mgRecordingID16
	operation   mgRecordingID16
	digest      mgRecordingDigest32
}

func (b mgRecordingBirth) valid() bool {
	return b.incarnation != (mgRecordingID16{}) && b.operation != (mgRecordingID16{}) && b.digest != (mgRecordingDigest32{})
}

type mgRecordingID16 [16]byte

func (v *mgRecordingID16) Scan(value any) error {
	b, ok := value.([]byte)
	if !ok || len(b) != 16 {
		return fmt.Errorf("recording ID: %w", ErrInvalid)
	}
	copy(v[:], b)
	return nil
}

type mgRecordingDigest32 [32]byte

func (v *mgRecordingDigest32) Scan(value any) error {
	b, ok := value.([]byte)
	if !ok || len(b) != 32 {
		return fmt.Errorf("recording digest: %w", ErrInvalid)
	}
	copy(v[:], b)
	return nil
}

type mgRecordingOptionalID struct {
	id    mgRecordingID16
	valid bool
}

func (v *mgRecordingOptionalID) Scan(value any) error {
	*v = mgRecordingOptionalID{}
	if value == nil {
		return nil
	}
	if err := v.id.Scan(value); err != nil {
		return err
	}
	v.valid = true
	return nil
}

type mgRecordingNullableText struct {
	text  string
	valid bool
}

func (v *mgRecordingNullableText) Scan(value any) error {
	*v = mgRecordingNullableText{}
	if value == nil {
		return nil
	}
	s, ok := value.(string)
	if !ok {
		return ErrInvalid
	}
	v.text, v.valid = s, true
	return nil
}

// One exact native dvr_recordings row. Every field is a comparison fact used by
// the owner/revision/state fence and the exact readback; none is authority.
type mgRecordingIntentRow struct {
	id, ownerKey, authority, account, profile, source, channel, programme, generation, ruleID string
	ruleRevision, startMS, endMS, revision                                                    int64
	state, reason                                                                             string
	keep, cancelGeneration, claimGeneration                                                   int64
	token                                                                                     string
	nextAttemptMS, recoveryAttempt, leaseMS                                                   int64
	ownerInstance, allocationID                                                               string
	itemID                                                                                    int64
	artifactID, artifactDigest                                                                string
	shares                                                                                    int64
	shareLeader                                                                               mgRecordingNullableText
	incarnation, operation                                                                    mgRecordingID16
	birth                                                                                     mgRecordingDigest32
	member, lifetime                                                                          string
	lastSequence, ackSequence                                                                 int64
	guidePin                                                                                  mgRecordingNullableText
	launch                                                                                    mgRecordingOptionalID
	allocationGeneration                                                                      int64
	allocationToken, sourceFence                                                              string
	sourceRevision, writerRetiredSequence, physicalExitSequence, sharedReadySequence          int64
	createdMS, updatedMS, finishedMS                                                          int64
	programmeJSON, optionsJSON                                                                string
	manual, priority                                                                          int64
	hasImage                                                                                  int64
	image                                                                                     mgRecordingNullableText
}

func (r mgRecordingIntentRow) valid() bool {
	if !mgRecordingHex(r.id, 64) || !mgRecordingHex(r.ownerKey, 64) || r.authority != "local" ||
		!mgRecordingText(r.account, true, 256) || !mgRecordingText(r.profile, true, 256) ||
		!mgRecordingHex(r.source, 48) || !mgRecordingHex(r.channel, 64) || !mgRecordingHex(r.programme, 64) ||
		!mgRecordingHex(r.generation, 48) || !mgRecordingRevisionValid(r.revision) ||
		r.incarnation == (mgRecordingID16{}) || r.operation == (mgRecordingID16{}) || r.birth == (mgRecordingDigest32{}) ||
		!mgRecordingHex(r.member, 32) || !mgRecordingHex(r.lifetime, 32) ||
		r.lastSequence < 0 || r.ackSequence < 0 || r.ackSequence > r.lastSequence || r.lastSequence-r.ackSequence > 1 ||
		r.createdMS < 0 || r.updatedMS < r.createdMS || r.finishedMS < 0 ||
		r.startMS < 0 || r.endMS <= r.startMS || r.priority < -1000 || r.priority > 1000 ||
		(r.keep != 0 && r.keep != 1) || (r.manual != 0 && r.manual != 1) || (r.shares != 0 && r.shares != 1) || (r.hasImage != 0 && r.hasImage != 1) ||
		r.claimGeneration < 0 || r.cancelGeneration < 0 || r.nextAttemptMS < 0 || r.recoveryAttempt < 0 || r.leaseMS < 0 ||
		r.allocationGeneration < 0 || r.sourceRevision < 0 || r.writerRetiredSequence < 0 || r.physicalExitSequence < 0 || r.sharedReadySequence < 0 ||
		r.itemID < 0 {
		return false
	}
	switch r.state {
	case "scheduled", "conflicted", "waiting-source", "waiting-guide", "preparing", "recording", "finalizing", "completed", "incomplete-playable", "cancelled", "pending-delete", "deleted", "failed":
	default:
		return false
	}
	if !r.guidePin.valid || r.guidePin.text != r.generation || r.shares == 1 != r.shareLeader.valid {
		return false
	}
	var p mgRecordingProgrammeCopy
	var o mgRecordingOptions
	if mgRecordingDecode(r.programmeJSON, &p) != nil || !mgRecordingCopyValid(p) || p.ID != r.programme || p.ChannelID != r.channel {
		return false
	}
	if mgRecordingDecode(r.optionsJSON, &o) != nil || !o.valid() || o.Priority != r.priority {
		return false
	}
	canonical, err := json.Marshal(o)
	return err == nil && string(canonical) == r.optionsJSON
}

// No expired/released flag is physical exclusion. Window edits are limited to a
// never-launched, unshared, artifact-free pending intent. Pins survive.
func (r mgRecordingIntentRow) editable() bool {
	return (r.state == "scheduled" || r.state == "conflicted" || r.state == "waiting-source" || r.state == "waiting-guide") &&
		r.claimGeneration == 0 && r.cancelGeneration == 0 && r.token == "" && r.ownerInstance == "" && r.leaseMS == 0 &&
		r.allocationID == "" && r.artifactID == "" && r.itemID == 0 && r.recoveryAttempt == 0 && !r.launch.valid &&
		r.allocationGeneration == 0 && r.allocationToken == "" && r.sourceRevision == 0 && r.sourceFence == "" &&
		r.writerRetiredSequence == 0 && r.physicalExitSequence == 0 && r.sharedReadySequence == 0 && r.shares == 0 &&
		!r.shareLeader.valid && r.guidePin.valid && r.guidePin.text == r.generation && r.lastSequence == r.ackSequence
}

func (r mgRecordingIntentRow) ownerMatches(o mgRecordingOwner) bool {
	return r.id == mgRecordingID(o, r.source, r.programme) && r.ownerKey == o.key() && r.authority == "local" &&
		r.account == o.account && r.profile == o.profile && r.member == o.memberIncarnation && r.lifetime == o.profileLifetime
}

func (r mgRecordingIntentRow) fenceValues() []any {
	return []any{r.id, r.ownerKey, r.account, r.profile, r.revision, r.incarnation[:], r.member, r.lifetime, r.lastSequence, r.ackSequence, r.state, r.token, r.claimGeneration, r.cancelGeneration, r.ownerInstance, r.leaseMS}
}

func mgRecordingRevisionValid(revision int64) bool { return revision > 0 && revision < math.MaxInt64 }

// An enabled reservation window is the native tuner-conflict protection. It is
// never a grant, and its row is retained (only disabled) when intent is
// cancelled: physical capture custody is not refunded by this command.
type mgRecordingReservation struct {
	present                           bool
	source                            string
	startMS, endMS, priority, enabled int64
}

type mgRecordingIntentFacts struct {
	row           mgRecordingIntentRow
	reservation   mgRecordingReservation
	ownerRevision int64
}

// The saved result is the original intent facts: identity, exact window,
// options JSON, state and both revisions. It is not a current DTO and never
// proves a claim, allocation, artifact or physical exit.
func (f mgRecordingIntentFacts) result() mgRecordingIntentResult {
	n := f.row
	return mgRecordingIntentResult{
		id: n.id, owner: n.ownerKey, source: n.source, channel: n.channel, programme: n.programme,
		generation: n.generation, state: n.state, optionsJSON: n.optionsJSON,
		startMS: n.startMS, endMS: n.endMS, priority: n.priority, keep: n.keep, manual: n.manual,
		revision: n.revision, ownerRevision: f.ownerRevision,
	}
}

type mgRecordingScheduleRequest struct {
	owner     mgRecordingOwner
	scope     mgRecordingScope
	programme mgRecordingProgramme
	options   mgRecordingOptions
}

type mgRecordingUpdateRequest struct {
	owner     mgRecordingOwner
	id        string
	expected  int64
	scope     mgRecordingScope
	programme mgRecordingProgramme
	options   mgRecordingOptions
}

type mgRecordingCancelRequest struct {
	owner    mgRecordingOwner
	id       string
	expected int64
}

type mgRecordingKeepRequest struct {
	owner    mgRecordingOwner
	id       string
	expected int64
	keep     bool
}

const mgRecordingRequestVersion uint16 = 1

func (r mgRecordingScheduleRequest) validate() error {
	if !r.owner.valid() || !r.scope.valid() || r.scope.generation != r.scope.head {
		return ErrInvalid
	}
	_, err := freezeMGRecordingIntent(r.programme, r.options)
	return err
}

func (r mgRecordingUpdateRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.id, 64) || r.expected < 1 || !r.scope.valid() || r.scope.generation != r.scope.head {
		return ErrInvalid
	}
	_, err := freezeMGRecordingIntent(r.programme, r.options)
	return err
}

func (r mgRecordingCancelRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.id, 64) || r.expected < 1 {
		return ErrInvalid
	}
	return nil
}

func (r mgRecordingKeepRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.id, 64) || r.expected < 1 {
		return ErrInvalid
	}
	return nil
}

// A typed command binds one immutable request to one command identity. The
// canonical digest is recomputed here; a caller digest that disagrees is a
// conflicting original, never a fresh admission.
type mgRecordingScheduleCommand struct {
	identity CommandIdentity
	request  mgRecordingScheduleRequest
	frozen   mgRecordingFrozen
	digest   RequestDigest
	valid    bool
}
type mgRecordingUpdateCommand struct {
	identity CommandIdentity
	request  mgRecordingUpdateRequest
	frozen   mgRecordingFrozen
	digest   RequestDigest
	valid    bool
}
type mgRecordingCancelCommand struct {
	identity CommandIdentity
	request  mgRecordingCancelRequest
	digest   RequestDigest
	valid    bool
}
type mgRecordingKeepCommand struct {
	identity CommandIdentity
	request  mgRecordingKeepRequest
	digest   RequestDigest
	valid    bool
}

func mgRecordingIdentityValid(id CommandIdentity) bool {
	return id.Sequence > 0 && id.Incarnation != ([16]byte{}) && id.OperationID != ([16]byte{})
}

func newMGRecordingScheduleCommand(id CommandIdentity, r mgRecordingScheduleRequest) (*mgRecordingScheduleCommand, error) {
	if !mgRecordingIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	frozen, err := freezeMGRecordingIntent(r.programme, r.options)
	if err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGScheduleRecording, mgRecordingRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgRecordingScheduleCommand{identity: id, request: r, frozen: frozen, digest: d, valid: true}, nil
}

func newMGRecordingUpdateCommand(id CommandIdentity, r mgRecordingUpdateRequest) (*mgRecordingUpdateCommand, error) {
	if !mgRecordingIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	frozen, err := freezeMGRecordingIntent(r.programme, r.options)
	if err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGUpdateRecording, mgRecordingRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgRecordingUpdateCommand{identity: id, request: r, frozen: frozen, digest: d, valid: true}, nil
}

func newMGRecordingCancelCommand(id CommandIdentity, r mgRecordingCancelRequest) (*mgRecordingCancelCommand, error) {
	if !mgRecordingIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGCancelRecording, mgRecordingRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgRecordingCancelCommand{identity: id, request: r, digest: d, valid: true}, nil
}

func newMGRecordingKeepCommand(id CommandIdentity, r mgRecordingKeepRequest) (*mgRecordingKeepCommand, error) {
	if !mgRecordingIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGKeepRecording, mgRecordingRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgRecordingKeepCommand{identity: id, request: r, digest: d, valid: true}, nil
}

func (c *mgRecordingScheduleCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGRecordingScheduleCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest || again.frozen != c.frozen {
		return ErrConflict
	}
	return nil
}
func (c *mgRecordingUpdateCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGRecordingUpdateCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest || again.frozen != c.frozen {
		return ErrConflict
	}
	return nil
}
func (c *mgRecordingCancelCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGRecordingCancelCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest {
		return ErrConflict
	}
	return nil
}
func (c *mgRecordingKeepCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGRecordingKeepCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest {
		return ErrConflict
	}
	return nil
}

func (c *mgRecordingScheduleCommand) birth() mgRecordingBirth {
	return mgRecordingBirth{mgRecordingID16(c.identity.Incarnation), mgRecordingID16(c.identity.OperationID), mgRecordingDigest32(c.digest)}
}

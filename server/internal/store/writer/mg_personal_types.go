package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MG owns explicit CommandID constants 800-899. Channel preferences and
// reminders are the sparse per-owner class: 811 updates one existing channel
// preference, 812 sets/renews one reminder and 813 deletes one reminder intent.
const (
	MGSetChannelPreference CommandID = 811
	MGSetReminder          CommandID = 812
	MGDeleteReminder       CommandID = 813
)

func mgPersonalDescriptors() []Descriptor {
	return []Descriptor{
		{MGSetChannelPreference, "mg.personal.channel-preference", Normal},
		{MGSetReminder, "mg.personal.set-reminder", Normal},
		{MGDeleteReminder, "mg.personal.delete-reminder", Normal},
	}
}

// Purpose and durability bind a private personal effect to the closed command
// it serves. A FULL, foreign or closed command can never borrow these sparse
// mutations, and a caller-supplied purpose number selects nothing.
func mgPersonalPurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	switch purpose {
	case MGSetChannelPreference, MGSetReminder, MGDeleteReminder:
		return nil
	}
	return ErrInvalid
}

type mgPersonalKind string

const (
	mgPersonalLive    mgPersonalKind = "live-source"
	mgPersonalLibrary mgPersonalKind = "library-channel"
)

// Frozen value inputs, constructed before admission. This owner tuple names a
// sparse namespace, NOT an authority proof: acceptMGPersonal* binds
// owner.account/owner.profile to the validated session identity before it
// looks up or writes anything, so a caller can never name another profile's
// preference/reminder. The session still owns current opaque member/profile/
// rating/source authority, the original receipt lookup/result codec, final
// disclosure, target admission, the effect and COMMIT. There is no request
// nonce/JSON flag that grants authority in these bodies.
type mgPersonalOwner struct{ authority, account, profile string }

func (o mgPersonalOwner) valid() bool {
	return o.authority == "local" && mgPersonalText(o.account) && mgPersonalText(o.profile)
}

// Same stable public ownership/ID encoding as livechannels, without importing
// its old writer, catalogue, policy callbacks or user-mutation implementation.
func mgPersonalID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte(":"))
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (o mgPersonalOwner) key() string { return mgPersonalID(o.authority, o.account, o.profile) }

type mgPersonalChannel struct {
	kind                        mgPersonalKind
	source, channel, generation string
	revision                    int64
}

func (c mgPersonalChannel) valid() bool {
	return (c.kind == mgPersonalLive || c.kind == mgPersonalLibrary) && mgPersonalText(c.source) && mgPersonalText(c.channel) && mgPersonalText(c.generation) && c.revision > 0 && (c.kind != mgPersonalLibrary || c.source == c.channel)
}
func (c mgPersonalChannel) preferenceSource() string {
	if c.kind == mgPersonalLibrary {
		return "library:" + c.channel
	}
	return c.source
}

// The compiled RB/RS immutable snapshot supplies this exact selected item/source
// value for an LC reminder. The native occurrence must match it; it never proves
// current library/item visibility or playback/rating authority by itself.
// A slate has no eligible library item and is not a reminder target.
type mgPersonalLibraryItem struct {
	item                   int64
	library, asset, source string
}

func (i mgPersonalLibraryItem) valid() bool {
	return i.item > 0 && mgPersonalText(i.library) && mgPersonalText(i.asset) && mgPersonalText(i.source)
}

// mgPersonalAdmissionTarget is the closed native identity of one Fresh personal
// effect. It carries no rating, visibility flag, viewer scope or allowed()
// callback: those are current viewer policy and are decided by the session that
// produced this target, against native rows in the same turn. A caller string
// therefore cannot widen or forge the admission decision.
type mgPersonalAdmissionTarget struct {
	kind                        mgPersonalKind
	source, channel, generation string
	programme                   string
	item                        mgPersonalLibraryItem
}

func (t mgPersonalAdmissionTarget) valid() bool {
	if (t.kind != mgPersonalLive && t.kind != mgPersonalLibrary) ||
		!mgPersonalText(t.source) || !mgPersonalText(t.channel) || !mgPersonalText(t.generation) {
		return false
	}
	if t.programme == "" {
		return t.item == mgPersonalLibraryItem{}
	}
	if !mgPersonalText(t.programme) {
		return false
	}
	if t.kind == mgPersonalLibrary {
		return t.item.valid()
	}
	return t.item == mgPersonalLibraryItem{}
}

type mgPersonalPreferenceRequest struct {
	owner            mgPersonalOwner
	target           mgPersonalChannel
	expected         int64
	favorite, hidden bool
}
type mgPersonalPreferenceFacts struct {
	revision         int64
	favorite, hidden bool
}
type mgPersonalReminderRequest struct {
	owner       mgPersonalOwner
	target      mgPersonalChannel
	programme   string // external programme ID or exact LC occurrence ID
	expected    int64
	libraryItem mgPersonalLibraryItem
}

func (r mgPersonalReminderRequest) id() string {
	return mgPersonalID("reminder", r.owner.key(), string(r.target.kind), r.target.source, r.target.channel, r.target.generation, r.programme)
}

type mgPersonalDeleteRequest struct {
	owner    mgPersonalOwner
	id       string
	expected int64
}

// Actual saved/deleted native row facts only, never Applied or an authorization
// grant. The two generated G refs are decoded and checked, not caller assigned.
type mgPersonalReminderFacts struct {
	id, owner                              string
	kind                                   mgPersonalKind
	source, channel, generation, programme string
	revision                               int64
	start                                  string
	externalGeneration, libraryGeneration  string
}

func mgPersonalText(s string) bool {
	return len(s) > 0 && len(s) <= 256 && utf8.ValidString(s) && !strings.ContainsRune(s, '\x00')
}

const mgPersonalRequestVersion uint16 = 1

func (r mgPersonalPreferenceRequest) validate() error {
	if !r.owner.valid() || !r.target.valid() || r.expected < 0 || r.expected == math.MaxInt64 {
		return ErrInvalid
	}
	return nil
}
func (r mgPersonalReminderRequest) validate() error {
	if !r.owner.valid() || !r.target.valid() || !mgPersonalText(r.programme) || r.expected < 0 || r.expected == math.MaxInt64 {
		return ErrInvalid
	}
	switch r.target.kind {
	case mgPersonalLibrary:
		if !r.libraryItem.valid() {
			return ErrInvalid
		}
	case mgPersonalLive:
		if r.libraryItem != (mgPersonalLibraryItem{}) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func (r mgPersonalDeleteRequest) validate() error {
	if !r.owner.valid() || len(r.id) != 64 || !mgPersonalText(r.id) || r.expected < 1 {
		return ErrInvalid
	}
	return nil
}

// A typed command binds one immutable request to one command identity. The
// canonical request digest is recomputed by its constructor; a caller digest
// that disagrees is a conflicting original, never a fresh admission.
type mgPersonalPreferenceCommand struct {
	identity CommandIdentity
	request  mgPersonalPreferenceRequest
	digest   RequestDigest
	valid    bool
}
type mgPersonalReminderCommand struct {
	identity CommandIdentity
	request  mgPersonalReminderRequest
	digest   RequestDigest
	valid    bool
}
type mgPersonalDeleteCommand struct {
	identity CommandIdentity
	request  mgPersonalDeleteRequest
	digest   RequestDigest
	valid    bool
}

func mgPersonalIdentityValid(id CommandIdentity) bool {
	return id.Sequence > 0 && id.Incarnation != ([16]byte{}) && id.OperationID != ([16]byte{})
}

func newMGPersonalPreferenceCommand(id CommandIdentity, r mgPersonalPreferenceRequest) (*mgPersonalPreferenceCommand, error) {
	if !mgPersonalIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGSetChannelPreference, mgPersonalRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgPersonalPreferenceCommand{identity: id, request: r, digest: d, valid: true}, nil
}
func newMGPersonalReminderCommand(id CommandIdentity, r mgPersonalReminderRequest) (*mgPersonalReminderCommand, error) {
	if !mgPersonalIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGSetReminder, mgPersonalRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgPersonalReminderCommand{identity: id, request: r, digest: d, valid: true}, nil
}
func newMGPersonalDeleteCommand(id CommandIdentity, r mgPersonalDeleteRequest) (*mgPersonalDeleteCommand, error) {
	if !mgPersonalIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	d, err := canonicalRequestDigest(MGDeleteReminder, mgPersonalRequestVersion, id, r)
	if err != nil {
		return nil, err
	}
	return &mgPersonalDeleteCommand{identity: id, request: r, digest: d, valid: true}, nil
}

func (c *mgPersonalPreferenceCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGPersonalPreferenceCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest {
		return ErrConflict
	}
	return nil
}
func (c *mgPersonalReminderCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGPersonalReminderCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest {
		return ErrConflict
	}
	return nil
}
func (c *mgPersonalDeleteCommand) check() error {
	if c == nil || !c.valid {
		return ErrInvalid
	}
	again, err := newMGPersonalDeleteCommand(c.identity, c.request)
	if err != nil || again.digest != c.digest {
		return ErrConflict
	}
	return nil
}

// Result projections carry only saved native row facts. The reminder projection
// keeps the copied start and the exact generated G reference kind.
func (r mgPersonalPreferenceRequest) result(f mgPersonalPreferenceFacts) mgPersonalPreferenceResult {
	return mgPersonalPreferenceResult{source: r.target.preferenceSource(), channel: r.target.channel, revision: f.revision, favorite: f.favorite, hidden: f.hidden}
}
func (f mgPersonalReminderFacts) result() mgPersonalReminderResult {
	return mgPersonalReminderResult{
		id: f.id, owner: f.owner, kind: string(f.kind), source: f.source, channel: f.channel,
		generation: f.generation, programme: f.programme, revision: f.revision, start: f.start,
		externalGeneration: f.externalGeneration, libraryGeneration: f.libraryGeneration,
	}
}

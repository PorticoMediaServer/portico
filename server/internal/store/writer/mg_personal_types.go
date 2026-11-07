package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MGSetChannelPreference CommandID = 811
	MGSetReminder          CommandID = 812
	MGDeleteReminder       CommandID = 813
)

func mgPersonalDescriptors() []Descriptor {
	return []Descriptor{{MGSetChannelPreference, "mg.personal.channel-preference", Normal}, {MGSetReminder, "mg.personal.set-reminder", Normal}, {MGDeleteReminder, "mg.personal.delete-reminder", Normal}}
}

type mgPersonalKind string

const (
	mgPersonalLive    mgPersonalKind = "live-source"
	mgPersonalLibrary mgPersonalKind = "library-channel"
)

// Frozen value inputs, constructed before admission. This owner tuple names a
// sparse namespace, NOT an authority proof. Parent composition owns current
// opaque member/profile/rating/source authority and user-mutation lifetime,
// original receipt lookup/result codec, final disclosure, admission and COMMIT.
// There is no request nonce/JSON flag that grants authority in these bodies.
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

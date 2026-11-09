package writer

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
)

// MG-11 composes the accepted public rule save/delete request with the merged
// native rule effects (mg_rule_effects.go). The request mirrors below are frozen
// value inputs supplied by the cutover adapter: they carry every public field so
// the canonical digest covers the ORIGINAL request, including untrimmed keyword
// padding. They grant no authority and never stand in for the public DTO.
type mgRuleAnchor struct{ source, channel, generation, programme string }

type mgRuleOptions struct{ beforeMS, afterMS, priority, retentionDays, episodeLimit int }

type mgRuleConfig struct {
	matchKind, title, channel, name, source, series, episodes string
	allowed, blocked, blockedKeywords, keywords               []string
	options                                                   mgRuleOptions
	enabled                                                   bool
}

type mgRuleSaveInput struct {
	requestID        string
	id               string
	expectedRevision int64
	useDefaults      bool
	anchor           mgRuleAnchor
	config           mgRuleConfig
}

type mgRuleDeleteInput struct {
	requestID        string
	expectedRevision int64
	future           string
}

// The adapter must populate these from the caller's unmodified public input and
// prepare the stored configuration separately with mginput.PrepareRuleConfig.
// The fresh-only CAS heads are NOT part of the original request identity: a
// retry after a guide refresh must still resolve to its original receipt.
//
// Cutover adapter contract: the writer hashes and stores the anchor,
// useDefaults, config.episodes="new" and the rest of the original request, but
// it does not ACT on them. Anchor/current-guide resolution, defaults, the
// new-evidence interpretation of episodes="new", and source/channel grants or
// programme visibility are resolved by the cutover adapter against the public
// request before it composes the opaque prepared configuration and the frozen
// owner/source CAS heads below. No field here grants authority.
type mgRuleSaveRequest struct {
	owner          mgRecordingOwner
	input          mgRuleSaveInput
	prepared       string
	sourceRevision int64
	generation     string
}

type mgRuleDeleteRequest struct {
	owner          mgRecordingOwner
	id             string
	input          mgRuleDeleteInput
	sourceRevision int64
	generation     string
}

// A typed command binds one immutable original request to one command identity.
// The digest is recomputed from the original request only; preparation output is
// deliberately excluded so a preparer change cannot rewrite request identity.
type mgRuleSaveCommand struct {
	identity CommandIdentity
	owner    mgRecordingOwner // current CAS tuple, never request identity
	request  mgRuleSaveIdentity
	prepared string
	digest   RequestDigest
	valid    bool
}

type mgRuleDeleteCommand struct {
	identity CommandIdentity
	owner    mgRecordingOwner // current CAS tuple, never request identity
	request  mgRuleDeleteIdentity
	digest   RequestDigest
	valid    bool
}

// Owner account/profile identity only. Epoch/incarnation/revisions are current
// CAS expectations, not original request identity: a legitimate identical retry
// after a profile/member revision bump must still resolve its original receipt.
type mgRuleSaveIdentity struct {
	account, profile string
	input            mgRuleSaveInput
}

type mgRuleDeleteIdentity struct {
	account, profile string
	id               string
	input            mgRuleDeleteInput
}

const mgRuleRequestVersion uint16 = 1

// The stored configuration's own identity must agree with the request values the
// adapter digested; otherwise a caller could hash one rule identity and store a
// different one. This is a consistency check, not a second preparer: mginput owns
// DTO validation, defaults and keyword trimming.
type mgRuleConfigIdentity struct {
	source, series, title, channel string
	enabled                        bool
}

func mgRulePreparedIdentity(prepared string) (out mgRuleConfigIdentity, err error) {
	if len(prepared) < 2 || len(prepared) > mgRuleMaxConfigBytes {
		return out, ErrCap
	}
	var wire struct {
		Title     string `json:"title"`
		ChannelID string `json:"channelId"`
		SourceID  string `json:"sourceId"`
		SeriesID  string `json:"seriesId"`
		Enabled   bool   `json:"enabled"`
	}
	decoder := json.NewDecoder(strings.NewReader(prepared))
	if err = decoder.Decode(&wire); err != nil {
		return out, ErrInvalid
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return out, ErrInvalid
	}
	out = mgRuleConfigIdentity{
		source: wire.SourceID, series: wire.SeriesID, channel: wire.ChannelID,
		title: strings.ToLower(strings.TrimSpace(wire.Title)), enabled: wire.Enabled,
	}
	if !mgRecordingHex(out.source, 48) || !mgRecordingText(out.series, false, 512) ||
		!mgRecordingText(out.title, false, 512) || !mgRecordingText(out.channel, false, 64) ||
		(out.series == "" && out.title == "") {
		return mgRuleConfigIdentity{}, ErrInvalid
	}
	return out, nil
}

func (a mgRuleAnchor) bounded() bool {
	for _, s := range []string{a.source, a.channel, a.generation, a.programme} {
		if !mgRecordingText(s, false, 512) {
			return false
		}
	}
	return true
}

// Shape bounds only: the public validator/preparer owns semantic acceptance.
// The keyword ceiling is the existing serialized rule-config ceiling, so the
// 384 KiB accepted contract is not silently narrowed to a generic 256 KiB hold.
func (c mgRuleConfig) bounded() bool {
	if !mgRecordingHex(c.source, 48) || !mgRecordingText(c.name, true, 160) ||
		!mgRecordingText(c.matchKind, false, 16) || !mgRecordingText(c.series, false, 512) ||
		!mgRecordingText(c.title, false, 512) || !mgRecordingText(c.channel, false, 64) ||
		(c.episodes != "all" && c.episodes != "new") {
		return false
	}
	if len(c.allowed) > 2000 || len(c.blocked) > 2000 || len(c.keywords) > 20 || len(c.blockedKeywords) > 20 {
		return false
	}
	for _, ids := range [][]string{c.allowed, c.blocked} {
		for _, id := range ids {
			if !mgRecordingText(id, true, 64) {
				return false
			}
		}
	}
	for _, keywords := range [][]string{c.keywords, c.blockedKeywords} {
		for _, keyword := range keywords {
			if !mgRecordingText(keyword, false, mgRuleMaxConfigBytes) {
				return false
			}
		}
	}
	return (c.series != "" || c.title != "") && (c.channel == "" || mgRecordingText(c.channel, true, 64))
}

func (r mgRuleSaveInput) validate() error {
	if !mgRecordingHex(r.requestID, 48) || !mgRecordingHex(r.id, 64) ||
		r.expectedRevision < 0 || r.expectedRevision == math.MaxInt64 || !r.anchor.bounded() || !r.config.bounded() {
		return ErrInvalid
	}
	return nil
}

func (r mgRuleDeleteInput) validate() error {
	if !mgRecordingHex(r.requestID, 48) || r.expectedRevision < 1 || r.expectedRevision == math.MaxInt64 ||
		(r.future != "keep" && r.future != "cancel") {
		return ErrInvalid
	}
	return nil
}

func (r mgRuleSaveRequest) validate() error {
	if !r.owner.valid() || r.sourceRevision < 1 || !mgRecordingHex(r.generation, 48) {
		return ErrInvalid
	}
	return r.input.validate()
}

func (r mgRuleDeleteRequest) validate() error {
	if !r.owner.valid() || !mgRecordingHex(r.id, 64) || r.sourceRevision < 1 || !mgRecordingHex(r.generation, 48) {
		return ErrInvalid
	}
	return r.input.validate()
}

// The stored configuration's identity must match the request identity that was
// hashed; enabled comes from the accepted config, never from a separate flag.
func mgRuleConfigIdentityFor(input mgRuleSaveInput, prepared string) (mgRuleConfigIdentity, error) {
	stored, err := mgRulePreparedIdentity(prepared)
	if err != nil {
		return mgRuleConfigIdentity{}, err
	}
	original := mgRuleConfigIdentity{
		source: input.config.source, series: input.config.series, channel: input.config.channel,
		title: strings.ToLower(strings.TrimSpace(input.config.title)), enabled: input.config.enabled,
	}
	if stored != original {
		return mgRuleConfigIdentity{}, ErrConflict
	}
	return stored, nil
}

func newMGRuleSaveCommand(id CommandIdentity, r mgRuleSaveRequest) (*mgRuleSaveCommand, error) {
	if !mgPersonalIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	// The prepared configuration is fresh-only: the original request digest never
	// covers it, so a replay or retired lookup must not depend on the adapter's
	// current prepared string. prepareMGRuleSave rechecks the stored identity
	// against the digested original before ANY fresh effect.
	request := mgRuleSaveIdentity{account: r.owner.account, profile: r.owner.profile, input: r.input}
	digest, err := canonicalRequestDigest(MGSaveRule, mgRuleRequestVersion, id, request)
	if err != nil {
		return nil, err
	}
	return &mgRuleSaveCommand{identity: id, owner: r.owner, request: request, prepared: r.prepared, digest: digest, valid: true}, nil
}

func newMGRuleDeleteCommand(id CommandIdentity, r mgRuleDeleteRequest) (*mgRuleDeleteCommand, error) {
	if !mgPersonalIdentityValid(id) {
		return nil, ErrInvalid
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	request := mgRuleDeleteIdentity{account: r.owner.account, profile: r.owner.profile, id: r.id, input: r.input}
	digest, err := canonicalRequestDigest(MGDeleteRule, mgRuleRequestVersion, id, request)
	if err != nil {
		return nil, err
	}
	return &mgRuleDeleteCommand{identity: id, owner: r.owner, request: request, digest: digest, valid: true}, nil
}

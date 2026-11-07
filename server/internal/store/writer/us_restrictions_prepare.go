package writer

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/securitypolicy"
)

// Complete replacement DTO. Flags describe the target; none confer authority.
type usRestrictionsEdit struct {
	ExpectedRevision                                          int64
	RatingSystem, MaximumAgeRating                            string
	AllowUnrated                                              bool
	BlockedLabels                                             []string
	AllowDownloads, AllowLiveTV, AllowDVR, AllowWatchTogether bool
}

// Source witnesses, including absence (whose resolved revision is one).
// currentDVR is only a comparison with the current stored/default value.
type usRestrictionsTarget struct {
	profile             usProfileTarget
	present, currentDVR bool
	revision            int64
}

type usRestrictionsRequest struct {
	ownerID                        string
	issuer                         usPolicyIssuer
	target                         usRestrictionsTarget
	expectedRevision               int64
	ratingSystem, maximumAgeRating string
	maximumAge                     int
	allowUnrated                   bool
	// Byte slices preserve source-accepted invalid UTF-8 in the full digest.
	labels                                                    [][]byte
	allowDownloads, allowLiveTV, allowDVR, allowWatchTogether bool
	labelsJSON                                                string
}

type usRestrictionsCommand struct {
	identity CommandIdentity
	request  usRestrictionsRequest
	digest   RequestDigest
}

func prepareUSRestrictions(identity CommandIdentity, owner string, issuer usPolicyIssuer, target usRestrictionsTarget, edit usRestrictionsEdit) (*usRestrictionsCommand, error) {
	p := target.profile
	if !validUSSecurityIdentity(identity, owner, issuer) || !validUSMemberTarget(p.member) || !usIdentifier(p.profile) || !usHex(p.lifetime, 32) || p.revision < 1 || target.revision < 1 || target.revision == math.MaxInt64 || !target.present && target.revision != 1 || edit.ExpectedRevision < 1 || edit.ExpectedRevision != target.revision {
		return nil, invalid("us_restrictions_target")
	}
	if len(edit.BlockedLabels) > 64 {
		return nil, invalid("blocked_labels")
	}
	labels := []string{}
	seen := map[string]bool{}
	for _, raw := range edit.BlockedLabels {
		label := strings.TrimSpace(raw)
		if label == "" || len(label) > 120 || strings.ContainsAny(label, "\r\n\t\x00") {
			return nil, invalid("blocked_labels")
		}
		key := strings.ToLower(label)
		if !seen[key] {
			seen[key] = true
			labels = append(labels, strings.Clone(label))
		}
	}
	sort.Strings(labels)
	system, rating := strings.TrimSpace(edit.RatingSystem), strings.TrimSpace(edit.MaximumAgeRating)
	if rating == "" {
		system = ""
	}
	age, ok := securitypolicy.RatingCeiling(system, rating)
	if !ok {
		return nil, invalid("rating")
	}
	raw, err := json.Marshal(labels)
	if err != nil {
		return nil, err
	}
	r := usRestrictionsRequest{ownerID: owner, issuer: issuer, target: target, expectedRevision: edit.ExpectedRevision, ratingSystem: system, maximumAgeRating: rating, maximumAge: age, allowUnrated: edit.AllowUnrated, labels: make([][]byte, len(labels)), allowDownloads: edit.AllowDownloads, allowLiveTV: edit.AllowLiveTV, allowDVR: edit.AllowDVR, allowWatchTogether: edit.AllowWatchTogether, labelsJSON: string(raw)}
	for i, s := range labels {
		r.labels[i] = []byte(s)
	}
	d, err := canonicalRequestDigest(usProfileRestrictionsEdit, 1, identity, r)
	if err != nil {
		return nil, err
	}
	return &usRestrictionsCommand{identity: identity, request: r, digest: d}, nil
}

func (usRestrictionsEdit) String() string             { return "usRestrictionsEdit{redacted}" }
func (v usRestrictionsEdit) GoString() string         { return v.String() }
func (usRestrictionsEdit) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usRestrictionsEdit") }
func (usRestrictionsTarget) String() string           { return "usRestrictionsTarget{redacted}" }
func (v usRestrictionsTarget) GoString() string       { return v.String() }
func (usRestrictionsTarget) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usRestrictionsTarget")
}
func (usRestrictionsRequest) String() string     { return "usRestrictionsRequest{redacted}" }
func (v usRestrictionsRequest) GoString() string { return v.String() }
func (usRestrictionsRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usRestrictionsRequest")
}
func (usRestrictionsCommand) String() string     { return "usRestrictionsCommand{redacted}" }
func (v usRestrictionsCommand) GoString() string { return v.String() }
func (usRestrictionsCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usRestrictionsCommand")
}

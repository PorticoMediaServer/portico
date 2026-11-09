package writer

import (
	"database/sql"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/securitypolicy"
)

// Product bounds copied from identity.normalizeLabels: at most 64 labels of at
// most 120 input bytes each. JSON replaces every invalid UTF-8 byte with U+FFFD,
// so a 120-byte label can become at most 360 response octets. Neither number is
// a new product permission cap or a new user cap.
const (
	usMaxRestrictionLabels        = 64
	usMaxRestrictionLabelBytes    = 120
	usMaxRestrictionResponseBytes = 360
	usRestrictionsResultCodec     = "profile_restrictions_v1"
)

// These are frozen comparison witnesses, never authority. The caller must hold
// a current US-01 owner/main-profile stamp in the SAME frame before any source,
// receipt or result read. No role string or caller-created scope grants power.
type usRestrictionsRequest struct {
	ownerID                                                   string // original retained receipt owner
	issuer                                                    usPolicyIssuer
	account, memberIncarnation, profile, profileLifetime      string
	expectedRevision                                          int64
	ratingSystem, maximumAgeRating                            string
	allowUnrated                                              bool
	blockedLabels                                             []string
	allowDownloads, allowLiveTV, allowDVR, allowWatchTogether bool
}

// A frozen request is preparation only: it owns no writer, governor grant,
// receipt classification, Applied result or token-issuance capability.
type usRestrictionsCommand struct {
	kind     CommandID
	identity CommandIdentity
	request  usRestrictionsRequest
	digest   RequestDigest
	// Projections of the frozen request, never caller-selected SQL. maximumAge is
	// the rating ceiling resolved once at preparation; a replay never re-runs the
	// rating table. blockedJSON is the exact TEXT the single replacement writes.
	maximumAge  int64
	blockedJSON string
}

// prepareUSRestrictions freezes one complete ProfileRestrictions replacement.
// It copies the live product's normalization (trim, control-character refusal,
// case-insensitive duplicate removal, sorted order) and then the exact JSON
// response spelling, so the frozen labels are already what the caller would read
// back. Nothing here authenticates or writes.
func prepareUSRestrictions(kind CommandID, identity CommandIdentity, in usRestrictionsRequest) (*usRestrictionsCommand, error) {
	if kind != USProfileRestrictions {
		return nil, invalid("us_restrictions_purpose")
	}
	if !validUSSecurityIdentity(identity, in.ownerID, in.issuer) {
		return nil, invalid("us_restrictions_identity")
	}
	if !usIdentifier(in.account) || !usHex(in.memberIncarnation, 32) || !usIdentifier(in.profile) || !usHex(in.profileLifetime, 32) {
		return nil, invalid("us_restrictions_target")
	}
	if in.expectedRevision < 1 || in.expectedRevision >= usMaxRevision {
		return nil, invalid("us_restrictions_revision")
	}
	labels, err := usRestrictionLabels(in.blockedLabels)
	if err != nil {
		return nil, err
	}
	system, code, ceiling, err := usRestrictionRating(in.ratingSystem, in.maximumAgeRating)
	if err != nil {
		return nil, err
	}
	in.ratingSystem, in.maximumAgeRating = system, code
	in.blockedLabels = labels
	digest, err := canonicalRequestDigest(kind, 1, identity, in)
	if err != nil {
		return nil, err
	}
	blocked, err := json.Marshal(in.blockedLabels)
	if err != nil {
		return nil, err
	}
	return &usRestrictionsCommand{kind: kind, identity: identity, request: in, digest: digest,
		maximumAge: ceiling, blockedJSON: string(blocked)}, nil
}

// usRestrictionRating copies identity.ProfileRestrictionEdit.normalize's rating
// rule: trim, clear the system when no code is present, and refuse a code the
// product's rating table does not know. The resolved ceiling is stored beside
// the code so historical reads never depend on a later rating-table edit.
func usRestrictionRating(system, code string) (string, string, int64, error) {
	system, code = strings.TrimSpace(system), strings.TrimSpace(code)
	if code == "" {
		system = ""
	}
	ceiling, ok := securitypolicy.RatingCeiling(system, code)
	if !ok {
		return "", "", 0, invalid("us_restrictions_rating")
	}
	return system, code, int64(ceiling), nil
}

// usRestrictionLabels copies identity.normalizeLabels and then the exact JSON
// response spelling. Order is the product's sorted order. The product removes
// duplicates before JSON encoding, so a repeated label keeps one position; the
// saved result validator still accepts equal spellings at distinct positions
// because a legacy/foreign source row can decode to them.
func usRestrictionLabels(raw []string) ([]string, error) {
	if len(raw) > usMaxRestrictionLabels {
		return nil, &PolicyError{Field: "us_restrictions_labels", Limit: usMaxRestrictionLabels, Cause: ErrCap}
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, label := range raw {
		label = strings.TrimSpace(label)
		if label == "" || len(label) > usMaxRestrictionLabelBytes || strings.ContainsAny(label, "\r\n\t\x00") {
			return nil, invalid("us_restrictions_label")
		}
		key := strings.ToLower(label)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, label)
	}
	sort.Strings(out)
	for i, label := range out {
		response, err := usRestrictionResponseLabel(label)
		if err != nil || response == "" || len(response) > usMaxRestrictionResponseBytes {
			return nil, invalid("us_restrictions_label")
		}
		out[i] = response
	}
	return out, nil
}

// usRestrictionResponseLabel is the JSON string round trip the product's stored
// row and response share. encoding/json replaces each invalid UTF-8 byte with
// U+FFFD, so the prepared label equals what a reader would decode.
func usRestrictionResponseLabel(label string) (string, error) {
	data, err := json.Marshal(label)
	if err != nil {
		return "", err
	}
	var out string
	if err = json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	return out, nil
}

// The cached SQL projection is not an independent request. A future composition
// bug cannot change the stored TEXT or the resolved ceiling while keeping the
// frozen digest. The labels are compared to the exact decoded row image.
func validateUSRestrictionsProjection(input *usRestrictionsCommand) error {
	if input == nil || input.kind != USProfileRestrictions || input.request.expectedRevision < 1 || input.request.expectedRevision >= usMaxRevision {
		return ErrInvalid
	}
	_, _, ceiling, err := usRestrictionRating(input.request.ratingSystem, input.request.maximumAgeRating)
	if err != nil || ceiling != input.maximumAge {
		return ErrConflict
	}
	if err = usRestrictionsValidateLabels(input.request.blockedLabels); err != nil {
		return ErrConflict
	}
	blocked, err := json.Marshal(input.request.blockedLabels)
	if err != nil || string(blocked) != input.blockedJSON {
		return ErrConflict
	}
	var decoded []string
	if err = json.Unmarshal([]byte(input.blockedJSON), &decoded); err != nil || !slices.Equal(decoded, input.request.blockedLabels) {
		return ErrConflict
	}
	return nil
}

// usRestrictionsValidateLabels is the closed saved/response label contract. It
// permits equal spellings at different positions (JSON replacement can merge two
// distinct invalid source spellings) and never re-sorts the saved order.
func usRestrictionsValidateLabels(labels []string) error {
	if labels == nil || len(labels) > usMaxRestrictionLabels {
		return ErrResolveRequired
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > usMaxRestrictionResponseBytes || !utf8.ValidString(label) ||
			strings.TrimSpace(label) != label || strings.ContainsAny(label, "\r\n\t\x00") {
			return ErrResolveRequired
		}
	}
	return nil
}

// usRestrictionsRatingShape keeps the stored/null rating contract explicit: no
// code means no system and no ceiling (-1); a code means a named system and a
// resolved non-negative age. Historical replay validates this shape only; it
// never re-runs the rating table or the current product policy.
func usRestrictionsRatingShape(system, code string, age int64) bool {
	if code == "" {
		return system == "" && age == -1
	}
	return system != "" && age >= 0
}

// policyJSON is the exact source TEXT the replacement writes. An empty list is
// TEXT "[]", never SQL NULL: profile_restrictions.blocked_labels is NOT NULL.
func (c *usRestrictionsCommand) policyJSON() (sql.NullString, error) {
	if c == nil || c.kind != USProfileRestrictions || c.blockedJSON == "" {
		return sql.NullString{}, ErrInvalid
	}
	return sql.NullString{String: c.blockedJSON, Valid: true}, nil
}

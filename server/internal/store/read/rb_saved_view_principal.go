package read

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// Saved resources are account/profile authority, independent of a catalogue
// library. Reuse the SAME registered global/principal birth joins as browse;
// retain all actual restrictions/revisions, with no invented library or viewer.
// Catalogue execution separately takes the full existing browse fence for the
// library resolved from the actual stored definition in that immutable snapshot.
func readSavedViewPrincipal(q browseQuery, in BrowseInput) (browseFence, error) {
	global, e := q("global_authority", int64(1))
	if e != nil {
		return browseFence{}, e
	}
	if len(global) != 1 {
		return browseFence{}, ErrDenied
	}
	rows, e := q("principal", in.TokenHash, global[0][0])
	if e != nil {
		return browseFence{}, e
	}
	if len(rows) != 1 {
		return browseFence{}, ErrDenied
	}
	r := rows[0]
	expires, e1 := time.Parse(time.RFC3339Nano, r[8].(string))
	horizon, e2 := time.Parse(time.RFC3339Nano, r[9].(string))
	if e1 != nil || e2 != nil || !in.Now.Before(expires) || !in.Now.Before(horizon) {
		return browseFence{}, ErrDenied
	}
	if r[15] != nil {
		return browseFence{}, ErrUnqualified
	}
	var allowed []string
	if json.Unmarshal([]byte(r[6].(string)), &allowed) != nil || allowed == nil || len(allowed) > 100 {
		return browseFence{}, ErrUnqualified
	}
	if r[7] != nil {
		var profile []string
		if json.Unmarshal([]byte(r[7].(string)), &profile) != nil || profile == nil || len(profile) > 100 {
			return browseFence{}, ErrUnqualified
		}
	}
	var labels []string
	if !utf8.ValidString(r[14].(string)) || json.Unmarshal([]byte(r[14].(string)), &labels) != nil || labels == nil || len(labels) > 64 {
		return browseFence{}, ErrUnqualified
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 120 {
			return browseFence{}, ErrUnqualified
		}
	}
	// Preserve the common closed-policy refusal, rather than guessing how a new
	// restriction class should affect saved-resource disclosure.
	if len(labels) > 0 {
		return browseFence{}, ErrUnqualified
	}
	f := browseFence{database: strings.Clone(in.Database), token: strings.Clone(in.TokenHash), account: r[0].(string), profile: r[1].(string), membership: r[2].(string), lifetime: r[3].(string), epoch: r[4].(int64), role: r[5].(string), memberLibraries: r[6].(string), profileRestricted: r[7] != nil, expiresAt: r[8].(string), horizon: r[9].(string), authority: r[10].(int64), membershipRevision: r[11].(int64), maximumAge: r[12].(int64), allowUnrated: r[13].(int64), profileLabels: r[14].(string), profileRevision: r[16].(int64), restrictionRevision: r[17].(int64)}
	if r[7] != nil {
		f.profileLibraries = r[7].(string)
	}
	return f, nil
}

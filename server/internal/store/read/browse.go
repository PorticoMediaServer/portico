// Package read implements harness-only foreground reads of schema-v1. It has
// no connection, transaction or writer capability. Composition supplies bounded
// GV requests and holds separately reserved output through actual consumption.
package read

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInput       = errors.New("invalid browse window")
	ErrDenied      = errors.New("browse authority or library denied")
	ErrStale       = errors.New("browse continuation fence changed")
	ErrUnqualified = errors.New("browse policy is not implemented by this read recipe")
)

const MaxPage = 40
const MaxRaw = 64

// BrowseInput comes from trusted harness composition, never directly from a
// network request. TokenHash identifies an already hashed local viewing token.
// Database is the composition's database incarnation, changed on restore/reset.
// Now is supplied by that composition's clock, not a client's clock.
type BrowseInput struct {
	Database, TokenHash      string
	Library                  int64
	Kind                     int64
	Limit                    int
	Descending, KeepArticles bool
	Now                      time.Time
	After                    *BrowseCursor
	policy                   readPolicyBinding
}

// BrowseCursor is an internal, values-only continuation. Its private fields
// prevent callers fabricating another viewer/query's anchor. It is not a wire
// cursor; cutover still needs the existing authenticated cursor codec.
type BrowseCursor struct {
	fence browseFence
	key   string
	id    int64
}

type browseFence struct {
	database, token, account, profile, membership, lifetime, library                            string
	epoch, authority, membershipRevision, profileRevision, restrictionRevision                  int64
	maximumAge, allowUnrated, container                                                         int64
	role, memberLibraries, profileLibraries, profileLabels, expiresAt, horizon                  string
	profileRestricted                                                                           bool
	libraryID, kind, dataRevision, orderRevision, metadataRevision, ageRevision, viewerRevision int64
	descending, keepArticles                                                                    bool
	root, bridgeRoot                                                                            string
	originIncarnation                                                                           [16]byte
	originRevision                                                                              int64
	policy                                                                                      readPolicyID
	policySubject                                                                               int64
	policyWitness                                                                               string
}

type BrowseItem struct {
	ID       int64
	PublicID [16]byte
	Title    string
	Revision int64
}

type BrowsePage struct {
	Items []BrowseItem
	// Exhausted proves the entire remaining raw stream was consumed. An empty
	// non-exhausted page is pending work, never a zero-match answer.
	Exhausted                bool
	Next                     *BrowseCursor
	RawPositions, Statements int
	// RawRows counts fetched title rows, including unused lookahead that may be
	// read again next time. Snapshot accounting also charges all fence rows.
	// RawPositions counts only cursor positions consumed.
	RawRows int
	// LookupElapsed includes title SQL and bounded hydration/reduction only.
	// It omits authority/fences/GV and must never be labelled route latency.
	LookupElapsed time.Duration
}

// browseQuery is confined to the registered Query API of one bounded scope.
type browseQuery func(string, ...any) ([][]any, error)

// readBrowseFence runs identically in the lookup and fresh publication views.
// Besides revisions, retain the actual policy values used by the item predicate
// so an unversioned policy drift still cannot release the earlier page.
func readPrincipalFence(query browseQuery, in BrowseInput) (browseFence, error) {
	global, err := query("global_authority", int64(1))
	if err != nil {
		return browseFence{}, err
	}
	if len(global) != 1 {
		return browseFence{}, ErrDenied
	}
	a, err := query("principal", in.TokenHash, global[0][0])
	if err != nil {
		return browseFence{}, err
	}
	if len(a) != 1 {
		return browseFence{}, ErrDenied
	}
	r := a[0]
	// The same-snapshot global point and joined account/profile/member/device state prove the
	// token's native epoch and family generation. Parse time rather than rely
	// on variable-width RFC3339 lexical comparisons.
	expires, e1 := time.Parse(time.RFC3339Nano, r[8].(string))
	horizon, e2 := time.Parse(time.RFC3339Nano, r[9].(string))
	if e1 != nil || e2 != nil || !in.Now.Before(expires) || !in.Now.Before(horizon) {
		return browseFence{}, ErrDenied
	}
	if r[15] != nil {
		return browseFence{}, ErrUnqualified
	}
	var allowed []string
	if err = json.Unmarshal([]byte(r[6].(string)), &allowed); err != nil || allowed == nil || len(allowed) > 100 {
		return browseFence{}, ErrUnqualified
	}
	var profileAllowed []string
	if r[7] != nil {
		if err = json.Unmarshal([]byte(r[7].(string)), &profileAllowed); err != nil || len(profileAllowed) > 100 {
			return browseFence{}, ErrUnqualified
		}
		// JSON null is not an inherited SQL NULL policy.
		if profileAllowed == nil {
			return browseFence{}, ErrUnqualified
		}
	}
	var labels []string
	if !utf8.ValidString(r[14].(string)) {
		return browseFence{}, ErrUnqualified
	}
	if err = json.Unmarshal([]byte(r[14].(string)), &labels); err != nil || labels == nil || len(labels) > 64 {
		return browseFence{}, ErrUnqualified
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 120 {
			return browseFence{}, ErrUnqualified
		}
	}
	if len(labels) > 0 {
		return browseFence{}, ErrUnqualified
	}
	f := browseFence{database: strings.Clone(in.Database), token: strings.Clone(in.TokenHash), account: r[0].(string), profile: r[1].(string), membership: r[2].(string), lifetime: r[3].(string), epoch: r[4].(int64), authority: r[10].(int64), membershipRevision: r[11].(int64), profileRevision: r[16].(int64), restrictionRevision: r[17].(int64), maximumAge: r[12].(int64), allowUnrated: r[13].(int64), role: r[5].(string), memberLibraries: r[6].(string), profileLabels: r[14].(string), expiresAt: r[8].(string), horizon: r[9].(string), profileRestricted: r[7] != nil, descending: in.Descending, keepArticles: in.KeepArticles, kind: in.Kind}
	if r[7] != nil {
		f.profileLibraries = r[7].(string)
	}
	return f, nil
}

func readBrowseFence(query browseQuery, in BrowseInput) (browseFence, error) {
	f, err := readPrincipalFence(query, in)
	if err != nil {
		return browseFence{}, err
	}
	policy, err := resolveReadPolicy(in)
	if err != nil {
		return browseFence{}, err
	}
	if !validReadPolicyScope(in, policy) {
		return browseFence{}, ErrInput
	}
	if policy.scope == readPrincipalScope {
		if err := validateReadPolicy(query, in, &f); err != nil {
			return browseFence{}, err
		}
		if in.After != nil && in.After.fence != f {
			return browseFence{}, ErrStale
		}
		return f, nil
	}
	var allowed, profileAllowed []string
	_ = json.Unmarshal([]byte(f.memberLibraries), &allowed)
	if f.profileRestricted {
		_ = json.Unmarshal([]byte(f.profileLibraries), &profileAllowed)
	}
	bridge, err := query("library_identity", in.Library)
	if err != nil {
		return browseFence{}, err
	}
	if len(bridge) != 1 {
		return browseFence{}, ErrDenied
	}
	l, err := query("library", f.profile, bridge[0][0])
	if err != nil {
		return browseFence{}, err
	}
	if len(l) != 1 {
		return browseFence{}, ErrDenied
	}
	lr := l[0]
	lib := lr[0].(string)
	contains := func(values []string) bool {
		for _, v := range values {
			if v == lib {
				return true
			}
		}
		return false
	}
	if f.role != "owner" && !contains(allowed) || f.profileRestricted && !contains(profileAllowed) {
		return browseFence{}, ErrDenied
	}
	if lr[7].(int64) != 0 || lr[5] != lr[6] {
		return browseFence{}, ErrUnqualified
	}
	k, err := query("kind", in.Kind)
	if err != nil {
		return browseFence{}, err
	}
	if len(k) != 1 {
		return browseFence{}, ErrUnqualified
	}
	f.library = lib
	f.libraryID = in.Library
	f.dataRevision = lr[1].(int64)
	f.orderRevision = lr[2].(int64)
	f.metadataRevision = lr[3].(int64)
	f.viewerRevision = lr[4].(int64)
	f.ageRevision = lr[5].(int64)
	f.container = k[0][0].(int64)

	f.root = lr[8].(string)
	f.bridgeRoot = bridge[0][1].(string)
	origin, ok := lr[9].([]byte)
	if !ok || len(origin) != 16 {
		return browseFence{}, ErrUnqualified
	}
	copy(f.originIncarnation[:], origin)
	f.originRevision = lr[10].(int64)
	if err := validateReadPolicy(query, in, &f); err != nil {
		return browseFence{}, err
	}
	if in.After != nil && in.After.fence != f {
		return browseFence{}, ErrStale
	}
	return f, nil
}

// A draft is private until BrowseReader.Window completes fresh validation.
func readBrowsePage(ctx context.Context, query browseQuery, in BrowseInput, f browseFence) (page BrowsePage, err error) {
	// Each statement limits RAW positions before live/age rejection. Retired
	// subjects consume this same budget and advance the raw continuation.
	name := "title."
	if in.KeepArticles {
		name += "keep."
	} else {
		name += "ignore."
	}
	if in.Descending {
		name += "desc."
	} else {
		name += "asc."
	}
	var raw [][]any
	lookupStarted := time.Now()
	if in.After == nil {
		raw, err = query(name+"first", in.Library, in.Kind, int64(MaxRaw))
	} else {
		raw, err = query(name+"tail", in.Library, in.Kind, in.After.key, in.After.id, int64(MaxRaw))
		if err == nil && len(raw) < MaxRaw {
			var next [][]any
			next, err = query(name+"next", in.Library, in.Kind, in.After.key, int64(MaxRaw-len(raw)))
			raw = append(raw, next...)
		}
	}
	if err != nil {
		return BrowsePage{}, err
	}
	page.RawRows = len(raw)
	for _, v := range raw {
		if err = ctx.Err(); err != nil {
			return BrowsePage{}, err
		}
		page.RawPositions++
		page.Next = &BrowseCursor{fence: f, key: v[1].(string), id: v[0].(int64)}
		if !admittedItem(v, f) {
			continue
		}
		item, e := itemValue(v)
		if e != nil {
			return BrowsePage{}, e
		}
		page.Items = append(page.Items, item)
		if len(page.Items) == in.Limit {
			break
		}
	}
	page.Exhausted = page.RawPositions == len(raw) && len(raw) < MaxRaw
	if page.Exhausted {
		page.Next = nil
	}
	page.LookupElapsed = time.Since(lookupStarted)
	return page, nil
}

// admittedItem and itemValue consume the common seven-column item shape.
func admittedItem(row []any, f browseFence) bool {
	if row[4].(int64) != 0 {
		return false
	}
	if row[6] == nil {
		return f.allowUnrated != 0
	}
	return f.maximumAge < 0 || row[6].(int64) <= f.maximumAge
}
func itemValue(row []any) (BrowseItem, error) {
	id, ok := row[2].([]byte)
	if !ok || len(id) != 16 {
		return BrowseItem{}, fmt.Errorf("invalid public identity")
	}
	var public [16]byte
	copy(public[:], id)
	return BrowseItem{ID: row[0].(int64), PublicID: public, Title: row[3].(string), Revision: row[5].(int64)}, nil
}

// Package read implements harness-only foreground reads of schema-v1. It has
// no connection, transaction or writer capability. Composition owns GV snapshot
// admission and separately reserves any output retained after its callback.
package read

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
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
	libraryID, kind, dataRevision, orderRevision, metadataRevision, ageRevision, viewerRevision int64
	descending, keepArticles                                                                    bool
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
	// RawRows counts all fetched rows, including unused lookahead that may be
	// read again next time. RawPositions counts only cursor positions consumed.
	RawRows int
	// LookupElapsed includes title SQL and bounded hydration/reduction only.
	// It omits authority/fences/GV and must never be labelled route latency.
	LookupElapsed time.Duration
}

// BrowseWindow is the callable QL read kernel. Invoke only inside Snapshots.Read
// with Registry(), <=1s lifetime and enough raw-row/native/Go memory admission.
// It includes principal, authority, library, revision and kind checks, plus
// bounded card hydration. Measure the enclosing GV call as the route equivalent;
// lookup-only SQL is not that result. No IO, wait, callback or SQL handle escapes.
// Output retained beyond GV's callback requires separately charged custody.
//
// Initial implemented semantic envelope: local viewing sessions, open profiles,
// and age-restricted leaves. Stored member policies, blocked labels and restricted
// containers explicitly refuse until their complete bounded verifiers land.
func BrowseWindow(scope *governor.ReadScope, in BrowseInput) (page BrowsePage, err error) {
	if scope == nil || len(in.Database) == 0 || len(in.Database) > 256 || len(in.TokenHash) == 0 || len(in.TokenHash) > 256 || in.Library <= 0 || in.Kind <= 0 || in.Kind > 99 || in.Limit < 1 || in.Limit > MaxPage || in.Now.IsZero() {
		return page, ErrInput
	}
	if in.Kind > 10 {
		return page, ErrUnqualified
	}
	query := func(id string, args ...any) ([][]any, error) {
		page.Statements++
		return scope.Query(statementID(id), args...)
	}
	a, err := query("principal", in.TokenHash)
	if err != nil {
		return BrowsePage{}, err
	}
	if len(a) != 1 {
		return BrowsePage{}, ErrDenied
	}
	r := a[0]
	// Joined current account/profile/membership/device/global state proves the
	// token's native epoch and family generation. Parse time rather than rely
	// on variable-width RFC3339 lexical comparisons.
	expires, e1 := time.Parse(time.RFC3339Nano, r[8].(string))
	horizon, e2 := time.Parse(time.RFC3339Nano, r[9].(string))
	if e1 != nil || e2 != nil || !in.Now.Before(expires) || !in.Now.Before(horizon) {
		return BrowsePage{}, ErrDenied
	}
	if r[15] != nil {
		return BrowsePage{}, ErrUnqualified
	}
	var allowed []string
	if err = json.Unmarshal([]byte(r[6].(string)), &allowed); err != nil || allowed == nil || len(allowed) > 100 {
		return BrowsePage{}, ErrUnqualified
	}
	var profileAllowed []string
	if r[7] != nil {
		if err = json.Unmarshal([]byte(r[7].(string)), &profileAllowed); err != nil || len(profileAllowed) > 100 {
			return BrowsePage{}, ErrUnqualified
		}
		// JSON null is not an inherited SQL NULL policy.
		if profileAllowed == nil {
			return BrowsePage{}, ErrUnqualified
		}
	}
	var labels []string
	if !utf8.ValidString(r[14].(string)) {
		return BrowsePage{}, ErrUnqualified
	}
	if err = json.Unmarshal([]byte(r[14].(string)), &labels); err != nil || labels == nil || len(labels) > 64 {
		return BrowsePage{}, ErrUnqualified
	}
	for _, label := range labels {
		if len(label) == 0 || len(label) > 120 {
			return BrowsePage{}, ErrUnqualified
		}
	}
	if len(labels) > 0 {
		return BrowsePage{}, ErrUnqualified
	}
	l, err := query("library", r[1].(string), in.Library)
	if err != nil {
		return BrowsePage{}, err
	}
	if len(l) != 1 {
		return BrowsePage{}, ErrDenied
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
	if r[5].(string) != "owner" && !contains(allowed) || r[7] != nil && !contains(profileAllowed) {
		return BrowsePage{}, ErrDenied
	}
	if lr[7].(int64) != 0 || lr[5] != lr[6] {
		return BrowsePage{}, ErrUnqualified
	}
	k, err := query("kind", in.Kind)
	if err != nil {
		return BrowsePage{}, err
	}
	if len(k) != 1 {
		return BrowsePage{}, ErrUnqualified
	}
	maximum, unrated := r[12].(int64), r[13].(int64)
	if k[0][0].(int64) != 0 && (maximum >= 0 || unrated == 0) {
		return BrowsePage{}, ErrUnqualified
	}
	f := browseFence{database: strings.Clone(in.Database), token: strings.Clone(in.TokenHash), account: r[0].(string), profile: r[1].(string), membership: r[2].(string), lifetime: r[3].(string), library: lib,
		epoch: r[4].(int64), authority: r[10].(int64), membershipRevision: r[11].(int64), profileRevision: r[16].(int64), restrictionRevision: r[17].(int64),
		libraryID: in.Library, kind: in.Kind, dataRevision: lr[1].(int64), orderRevision: lr[2].(int64), metadataRevision: lr[3].(int64), viewerRevision: lr[4].(int64), ageRevision: lr[5].(int64), descending: in.Descending, keepArticles: in.KeepArticles}
	if in.After != nil && in.After.fence != f {
		return BrowsePage{}, ErrStale
	}
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
		if err = scope.Context().Err(); err != nil {
			return BrowsePage{}, err
		}
		page.RawPositions++
		page.Next = &BrowseCursor{fence: f, key: v[1].(string), id: v[0].(int64)}
		if v[4].(int64) != 0 {
			continue
		}
		if v[6] == nil {
			if unrated == 0 {
				continue
			}
		} else if maximum >= 0 && v[6].(int64) > maximum {
			continue
		}
		var public [16]byte
		id, ok := v[2].([]byte)
		if !ok || len(id) != 16 {
			return BrowsePage{}, fmt.Errorf("invalid public identity")
		}
		copy(public[:], id)
		page.Items = append(page.Items, BrowseItem{ID: v[0].(int64), PublicID: public, Title: v[3].(string), Revision: v[5].(int64)})
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

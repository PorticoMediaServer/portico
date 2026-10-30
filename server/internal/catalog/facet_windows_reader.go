package catalog

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// FacetWindowCheck must reauthenticate live admission/policy/source authority,
// including library lifetime and snapshot.Authority. Content-only churn may
// retain an immutable COMPLETE basis. It MUST NOT require the basis to remain
// the active content head. The reader calls it before and after every response,
// including cached success. Nil is disabled, never implicit authorization.
type FacetWindowCheck func(context.Context, readprep.Caller, FacetBaseSnapshot) error

type FacetWindows struct {
	db    *sql.DB
	store *readprep.Store
	check FacetWindowCheck
	key   [32]byte
	now   func() time.Time
}

// NewFacetWindows is an explicit composition-root adapter around the ONE shared
// readprep store. It starts no workers and has no separate query/handle cache.
func NewFacetWindows(db *sql.DB, store *readprep.Store, check FacetWindowCheck) (*FacetWindows, error) {
	if db == nil || store == nil || check == nil {
		return nil, ErrFacetBaseUnavailable
	}
	w := &FacetWindows{db: db, store: store, check: check, now: time.Now}
	if _, err := rand.Read(w.key[:]); err != nil {
		return nil, err
	}
	return w, nil
}

type SimpleFacetWindow struct {
	QueryID  string `json:"queryId"`
	Basis    string `json:"basis"`
	Limit    int    `json:"limit"`
	Returned int    `json:"returned"`
	Next     string `json:"next,omitempty"`
	Previous string `json:"previous,omitempty"`
	Reload   string `json:"reload,omitempty"`
}
type SimpleFacetPage struct {
	Kind              string            `json:"kind"`
	Field             string            `json:"field"`
	Revision          string            `json:"revision"`
	Total             int64             `json:"total"`
	TotalIsLowerBound bool              `json:"totalIsLowerBound"`
	Values            []FacetValue      `json:"values"`
	Window            SimpleFacetWindow `json:"window"`
}
type SimpleFacetPreparing struct {
	Kind         string `json:"kind"`
	State        string `json:"state"`
	QueryID      string `json:"queryId"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

// Exactly one member is set on success; pending never carries values or totals.
// The HTTP owner registers explicit 200/202 schemas and the poll/cancel routes.
type SimpleFacetResult struct {
	Page      *SimpleFacetPage
	Preparing *SimpleFacetPreparing
	Fallback  *SimpleFacetFallbackPage
	release   func()
}

// Release must be deferred by HTTP through final serialization/transport. It
// releases the shared store's delivery reservation; copies share one lease.
func (r SimpleFacetResult) Release() {
	if r.release != nil {
		r.release()
	}
}

type facetWindowSpec struct {
	Needle    string `json:"q"`
	Limit     int    `json:"limit"`
	Anchor    int64  `json:"anchor"`
	Direction byte   `json:"direction"`
}
type facetWindowSummary struct {
	Spec             facetWindowSpec `json:"spec"`
	IDs              []int64         `json:"ids"`
	Total            int64           `json:"total"`
	Earlier          bool            `json:"earlier"`
	Later            bool            `json:"later"`
	CompleteMatchSet bool            `json:"completeMatchSet,omitempty"`
	CompleteMatchIDs []int64         `json:"completeMatchIDs,omitempty"`
}

// Initial never performs a library aggregate or writes a query. No installed
// COMPLETE exact base is an unavailable error, not successful empty results.
func (w *FacetWindows) Initial(ctx context.Context, caller readprep.Caller, scope FacetBaseScope, q string, limit int) (SimpleFacetResult, error) {
	if w == nil || !scope.valid() || caller == "" || len(q) > 100 || limit < 1 || limit > 200 {
		return SimpleFacetResult{}, ErrFacetBaseInput
	}
	var id int64
	err := dbwork.QueryRow(ctx, w.db, `SELECT COALESCE(active_base,0) FROM catalog_facet_base_classes WHERE library_id=? AND library=? AND field=? AND policy=? AND source=?`, scope.LibraryID, scope.Library, scope.Field, scope.Policy, scope.Source).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || id == 0 {
		return SimpleFacetResult{}, ErrFacetBaseUnavailable
	}
	if err != nil {
		return SimpleFacetResult{}, err
	}
	snap, err := w.authorize(ctx, caller, id)
	if err != nil {
		return SimpleFacetResult{}, err
	}
	return w.submit(ctx, caller, snap, facetWindowSpec{Needle: sorttext.Fold(q), Limit: limit}, 0)
}

// Continue accepts only opaque authenticated ID anchors. No label, value,
// sort key, query text or arbitrary offset/rank is serialized into the token.
func (w *FacetWindows) Continue(ctx context.Context, caller readprep.Caller, token string) (SimpleFacetResult, error) {
	t, err := w.decode(token)
	if err != nil || t.direction < 1 || t.direction > 3 || t.row <= 0 {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	snap, err := w.authorize(ctx, caller, t.base)
	if err != nil {
		_ = w.store.Cancel(ctx, caller, t.handle)
		return SimpleFacetResult{}, err
	}
	r, err := w.store.Poll(ctx, caller, t.handle)
	if err != nil {
		return SimpleFacetResult{}, err
	}
	defer r.Release()
	if r.State != readprep.Ready {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	if err = r.ResizeDelivery(facetWindowDecodeReservation(r.Payload)); err != nil {
		return SimpleFacetResult{}, err
	}
	var summary facetWindowSummary
	if err = json.Unmarshal(r.Payload, &summary); err != nil {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	summary.CompleteMatchIDs = nil // free seed decoding before hydration resizes delivery
	// Row lifetime is checked in the immutable base, not resolved from raw facts.
	var present bool
	if err = dbwork.QueryRow(ctx, w.db, `SELECT EXISTS(SELECT 1 FROM catalog_facet_base_rows WHERE base_id=? AND value_id=?)`, snap.ID, t.row).Scan(&present); err != nil {
		return SimpleFacetResult{}, err
	}
	if !present {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	summary.Spec.Anchor, summary.Spec.Direction = t.row, t.direction
	return w.submit(ctx, caller, snap, summary.Spec, t.expires)
}

func (w *FacetWindows) Poll(ctx context.Context, caller readprep.Caller, queryID string) (SimpleFacetResult, error) {
	t, err := w.decode(queryID)
	if err != nil || t.direction != 0 || t.row != 0 {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	snap, err := w.authorize(ctx, caller, t.base)
	if err != nil {
		_ = w.store.Cancel(ctx, caller, t.handle)
		return SimpleFacetResult{}, err
	}
	r, err := w.store.Poll(ctx, caller, t.handle)
	if err != nil {
		return SimpleFacetResult{}, err
	}
	transferred := false
	defer func() {
		if !transferred {
			r.Release()
		}
	}()
	out := SimpleFacetResult{}
	if r.State == readprep.Preparing {
		out.Preparing = &SimpleFacetPreparing{Kind: "facet-preparing", State: "preparing", QueryID: queryID, RetryAfterMs: 1000}
	} else {
		if err = r.ResizeDelivery(facetWindowDecodeReservation(r.Payload)); err != nil {
			return out, err
		}
		var summary facetWindowSummary
		if r.Basis != strconv.FormatInt(snap.ID, 10) || json.Unmarshal(r.Payload, &summary) != nil {
			return out, readprep.ErrStale
		}
		summary.CompleteMatchIDs = nil // page hydration never retains prefix candidates
		page, err := w.hydrate(ctx, snap, summary, t, r)
		if err != nil {
			return out, err
		}
		out.Page = &page
	}
	if _, err = w.authorize(ctx, caller, t.base); err != nil {
		_ = w.store.Cancel(ctx, caller, t.handle)
		return SimpleFacetResult{}, err
	}
	out.release = r.Release
	transferred = true
	return out, nil
}

// Cancel is idempotent and caller-private, including unknown/expired tokens.
func (w *FacetWindows) Cancel(ctx context.Context, caller readprep.Caller, queryID string) error {
	t, err := w.decode(queryID)
	if err != nil {
		return nil
	}
	return w.store.Cancel(ctx, caller, t.handle)
}
func (w *FacetWindows) authorize(ctx context.Context, caller readprep.Caller, id int64) (FacetBaseSnapshot, error) {
	s, err := facetBaseSnapshot(ctx, dbwork.ReadHandle(ctx, w.db), id, true)
	if err != nil {
		return s, readprep.ErrStale
	}
	if err = w.check(ctx, caller, s); err != nil {
		return s, err
	}
	return s, nil
}
func (w *FacetWindows) submit(ctx context.Context, caller readprep.Caller, s FacetBaseSnapshot, spec facetWindowSpec, originalExpiry int64) (SimpleFacetResult, error) {
	key := facetWindowKey(s, spec)
	// Full equality includes scope/policy/witness/authority/basis and window.
	// Complete seed identities share this same store and its aggregate budget.
	initial := int64(8192 + len(key.Canonical) + len(spec.Needle) + len(caller))
	expiryNanos := &atomic.Int64{}
	until := time.Time{}
	if originalExpiry > 0 {
		until = time.Unix(originalExpiry, 0)
	}
	h, err := w.store.SubmitUntil(ctx, caller, key, readprep.Plan{InitialBytes: initial, New: func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if err := b.Resize(initial); err != nil {
			return nil, err
		}
		return newFacetWindowScan(w, caller, s, spec, initial, expiryNanos), nil
	}}, until)
	if err != nil {
		return SimpleFacetResult{}, err
	}
	expiry, err := w.store.LeaseExpiry(ctx, caller, h)
	if err != nil {
		_ = w.store.Cancel(ctx, caller, h)
		return SimpleFacetResult{}, err
	}
	expiryNanos.Store(expiry.UnixNano())
	t := facetWindowToken{base: s.ID, handle: h, expires: expiry.Unix()}
	out, err := w.Poll(ctx, caller, w.encode(t))
	if err != nil {
		_ = w.store.Cancel(ctx, caller, h)
	}
	return out, err
}

type facetWindowToken struct {
	base, row int64
	handle    readprep.Handle
	direction byte
	expires   int64
}

func (w *FacetWindows) encode(t facetWindowToken) string {
	raw := make([]byte, 58, 90)
	raw[0] = 1
	raw[1] = t.direction
	binary.BigEndian.PutUint64(raw[2:10], uint64(t.base))
	binary.BigEndian.PutUint64(raw[10:18], uint64(t.row))
	binary.BigEndian.PutUint64(raw[18:26], uint64(t.expires))
	h, _ := base64.RawURLEncoding.DecodeString(string(t.handle))
	copy(raw[26:58], h)
	mac := hmac.New(sha256.New, w.key[:])
	mac.Write(raw)
	raw = append(raw, mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func (w *FacetWindows) decode(token string) (facetWindowToken, error) {
	var t facetWindowToken
	if w == nil || len(token) != 120 {
		return t, readprep.ErrStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 90 || raw[0] != 1 {
		return t, readprep.ErrStale
	}
	mac := hmac.New(sha256.New, w.key[:])
	mac.Write(raw[:58])
	if !hmac.Equal(raw[58:], mac.Sum(nil)) {
		return t, readprep.ErrStale
	}
	t = facetWindowToken{base: int64(binary.BigEndian.Uint64(raw[2:10])), row: int64(binary.BigEndian.Uint64(raw[10:18])), expires: int64(binary.BigEndian.Uint64(raw[18:26])), direction: raw[1], handle: readprep.Handle(base64.RawURLEncoding.EncodeToString(raw[26:58]))}
	if t.base <= 0 || t.row < 0 || w.now().Unix() >= t.expires {
		return t, readprep.ErrStale
	}
	return t, nil
}

// hydrate keeps scalar bytes OUT of readprep. A legal oversized first row is
// returned losslessly, alone, with explicit transient O(B) SQL/transport memory.
// Ordinary windows stop at 256KiB (estimated JSON escaping included), never
// silently drop a row: the last returned ID anchors the next window.
func (w *FacetWindows) hydrate(ctx context.Context, s FacetBaseSnapshot, sum facetWindowSummary, t facetWindowToken, delivery readprep.Result) (SimpleFacetPage, error) {
	out := SimpleFacetPage{Kind: "simple-keyset", Field: s.Scope.Field, Revision: strconv.FormatInt(s.ID, 10), Total: sum.Total, Values: []FacetValue{}, Window: SimpleFacetWindow{QueryID: w.encode(t), Basis: strconv.FormatInt(s.ID, 10), Limit: sum.Spec.Limit}}
	read := dbwork.ReadHandle(ctx, w.db)
	// Immutable base/text identities allow bounded single-statement snapshots.
	// Keep no transaction pinned across legal exceptional scalar transport.
	err := func() error {
		if _, err := facetBaseSnapshot(ctx, read, s.ID, true); err != nil {
			return readprep.ErrStale
		}
		if sum.Total < 0 || sum.Total > 9007199254740991 {
			return ErrFacetBaseInput
		}
		// Before pages are adjacent to their right edge even when the byte
		// target fits fewer rows: retain a suffix, not the distant prefix.
		sizes := make([]int64, len(sum.IDs))
		for i, id := range sum.IDs {
			if err := read.QueryRowContext(ctx, `SELECT 6*(length(CAST(v.value AS BLOB))+length(CAST(x.label AS BLOB)))+128 FROM catalog_facet_base_rows b JOIN catalog_facet_base_values v ON v.id=b.value_id JOIN catalog_facet_base_texts x ON x.id=b.text_id WHERE b.base_id=? AND b.value_id=?`, s.ID, id).Scan(&sizes[i]); err != nil {
				return err
			}
		}
		lo, hi := 0, len(sum.IDs)
		used := int64(0)
		if sum.Spec.Direction == 2 {
			lo = hi
			for lo > 0 {
				if lo < hi && used+sizes[lo-1] > 256<<10 {
					break
				}
				lo--
				used += sizes[lo]
			}
			if lo > 0 {
				sum.Earlier = true
			}
		} else {
			hi = 0
			for hi < len(sum.IDs) {
				if hi > 0 && used+sizes[hi] > 256<<10 {
					break
				}
				used += sizes[hi]
				hi++
			}
			if hi < len(sum.IDs) {
				sum.Later = true
			}
		}
		sum.IDs = sum.IDs[lo:hi]
		rawBytes := int64(0)
		for _, size := range sizes[lo:hi] {
			rawBytes += (size - 128) / 6
		}
		// Reserve BEFORE string scans. Cover driver/string overlap, bounded
		// structs/decoder state, JSON encoder growth and transport buffering.
		// Only one intrinsically oversized legal scalar may use the explicit
		// uncached O(B) exception; ordinary contention never bypasses admission.
		reserve := int64(32768) + 3*used + 2*rawBytes
		if err := delivery.ResizeDelivery(reserve); err != nil {
			exceptional := len(sum.IDs) == 1 && rawBytes > 256<<10 && errors.Is(err, readprep.ErrCapacity)
			if !exceptional {
				return err
			}
		}
		out.Values = make([]FacetValue, 0, len(sum.IDs))
		for _, id := range sum.IDs {
			var f FacetValue
			var count int64
			if err := read.QueryRowContext(ctx, `SELECT v.value,x.label,-b.negative_count FROM catalog_facet_base_rows b JOIN catalog_facet_base_values v ON v.id=b.value_id JOIN catalog_facet_base_texts x ON x.id=b.text_id WHERE b.base_id=? AND b.value_id=?`, s.ID, id).Scan(&f.Value, &f.Label, &count); err != nil {
				return err
			}
			if count < 1 || count > 9007199254740991 || int64(int(count)) != count {
				return ErrFacetBaseInput
			}
			f.Count = int(count)
			out.Values = append(out.Values, f)
		}
		return nil
	}()
	if err == nil {
		if _, checkErr := facetBaseSnapshot(ctx, read, s.ID, true); checkErr != nil {
			err = readprep.ErrStale
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = readprep.ErrStale
	}
	if err != nil {
		return out, err
	}
	n := len(out.Values)
	out.Window.Returned = n
	if n > 0 {
		edge := t
		edge.row = sum.IDs[0]
		edge.direction = 3
		out.Window.Reload = w.encode(edge)
		if sum.Earlier {
			edge.direction = 2
			out.Window.Previous = w.encode(edge)
		}
		if sum.Later || n < len(sum.IDs) {
			edge.row = sum.IDs[n-1]
			edge.direction = 1
			out.Window.Next = w.encode(edge)
		}
	}
	return out, nil
}

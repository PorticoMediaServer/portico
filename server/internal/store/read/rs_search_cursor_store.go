package read

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// The private RSSearchCursor is the continuation a search page hands back. It
// carries query/source coordinates, a native rank bank/revision, retained read
// stamps and the preview seen set (up to NativePreviewCandidates keys, 8-70KB).
// None of that is wire data. A page token therefore carries only a random
// handle, its expiry and an HMAC; the cursor itself stays in this bounded
// server-side store until the token expires or is evicted.
//
// This is an inert phase-4 contract: no HTTP route, product dispatch or native
// read calls it yet. It is not admission, authority, a prepared cache or a
// second preparation lifetime. Its byte ceiling accounts for the cursor the
// caller transfers at handoff; the governor still owns every read, budget and
// lease on the next page.
const (
	rsSearchCursorTokenVersion     byte = 1
	rsSearchCursorTokenPrefix           = "rs1."
	rsSearchCursorHandleBytes           = 16
	rsSearchCursorExpiryBytes           = 8
	rsSearchCursorTokenRawBytes         = 1 + rsSearchCursorExpiryBytes + rsSearchCursorHandleBytes + sha256.Size
	rsSearchCursorTokenLength           = len(rsSearchCursorTokenPrefix) + (rsSearchCursorTokenRawBytes*4+2)/3
	rsSearchCursorEntryOverhead         = int64(768)
	rsSearchCursorMaxTTL                = 5 * time.Minute
	rsSearchCursorMaxHandleRetries      = 8
	rsSearchCursorMaxQueryBytes         = 1536
	rsSearchCursorMaxRevisionBytes      = 4096
)

// RSSearchCursorStoreLimit is an explicit ceiling, never a tuning hint. TTL is
// the maximum page-handle lifetime from Issue; a continuation may only shorten
// it by carrying an earlier ceiling forward. Refusal at a ceiling is explicit;
// a token is never served from a partial or truncated cursor.
type RSSearchCursorStoreLimit struct {
	MaxEntries    int
	MaxPerOwner   int
	MaxBytes      int64
	MaxEntryBytes int64
	TTL           time.Duration
}

// DefaultRSSearchCursorStoreLimit bounds typical concurrent page handles. A
// preview seen set is a few tens of KiB; a selected multi-library cursor also
// retains its source stamps. The per-entry allowance equals the largest
// preparation payload so a cursor RS already admitted cannot be refused here,
// while the aggregate stays inside the preparation pool's ceiling.
func DefaultRSSearchCursorStoreLimit() RSSearchCursorStoreLimit {
	return RSSearchCursorStoreLimit{
		MaxEntries:    32,
		MaxPerOwner:   4,
		MaxBytes:      32 << 20,
		MaxEntryBytes: governor.PreparationMaxJobBytes,
		TTL:           30 * time.Second,
	}
}

type rsSearchCursorHandle [rsSearchCursorHandleBytes]byte

type rsSearchCursorEntry struct {
	owner           governor.OwnerID
	identity        [32]byte
	query           string
	bank, revision  int64
	issued, expires time.Time
	sequence        uint64
	bytes           int64
	cursor          *RSSearchCursor
}

// RSSearchCursorStore retains opaque search continuations keyed by a random
// authenticated handle. All shared state is serialized by mu; the key is
// immutable after construction. Closed, evicted, expired and superseded handles
// are stale, never a fresh first page. Eviction may reclaim expired handles of
// any owner and the issuing owner's own oldest handle, but never another
// owner's live handle: a store full of other owners' live entries refuses with
// governor.ErrBusy instead.
type RSSearchCursorStore struct {
	mu      sync.Mutex
	key     [32]byte
	limit   RSSearchCursorStoreLimit
	now     func() time.Time
	entries map[rsSearchCursorHandle]*rsSearchCursorEntry
	bytes   int64
	nextSeq uint64
	closed  bool
}

type RSSearchCursorStoreStats struct {
	Entries    int
	Bytes      int64
	MaxEntries int
	MaxBytes   int64
}

// NewRSSearchCursorStore validates an explicit key and ceiling. The key is the
// page-token MAC key and is never derived from a client token or query. The
// caller supplies it; key generation and storage are a phase-4 contract and are
// not owned here. Rotating the key invalidates every live handle, which then
// resolves stale rather than being re-issued.
func NewRSSearchCursorStore(key [32]byte, limit RSSearchCursorStoreLimit) (*RSSearchCursorStore, error) {
	return newRSSearchCursorStore(key, limit, time.Now)
}

func newRSSearchCursorStore(key [32]byte, limit RSSearchCursorStoreLimit, now func() time.Time) (*RSSearchCursorStore, error) {
	if key == ([32]byte{}) || now == nil {
		return nil, ErrInput
	}
	if limit.MaxEntries < 1 || limit.MaxEntries > 4096 || limit.MaxPerOwner < 1 || limit.MaxPerOwner > limit.MaxEntries ||
		limit.MaxBytes < rsSearchCursorEntryOverhead || limit.MaxBytes > governor.PreparationMaxBytes ||
		limit.MaxEntryBytes < rsSearchCursorEntryOverhead || limit.MaxEntryBytes > limit.MaxBytes ||
		limit.TTL <= 0 || limit.TTL > rsSearchCursorMaxTTL {
		return nil, ErrInput
	}
	return &RSSearchCursorStore{key: key, limit: limit, now: now, entries: make(map[rsSearchCursorHandle]*rsSearchCursorEntry)}, nil
}

// RSSearchCursorRequest is the continuation's trusted binding. Input must be
// the same request the next page would pass to RSSearchReader.Begin; After is
// ignored because this call is what supplies it. Bank and Revision are the
// accepted native bank generation of this continuation. A cursor issued with
// retained bank evidence refuses a different generation; one issued without it
// (for example a synthetic test cursor) is not bank-bound here and the native
// read still revalidates its stamps.
type RSSearchCursorRequest struct {
	Owner          governor.OwnerID
	Input          RSSearchInput
	Bank, Revision int64
}

// RSSearchCursorIdentity reproduces the private identity RSSearchReader.Begin
// binds into a continuation (owner + sorted views + folded query + group + full
// + order/direction + capacity). A phase-4 adapter needs it to authenticate a
// page token before Begin can accept the cursor. It is not authorization and
// grants no read. The cursor-store test asserts equality with Begin.
func RSSearchCursorIdentity(owner governor.OwnerID, in RSSearchInput) ([32]byte, error) {
	if owner == 0 || len(in.Views) == 0 || in.Query.Mode() != search.ReadFull || in.Capacity < 1 || in.Capacity > 512 || !rsSearchValidOrder(in.Order) {
		return [32]byte{}, ErrInput
	}
	if in.Direction == "" {
		in.Direction = rsSearchDefaultDirection(in.Order)
	}
	if in.Direction != "asc" && in.Direction != "desc" {
		return [32]byte{}, ErrInput
	}
	if in.Group == 21 {
		in.Group = RSSearchReading
	}
	if in.Group < RSSearchPeople || in.Group > 99 || in.Group != 0 && !rsSearchPublicGroup(in.Group) || in.Full && in.Group == 0 {
		return [32]byte{}, ErrInput
	}
	if len(in.Views) > MaxNativeRetained-1 {
		return [32]byte{}, ErrInput
	}
	views := append([]BrowseInput(nil), in.Views...)
	sort.Slice(views, func(i, j int) bool { return views[i].Library < views[j].Library })
	base := views[0]
	keyViews := make([]BrowseInput, len(views))
	for i, v := range views {
		if v.Library <= 0 || v.Database == "" || v.TokenHash == "" || v.Now.IsZero() || v.After != nil || v.Kind != 0 || v.Descending || v.KeepArticles || v.policy.recipe != 0 || v.Database != base.Database || v.TokenHash != base.TokenHash || !v.Now.Equal(base.Now) || i > 0 && v.Library == views[i-1].Library {
			return [32]byte{}, ErrInput
		}
		keyViews[i] = v
		keyViews[i].Now = time.Time{}
		keyViews[i].Limit = 0
	}
	return sha256.Sum256([]byte(fmt.Sprintf("%d/%#v/%s/%d/%t/%s/%d", owner, keyViews, in.Query.Fold(), in.Group, in.Full, in.Order+"/"+in.Direction, in.Capacity))), nil
}

// Issue retains c and returns its page token plus expiry. The caller deposits
// the exact cursor RS composed and must not mutate it afterwards; private
// fields already prevent mutation outside this package. Issue is the fresh
// lifetime; use IssueUntil to carry an earlier continuation ceiling forward.
// A page that continues an existing handle must use IssueNext so the consumed
// handle is retired instead of accumulating against the owner's allowance.
func (s *RSSearchCursorStore) Issue(owner governor.OwnerID, c *RSSearchCursor) (string, time.Time, error) {
	return s.issue(owner, "", c, time.Time{})
}

// IssueUntil is Issue with the original continuation ceiling. A page token
// never extends a walk: expiry is the earliest of now+TTL and until. A ceiling
// that has already passed is stale.
func (s *RSSearchCursorStore) IssueUntil(owner governor.OwnerID, c *RSSearchCursor, until time.Time) (string, time.Time, error) {
	return s.issue(owner, "", c, until)
}

// IssueNext issues the continuation for the page after predecessor and retires
// that predecessor in the same locked section. The predecessor must be a live,
// unexpired handle for the same owner and continuation identity as c; it is
// then consumed, so replaying it resolves stale and it no longer counts against
// the owner's allowance. Retiring and issuing are atomic: a refusal leaves the
// predecessor exactly as it was. Predecessor validation failure is stale (or
// denied for another owner's token) and never panics.
func (s *RSSearchCursorStore) IssueNext(owner governor.OwnerID, predecessor string, c *RSSearchCursor, until time.Time) (string, time.Time, error) {
	if predecessor == "" {
		return "", time.Time{}, ErrInput
	}
	return s.issue(owner, predecessor, c, until)
}

func (s *RSSearchCursorStore) issue(owner governor.OwnerID, predecessor string, c *RSSearchCursor, until time.Time) (string, time.Time, error) {
	if s == nil {
		return "", time.Time{}, ErrInput
	}
	if owner == 0 || c == nil || c.identity == ([32]byte{}) || c.offset != 0 || c.rank == nil && c.order == nil || c.rank != nil && c.order != nil {
		return "", time.Time{}, ErrInput
	}
	if c.rank != nil && (c.rank.query == "" || len(c.rank.query) > rsSearchCursorMaxQueryBytes || c.rank.bank <= 0 || c.rank.revision <= 0 || c.rank.input < 0 || len(c.rank.after.lead.Revision) > rsSearchCursorMaxRevisionBytes) {
		return "", time.Time{}, ErrInput
	}
	for _, anchor := range c.anchors {
		if anchor == nil {
			return "", time.Time{}, ErrInput
		}
	}
	charge := rsSearchCursorRetainedBytes(c)
	query, bank, revision := rsSearchCursorBinding(c)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", time.Time{}, ErrStale
	}
	now := s.now()
	expires := now.Add(s.limit.TTL)
	if !until.IsZero() {
		if !now.Before(until) {
			return "", time.Time{}, ErrStale
		}
		if until.Before(expires) {
			expires = until
		}
	}
	if charge > s.limit.MaxEntryBytes {
		return "", time.Time{}, governor.ErrSnapshotBudget
	}
	s.sweepLocked(now)
	var retire *rsSearchCursorHandle
	if predecessor != "" {
		h, tokenExpiry, err := s.decode(predecessor)
		if err != nil {
			return "", time.Time{}, err
		}
		e := s.entries[h]
		if e == nil || !now.Before(tokenExpiry) || !now.Before(e.expires) {
			return "", time.Time{}, ErrStale
		}
		if e.owner != owner {
			return "", time.Time{}, ErrDenied
		}
		if e.identity != c.identity {
			return "", time.Time{}, ErrStale
		}
		retire = &h
	}
	owned := 0
	for h, e := range s.entries {
		if e.owner == owner && (retire == nil || h != *retire) {
			owned++
		}
	}
	if owned >= s.limit.MaxPerOwner {
		return "", time.Time{}, governor.ErrBusy
	}
	if !s.makeRoomLocked(owner, charge, retire) {
		return "", time.Time{}, governor.ErrBusy
	}
	handle, err := s.newHandleLocked()
	if err != nil {
		return "", time.Time{}, err
	}
	if retire != nil {
		s.removeLocked(*retire)
	}
	s.nextSeq++
	s.entries[handle] = &rsSearchCursorEntry{owner: owner, identity: c.identity, query: query, bank: bank, revision: revision, issued: now, expires: expires, sequence: s.nextSeq, bytes: charge, cursor: c}
	s.bytes += charge
	return s.encode(handle, expires), expires, nil
}

// Resolve authenticates the token, then checks the live entry against the
// continuation's owner, private identity, folded query and accepted bank. It
// returns the retained cursor and the original expiry ceiling for the next
// IssueNext. The cursor is shared immutable state: callers pass it to Begin
// (which copies it) and never modify it. Replaying a live token returns the same
// cursor without advancing or authorizing anything. A missing, expired,
// evicted, superseded, restarted or closed entry is stale.
func (s *RSSearchCursorStore) Resolve(token string, req RSSearchCursorRequest) (*RSSearchCursor, time.Time, error) {
	if s == nil {
		return nil, time.Time{}, ErrInput
	}
	handle, tokenExpiry, err := s.decode(token)
	if err != nil {
		return nil, time.Time{}, err
	}
	identity, err := RSSearchCursorIdentity(req.Owner, req.Input)
	if err != nil {
		return nil, time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, time.Time{}, ErrStale
	}
	now := s.now()
	e := s.entries[handle]
	if e == nil {
		return nil, time.Time{}, ErrStale
	}
	if !now.Before(tokenExpiry) || !now.Before(e.expires) {
		s.removeLocked(handle)
		return nil, time.Time{}, ErrStale
	}
	if e.owner != req.Owner {
		return nil, time.Time{}, ErrDenied
	}
	if e.identity != identity || e.query != "" && e.query != req.Input.Query.Fold() {
		return nil, time.Time{}, ErrStale
	}
	if e.bank > 0 && (e.bank != req.Bank || e.revision != req.Revision) {
		return nil, time.Time{}, ErrStale
	}
	return e.cursor, e.expires, nil
}

// Sweep drops expired handles and returns how many it removed. It is optional
// maintenance: Issue and Resolve also expire lazily.
func (s *RSSearchCursorStore) Sweep() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0
	}
	return s.sweepLocked(s.now())
}

// Stats reports the bounded state without exposing any cursor content.
func (s *RSSearchCursorStore) Stats() RSSearchCursorStoreStats {
	if s == nil {
		return RSSearchCursorStoreStats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return RSSearchCursorStoreStats{Entries: len(s.entries), Bytes: s.bytes, MaxEntries: s.limit.MaxEntries, MaxBytes: s.limit.MaxBytes}
}

// Close releases every retained cursor. Outstanding tokens become stale; the
// cleared map and byte count cannot be observed half-updated because every
// access shares this mutex. Close is idempotent and safe during Issue/Resolve.
func (s *RSSearchCursorStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.entries = nil
	s.bytes = 0
	s.nextSeq = 0
	return nil
}

func (s *RSSearchCursorStore) newHandleLocked() (rsSearchCursorHandle, error) {
	for attempt := 0; attempt < rsSearchCursorMaxHandleRetries; attempt++ {
		var h rsSearchCursorHandle
		if _, err := rand.Read(h[:]); err != nil {
			return h, err
		}
		if _, exists := s.entries[h]; !exists {
			return h, nil
		}
	}
	return rsSearchCursorHandle{}, governor.ErrBusy
}

// makeRoomLocked keeps the store strictly bounded without letting one owner
// displace another's live continuation. Expired entries are already swept. If
// the entry or byte ceiling is still reached, only the issuing owner's own
// earliest-expiring handle (then earliest issued) may be retired; a predecessor
// that IssueNext is about to consume is counted as already freed. When the
// store is full of other owners' live entries, refusal is explicit
// (governor.ErrBusy) rather than a silent cross-owner eviction.
func (s *RSSearchCursorStore) makeRoomLocked(owner governor.OwnerID, charge int64, retire *rsSearchCursorHandle) bool {
	entries, bytes := len(s.entries), s.bytes
	if retire != nil {
		if e := s.entries[*retire]; e != nil {
			entries--
			bytes -= e.bytes
		}
	}
	for entries > 0 && (entries >= s.limit.MaxEntries || bytes+charge > s.limit.MaxBytes) {
		victim, ok := s.oldestOwnedLocked(owner, retire)
		if !ok {
			return false
		}
		bytes -= s.entries[victim].bytes
		entries--
		s.removeLocked(victim)
	}
	return entries < s.limit.MaxEntries && bytes+charge <= s.limit.MaxBytes
}

// oldestOwnedLocked picks the earliest-expiring, then earliest-issued, live
// entry belonging to owner. skip is the IssueNext predecessor, which is retired
// separately and is not an eviction candidate.
func (s *RSSearchCursorStore) oldestOwnedLocked(owner governor.OwnerID, skip *rsSearchCursorHandle) (rsSearchCursorHandle, bool) {
	var victim rsSearchCursorHandle
	found := false
	for h, e := range s.entries {
		if e.owner != owner || skip != nil && h == *skip {
			continue
		}
		if !found || e.expires.Before(s.entries[victim].expires) || e.expires.Equal(s.entries[victim].expires) && e.sequence < s.entries[victim].sequence {
			victim, found = h, true
		}
	}
	return victim, found
}

func (s *RSSearchCursorStore) sweepLocked(now time.Time) int {
	removed := 0
	for h, e := range s.entries {
		if !now.Before(e.expires) {
			s.removeLocked(h)
			removed++
		}
	}
	return removed
}

func (s *RSSearchCursorStore) removeLocked(h rsSearchCursorHandle) {
	e := s.entries[h]
	if e == nil {
		return
	}
	delete(s.entries, h)
	s.bytes -= e.bytes
}

func (s *RSSearchCursorStore) encode(h rsSearchCursorHandle, expires time.Time) string {
	raw := make([]byte, rsSearchCursorTokenRawBytes)
	raw[0] = rsSearchCursorTokenVersion
	binary.BigEndian.PutUint64(raw[1:1+rsSearchCursorExpiryBytes], uint64(expires.UnixNano()))
	copy(raw[1+rsSearchCursorExpiryBytes:], h[:])
	mac := hmac.New(sha256.New, s.key[:])
	mac.Write(raw[:rsSearchCursorTokenRawBytes-sha256.Size])
	copy(raw[rsSearchCursorTokenRawBytes-sha256.Size:], mac.Sum(nil))
	return rsSearchCursorTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func (s *RSSearchCursorStore) decode(token string) (rsSearchCursorHandle, time.Time, error) {
	var h rsSearchCursorHandle
	if len(token) != rsSearchCursorTokenLength || token[:len(rsSearchCursorTokenPrefix)] != rsSearchCursorTokenPrefix {
		return h, time.Time{}, ErrStale
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(rsSearchCursorTokenPrefix):])
	if err != nil || len(raw) != rsSearchCursorTokenRawBytes || raw[0] != rsSearchCursorTokenVersion {
		return h, time.Time{}, ErrStale
	}
	mac := hmac.New(sha256.New, s.key[:])
	mac.Write(raw[:rsSearchCursorTokenRawBytes-sha256.Size])
	if !hmac.Equal(raw[rsSearchCursorTokenRawBytes-sha256.Size:], mac.Sum(nil)) {
		return h, time.Time{}, ErrStale
	}
	nanos := int64(binary.BigEndian.Uint64(raw[1 : 1+rsSearchCursorExpiryBytes]))
	if nanos <= 0 {
		return h, time.Time{}, ErrStale
	}
	copy(h[:], raw[1+rsSearchCursorExpiryBytes:rsSearchCursorTokenRawBytes-sha256.Size])
	return h, time.Unix(0, nanos), nil
}

// rsSearchCursorBinding names the source/bank evidence a cursor retains. The
// rank cursor is authoritative; a stamp on an ordered continuation is the next
// best evidence. Absent evidence leaves the cursor unbound here.
func rsSearchCursorBinding(c *RSSearchCursor) (query string, bank, revision int64) {
	if c == nil {
		return "", 0, 0
	}
	if c.rank != nil {
		query, bank, revision = c.rank.query, c.rank.bank, c.rank.revision
	}
	stamp := c.anchor
	if stamp == nil {
		for _, anchor := range c.anchors {
			if anchor != nil {
				stamp = anchor
				break
			}
		}
	}
	if bank <= 0 && stamp != nil {
		bank, revision = stamp.fence.bank, stamp.fence.revision
	}
	return query, bank, revision
}

// rsSearchCursorRetainedBytes charges one retained cursor including map/entry
// overhead, owned strings, continuation slices and the measured source-stamp
// metadata. Every text and slice allowance is doubled to fund the store's own
// entry plus the adapter's resolved copy; native stamp metadata is measured by
// its issuer, never re-invented here.
func rsSearchCursorRetainedBytes(c *RSSearchCursor) int64 {
	if c == nil {
		return 0
	}
	n := rsSearchCursorEntryOverhead + int64(unsafe.Sizeof(*c))
	n += int64(cap(c.anchors)) * int64(unsafe.Sizeof((*FullSearchReadStamp)(nil)))
	n += 2 * c.anchor.MetadataBytes()
	for _, anchor := range c.anchors {
		n += 2 * anchor.MetadataBytes()
	}
	if o := c.order; o != nil {
		n += int64(unsafe.Sizeof(*o)) + 2*int64(len(o.SortKey)+len(o.AddedAt))
	}
	if r := c.rank; r != nil {
		n += int64(unsafe.Sizeof(*r)) + 2*int64(len(r.query)+len(r.after.lead.Revision))
		auto := r.after.route.automatic
		n += 2 * int64(len(auto.Provider)+len(auto.Kind)+len(auto.ProviderID)+len(auto.Evidence))
		if v := r.native; v != nil {
			n += int64(unsafe.Sizeof(*v)) + rsSearchFenceBytes(v.fence)
			n += 2 * int64(cap(v.counts)) * 8
			n += 2 * int64(cap(v.top)) * int64(unsafe.Sizeof(nativeRankHit{}))
			n += 2 * int64(cap(v.seen)) * int64(unsafe.Sizeof(nativeSeenKey{}))
			if e := v.expansion; e != nil {
				n += int64(unsafe.Sizeof(*e)) + 2*int64(len(e.source.Revision))
				n += 2 * 2 * int64(unsafe.Sizeof(ownerCursor{}))
			}
		}
	}
	return n
}

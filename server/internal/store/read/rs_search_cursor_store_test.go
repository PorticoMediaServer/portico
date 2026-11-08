package read

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

var rsCursorStoreEpoch = time.Unix(1_760_000_000, 0).UTC()

type rsCursorStoreClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *rsCursorStoreClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *rsCursorStoreClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

func rsCursorStoreTestKey() [32]byte { return sha256.Sum256([]byte("rs search cursor store test key")) }

func rsCursorStoreTestLimit() RSSearchCursorStoreLimit {
	return RSSearchCursorStoreLimit{MaxEntries: 8, MaxPerOwner: 4, MaxBytes: 1 << 20, MaxEntryBytes: 256 << 10, TTL: time.Minute}
}

func rsCursorStoreTestStore(t *testing.T, clock *rsCursorStoreClock, limit RSSearchCursorStoreLimit) *RSSearchCursorStore {
	t.Helper()
	s, err := newRSSearchCursorStore(rsCursorStoreTestKey(), limit, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rsCursorStoreQuery(t *testing.T, text string) search.Query {
	t.Helper()
	q, err := search.PlanQuery(text, true)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func rsCursorStoreInput(t *testing.T, text string, library int64, token string) RSSearchInput {
	t.Helper()
	return RSSearchInput{
		Views:    []BrowseInput{{Database: "cursor-db", TokenHash: token, Library: library, Now: rsCursorStoreEpoch}},
		Query:    rsCursorStoreQuery(t, text),
		Group:    1,
		Full:     true,
		Order:    "relevance",
		Capacity: 64,
	}
}

func rsCursorStoreIdentity(t *testing.T, owner governor.OwnerID, in RSSearchInput) [32]byte {
	t.Helper()
	id, err := RSSearchCursorIdentity(owner, in)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func rsCursorStoreCursor(t *testing.T, identity [32]byte, query string, bank, revision int64, seen int) *RSSearchCursor {
	t.Helper()
	keys := make([]nativeSeenKey, seen)
	for i := range keys {
		keys[i] = nativeSeenKey{subject: search.ManifestEntity, id: int64(5000 + i)}
	}
	return &RSSearchCursor{
		identity: identity,
		rank: &FullSearchCursor{
			query:    query,
			bank:     bank,
			revision: revision,
			input:    1,
			after:    canonicalCandidateProof{key: fullResultKey{family: search.ManifestEntity, id: 42, library: 1}, tier: 1, lead: search.FieldKey{Revision: "rev-1"}},
			native:   &nativeContinuation{seen: keys},
		},
	}
}

func rsCursorStoreAppendSeen(c *RSSearchCursor, keys ...nativeSeenKey) *RSSearchCursor {
	next := *c
	rank := *c.rank
	native := *c.rank.native
	native.seen = append(slices.Clone(native.seen), keys...)
	rank.native = &native
	next.rank = &rank
	return &next
}

func rsCursorStoreTamper(t *testing.T, token string, mutate func([]byte)) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token[len(rsSearchCursorTokenPrefix):])
	if err != nil {
		t.Fatal(err)
	}
	mutate(raw)
	return rsSearchCursorTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

func TestRSSearchCursorStoreRoundTripTamperAndBinding(t *testing.T) {
	clock := &rsCursorStoreClock{at: rsCursorStoreEpoch}
	store := rsCursorStoreTestStore(t, clock, rsCursorStoreTestLimit())
	owner := governor.OwnerID(5)
	in := rsCursorStoreInput(t, "hello", 9, "token-a")
	identity := rsCursorStoreIdentity(t, owner, in)
	cursor := rsCursorStoreCursor(t, identity, in.Query.Fold(), 21, 9, 12)

	token, expires, err := store.Issue(owner, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != rsSearchCursorTokenLength || expires != clock.now().Add(time.Minute) {
		t.Fatalf("token/expiry not bounded: %d %v", len(token), expires)
	}
	got, until, err := store.Resolve(token, RSSearchCursorRequest{Owner: owner, Input: in, Bank: 21, Revision: 9})
	if err != nil {
		t.Fatal(err)
	}
	if !until.Equal(expires) || !reflect.DeepEqual(got, cursor) {
		t.Fatalf("retained continuation changed: %v", until)
	}
	if got.rank == nil || got.rank.native == nil || len(got.rank.native.seen) != 12 {
		t.Fatal("server-side preview seen set was not retained", got)
	}

	refuse := func(name string, req RSSearchCursorRequest, want error) {
		t.Helper()
		if _, _, err := store.Resolve(token, req); !errors.Is(err, want) {
			t.Fatalf("%s: got %v, want %v", name, err, want)
		}
	}
	refuse("other viewer", RSSearchCursorRequest{Owner: 6, Input: in, Bank: 21, Revision: 9}, ErrDenied)
	refuse("other bank", RSSearchCursorRequest{Owner: owner, Input: in, Bank: 22, Revision: 9}, ErrStale)
	refuse("other revision", RSSearchCursorRequest{Owner: owner, Input: in, Bank: 21, Revision: 10}, ErrStale)

	profile := in
	profile.Views = slices.Clone(in.Views)
	profile.Views[0].TokenHash = "token-b"
	refuse("other profile", RSSearchCursorRequest{Owner: owner, Input: profile, Bank: 21, Revision: 9}, ErrStale)

	otherQuery := in
	otherQuery.Query = rsCursorStoreQuery(t, "other")
	refuse("other query", RSSearchCursorRequest{Owner: owner, Input: otherQuery, Bank: 21, Revision: 9}, ErrStale)

	// Request-entry time is not part of the binding.
	later := in
	later.Views = slices.Clone(in.Views)
	later.Views[0].Now = clock.now().Add(time.Hour)
	if _, _, err := store.Resolve(token, RSSearchCursorRequest{Owner: owner, Input: later, Bank: 21, Revision: 9}); err != nil {
		t.Fatalf("request clock changed the binding: %v", err)
	}

	restarted := rsCursorStoreTestStore(t, &rsCursorStoreClock{at: rsCursorStoreEpoch}, rsCursorStoreTestLimit())
	if _, _, err := restarted.Resolve(token, RSSearchCursorRequest{Owner: owner, Input: in, Bank: 21, Revision: 9}); !errors.Is(err, ErrStale) {
		t.Fatalf("restart accepted an old token: %v", err)
	}

	otherKey := sha256.Sum256([]byte("another cursor store key"))
	wrongKey, err := newRSSearchCursorStore(otherKey, rsCursorStoreTestLimit(), clock.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := wrongKey.Resolve(token, RSSearchCursorRequest{Owner: owner, Input: in, Bank: 21, Revision: 9}); !errors.Is(err, ErrStale) {
		t.Fatalf("foreign key accepted a token: %v", err)
	}

	for name, mutate := range map[string]func([]byte){
		"version": func(raw []byte) { raw[0] ^= 1 },
		"expiry":  func(raw []byte) { raw[1] ^= 1 },
		"handle":  func(raw []byte) { raw[10] ^= 1 },
		"mac":     func(raw []byte) { raw[len(raw)-1] ^= 1 },
	} {
		token := token
		if _, _, err := store.Resolve(rsCursorStoreTamper(t, token, mutate), RSSearchCursorRequest{Owner: owner, Input: in, Bank: 21, Revision: 9}); !errors.Is(err, ErrStale) {
			t.Fatalf("%s tamper accepted: %v", name, err)
		}
	}
	for _, broken := range []string{token[:len(token)-1], token + "A", token[:len(rsSearchCursorTokenPrefix)] + "AAAA", "not-a-token"} {
		if _, _, err := store.Resolve(broken, RSSearchCursorRequest{Owner: owner, Input: in, Bank: 21, Revision: 9}); !errors.Is(err, ErrStale) {
			t.Fatalf("malformed token accepted: %v", err)
		}
	}
}

func TestRSSearchCursorStoreExpiryEvictionAndRestart(t *testing.T) {
	clock := &rsCursorStoreClock{at: rsCursorStoreEpoch}
	limit := rsCursorStoreTestLimit()
	limit.TTL = 10 * time.Second
	store := rsCursorStoreTestStore(t, clock, limit)
	owner := governor.OwnerID(3)
	in := rsCursorStoreInput(t, "hello", 1, "token")
	identity := rsCursorStoreIdentity(t, owner, in)
	cursor := rsCursorStoreCursor(t, identity, in.Query.Fold(), 4, 2, 3)
	req := RSSearchCursorRequest{Owner: owner, Input: in, Bank: 4, Revision: 2}

	token, expires, err := store.Issue(owner, cursor)
	if err != nil {
		t.Fatal(err)
	}
	clock.advance(9 * time.Second)
	if _, _, err := store.Resolve(token, req); err != nil {
		t.Fatalf("live handle refused early: %v", err)
	}
	clock.advance(2 * time.Second)
	if _, _, err := store.Resolve(token, req); !errors.Is(err, ErrStale) {
		t.Fatalf("expired handle accepted: %v", err)
	}
	if stats := store.Stats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("expired handle still charged: %v", stats)
	}

	clock = &rsCursorStoreClock{at: rsCursorStoreEpoch}
	store = rsCursorStoreTestStore(t, clock, limit)
	// A continuation ceiling shorter than the store TTL is carried forward.
	short, expires, err := store.IssueUntil(owner, cursor, rsCursorStoreEpoch.Add(3*time.Second))
	if err != nil || !expires.Equal(rsCursorStoreEpoch.Add(3*time.Second)) {
		t.Fatalf("ceiling not shortened: %v %v", expires, err)
	}
	clock.advance(4 * time.Second)
	if _, _, err := store.Resolve(short, req); !errors.Is(err, ErrStale) {
		t.Fatalf("inherited ceiling ignored: %v", err)
	}
	// A later ceiling never extends the store TTL.
	if _, laterExpiry, err := store.IssueUntil(owner, cursor, rsCursorStoreEpoch.Add(time.Hour)); err != nil || !laterExpiry.Equal(clock.now().Add(limit.TTL)) {
		t.Fatalf("ceiling extended the handle: %v %v", laterExpiry, err)
	}
	if _, _, err := store.IssueUntil(owner, cursor, clock.now().Add(-time.Second)); !errors.Is(err, ErrStale) {
		t.Fatalf("past ceiling accepted: %v", err)
	}

	// Global capacity never retires another owner's live handle: a new owner is
	// refused once the store is full, and a continuing owner only displaces its
	// own superseded handle.
	clock = &rsCursorStoreClock{at: rsCursorStoreEpoch}
	small := rsCursorStoreTestLimit()
	small.MaxEntries = 2
	small.MaxPerOwner = 1
	evicting := rsCursorStoreTestStore(t, clock, small)
	owned := func(o governor.OwnerID) *RSSearchCursor {
		return rsCursorStoreCursor(t, rsCursorStoreIdentity(t, o, in), in.Query.Fold(), 4, 2, 3)
	}
	first, _, err := evicting.Issue(1, owned(1))
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := evicting.Issue(2, owned(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := evicting.Issue(3, owned(3)); !errors.Is(err, governor.ErrBusy) {
		t.Fatalf("a new owner displaced live handles: %v", err)
	}
	for owner, token := range map[governor.OwnerID]string{1: first, 2: second} {
		got, _, err := evicting.Resolve(token, RSSearchCursorRequest{Owner: owner, Input: in, Bank: 4, Revision: 2})
		if err != nil || got == nil {
			t.Fatalf("refused owner lost a live handle: %v", err)
		}
	}
	if _, _, err := evicting.Issue(1, owned(1)); !errors.Is(err, governor.ErrBusy) {
		t.Fatalf("per-owner allowance accepted a fifth handle: %v", err)
	}
	third, _, err := evicting.IssueNext(1, first, owned(1), time.Time{})
	if err != nil {
		t.Fatalf("continuing owner could not displace its own handle: %v", err)
	}
	if _, _, err := evicting.Resolve(first, req); !errors.Is(err, ErrStale) {
		t.Fatalf("superseded handle survived: %v", err)
	}
	for owner, token := range map[governor.OwnerID]string{2: second, 1: third} {
		got, _, err := evicting.Resolve(token, RSSearchCursorRequest{Owner: owner, Input: in, Bank: 4, Revision: 2})
		if err != nil || got == nil {
			t.Fatalf("surviving handle lost: %v", err)
		}
	}
	if stats := evicting.Stats(); stats.Entries != 2 || stats.Bytes > small.MaxBytes || stats.Entries > small.MaxEntries {
		t.Fatalf("store exceeded its ceiling: %v", stats)
	}
}

func TestRSSearchCursorStoreMultiOwnerFloodKeepsLiveHandles(t *testing.T) {
	clock := &rsCursorStoreClock{at: rsCursorStoreEpoch}
	limit := rsCursorStoreTestLimit()
	limit.MaxEntries = 4
	limit.MaxPerOwner = 4
	limit.TTL = time.Minute
	store := rsCursorStoreTestStore(t, clock, limit)
	in := rsCursorStoreInput(t, "hello", 1, "token")
	owned := func(o governor.OwnerID) *RSSearchCursor {
		return rsCursorStoreCursor(t, rsCursorStoreIdentity(t, o, in), in.Query.Fold(), 4, 2, 2)
	}
	req := func(o governor.OwnerID) RSSearchCursorRequest {
		return RSSearchCursorRequest{Owner: o, Input: in, Bank: 4, Revision: 2}
	}
	resolves := func(o governor.OwnerID, token string) bool {
		_, _, err := store.Resolve(token, req(o))
		return err == nil
	}

	first, _, err := store.Issue(1, owned(1))
	if err != nil {
		t.Fatal(err)
	}
	ownerOne := []string{first}
	for i := 0; i < 2; i++ {
		token, _, err := store.Issue(1, owned(1))
		if err != nil {
			t.Fatal(err)
		}
		ownerOne = append(ownerOne, token)
	}
	ownerTwo, _, err := store.Issue(2, owned(2))
	if err != nil {
		t.Fatal(err)
	}
	if stats := store.Stats(); stats.Entries != 4 {
		t.Fatalf("store not filled: %v", stats)
	}
	if _, _, err := store.Issue(3, owned(3)); !errors.Is(err, governor.ErrBusy) {
		t.Fatalf("a ninth owner evicted a live handle: %v", err)
	}
	for i, token := range ownerOne {
		if !resolves(1, token) {
			t.Fatalf("refused newcomer lost owner 1 handle %d", i)
		}
	}
	if !resolves(2, ownerTwo) {
		t.Fatal("refused newcomer lost owner 2 handle")
	}

	// The flooding owner may displace only its own oldest live handle.
	replacement, _, err := store.Issue(1, owned(1))
	if err != nil {
		t.Fatalf("flooding owner could not displace its own handle: %v", err)
	}
	if resolves(1, ownerOne[0]) {
		t.Fatal("own oldest handle was not displaced")
	}
	for i, token := range ownerOne[1:] {
		if !resolves(1, token) {
			t.Fatalf("flood displaced a newer own handle %d", i+1)
		}
	}
	if !resolves(2, ownerTwo) {
		t.Fatal("flood displaced another owner's live handle")
	}
	if _, _, err := store.Issue(4, owned(4)); !errors.Is(err, governor.ErrBusy) {
		t.Fatalf("second newcomer evicted a live handle: %v", err)
	}
	if !resolves(1, replacement) || !resolves(2, ownerTwo) {
		t.Fatal("refusal damaged a live handle")
	}
	if stats := store.Stats(); stats.Entries > limit.MaxEntries || stats.Bytes > limit.MaxBytes {
		t.Fatalf("flood escaped its ceiling: %v", stats)
	}
}

func TestRSSearchCursorStoreFloodBoundedAndCharged(t *testing.T) {
	clock := &rsCursorStoreClock{at: rsCursorStoreEpoch}
	limit := rsCursorStoreTestLimit()
	limit.MaxEntries = 4
	limit.MaxPerOwner = 2
	limit.MaxBytes = 1 << 20
	store := rsCursorStoreTestStore(t, clock, limit)
	in := rsCursorStoreInput(t, "hello", 1, "token")
	identity := rsCursorStoreIdentity(t, 1, in)
	cursor := rsCursorStoreCursor(t, identity, in.Query.Fold(), 8, 3, 16)
	req := RSSearchCursorRequest{Owner: 1, Input: in, Bank: 8, Revision: 3}

	first, _, err := store.Issue(1, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Issue(1, cursor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Issue(1, cursor); !errors.Is(err, governor.ErrBusy) {
		t.Fatalf("one viewer flooded its owner allowance: %v", err)
	}
	stats := store.Stats()
	if stats.Entries != 2 || stats.Bytes != 2*rsSearchCursorRetainedBytes(cursor) {
		t.Fatalf("flood charge not bounded/exact: %v", stats)
	}
	if stats.Bytes <= 0 || stats.MaxBytes > governor.PreparationMaxBytes {
		t.Fatalf("uncharged or unbounded store: %v", stats)
	}
	if _, _, err := store.Resolve(first, req); err != nil {
		t.Fatalf("flood refused a live handle: %v", err)
	}

	// Other viewers may use the remaining global slots, but once the store is
	// full a new viewer is refused instead of evicting a live handle.
	stores := make([]string, 0, 4)
	for owner := governor.OwnerID(2); owner <= 3; owner++ {
		token, _, err := store.Issue(owner, rsCursorStoreCursor(t, rsCursorStoreIdentity(t, owner, in), in.Query.Fold(), 8, 3, 16))
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, token)
	}
	if _, _, err := store.Issue(4, rsCursorStoreCursor(t, rsCursorStoreIdentity(t, 4, in), in.Query.Fold(), 8, 3, 16)); !errors.Is(err, governor.ErrBusy) {
		t.Fatalf("new viewer displaced a live handle: %v", err)
	}
	if stats := store.Stats(); stats.Entries > limit.MaxEntries || stats.Bytes > limit.MaxBytes {
		t.Fatalf("global ceiling escaped: %v", stats)
	}
	if len(stores) != 2 {
		t.Fatal("expected every other viewer with room to receive one bounded handle", len(stores))
	}
	for owner, token := range map[governor.OwnerID]string{1: first, 2: stores[0], 3: stores[1]} {
		if _, _, err := store.Resolve(token, RSSearchCursorRequest{Owner: owner, Input: in, Bank: 8, Revision: 3}); err != nil {
			t.Fatalf("live handle of owner %d lost to the flood refusal: %v", owner, err)
		}
	}

	// A single cursor larger than its entry allowance is refused rather than
	// truncated, charged or silently split.
	big := rsCursorStoreCursor(t, identity, in.Query.Fold(), 8, 3, NativePreviewCandidates)
	charge := rsSearchCursorRetainedBytes(big)
	if charge < 8<<10 || charge > 70<<10 || charge < NativePreviewCandidates*int64(unsafe.Sizeof(nativeSeenKey{})) {
		t.Fatalf("preview seen set not in the expected 8-70KB range: %d", charge)
	}
	tight := rsCursorStoreTestLimit()
	tight.MaxEntryBytes = charge - 1
	if tight.MaxEntryBytes < rsSearchCursorEntryOverhead {
		t.Fatal("test limit invalid")
	}
	small := rsCursorStoreTestStore(t, clock, tight)
	if _, _, err := small.Issue(1, big); !errors.Is(err, governor.ErrSnapshotBudget) {
		t.Fatalf("oversized cursor accepted: %v", err)
	}
	full := rsCursorStoreTestStore(t, clock, rsCursorStoreTestLimit())
	token, _, err := full.Issue(1, big)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) >= 128 {
		t.Fatalf("page token grew with the seen set: %d", len(token))
	}
	got, _, err := full.Resolve(token, RSSearchCursorRequest{Owner: 1, Input: in, Bank: 8, Revision: 3})
	if err != nil || got.rank.native == nil || len(got.rank.native.seen) != NativePreviewCandidates {
		t.Fatalf("2000-key continuation not exact: %v", err)
	}
}

func TestRSSearchCursorStoreExactReachabilityDedup(t *testing.T) {
	clock := &rsCursorStoreClock{at: rsCursorStoreEpoch}
	limit := DefaultRSSearchCursorStoreLimit()
	store := rsCursorStoreTestStore(t, clock, limit)
	owner := governor.OwnerID(7)
	in := rsCursorStoreInput(t, "hello", 1, "token")
	identity := rsCursorStoreIdentity(t, owner, in)
	req := RSSearchCursorRequest{Owner: owner, Input: in, Bank: 11, Revision: 5}
	cursor := rsCursorStoreCursor(t, identity, in.Query.Fold(), 11, 5, 0)

	// Walk three times the per-owner allowance at one page per second. Every
	// consumed handle must be superseded, so the walk never reaches ErrBusy and
	// only the live page stays charged.
	const pages = 3 * 4
	seen := map[int64]struct{}{}
	var emitted []int64
	var ceiling time.Time
	tokenLength := 0
	predecessor := ""
	for page := 0; page < pages; page++ {
		fresh := make([]nativeSeenKey, 0, 7)
		for i := 0; i < 7; i++ {
			id := int64(100*page + i)
			if _, duplicate := seen[id]; duplicate {
				t.Fatal("test emitted a duplicate candidate", id)
			}
			seen[id] = struct{}{}
			emitted = append(emitted, id)
			fresh = append(fresh, nativeSeenKey{subject: search.ManifestEntity, id: id})
		}
		cursor = rsCursorStoreAppendSeen(cursor, fresh...)
		if page == 1 {
			cursor.anchor = &FullSearchReadStamp{query: in.Query.Fold(), fence: nativeSearchFence{bank: 11, revision: 5}, started: clock.now(), clock: clock.now(), deadline: clock.now().Add(time.Hour)}
		}
		var token string
		var expires time.Time
		var err error
		if page == 0 {
			token, expires, err = store.IssueUntil(owner, cursor, ceiling)
		} else {
			token, expires, err = store.IssueNext(owner, predecessor, cursor, ceiling)
		}
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if page == 0 {
			ceiling = expires
			tokenLength = len(token)
			if tokenLength >= 128 {
				t.Fatalf("page token is not compact: %d", tokenLength)
			}
		} else if !expires.Equal(ceiling) || len(token) != tokenLength {
			t.Fatalf("page %d changed the retained handle shape: %v %d", page, expires, len(token))
		}
		if predecessor != "" {
			if _, _, err := store.Resolve(predecessor, req); !errors.Is(err, ErrStale) {
				t.Fatalf("page %d did not retire its predecessor: %v", page, err)
			}
		}
		if stats := store.Stats(); stats.Entries != 1 {
			t.Fatalf("page %d retained a consumed handle: %v", page, stats)
		}
		got, until, err := store.Resolve(token, req)
		if err != nil || !until.Equal(ceiling) {
			t.Fatalf("page %d did not resolve: %v %v", page, err, until)
		}
		if !reflect.DeepEqual(got, cursor) || !slices.Equal(got.rank.native.seen, cursor.rank.native.seen) {
			t.Fatalf("page %d lost exact continuation state", page)
		}
		if page == 1 && (got.anchor == nil || len(got.anchor.libraries) != 0) {
			t.Fatal("retained source stamp was not carried server-side", got.anchor)
		}
		predecessor = token
		clock.advance(time.Second)
	}
	if len(emitted) != pages*7 || len(seen) != len(emitted) {
		t.Fatalf("walk lost or duplicated hits: %d/%d", len(emitted), pages)
	}
	for _, id := range emitted {
		found := false
		for _, key := range cursor.rank.native.seen {
			found = found || key.id == id
		}
		if !found {
			t.Fatalf("final continuation cannot reach hit %d", id)
		}
	}
	if stats := store.Stats(); stats.Entries != 1 || stats.Bytes <= 0 {
		t.Fatalf("live page was not charged: %v", stats)
	}

	// An abandoned (never superseded) handle still frees at its TTL.
	clock.advance(limit.TTL + time.Second)
	if _, _, err := store.Resolve(predecessor, req); !errors.Is(err, ErrStale) {
		t.Fatalf("abandoned handle outlived its TTL: %v", err)
	}
	if stats := store.Stats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("expired abandoned handle still charged: %v", stats)
	}
}

func TestRSSearchCursorStoreIdentityMatchesBegin(t *testing.T) {
	f, _ := newRSSearchFixture(t)
	query := rsCursorStoreQuery(t, "hello")
	view := f.input
	view.Kind, view.Limit, view.After = 0, 0, nil
	second := view
	second.Library = view.Library + 1
	reader, err := newRSSearchReaderWithRead(f.authority, func(context.Context, governor.Request, time.Duration, BrowseOutput, FullSearchInput, search.Query) (FullSearchPage, *BrowseReadResult, error) {
		return FullSearchPage{}, nil, ErrUnqualified
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []RSSearchInput{
		{Views: []BrowseInput{view}, Query: query, Group: 1, Full: true, Order: "relevance", Capacity: 64},
		{Views: []BrowseInput{view}, Query: query, Group: 0, Order: "title", Capacity: 8},
		{Views: []BrowseInput{view, second}, Query: query, Group: 0, Order: "dateAdded", Direction: "asc", Capacity: 16},
		{Views: []BrowseInput{view}, Query: query, Group: 21, Order: "releaseYear", Capacity: 40},
	}
	for i, in := range cases {
		id, err := RSSearchCursorIdentity(1, in)
		if err != nil {
			t.Fatalf("case %d identity: %v", i, err)
		}
		p, refusal, err := reader.Begin(t.Context(), 1, time.Now().Add(10*time.Second), 1<<20, in)
		if err != nil || refusal != nil {
			t.Fatalf("case %d begin: %v %v", i, err, refusal)
		}
		if p.identity != id {
			t.Fatalf("case %d identity drifted from Begin", i)
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRSSearchCursorStoreIdentityGolden pins the identity hash bytes. A new
// pointer field in BrowseInput would otherwise silently hash an address instead
// of its value, so the pin fails loudly for review.
func TestRSSearchCursorStoreIdentityGolden(t *testing.T) {
	in := rsCursorStoreInput(t, "hello", 7, "pin-token")
	id := rsCursorStoreIdentity(t, 12, in)
	const want = "76ad5c0c1b86dfcbe7b94ef39dd6a3a65da8ced892787a285ae2781453621e92"
	if got := fmt.Sprintf("%x", id); got != want {
		t.Fatalf("identity hash drifted: got %s", got)
	}
}

// TestRSSearchCursorStoreResolveSurvivesBegin proves Begin copies the resolved
// continuation rather than mutating the store's shared pointer.
func TestRSSearchCursorStoreResolveSurvivesBegin(t *testing.T) {
	f, _ := newRSSearchFixture(t)
	clock := &rsCursorStoreClock{at: rsCursorStoreEpoch}
	store := rsCursorStoreTestStore(t, clock, rsCursorStoreTestLimit())
	owner := governor.OwnerID(1)
	query := rsCursorStoreQuery(t, "hello")
	view := f.input
	view.Kind, view.Limit, view.After = 0, 0, nil
	in := RSSearchInput{Views: []BrowseInput{view}, Query: query, Group: 1, Full: true, Order: "relevance", Capacity: 64}
	cursor := rsCursorStoreCursor(t, rsCursorStoreIdentity(t, owner, in), query.Fold(), 11, 5, 4)
	cursor.rank.native = nil // Begin accepts a rank cursor without a native continuation
	token, _, err := store.Issue(owner, cursor)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := store.Resolve(token, RSSearchCursorRequest{Owner: owner, Input: in, Bank: 11, Revision: 5})
	if err != nil {
		t.Fatal(err)
	}
	before := *got
	before.rank = cloneFullCursor(got.rank)
	if got.order != nil {
		order := *got.order
		before.order = &order
	}
	reader, err := newRSSearchReaderWithRead(f.authority, func(context.Context, governor.Request, time.Duration, BrowseOutput, FullSearchInput, search.Query) (FullSearchPage, *BrowseReadResult, error) {
		return FullSearchPage{}, nil, ErrUnqualified
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	next := in
	next.After = got
	p, refusal, err := reader.Begin(t.Context(), owner, time.Now().Add(10*time.Second), 1<<20, next)
	if err != nil || refusal != nil {
		t.Fatalf("begin: %v %v", err, refusal)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, &before) {
		t.Fatal("Begin mutated the resolved continuation")
	}
}

func TestRSSearchCursorStoreCloseSerialized(t *testing.T) {
	clock := &rsCursorStoreClock{at: rsCursorStoreEpoch}
	store := rsCursorStoreTestStore(t, clock, rsCursorStoreTestLimit())
	in := rsCursorStoreInput(t, "hello", 1, "token")
	identity := rsCursorStoreIdentity(t, 1, in)
	cursor := rsCursorStoreCursor(t, identity, in.Query.Fold(), 6, 1, 8)
	req := RSSearchCursorRequest{Owner: 1, Input: in, Bank: 6, Revision: 1}
	token, _, err := store.Issue(1, cursor)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n := 0; n < 64; n++ {
				_, _, _ = store.Resolve(token, req)
				_, _, _ = store.Issue(governor.OwnerID(n%3+1), cursor)
				_ = store.Stats()
			}
		}()
	}
	close(start)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if _, _, err := store.Resolve(token, req); !errors.Is(err, ErrStale) {
		t.Fatalf("closed store resolved: %v", err)
	}
	if _, _, err := store.Issue(1, cursor); !errors.Is(err, ErrStale) {
		t.Fatalf("closed store issued: %v", err)
	}
	if stats := store.Stats(); stats.Entries != 0 || stats.Bytes != 0 {
		t.Fatalf("closed store retains bytes: %v", stats)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

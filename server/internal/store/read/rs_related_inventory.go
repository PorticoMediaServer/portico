package read

import (
	"errors"
)

// ErrRelatedCapacity is the explicit refusal of an exact related answer whose
// retained source/asset proof would exceed this family's bounded window. It is
// never a truncated success and never a partially available exact card.
var ErrRelatedCapacity = errors.New("related work exceeded its exact capacity")

// rsRelatedInventoryVerdict separates a proven negative from evidence that is
// not yet decidable. Only Available may contribute an available card. Pending
// and Unresolved leave the decision to the caller's pending/refusal handling;
// a changed proof is ErrStale from verify and is never one of these values.
type rsRelatedInventoryVerdict uint8

const (
	// rsRelatedInventoryUnresolved means the retained evidence is internally
	// incomplete (for example a link whose asset row is absent in the same
	// snapshot). It must not be published as a proven unavailable item.
	rsRelatedInventoryUnresolved rsRelatedInventoryVerdict = iota
	// rsRelatedInventoryUnavailable is the canonical negative: retirement, a
	// current object that is not ready, or an unaccepted/absent file with no
	// usable source-root fallback.
	rsRelatedInventoryUnavailable
	// rsRelatedInventoryAvailable means at least one current link has canonical
	// accepted availability, with the complete source-root proof retained.
	rsRelatedInventoryAvailable
	// rsRelatedInventoryPending means an accepted information publication is
	// not applied yet. The old canonical rows may already be gone, so this must
	// stay pending rather than become an unavailable exact card.
	rsRelatedInventoryPending
)

func (v rsRelatedInventoryVerdict) String() string {
	switch v {
	case rsRelatedInventoryUnresolved:
		return "unresolved"
	case rsRelatedInventoryUnavailable:
		return "unavailable"
	case rsRelatedInventoryAvailable:
		return "available"
	case rsRelatedInventoryPending:
		return "pending"
	}
	return "unknown"
}

// Owner evidence that this family cannot yet prove. IA's typed result/collection
// children (group C) and MP's typed resume decoder (group F) are not adopted, so
// task 09 must keep the matching descendant/candidate pending. This helper is a
// named marker, not a permission to treat an unsupported walk as unavailable.
const (
	rsRelatedOwnerIATypedChildren = "ia.typed_children"
	rsRelatedOwnerMPTypedResume   = "mp.typed_resume"
)

func rsRelatedInventoryOwnerPending(owner string) rsRelatedInventoryVerdict {
	switch owner {
	case rsRelatedOwnerIATypedChildren, rsRelatedOwnerMPTypedResume:
		return rsRelatedInventoryPending
	}
	return rsRelatedInventoryUnresolved
}

// rsRelatedInventoryBudget is the caller's retained-byte/lifecycle handle. It
// only forwards to the existing RS forecast window; it creates no grant,
// ledger, cache or second deadline. A nil/nil-window budget is used by unit
// controls that exercise the reducer without a governed snapshot.
type rsRelatedInventoryBudget struct {
	Windows   *rsByteWindow
	CardBytes *int64
	WaitByte  *bool
}

func (b *rsRelatedInventoryBudget) card() int64 {
	if b == nil || b.CardBytes == nil {
		return 0
	}
	return *b.CardBytes
}

func (b *rsRelatedInventoryBudget) arm(bytes int64) error {
	if b == nil || b.Windows == nil {
		return nil
	}
	if bytes < 1 {
		bytes = 1
	}
	if err := b.Windows.arm(bytes, 2); err != nil {
		return err
	}
	if b.WaitByte != nil {
		*b.WaitByte = true
	}
	return nil
}

func (b *rsRelatedInventoryBudget) reserve(actual int64) error {
	if actual < 0 || actual > 1<<20-b.card() {
		return ErrRelatedCapacity
	}
	if b == nil || b.CardBytes == nil {
		return nil
	}
	// The same text is retained in the reducer and copied into the DTO later.
	*b.CardBytes += 2 * actual
	return nil
}

// rsRelatedInventory is one entity's exact canonical availability proof. It is
// reduced one bounded point per call under the caller's existing preparation
// lease; it retains no connection, snapshot or raw SQL. Task 09 owns the
// surrounding related reducers and calls step/verify from its own windows.
// Typed album/book descendants remain caller-owned: this adapter proves one
// candidate ID at a time, so a container must enumerate its complete typed
// child stream (never a five-child sample) and require one Available child.
// Groups C/F owner evidence that is not adopted stays an explicit Pending
// marker in the caller; it must never become an unavailable exact card.
type rsRelatedInventory struct {
	Item      int64
	Library   string
	LibraryID int64

	stage   int
	source  int
	link    int
	retired bool

	ids     [][]any
	sources map[string][]any
	links   [][]any
	assets  map[int64][]any
	objects map[int64][][]any

	pending    bool
	unresolved bool
	available  bool

	verdict rsRelatedInventoryVerdict
	done    bool
}

func newRSRelatedInventory(item, libraryID int64, library string) *rsRelatedInventory {
	return &rsRelatedInventory{Item: item, Library: library, LibraryID: libraryID, sources: map[string][]any{}, assets: map[int64][]any{}, objects: map[int64][][]any{}}
}

// Verdict is meaningful only after step reports done.
func (v *rsRelatedInventory) Verdict() rsRelatedInventoryVerdict { return v.verdict }

// Available is the only value task 09 may use to admit an available card.
func (v *rsRelatedInventory) Available() bool { return v.verdict == rsRelatedInventoryAvailable }

// step advances one bounded unit of work. done reports a complete verdict;
// err is a refusal/stale signal, never an implicit pending success. A source
// set above the raw bound, an over-wide source id, more than MaxRaw links or
// more than MaxRaw objects behind one asset refuses capacity rather than
// sampling. Every window binds MaxRaw+1 so a full page is distinguishable from
// a truncated one.
func (v *rsRelatedInventory) step(q browseQuery, b *rsRelatedInventoryBudget) (bool, error) {
	if v == nil || q == nil || v.Item <= 0 || v.Library == "" || v.LibraryID <= 0 {
		return false, ErrInput
	}
	if v.done {
		return true, nil
	}
	switch v.stage {
	case 0:
		ids, err := q("rs.related.inventory.source_ids", v.Library, int64(MaxRaw+1))
		if err != nil {
			return false, err
		}
		if len(ids) > MaxRaw {
			return false, ErrRelatedCapacity
		}
		for _, id := range ids {
			s, ok := id[0].(string)
			if !ok || len(s) == 0 || len(s) > 256 {
				return false, ErrRelatedCapacity
			}
		}
		v.ids = ids
		gone, err := q("rs.related.inventory.retired", v.Item)
		if err != nil {
			return false, err
		}
		v.retired = len(gone) > 0
		v.stage = 1
	case 1:
		if v.source == len(v.ids) {
			v.stage = 3
			break
		}
		id := v.ids[v.source][0].(string)
		n, err := rsReadingOctets(q, "rs.related.inventory.source_octets", id, v.Library)
		if err != nil {
			return false, err
		}
		if err = b.reserve(n); err != nil {
			return false, err
		}
		if err = b.arm(n); err != nil {
			return false, err
		}
		v.stage = 2
	case 2:
		id := v.ids[v.source][0].(string)
		rows, err := q("rs.related.inventory.source", id, v.Library)
		if err != nil {
			return false, err
		}
		if len(rows) != 1 {
			return false, ErrStale
		}
		v.sources[id] = rows[0]
		for _, col := range []int{0, 1, 3, 4} {
			if _, ok := rows[0][col].(string); !ok {
				return false, ErrStale
			}
		}
		v.source++
		v.stage = 1
	case 3:
		rows, err := q("rs.related.inventory.links", v.Item, int64(MaxRaw+1))
		if err != nil {
			return false, err
		}
		if len(rows) > MaxRaw {
			return false, ErrRelatedCapacity
		}
		v.links = rows
		v.link = 0
		v.stage = 4
	case 4:
		if v.link == len(v.links) {
			v.stage = 7
			break
		}
		asset := v.links[v.link][0].(int64)
		n, err := rsReadingOctets(q, "rs.related.inventory.asset_octets", asset, v.LibraryID)
		if err != nil {
			return false, err
		}
		if err = b.reserve(n); err != nil {
			return false, err
		}
		if err = b.arm(n); err != nil {
			return false, err
		}
		v.stage = 5
	case 5:
		asset := v.links[v.link][0].(int64)
		rows, err := q("rs.related.inventory.asset", asset, v.LibraryID)
		if err != nil {
			return false, err
		}
		if len(rows) != 1 {
			// A link without its asset is incomplete evidence, not a negative.
			v.unresolved = true
			v.link++
			v.stage = 4
			break
		}
		v.assets[asset] = rows[0]
		if _, ok1 := rows[0][0].(string); !ok1 {
			return false, ErrStale
		}
		if _, ok2 := rows[0][1].(string); !ok2 {
			return false, ErrStale
		}
		v.stage = 6
	case 6:
		asset := v.links[v.link][0].(int64)
		objects, err := q("rs.related.inventory.objects", v.assets[asset][0], v.Library, int64(MaxRaw+1))
		if err != nil {
			return false, err
		}
		if len(objects) > MaxRaw {
			return false, ErrRelatedCapacity
		}
		v.objects[asset] = objects
		switch v.classify(v.links[v.link], v.assets[asset], objects) {
		case rsRelatedInventoryAvailable:
			v.available = true
			return v.finish()
		case rsRelatedInventoryPending:
			v.pending = true
		case rsRelatedInventoryUnresolved:
			v.unresolved = true
		}
		v.link++
		v.stage = 4
	case 7:
		return v.finish()
	default:
		return false, ErrInput
	}
	return false, nil
}

func (v *rsRelatedInventory) finish() (bool, error) {
	v.verdict = rsRelatedInventoryUnavailable
	switch {
	case v.available:
		v.verdict = rsRelatedInventoryAvailable
	case v.pending:
		v.verdict = rsRelatedInventoryPending
	case v.unresolved:
		v.verdict = rsRelatedInventoryUnresolved
	}
	v.done = true
	return true, nil
}

// classify uses the canonical availability recipe: a current accepted object
// overrides link/asset flags; any present-but-stale/retired/unsupported object
// forbids fallback; only when no object exists do the asset flag and exact
// source root/configured-root/enable/health fallback apply. Retirement wins
// before any of that. An unaccepted information publication is pending and
// suppresses the answer entirely.
func (v *rsRelatedInventory) classify(link, asset []any, objects [][]any) rsRelatedInventoryVerdict {
	if v.retired {
		return rsRelatedInventoryUnavailable
	}
	if link[3] != nil || asset[5].(int64) != 0 {
		return rsRelatedInventoryPending
	}
	exists, ready := false, false
	for _, o := range objects {
		exists = true
		if o[0].(int64) == 0 && o[1].(int64) == 0 && o[2].(int64) == 0 && o[3].(int64) == 0 && o[4].(int64) == 1 && o[5].(int64) != 0 && o[6].(int64) != 0 {
			ready = true
		}
	}
	if exists {
		if ready {
			return rsRelatedInventoryAvailable
		}
		return rsRelatedInventoryUnavailable
	}
	if asset[2].(int64) == 0 {
		return rsRelatedInventoryUnavailable
	}
	path := asset[1].(string)
	matches, goodMatch, anyGood := false, true, false
	for _, s := range v.sources {
		good := s[2].(int64) != 0 && s[3] != "root_changed" && s[3] != "removing"
		anyGood = anyGood || good
		if rsReadingRootMatch(path, s[0].(string)) || rsReadingRootMatch(path, s[1].(string)) {
			matches = true
			goodMatch = goodMatch && good
		}
	}
	if matches {
		if goodMatch {
			return rsRelatedInventoryAvailable
		}
		return rsRelatedInventoryUnavailable
	}
	if len(v.sources) == 0 || anyGood {
		return rsRelatedInventoryAvailable
	}
	return rsRelatedInventoryUnavailable
}

// verify rechecks the complete retained proof in the final business window.
// Source roots are part of that proof, not only the first read: a changed root,
// configured root, enabled/health/incarnation/generation, asset token/path/
// availability/size/mtime, accepted publication flag, link window or object
// readiness forces ErrStale so the caller retries inside its existing deadline.
// An object window above MaxRaw is an explicit capacity refusal, never accepted
// as an unchanged (truncated) proof.
func (v *rsRelatedInventory) verify(q browseQuery) error {
	if v == nil || q == nil || !v.done {
		return ErrStale
	}
	ids, err := q("rs.related.inventory.source_ids", v.Library, int64(MaxRaw+1))
	if err != nil {
		return err
	}
	if len(ids) > MaxRaw || !equalSourceRows(ids, v.ids) {
		return ErrStale
	}
	gone, err := q("rs.related.inventory.retired", v.Item)
	if err != nil {
		return err
	}
	if (len(gone) > 0) != v.retired {
		return ErrStale
	}
	for id, s := range v.sources {
		rows, err := q("rs.related.inventory.verify_source", id, v.Library, s[0], s[1], s[2], s[3], s[4], s[5])
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrStale
		}
	}
	links, err := q("rs.related.inventory.links", v.Item, int64(MaxRaw+1))
	if err != nil {
		return err
	}
	if len(links) > MaxRaw || !equalSourceRows(links, v.links) {
		return ErrStale
	}
	for asset, a := range v.assets {
		rows, err := q("rs.related.inventory.verify_asset", asset, v.LibraryID, a[0], a[1], a[2], a[3], a[4], a[5])
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return ErrStale
		}
		objects, err := q("rs.related.inventory.objects", a[0], v.Library, int64(MaxRaw+1))
		if err != nil {
			return err
		}
		if len(objects) > MaxRaw {
			return ErrRelatedCapacity
		}
		if !equalSourceRows(objects, v.objects[asset]) {
			return ErrStale
		}
	}
	return nil
}

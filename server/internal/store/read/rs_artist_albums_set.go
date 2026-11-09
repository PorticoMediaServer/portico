package read

import (
	"cmp"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// rsArtistAlbumOrderVersion names the one public artist-album order. A cursor
// minted under any other order is not a continuation of this result.
const rsArtistAlbumOrderVersion = 1

// rsArtistAlbumEntry is the computed legacy ordering key: the album entity's
// current year and title plus its entity id. Nothing here grants permission.
type rsArtistAlbumEntry struct {
	id, year int64
	title    string
}

// rsArtistAlbumSet is a values-only reducer. Its backing storage must be
// allocated from an admitted PreparationLease payload before enumeration.
// Callers insert ONLY after current song/album policy proofs. This type cannot
// grant permission and deliberately exposes no public page or continuation.
//
// Membership is deduplicated by album entity id while ids stay entity-sorted.
// The complete union is ordered once at finish by the legacy artist releases
// key from catalog/listening_content.go:
//
//	CASE WHEN year=0 THEN 1 ELSE 0 END, year DESC,
//	title COLLATE NOCASE, entity_id
//
// Release-type/role grouping is not part of that order (albumRoleSQL tags a
// role but the section's ORDER BY never uses it). Capacity refusal remains
// governor.ErrSnapshotBudget: a caller never receives a truncated exact page.
type rsArtistAlbumSet struct {
	ids      []int64
	entries  []rsArtistAlbumEntry
	complete bool
}

// grow reserves one more id and one more entry. The caller has already charged
// the lease payload; this only splits the two aligned allocations.
func (s *rsArtistAlbumSet) grow() error {
	if s.complete || len(s.ids) != len(s.entries) {
		return ErrInput
	}
	n := len(s.ids) + 1
	if cap(s.ids) < n {
		ids := make([]int64, len(s.ids), n)
		copy(ids, s.ids)
		s.ids = ids
	}
	if cap(s.entries) < n {
		entries := make([]rsArtistAlbumEntry, len(s.entries), n)
		copy(entries, s.entries)
		s.entries = entries
	}
	return nil
}

func (s *rsArtistAlbumSet) add(id, year int64, title string) error {
	if s.complete || id <= 0 {
		return ErrInput
	}
	i, found := slices.BinarySearch(s.ids, id)
	if found {
		return nil
	}
	if len(s.ids) == cap(s.ids) || len(s.entries) == cap(s.entries) {
		return governor.ErrSnapshotBudget
	}
	s.ids = append(s.ids, 0)
	copy(s.ids[i+1:], s.ids[i:len(s.ids)-1])
	s.ids[i] = id
	s.entries = append(s.entries, rsArtistAlbumEntry{id: id, year: year, title: title})
	return nil
}

// finish sorts the complete deduplicated union by the legacy key. It must only
// run after enumeration has proved the union complete.
func (s *rsArtistAlbumSet) finish() error {
	if s.complete || len(s.ids) != len(s.entries) {
		return ErrInput
	}
	slices.SortFunc(s.entries, rsArtistAlbumCompare)
	s.complete = true
	return nil
}

// page returns the entries strictly after the cursor key. The total order is
// (year-zero-last, year DESC, title NOCASE, entity id), so a replay under a
// different order or key cannot silently continue another result.
func (s *rsArtistAlbumSet) page(after rsArtistAlbumEntry, hasAfter bool, limit int) ([]rsArtistAlbumEntry, bool, error) {
	if !s.complete || limit < 1 || limit > MaxPage {
		return nil, false, ErrInput
	}
	start := 0
	if hasAfter {
		start = sortSearchEntries(s.entries, after)
	}
	end := min(start+limit, len(s.entries))
	if end < start {
		return nil, false, ErrInput
	}
	return s.entries[start:end], end == len(s.entries), nil
}

func sortSearchEntries(entries []rsArtistAlbumEntry, after rsArtistAlbumEntry) int {
	lo, hi := 0, len(entries)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if rsArtistAlbumCompare(entries[mid], after) <= 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// rsArtistAlbumCompare implements the legacy SQLite order exactly: unknown year
// (0) after every known year, years descending, title under SQLite NOCASE
// (ASCII-only folding, NUL-terminated comparison, then original byte length),
// and entity id ascending as the unique tie-break.
func rsArtistAlbumCompare(a, b rsArtistAlbumEntry) int {
	az, bz := 0, 0
	if a.year == 0 {
		az = 1
	}
	if b.year == 0 {
		bz = 1
	}
	if c := cmp.Compare(az, bz); c != 0 {
		return c
	}
	if c := cmp.Compare(b.year, a.year); c != 0 {
		return c
	}
	if c := rsListeningKeyCompare(a.title, b.title); c != 0 {
		return c
	}
	return cmp.Compare(a.id, b.id)
}

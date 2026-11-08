package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"slices"
)

// rsArtistAlbumSet is a values-only reducer. Its backing storage must be
// allocated from an admitted PreparationLease payload before enumeration.
// Callers insert ONLY after current song/album policy proofs. This type cannot
// grant permission and deliberately exposes no public page or continuation.
type rsArtistAlbumSet struct {
	ids      []int64
	complete bool
}

func (s *rsArtistAlbumSet) add(id int64) error {
	if s.complete || id <= 0 {
		return ErrInput
	}
	i, found := slices.BinarySearch(s.ids, id)
	if found {
		return nil
	}
	if len(s.ids) == cap(s.ids) {
		return governor.ErrSnapshotBudget
	}
	s.ids = append(s.ids, 0)
	copy(s.ids[i+1:], s.ids[i:len(s.ids)-1])
	s.ids[i] = id
	return nil
}
func (s *rsArtistAlbumSet) page(after int64, limit int) ([]int64, bool, error) {
	if !s.complete || after < 0 || limit < 1 || limit > MaxPage {
		return nil, false, ErrInput
	}
	i, _ := slices.BinarySearch(s.ids, after)
	if i < len(s.ids) && s.ids[i] == after {
		i++
	}
	end := min(i+limit, len(s.ids))
	return s.ids[i:end], end == len(s.ids), nil
}

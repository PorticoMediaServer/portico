package httpapi

import "encoding/json"

// Reading pages are bounded by bytes as well as by count. Owner text can make
// one item tens of kilobytes after JSON escaping, so a full count page could
// exceed what clients admit (client-core reads item lists up to 256 KiB and the
// other Reading pages up to 2 MiB). A page stops before the entry that would
// pass its budget and continues from there; one entry always fits, so a page
// always advances.
const (
	readingItemsPageBytes   = 192 << 10
	readingPageBytes        = 1 << 20
	readingDiscoverRowBytes = 384 << 10 // up to four paged rows plus one newspaper slot
)

// readingBudget keeps the leading projected entries that fit budget and, when it
// drops any, moves the page's continuation to the last entry kept.
func readingBudget[T any](projected []T, budget int, truncate func(int) error) ([]T, error) {
	used := 0
	for i, entry := range projected {
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		if used += len(raw) + 1; used > budget && i > 0 {
			if err = truncate(i); err != nil {
				return nil, err
			}
			return projected[:i], nil
		}
	}
	return projected, nil
}

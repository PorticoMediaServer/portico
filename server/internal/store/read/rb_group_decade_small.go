package read

import "strconv"

// Public holder semantics exclude unknown and out-of-range years. TV uses the
// show's own accepted year, not an episode's year or a hidden-child aggregate.
func rbDecadeValue(year int64) (string, bool) {
	if year < 1800 || year > 2199 {
		return "", false
	}
	return strconv.FormatInt(year/10*10, 10), true
}

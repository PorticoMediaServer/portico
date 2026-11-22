package db

import (
	"strconv"
	"strings"
)

// oneCopyEach keeps the first of each title in a list of suggestions: a film or show the
// server holds in two libraries (a 4K library beside an HD one) is suggested once, not twice.
// A title is the same when its kind, title and year are. seen carries over between calls.
func oneCopyEach(items []Item, seen map[string]bool) []Item {
	out := items[:0]
	for _, it := range items {
		key := strconv.Itoa(int(it.Kind)) + "|" + strings.ToLower(strings.TrimSpace(it.Title)) + "|" + strconv.Itoa(it.Year)
		if !seen[key] {
			seen[key] = true
			out = append(out, it)
		}
	}
	return out
}

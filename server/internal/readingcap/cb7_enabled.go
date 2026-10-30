//go:build portico_cb7

package readingcap

// buildEnabled is set only by an explicit portico_cb7 build. Merging the
// custody foundation or the exposure adapters never enables the format.
func buildEnabled() bool { return true }

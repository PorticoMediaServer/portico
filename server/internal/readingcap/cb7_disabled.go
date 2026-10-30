//go:build !portico_cb7

package readingcap

// buildEnabled is false for every ordinary build; CB7 surface stays dark.
func buildEnabled() bool { return false }

package httpapi

import "testing"

// Keep session-expiry expectations in the short suite as well as the full
// restriction matrix, which only runs at night.
func TestReadingRestrictionSessionExpiry(t *testing.T) {
	t.Run("search", readingBodySearchRestrictionCheck)
	t.Run("member-session", readingRestrictionCheck("POST /v1/reading/sessions"))
}

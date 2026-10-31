package httpapi

import "strings"

// pinReentryRoute is what a session whose profile PIN changed may still ask
// for: who it is, the account's profiles, choosing a profile (which is how the
// PIN is entered again) and signing out. Everything else answers
// profile_pin_reentry until a profile is chosen.
func pinReentryRoute(method, path string) bool {
	switch {
	case method == "GET" && (path == "/v1/me" || path == "/v1/direct" || path == "/v1/system"):
		return true
	case method == "DELETE" && path == "/v1/sessions/current":
		return true
	case method == "POST" && strings.HasPrefix(path, "/v1/direct/profiles/") && strings.HasSuffix(path, "/select"):
		return strings.Count(strings.TrimSuffix(strings.TrimPrefix(path, "/v1/direct/profiles/"), "/select"), "/") == 0
	}
	return false
}

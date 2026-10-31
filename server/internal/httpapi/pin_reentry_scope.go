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

// runningPlaybackRoute is a request on a playback that has already started: its
// session (control, observation, chapters, receivers, stop), the media its
// grant hands out, its subtitle plan and subtitle fonts. Starting a playback (POST /v1/playback/sessions) is not one.
func runningPlaybackRoute(method, path string) bool {
	switch {
	case strings.HasPrefix(path, "/v1/playback/sessions/"):
		return strings.TrimPrefix(path, "/v1/playback/sessions/") != ""
	case strings.HasPrefix(path, "/v1/media/"):
		return (method == "GET" || method == "HEAD") && strings.TrimPrefix(path, "/v1/media/") != ""
	case strings.HasPrefix(path, "/v1/items/"):
		// What belongs to a playback of a title (its subtitle plan), not the title.
		return method == "GET" && strings.Contains(path, "/playback/")
	case strings.HasPrefix(path, "/v1/subtitle-fonts/"):
		return method == "GET"
	}
	return false
}

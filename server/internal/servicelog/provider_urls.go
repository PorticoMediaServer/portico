package servicelog

import (
	"net/url"
	"regexp"
	"strings"
)

var providerLogURL = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)

// Preserve the address and resource that explain a failed request, but remove
// credentials in known provider positions, even when too short for longSecret.
func redactProviderURL(raw string) string {
	authorityStart := strings.Index(raw, "://") + 3
	authorityEnd := len(raw)
	if end := strings.IndexAny(raw[authorityStart:], "/?#"); end >= 0 {
		authorityEnd = authorityStart + end
	}
	host := ""
	if u, err := url.Parse(raw[:authorityEnd]); err == nil {
		host = strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	}
	metadata := host == "api.themoviedb.org" || host == "api4.thetvdb.com" || host == "api.acoustid.org" || host == "api.opensubtitles.com" || strings.HasSuffix(host, ".opensubtitles.com")
	// Parse the path and query separately: malformed escaping in a credential
	// must not cause URL parsing to skip redaction of the whole address.
	tail, fragment, hasFragment := strings.Cut(raw[authorityEnd:], "#")
	path, query, hasQuery := strings.Cut(tail, "?")
	segments := strings.Split(path, "/")
	xtream := false
	if i := len(segments) - 4; i >= 1 && !metadata && xtreamStreamResource(segments[i+3]) {
		route, _ := url.PathUnescape(segments[i])
		switch strings.ToLower(route) {
		case "live", "movie", "series":
			// Xtream uses /<kind>/<username>/<password>/<stream>, possibly
			// below a configured base path (livechannels.SourceFetcher).
			segments[i+1], segments[i+2] = "[REDACTED]", "[REDACTED]"
			xtream = true
		}
	}
	endpoint, _ := url.PathUnescape(segments[len(segments)-1])
	switch strings.ToLower(endpoint) {
	case "player_api.php", "xmltv.php", "get.php":
		xtream = true
	}
	// Generic credential names are secret on any host, including private update
	// feeds. Provider-specific usernames/client keys stay scoped to their format.
	if hasQuery {
		fields := strings.Split(query, "&")
		for i, field := range fields {
			key, _, hasValue := strings.Cut(field, "=")
			if !hasValue {
				continue
			}
			name, err := url.QueryUnescape(key)
			if err != nil {
				continue
			}
			name = strings.ToLower(name)
			secret := xtream && (name == "username" || name == "password") || host == "api.acoustid.org" && name == "client"
			switch strings.ReplaceAll(strings.ReplaceAll(name, "_", ""), "-", "") {
			case "grant", "token", "key", "credential", "apikey", "accesstoken", "refreshtoken", "devicecode", "password", "secret", "signature":
				secret = true
			}
			if secret {
				fields[i] = key + "=[REDACTED]"
			}
		}
		query = strings.Join(fields, "&")
	}
	clean := raw[:authorityEnd] + strings.Join(segments, "/")
	if hasQuery {
		clean += "?" + query
	}
	if hasFragment {
		clean += "#" + fragment
	}
	return clean
}

func xtreamStreamResource(raw string) bool {
	id, _, _ := strings.Cut(raw, ".")
	id, err := url.PathUnescape(id)
	if err != nil {
		return false
	}
	if id == "" {
		return false
	}
	for _, char := range id {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

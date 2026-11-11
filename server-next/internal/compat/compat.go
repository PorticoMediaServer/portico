// Package compat is the part of the Jellyfin-compatible routes that does not touch the
// server: which operation an address names, where a tool put its key, and the ids tools see.
// The routes serve companion tools (Sonarr, Radarr, Bazarr, Seerr, Home Assistant and the
// like). They are a fixed list of 30 operations, kept and never expanded; nothing can be
// played through them.
package compat

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
)

// Version is the Jellyfin version these routes answer as, so tools' version checks pass.
// Portico's own build is never shown before sign-in.
const Version = "10.10.0"

// The operations, by name. The handler switches on these.
const (
	PublicInfo     = "public_info"
	Info           = "info"
	Configuration  = "configuration"
	Authenticate   = "authenticate"
	MintKey        = "mint_key"
	Keys           = "keys"
	Users          = "users"
	User           = "user"
	UserImage      = "user_image"
	DisplayPrefs   = "display_preferences"
	Views          = "views"
	MediaFolders   = "media_folders"
	VirtualFolders = "virtual_folders"
	UserItems      = "user_items"
	UserLatest     = "user_latest"
	UserItem       = "user_item"
	ItemsList      = "items"
	Latest         = "latest"
	PlaybackInfo   = "playback_info"
	Image          = "image"
	IndexedImage   = "indexed_image"
	Seasons        = "seasons"
	Episodes       = "episodes"
	MediaUpdated   = "media_updated"
	LibraryRefresh = "library_refresh"
	ItemRefresh    = "item_refresh"
	NotifyAdmins   = "notify_admins"
	Sessions       = "sessions"
	SessionCommand = "session_command"
	Plugins        = "plugins"
)

type route struct {
	method string
	parts  []string // lower case; "{}" is a value
	op     string
}

// routes are the 30 operations the old server registered, as tools send them.
var routes = func() []route {
	var out []route
	for _, r := range []struct{ method, path, op string }{
		{"GET", "/System/Info/Public", PublicInfo},
		{"GET", "/System/Info", Info},
		{"GET", "/System/Configuration", Configuration},
		{"POST", "/Users/AuthenticateByName", Authenticate},
		{"POST", "/Auth/Keys", MintKey},
		{"GET", "/Auth/Keys", Keys},
		{"GET", "/Users", Users},
		{"GET", "/Users/{}", User},
		{"GET", "/Users/{}/Images/Primary", UserImage},
		{"GET", "/DisplayPreferences/usersettings", DisplayPrefs},
		{"GET", "/Users/{}/Views", Views},
		{"GET", "/Library/MediaFolders", MediaFolders},
		{"GET", "/Library/VirtualFolders", VirtualFolders},
		{"GET", "/Users/{}/Items", UserItems},
		{"GET", "/Users/{}/Items/Latest", UserLatest},
		{"GET", "/Users/{}/Items/{}", UserItem},
		{"GET", "/Items", ItemsList},
		{"GET", "/Items/Latest", Latest},
		{"GET", "/Items/{}/PlaybackInfo", PlaybackInfo},
		{"GET", "/Items/{}/Images/{}", Image},
		{"GET", "/Items/{}/Images/{}/{}", IndexedImage},
		{"GET", "/Shows/{}/Seasons", Seasons},
		{"GET", "/Shows/{}/Episodes", Episodes},
		{"POST", "/Library/Media/Updated", MediaUpdated},
		{"POST", "/Library/Refresh", LibraryRefresh},
		{"POST", "/Items/{}/Refresh", ItemRefresh},
		{"POST", "/Notifications/Admin", NotifyAdmins},
		{"GET", "/Sessions", Sessions},
		{"POST", "/Sessions/{}/Playing/{}", SessionCommand},
		{"GET", "/Plugins", Plugins},
	} {
		out = append(out, route{r.method, strings.Split(strings.ToLower(strings.Trim(r.path, "/")), "/"), r.op})
	}
	return out
}()

// Match names the operation a request asks for, with the values in its path in order. An
// address matches whatever its capitals, with or without the /mediabrowser prefix and a
// trailing slash. ok is false for an address that is not one of the 30.
func Match(method, path string) (op string, values []string, ok bool) {
	trimmed := strings.Trim(path, "/")
	if len(trimmed) >= len("mediabrowser") && strings.EqualFold(trimmed[:len("mediabrowser")], "mediabrowser") &&
		(len(trimmed) == len("mediabrowser") || trimmed[len("mediabrowser")] == '/') {
		trimmed = strings.TrimPrefix(trimmed[len("mediabrowser"):], "/")
	}
	parts := strings.Split(trimmed, "/")
	pathMatched := false
	for _, r := range routes {
		if len(r.parts) != len(parts) {
			continue
		}
		var got []string
		matched := true
		for i, want := range r.parts {
			switch {
			case want == "{}" && parts[i] != "":
				got = append(got, parts[i])
			case !strings.EqualFold(want, parts[i]):
				matched = false
			}
			if !matched {
				break
			}
		}
		// A literal part wins over a value: /Users/AuthenticateByName is not /Users/{id}.
		if matched && r.method == method {
			if op == "" || literals(r) > literals(routeOf(op)) {
				op, values = r.op, got
			}
		}
		pathMatched = pathMatched || matched
	}
	return op, values, op != "" || pathMatched
}

func literals(r route) int {
	n := 0
	for _, p := range r.parts {
		if p != "{}" {
			n++
		}
	}
	return n
}

func routeOf(op string) route {
	for _, r := range routes {
		if r.op == op {
			return r
		}
	}
	return route{}
}

// Token is the key a tool sent, from wherever these tools send one: "Authorization: Bearer",
// "Authorization: MediaBrowser Token=…", X-Emby-Token, X-MediaBrowser-Token,
// X-Emby-Authorization, or api_key or apikey in the address. Two different keys in one
// request is ambiguous and gives ok false.
func Token(r *http.Request) (token string, ok bool) {
	var found []string
	for _, h := range append(r.Header.Values("Authorization"), r.Header.Values("X-Emby-Authorization")...) {
		if bearer, isBearer := strings.CutPrefix(h, "Bearer "); isBearer {
			found = append(found, strings.TrimSpace(bearer))
		} else if t := mediaBrowserToken(h); t != "" {
			found = append(found, t)
		}
	}
	found = append(found, r.Header.Values("X-Emby-Token")...)
	found = append(found, r.Header.Values("X-MediaBrowser-Token")...)
	for key, values := range r.URL.Query() {
		if strings.EqualFold(key, "api_key") || strings.EqualFold(key, "apikey") {
			found = append(found, values...)
		}
	}
	for _, t := range found {
		if t == "" || len(t) > 256 || (token != "" && t != token) {
			return "", false
		}
		token = t
	}
	return token, true
}

// mediaBrowserToken reads Token="…" from a header such as
// `MediaBrowser Client="Sonarr", Device="x", DeviceId="y", Version="4", Token="…"`.
func mediaBrowserToken(h string) string {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(h), " ")
	if !ok || (!strings.EqualFold(scheme, "MediaBrowser") && !strings.EqualFold(scheme, "Emby")) {
		return ""
	}
	for _, field := range strings.Split(rest, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if ok && strings.EqualFold(strings.TrimSpace(name), "token") {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

// Ids. A tool sees a 32-digit hexadecimal id, as Jellyfin's are. It is worked out from
// Portico's own id, so nothing is stored and it never changes, across a restart, an upgrade
// or a restore: the first byte names the kind, the next ten are the id's random part.

var idEncoding = base32.NewEncoding("0123456789abcdefghjkmnpqrstvwxyz").WithPadding(base32.NoPadding)

var prefixes = []string{"", "itm", "lib", "prf", "usr", "pbs", "key"}

// ID turns a Portico id into the id a tool sees, or "" for one it cannot.
func ID(portico string) string {
	prefix, random, ok := strings.Cut(portico, "_")
	kind := slices.Index(prefixes, prefix)
	raw, err := idEncoding.DecodeString(random)
	if !ok || kind <= 0 || err != nil || len(raw) == 0 || len(raw) > 14 {
		return ""
	}
	var out [16]byte
	out[0] = byte(kind)
	out[15] = byte(len(raw))
	copy(out[1:], raw)
	return hex.EncodeToString(out[:])
}

// ServerID is the id a tool sees for the server itself. It is never turned back.
func ServerID(portico string) string {
	sum := sha256.Sum256([]byte(portico))
	return hex.EncodeToString(sum[:16])
}

// PorticoID turns a tool's id back into Portico's, or "" for one that is not ours. Ids with
// dashes, as some tools write them, are accepted.
func PorticoID(id string) string {
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(raw) != 16 || raw[0] == 0 || int(raw[0]) >= len(prefixes) || raw[15] == 0 || raw[15] > 14 {
		return ""
	}
	return prefixes[raw[0]] + "_" + idEncoding.EncodeToString(raw[1:1+raw[15]])
}

// Ticks are Jellyfin's unit of time: a ten-millionth of a second.
func Ticks(ms int64) int64 { return ms * 10_000 }

// Kind is the Jellyfin type for a Portico kind of title.
func Kind(portico string) string {
	if k, ok := map[string]string{"movie": "Movie", "show": "Series", "season": "Season", "episode": "Episode", "artist": "MusicArtist",
		"album": "MusicAlbum", "track": "Audio", "song": "Audio", "book": "Book", "audiobook": "AudioBook", "collection": "BoxSet"}[portico]; ok {
		return k
	}
	return "BaseItem"
}

// PorticoKinds are the Portico kinds for a list of Jellyfin types, as IncludeItemTypes sends
// them. A type Portico has no kind for is left out.
func PorticoKinds(types string) []string {
	var out []string
	for _, t := range strings.Split(types, ",") {
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "movie":
			out = append(out, "movie")
		case "series":
			out = append(out, "show")
		case "season":
			out = append(out, "season")
		case "episode":
			out = append(out, "episode")
		case "musicartist":
			out = append(out, "artist")
		case "musicalbum":
			out = append(out, "album")
		case "audio":
			out = append(out, "track")
		case "book":
			out = append(out, "book")
		}
	}
	return out
}

// CollectionType is the Jellyfin collection type for a Portico library kind.
func CollectionType(kind string) string {
	if t, ok := map[string]string{"movies": "movies", "movie": "movies", "shows": "tvshows", "show": "tvshows", "tv": "tvshows", "music": "music",
		"books": "books", "audiobooks": "books"}[kind]; ok {
		return t
	}
	return "mixed"
}

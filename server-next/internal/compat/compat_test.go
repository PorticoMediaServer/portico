package compat

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// What each companion tool is known to ask, as its own code writes the address (from the
// old server's notes on each tool; none has been tried against the real tool yet). Every
// one must reach its operation whatever its capitals, with or without /mediabrowser.
func TestToolAddresses(t *testing.T) {
	for _, c := range []struct{ tool, method, path, op string }{
		{"every tool", "GET", "/System/Info/Public", PublicInfo},
		{"Sonarr and Radarr", "GET", "/mediabrowser/System/Info", Info},
		{"Sonarr and Radarr", "POST", "/mediabrowser/Library/Media/Updated", MediaUpdated},
		{"Sonarr and Radarr", "POST", "/mediabrowser/Notifications/Admin", NotifyAdmins},
		{"Lidarr", "POST", "/mediabrowser/Library/Refresh", LibraryRefresh},
		{"Bazarr", "GET", "/Items", ItemsList},
		{"Bazarr", "GET", "/Shows/0a1b/Episodes", Episodes},
		{"Bazarr", "GET", "/Shows/0a1b/Seasons", Seasons},
		{"Bazarr", "POST", "/Items/0a1b/Refresh", ItemRefresh},
		{"Seerr", "POST", "/Users/AuthenticateByName", Authenticate},
		{"Seerr", "POST", "/Auth/Keys", MintKey},
		{"Seerr", "GET", "/Auth/Keys", Keys},
		{"Seerr", "GET", "/Users/Me", User},
		{"Seerr", "GET", "/Users", Users},
		{"Seerr", "GET", "/Library/MediaFolders", MediaFolders},
		{"Seerr", "GET", "/Library/VirtualFolders", VirtualFolders},
		{"Seerr", "GET", "/Users/0a1b/Items", UserItems},
		{"Seerr", "GET", "/Users/0a1b/Items/Latest", UserLatest},
		{"Seerr", "GET", "/Users/0a1b/Items/0c2d", UserItem},
		{"Seerr", "GET", "/DisplayPreferences/usersettings", DisplayPrefs},
		{"Seerr", "GET", "/Users/0a1b/Images/Primary", UserImage},
		{"Home Assistant", "GET", "/Sessions", Sessions},
		{"Home Assistant", "GET", "/Users/0a1b/Views", Views},
		{"Home Assistant", "POST", "/Sessions/0a1b/Playing/Stop", SessionCommand},
		{"Jellystat", "GET", "/Items/0a1b/playbackinfo", PlaybackInfo},
		{"Jellystat", "GET", "/Items/Latest", Latest},
		{"Jellystat", "GET", "/Plugins", Plugins},
		{"Jellystat", "GET", "/Items/0a1b/Images/Primary", Image},
		{"any", "GET", "/Items/0a1b/Images/Backdrop/0", IndexedImage},
		{"any", "GET", "/System/Configuration", Configuration},
	} {
		bare := strings.TrimPrefix(c.path, "/mediabrowser")
		for _, path := range []string{c.path, bare, "/MediaBrowser" + bare, lower(c.path), upper(c.path) + "/"} {
			if op, _, _ := Match(c.method, path); op != c.op {
				t.Errorf("%s: %s %s matched %q, want %q", c.tool, c.method, path, op, c.op)
			}
		}
	}
	for _, path := range []string{"/Videos/0a1b/stream", "/socket", "/Sessions/Playing/Progress", "/mediabrowserx/System/Info", "/v1/system"} {
		if op, _, ok := Match("GET", path); op != "" || ok {
			t.Errorf("%s matched %q: nothing is added to the 30 operations", path, op)
		}
	}
	if op, values, _ := Match("POST", "/sessions/AB12/playing/Seek"); op != SessionCommand || values[0] != "AB12" || values[1] != "Seek" {
		t.Errorf("a session command kept %v", values)
	}
}

func lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'a' <= c && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// A tool's key is found wherever these tools put it; two different keys are refused.
func TestToolKeys(t *testing.T) {
	for _, c := range []struct{ header, value, query string }{
		{"Authorization", "Bearer pk_abc", ""},
		{"Authorization", `MediaBrowser Client="Sonarr", Device="Sonarr", DeviceId="1", Version="4", Token="pk_abc"`, ""},
		{"X-Emby-Authorization", `MediaBrowser Client="Seerr", Token="pk_abc"`, ""},
		{"X-Emby-Token", "pk_abc", ""},
		{"X-MediaBrowser-Token", "pk_abc", ""},
		{"", "", "?api_key=pk_abc"},
		{"", "", "?ApiKey=pk_abc"},
	} {
		r := httptest.NewRequest("GET", "/System/Info"+c.query, nil)
		if c.header != "" {
			r.Header.Set(c.header, c.value)
		}
		if token, ok := Token(r); !ok || token != "pk_abc" {
			t.Errorf("%s %s%s gave %q", c.header, c.value, c.query, token)
		}
	}
	r := httptest.NewRequest("GET", "/System/Info?api_key=pk_other", nil)
	r.Header.Set("X-Emby-Token", "pk_abc")
	if _, ok := Token(r); ok {
		t.Error("two different keys in one request were accepted")
	}
}

// A tool keeps the ids it saw: they are worked out from Portico's, both ways.
func TestIDs(t *testing.T) {
	for _, id := range []string{"itm_5h2k9q0x7vbd3m1c", "lib_0000000000000000", "usr_zzzzzzzzzzzzzzzz", "pbs_abcdefghjkmnpqrs"} {
		guid := ID(id)
		if len(guid) != 32 || PorticoID(guid) != id || PorticoID(guid[:8]+"-"+guid[8:12]+"-"+guid[12:16]+"-"+guid[16:20]+"-"+guid[20:]) != id {
			t.Errorf("%s became %s and back %s", id, guid, PorticoID(guid))
		}
	}
	if PorticoID("00000000000000000000000000000000") != "" || PorticoID("not-a-guid") != "" {
		t.Error("an id that is not Portico's was turned into one")
	}
}

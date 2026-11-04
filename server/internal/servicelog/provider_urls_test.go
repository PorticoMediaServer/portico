package servicelog

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportsScrubProviderURLCredentials(t *testing.T) {
	for name, pair := range map[string][2]string{
		"xtream movie":          {"https://provider.invalid/movie/private-user/short-private-pass/7.mp4", "https://provider.invalid/movie/[REDACTED]/[REDACTED]/7.mp4"},
		"xtream series":         {"https://provider.invalid/series/private-user/short-private-pass/7.mkv", "https://provider.invalid/series/[REDACTED]/[REDACTED]/7.mkv"},
		"xtream playlist query": {"https://provider.invalid/get.php?username=private-user&password=short-pass&type=m3u_plus", "https://provider.invalid/get.php?username=[REDACTED]&password=[REDACTED]&type=m3u_plus"},
		"xtream live":           {"https://provider.invalid/live/private-user/short-private-pass/7.ts", "https://provider.invalid/live/[REDACTED]/[REDACTED]/7.ts"},
		"xtream base path and escaped credentials": {"http://provider.invalid:8080/iptv/live/u%2Fname/p%3Fass/7.ts", "http://provider.invalid:8080/iptv/live/[REDACTED]/[REDACTED]/7.ts"},
		"xtream escaped route":                     {"https://provider.invalid/%6cive/private-user/short-private-pass/7.ts", "https://provider.invalid/%6cive/[REDACTED]/[REDACTED]/7.ts"},
		"xtream malformed credential escape":       {"https://provider.invalid/live/private-user/pass%zz/7.ts", "https://provider.invalid/live/[REDACTED]/[REDACTED]/7.ts"},
		"xtream malformed resource escape":         {"https://provider.invalid/live/private-user/short-pass/7.ts%zz", "https://provider.invalid/live/[REDACTED]/[REDACTED]/7.ts%zz"},
		"xtream query":                             {"https://provider.invalid/iptv/player_api.php?username=private-user&password=short-pass&action=get_live_streams", "https://provider.invalid/iptv/player_api.php?username=[REDACTED]&password=[REDACTED]&action=get_live_streams"},
		"xtream escaped repeated query":            {"https://provider.invalid/xmltv.php?%75sername=private-user&USERNAME=another-user&%70assword=short-pass", "https://provider.invalid/xmltv.php?%75sername=[REDACTED]&USERNAME=[REDACTED]&%70assword=[REDACTED]"},
		"acoustid client key":                      {"https://api.acoustid.org/v2/lookup?client=short-client&duration=42", "https://api.acoustid.org/v2/lookup?client=[REDACTED]&duration=42"},
		"tmdb escaped key":                         {"https://API.THEMOVIEDB.ORG:443/3/movie/603?api%5fkey=short-key&language=en", "https://API.THEMOVIEDB.ORG:443/3/movie/603?api%5fkey=[REDACTED]&language=en"},
	} {
		t.Run(name, func(t *testing.T) {
			input := `Get "` + pair[0] + `": provider_unavailable (503)`
			want := `Get "` + pair[1] + `": provider_unavailable (503)`
			if got := redactClientLog(input); got != want {
				t.Fatalf("provider diagnostic:\n got %s\nwant %s", got, want)
			}
			if got := redactClientLog(want); got != want {
				t.Fatalf("redaction must be idempotent: %s", got)
			}
		})
	}
}

func TestReportsPreservePublicProviderDiagnostics(t *testing.T) {
	for _, input := range []string{
		`Get "https://provider.invalid/live/chunk/7.ts": status 503`,
		`https://provider.invalid/live/index.m3u8?language=en`,
		`https://provider.invalid/live/news/sports/master.m3u8`,
		`https://api.themoviedb.org/3/movie/603/images/backdrops`,
		`https://api.themoviedb.org/3/movie/603/credits/7`,
		`https://api.themoviedb.org/3/movie/603?language=en&page=2`,
		`https://api.acoustid.org/v2/lookup?duration=42&format=json`,
		`https://api.acoustid.org.evil.invalid/v2/lookup?client=public-client`,
		`https://example.invalid/articles?username=public-author&client=web`,
		`/home/another-account/private/Family Video.mkv`,
	} {
		if got := redactClientLog(input); got != input {
			t.Fatalf("ordinary diagnostics changed:\n got %s\nwant %s", got, input)
		}
	}
}

func TestPreviousRunProviderURLsAreRedactedWhenRead(t *testing.T) {
	directory := t.TempDir()
	const message = "provider_unavailable https://provider.invalid/live/private-user/short-pass/7.ts"
	line := time.Now().UTC().Format(time.RFC3339Nano) + "\tinfo\tserver\t" + message + "\n"
	if err := os.WriteFile(filepath.Join(directory, "messages.log"), []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	r := New(Options{Directory: directory})
	defer r.Close()
	page, err := r.Read(Query{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("previous run: %+v %v", page, err)
	}
	if got, want := page.Items[0].Message, "provider_unavailable https://provider.invalid/live/[REDACTED]/[REDACTED]/7.ts"; got != want {
		t.Fatalf("previous run exposes credentials: got %s want %s", got, want)
	}
}

func TestProviderURLCredentialsDoNotReachLogCopies(t *testing.T) {
	directory := t.TempDir()
	r := New(Options{Directory: directory, Capacity: 10})
	defer r.Close()
	feed, unsubscribe := r.Subscribe("info")
	defer unsubscribe()
	var forwarded bytes.Buffer
	const input = "provider_unavailable https://provider.invalid/live/private-user/short-private-pass/7.ts\n"
	if n, err := io.WriteString(r.Tee(&forwarded), input); err != nil || n != len(input) {
		t.Fatalf("write: %d %v", n, err)
	}
	page, err := r.Read(Query{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("recorded log: %+v %v", page, err)
	}
	var event Record
	select {
	case event = <-feed:
	default:
		t.Fatal("live log event missing")
	}
	file, err := os.ReadFile(filepath.Join(directory, "messages.log"))
	if err != nil {
		t.Fatal(err)
	}
	for name, copy := range map[string]string{"forwarded": forwarded.String(), "ring": page.Items[0].Message, "live event": event.Message, "file": string(file)} {
		if strings.Contains(copy, "private-user") || strings.Contains(copy, "short-private-pass") || !strings.Contains(copy, "/live/[REDACTED]/[REDACTED]/7.ts") {
			t.Errorf("%s copy exposes credentials or loses the diagnostic: %s", name, copy)
		}
	}
}

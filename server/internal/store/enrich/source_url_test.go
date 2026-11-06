package enrich

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"unsafe"
)

func TestSafeTMDBSourceURLCanonicalAuthority(t *testing.T) {
	const want = "https://api.themoviedb.org/3/movie/550"
	for _, raw := range []string{
		want,
		"https://API.THEMOVIEDB.ORG/3/movie/550",
		"https://api.themoviedb.org:443/3/movie/550",
		"https://api.themoviedb.org:/3/movie/550",
		"https://API.THEMOVIEDB.ORG:443/3/movie/550?api_key=secret#secret",
	} {
		t.Run(raw, func(t *testing.T) {
			got, err := SafeTMDBSourceURL(raw, "550")
			if err != nil || got != want {
				t.Fatalf("noncanonical authority: got %q, want %q, err %v", got, want, err)
			}
		})
	}
	// A trailing dot is outside the closed hostname allowlist; it is not an
	// alternate spelling whose input authority is preserved in provenance.
	if got, err := SafeTMDBSourceURL("https://api.themoviedb.org./3/movie/550", "550"); !errors.Is(err, ErrSourceURL) || got != "" {
		t.Fatal("trailing-dot host admitted", got, err)
	}
}

func TestSafeTMDBSourceURLStripsEveryCredentialCarrier(t *testing.T) {
	const want = "https://api.themoviedb.org/3/movie/42"
	for _, input := range []string{
		want,
		want + "?api_key=secret-api-key&language=en-CA&append_to_response=credits",
		want + "?%61pi%5fkey=secret-api-key&ACCESS_TOKEN=bearer-token&unrecognized_credential=value",
		"https://user:password@api.themoviedb.org/3/movie/42?session_id=session-secret#access_token=fragment-secret",
		want + "?api_key=one&api_key=two;token=three&broken=%FF#api_key=fragment",
		"https://api.themoviedb.org/3/movie/%34%32?private_key=secret",
		want + "?",
	} {
		got, err := SafeTMDBSourceURL(input, "42")
		if err != nil || got != want {
			t.Fatal("unsafe source URL projection", err)
		}
		parsed, err := url.Parse(got)
		if err != nil || parsed.RawQuery != "" || parsed.User != nil || parsed.Fragment != "" || parsed.ForceQuery {
			t.Fatal("credential carrier retained", err)
		}
	}
}

func TestSafeTMDBSourceURLRefusesOtherRoutesWithoutEcho(t *testing.T) {
	for _, input := range []string{
		"", "https://api.themoviedb.org/3/movie/43?api_key=secret",
		"http://api.themoviedb.org/3/movie/42?api_key=secret",
		"https://evil.invalid/3/movie/42?api_key=secret",
		"https://api.themoviedb.org.evil.invalid/3/movie/42?api_key=secret",
		"https://api.themoviedb.org:444/3/movie/42?api_key=secret",
		"https://api.themoviedb.org/3/movie/42/secret?api_key=secret",
		"https://api.themoviedb.org/3/movie/42%3Fapi_key=secret",
		"https://api.themoviedb.org/3/movie/%ZZ?api_key=secret",
		"https:opaque-secret",
	} {
		got, err := SafeTMDBSourceURL(input, "42")
		if got != "" || !errors.Is(err, ErrSourceURL) || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid/credential-bearing source escaped", err)
		}
	}
	for _, id := range []string{"0", "-1", "0042", "42?api_key=secret", "9223372036854775808"} {
		if got, err := SafeTMDBSourceURL("https://api.themoviedb.org/3/movie/42", id); got != "" || !errors.Is(err, ErrSourceURL) {
			t.Fatal("invalid native provider ID", err)
		}
	}
}

func TestSafeTMDBSourceURLOwnsOnlySafeBytes(t *testing.T) {
	const prefix = "https://api.themoviedb.org/3/movie/42"
	backing := prefix + "?api_key=" + strings.Repeat("secret", 1<<16)
	input := backing[:len(prefix)]
	got, err := SafeTMDBSourceURL(input, "42")
	if err != nil || got != prefix || unsafe.StringData(got) == unsafe.StringData(input) {
		t.Fatal("retained acquisition URL backing", err)
	}
}

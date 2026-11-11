package remote

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
)

// Review desk A, for #2815. No test here opens a connection: names are resolved by a stand-in.

func resolveAll(t *testing.T, names map[string][]string) {
	before := lookupIP
	lookupIP = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		var out []net.IPAddr
		for _, a := range names[host] {
			out = append(out, net.IPAddr{IP: net.ParseIP(a)})
		}
		if out == nil {
			return nil, errors.New("no such host")
		}
		return out, nil
	}
	t.Cleanup(func() { lookupIP = before })
}

// A source on the internet gives its own name a second, private address beside its real one.
// Its reads still go to the public address, but the redirect rule now takes the source for a
// home one and follows its redirect to this computer.
func TestReviewAASecondPrivateAddressDoesNotMakeAPublicSourceAHomeOne(t *testing.T) {
	resolveAll(t, map[string][]string{"files.example": {"203.0.113.10", "10.0.0.1"}})
	from, _ := url.Parse("https://files.example/dav/film.mkv")
	to, _ := url.Parse("http://127.0.0.1:32500/v1/system")
	if err := checkRedirect(to, []*url.URL{from}, context.Background()); !errors.Is(err, ErrRedirectInward) {
		t.Fatalf("a source with a public and a private address redirected to this computer: %v, want it refused", err)
	}
}

// A NAS at home, named the way home networks name things: the name answers with the NAS's
// link-local IPv6 address as well as its ordinary one (mDNS and many routers do this). It is
// an ordinary home source and must not be refused as a cloud machine's metadata address.
func TestReviewAAHomeNASWhoseNameAlsoHasALinkLocalIPv6AddressIsNotRefused(t *testing.T) {
	resolveAll(t, map[string][]string{"nas.local": {"fe80::211:32ff:fe12:3456", "192.168.1.20"}})
	if err := checkRcloneHost(context.Background(), map[string]string{"type": "sftp", "host": "nas.local"}); err != nil {
		t.Fatalf("an sftp remote on a home NAS: %v", err)
	}
}

// The host of most kinds of storage is not one of the five plain host options: an
// S3-compatible remote's endpoint, and the sign-in addresses of the OAuth kinds, are options
// too, and name the metadata address as easily.
func TestReviewAAnEndpointOptionIsHeldToTheSameRule(t *testing.T) {
	for _, paste := range []string{
		"[a]\ntype = s3\nprovider = Other\naccess_key_id = k\nsecret_access_key = s\nendpoint = http://169.254.169.254\n",
		"[a]\ntype = swift\nuser = u\nkey = k\nauth = http://169.254.169.254/v3\n",
		"[a]\ntype = dropbox\nclient_id = c\nclient_secret = s\ntoken = {\"access_token\":\"x\"}\ntoken_url = http://metadata.google.internal/token\n",
	} {
		if _, err := ParseRcloneConfig(paste); !errors.Is(err, ErrAddressRefused) {
			t.Errorf("%q: %v, want the address refused", paste, err)
		}
	}
}

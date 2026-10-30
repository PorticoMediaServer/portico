package enrichment

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

type datasetResolver func(context.Context, string, string) ([]netip.Addr, error)

func (f datasetResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return f(ctx, network, host)
}

type datasetRoundTrip func(*http.Request) (*http.Response, error)

func (f datasetRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func publicDatasetResolver(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
}

// The existing signed-publisher/import tests still exercise the full request,
// signature, hash, delta and publication path. Only the external transport is
// replaced, so they need neither a private-origin exception nor live network.
func publisherService(db *sql.DB, publisher *publisher, dir string) *Service {
	s := New(db, Config{ManifestURL: "https://dataset.example/manifest.json", PublicKey: publisher.public, Dir: dir})
	s.resolver = datasetResolver(publicDatasetResolver)
	s.cfg.Client.Transport = datasetRoundTrip(func(request *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		publisher.ServeHTTP(recorder, request)
		response := recorder.Result()
		response.Request = request
		return response, nil
	})
	return s
}

func TestDatasetManifestSignaturePreservesEscapedPathAndQuery(t *testing.T) {
	for _, path := range []string{"/releases%2F2026/manifest.json", "/releases%2f2026/manifest%2ejson"} {
		t.Run(path, func(t *testing.T) {
			publisher := newPublisher(t)
			body, err := json.Marshal(Manifest{SchemaVersion: SchemaVersion, Domains: map[string]ManifestDomain{}})
			if err != nil {
				t.Fatal(err)
			}
			signature := base64.StdEncoding.EncodeToString(ed25519.Sign(publisher.key, body))
			s := New(nil, Config{ManifestURL: "https://dataset.example" + path + "?token=fixture", PublicKey: publisher.public})
			s.resolver = datasetResolver(publicDatasetResolver)
			requests := 0
			s.cfg.Client.Transport = datasetRoundTrip(func(request *http.Request) (*http.Response, error) {
				requests++
				var payload string
				switch request.URL.RequestURI() {
				case path + "?token=fixture":
					payload = string(body)
				case path + ".sig?token=fixture":
					payload = signature
				default:
					t.Fatalf("manifest path spelling or query changed: %s", request.URL.RequestURI())
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Header: http.Header{}, Request: request}, nil
			})
			manifest, err := s.FetchManifest(t.Context())
			if err != nil || manifest.SchemaVersion != SchemaVersion || requests != 2 {
				t.Fatal("escaped manifest did not verify", err, requests)
			}
		})
	}
}

func TestDatasetRedirectsAndAssetsRequireTheHTTPSPublicOrigin(t *testing.T) {
	for _, destination := range []string{
		"http://dataset.example/asset", "https://other.example/asset", "https://dataset.example:444/asset",
		"https://127.0.0.1/asset", "https://169.254.169.254/asset", "https://[64:ff9b::7f00:1]/asset",
		"https://user:password@dataset.example/asset", "https://dataset.example/asset#fragment",
	} {
		t.Run(destination, func(t *testing.T) {
			s := New(nil, Config{ManifestURL: "https://dataset.example/manifest.json", Dir: t.TempDir()})
			s.resolver = datasetResolver(publicDatasetResolver)
			requests := 0
			s.cfg.Client.Transport = datasetRoundTrip(func(request *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{destination}}, Body: http.NoBody, Request: request}, nil
			})
			if _, err := s.get(context.Background(), s.cfg.ManifestURL, 4096); err == nil || requests != 1 {
				t.Fatal("unsafe redirect reached a second request", err, requests)
			}
			// Independent signed HTTPS asset origins and ports remain valid;
			// the other forms fail before their first request too.
			if destination != "https://other.example/asset" && destination != "https://dataset.example:444/asset" {
				requests = 0
				if _, err := s.download(context.Background(), ManifestFile{URL: destination, Bytes: 1}); err == nil || requests != 0 {
					t.Fatal("unsafe signed asset URL reached transport", err, requests)
				}
			}
		})
	}
}

func TestDatasetRedirectRechecksAllDNSAnswersAndStripsReferer(t *testing.T) {
	for _, rebound := range []bool{false, true} {
		s := New(nil, Config{ManifestURL: "https://dataset.example/manifest.json?token=secret"})
		requests := 0
		s.resolver = datasetResolver(func(context.Context, string, string) ([]netip.Addr, error) {
			ips := []netip.Addr{netip.MustParseAddr("1.1.1.1")}
			if rebound && requests != 0 {
				ips = append(ips, netip.MustParseAddr("10.0.0.1"))
			}
			return ips, nil
		})
		s.cfg.Client.Transport = datasetRoundTrip(func(request *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"/release.json?token=second"}}, Body: http.NoBody, Request: request}, nil
			}
			if request.Header.Get("Referer") != "" {
				t.Fatal("signed URL was copied to a Referer")
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("release")), Header: http.Header{}, Request: request}, nil
		})
		raw, err := s.get(context.Background(), s.cfg.ManifestURL, 4096)
		if rebound {
			if err == nil || requests != 1 {
				t.Fatal("rebound redirect reached transport", err, requests)
			}
		} else if err != nil || string(raw) != "release" || requests != 2 {
			t.Fatal("ordinary same-origin HTTPS redirect failed", string(raw), err, requests)
		}
	}
}

func TestDatasetInitialAndDialAdmissionRefusePrivateAndTransitionAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "64:ff9b::a9fe:a9fe", "2002:7f00:1::", "2001::1"} {
		s := New(nil, Config{ManifestURL: "https://dataset.example/manifest.json"})
		s.resolver = datasetResolver(func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr(address)}, nil
		})
		s.cfg.Client.Transport = datasetRoundTrip(func(*http.Request) (*http.Response, error) {
			t.Fatal("forbidden initial destination reached transport")
			return nil, errors.New("unexpected transport")
		})
		if _, err := s.get(context.Background(), s.cfg.ManifestURL, 10); err == nil {
			t.Fatal("initial destination admitted", address)
		}
		if connection, err := s.dialDataset(context.Background(), "tcp", "dataset.example:443"); err == nil || connection != nil {
			t.Fatal("dial accepted a mixed public/private answer", address, err)
		}
	}
}

func TestDatasetDiagnosticsPersistOnlyBoundedTypedFacts(t *testing.T) {
	c := catalogtest.Open(t)
	s := New(c.DB, Config{})
	secretURL := "https://user:password@dataset.example/private-secret?key=query-secret#fragment-secret"
	for _, problem := range []error{
		datasetFailure("asset_integrity", secretURL),
		&url.Error{Op: "Get", URL: secretURL, Err: errors.New("provider-secret")},
		fmt.Errorf("wrapped %w", &url.Error{Op: "Get", URL: secretURL, Err: errors.New("provider-secret")}),
		errors.New(strings.Repeat("raw-secret", 4096)),
	} {
		if err := s.noteChecked(context.Background(), "screen", "", problem); err != nil {
			t.Fatal(err)
		}
		var stored string
		if err := c.DB.QueryRow(`SELECT error FROM enrichment_domains WHERE domain='screen'`).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if len(stored) > 384 || !strings.HasPrefix(stored, "title dataset: ") {
			t.Fatal("diagnostic is not bounded and typed", stored)
		}
		for _, secret := range []string{"user", "password", "private-secret", "query-secret", "fragment-secret", "provider-secret", "raw-secret"} {
			if strings.Contains(stored, secret) {
				t.Fatal("persistent diagnostic retained secret material", secret)
			}
		}
	}
}

func TestDatasetTransportErrorsAndOversizeBodiesAreSafe(t *testing.T) {
	s := New(nil, Config{ManifestURL: "https://dataset.example/manifest.json?key=query-secret"})
	s.resolver = datasetResolver(publicDatasetResolver)
	s.cfg.Client.Transport = datasetRoundTrip(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("failed %s with password=provider-secret", request.URL.String())
	})
	if _, err := s.get(context.Background(), s.cfg.ManifestURL, 4); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("request error retained credentials", err)
	}
	s.cfg.Client.Transport = datasetRoundTrip(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("oversized")), Header: http.Header{}, Request: request}, nil
	})
	if _, err := s.get(context.Background(), s.cfg.ManifestURL, 4); err == nil {
		t.Fatal("oversized body was silently truncated")
	}
}

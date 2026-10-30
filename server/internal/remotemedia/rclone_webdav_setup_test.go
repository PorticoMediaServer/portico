package remotemedia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestRcloneWebDAVSetupUsesRetainedConfigurationOnly(t *testing.T) {
	budget := NewOperationBudget(context.Background(), 1)
	hold, err := budget.AcquireTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	m, err := NewRcloneMetadata(12345, "private", "fixture-secret", hold.Operations(), "leaf:cipher")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var calls []string
	checks := 0
	m.endpoint.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.URL.Path)
		var in map[string]string
		if r.URL.Host != "127.0.0.1:12345" || r.Method != "POST" || r.Header.Get("Authorization") == "" || json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Fatal("lost private process binding")
		}
		var body string
		switch r.URL.Path {
		case "/config/get":
			if in["name"] != "leaf" || len(in) != 1 {
				t.Fatal("configuration scope changed")
			}
			body = `{"type":"webdav","url":"https://source.fixture/dav/","user":"fixture","pass":"YWFhYWFhYWFhYWFhYWFhYXMaGgIlEQ"}`
		case "/operations/fsinfo":
			if in["fs"] != "leaf:cipher" || len(in) != 1 {
				t.Fatal("filesystem scope changed")
			}
			body = `{"Name":"leaf","Root":"cipher"}`
		case "/options/get":
			if in["blocks"] != "main" || len(in) != 1 {
				t.Fatal("options scope changed")
			}
			body = `{"main":{"UserAgent":"rclone/fixture","InsecureSkipVerify":false,"Cookie":false}}`
		default:
			t.Fatal("arbitrary RC operation", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	d, err := m.SetupWebDAV(hold.Context(), hold, Policy{SourceEndpoints: true}, func(ctx context.Context) error { checks++; return ctx.Err() })
	if err != nil || d == nil {
		t.Fatal("exact saved configuration was not captured", err)
	}
	defer d.Close()
	if len(calls) != 3 || checks != 6 || d.dav.config.Root != "https://source.fixture/dav/cipher/" || d.dav.policy.UserAgent != "rclone/fixture" || d.headers.Get("Authorization") != "Basic Zml4dHVyZTpwb3RhdG8=" || d.headers.Get("Referer") != "https://source.fixture/dav/" {
		t.Fatal("saved request facts changed")
	}
	encoded, err := json.Marshal(d)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("private request facts escaped")
	}
	before := len(calls)
	if _, err = m.SetupWebDAV(hold.Context(), nil, Policy{SourceEndpoints: true}, func(ctx context.Context) error { return ctx.Err() }); !errors.Is(err, ErrPolicy) || len(calls) != before {
		t.Fatal("missing hold touched process")
	}
	d.Close()
	if len(d.headers) != 0 || d.dav.config.Root != "" {
		t.Fatal("closed setup retained request facts")
	}
}

func TestRcloneWebDAVSetupDoesNotApproximateSavedOptions(t *testing.T) {
	base := func() map[string]string {
		return map[string]string{"type": "webdav", "url": "https://source.fixture/dav/", "vendor": "other"}
	}
	for _, change := range []map[string]string{
		{"vendor": "sharepoint-ntlm"}, {"bearer_token_command": "fixture-command"}, {"unix_socket": "/private/socket"},
		{"encoding": "Slash,Dot"}, {"auth_redirect": "true"}, {"url": "https://source.fixture/dav/?token=private"},
		{"url": "https://user:private@source.fixture/dav/"}, {"pass": "not-obscured"}, {"headers": "Range,bytes=0-15"},
		{"headers": "Host,another.fixture"}, {"headers": "Authorization,secret\r\nHeader: value"},
	} {
		fields := base()
		for key, value := range change {
			fields[key] = value
		}
		d, err := resolvedWebDAVSetup(fields, "cipher", Policy{SourceEndpoints: true})
		if err != nil || d != nil {
			t.Fatal("unreproducible option was approximated")
		}
	}
	for _, root := range []string{"../cipher", "cipher//child", "/cipher", "cipher%2fchild", "cipher with space"} {
		if d, err := resolvedWebDAVSetup(base(), root, Policy{SourceEndpoints: true}); err != nil || d != nil {
			t.Fatal("raw root was reinterpreted")
		}
	}
	fields := base()
	fields["bearer_token"] = "saved-fixture-token"
	fields["headers"] = "Authorization,exact-saved-header,X-Fixture,value"
	d, err := resolvedWebDAVSetup(fields, "cipher", Policy{SourceEndpoints: true})
	if err != nil || d == nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.headers.Get("Authorization") != "exact-saved-header" || d.headers.Get("X-Fixture") != "value" {
		t.Fatal("saved header precedence changed")
	}
}

func TestRcloneWebDAVSetupUnknownNeverMakesAnOriginRequest(t *testing.T) {
	fields := map[string]string{"type": "webdav", "url": "https://source.fixture/"}
	d, err := resolvedWebDAVSetup(fields, "", Policy{SourceEndpoints: true})
	if err != nil || d == nil {
		t.Fatal(err)
	}
	defer d.Close()
	hold, err := NewOperationBudget(context.Background(), 1).AcquireTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	checks := 0
	support, err := d.Diagnose(hold.Context(), DAVEntry{Relative: "cipher%2fobject", Size: 64}, hold, 2, 0, func(ctx context.Context) error { checks++; return ctx.Err() })
	if err != nil || support != SetupRangeUnknown || checks != 0 {
		t.Fatal("unreproducible raw locator attempted I/O", support, err)
	}
}

func TestPublicDAVRequestStillRefusesSavedSetupHeaders(t *testing.T) {
	c := &Client{url: "https://source.fixture/", userAgent: "fixture"}
	if _, err := c.Request(context.Background(), "GET", nil, http.Header{"Authorization": {"private"}}); !errors.Is(err, ErrPolicy) {
		t.Fatal("public request acquired setup header authority", err)
	}
}

func TestRcloneWebDAVSetupSendsExactSavedRequest(t *testing.T) {
	requests := 0
	var origin string
	server := privateSetupServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" || r.URL.Path != "/dav/cipher/one.bin" || r.Header.Get("Authorization") != "Basic Zml4dHVyZTpwb3RhdG8=" || r.Header.Get("Referer") != origin+"/dav/" || r.UserAgent() != "rclone/fixture" || r.Header.Get("X-Fixture") != "saved" {
			t.Error("saved request changed")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start < 0 || end >= 64 || end-start >= 16 {
			t.Error("range bound changed")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/64", start, end))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, strings.Repeat("x", int(end-start+1)))
	}))
	origin = server.URL
	approval, err := (Policy{}).PrepareEndpointApproval(context.Background(), origin)
	if err != nil {
		t.Fatal(err)
	}
	d, err := resolvedWebDAVSetup(map[string]string{"type": "webdav", "url": origin + "/dav/", "user": "fixture", "pass": "YWFhYWFhYWFhYWFhYWFhYXMaGgIlEQ", "headers": "X-Fixture,saved"}, "cipher", Policy{SourceEndpoints: true, Approvals: []Approval{approval}, UserAgent: "rclone/fixture"})
	if err != nil || d == nil {
		t.Fatal(err)
	}
	defer d.Close()
	hold, err := NewOperationBudget(context.Background(), 1).AcquireTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	support, err := d.Diagnose(hold.Context(), DAVEntry{Relative: "one.bin", Size: 64}, hold, 50, 0, func(ctx context.Context) error { return ctx.Err() })
	if err != nil || support != SetupRangeSupported || requests != 3 {
		t.Fatal("exact saved request did not qualify", support, requests, err)
	}
}

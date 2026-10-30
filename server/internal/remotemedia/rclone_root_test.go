package remotemedia

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRcloneConnectionRootRequiresBackendDirectoryCheckWithoutObjectReads(t *testing.T) {
	budget := NewOperationBudget(context.Background(), 1)
	m, err := NewRcloneMetadata(12345, "fixture", "secret", budget, "leaf:base")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	calls := 0
	m.endpoint.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
		var input struct {
			FS, Remote string
			Opt        map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/operations/stat" || input.FS != "leaf:base" || input.Remote != "" || input.Opt["dirsOnly"] != true || input.Opt["showHash"] != false || input.Opt["noModTime"] != true || input.Opt["noMimeType"] != true {
			t.Fatal("not a closed directory connection test", r.URL.Path, input)
		}
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"item":{"Path":"","IsDir":true}}`))}, nil
	})
	if err = m.CheckRoot(context.Background(), func(ctx context.Context) error { return ctx.Err() }); err != nil || calls != 1 || budget.Counts().Active != 0 {
		t.Fatal(calls, err)
	}
	m.endpoint.client.Transport = serveRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"item":{"Path":"","IsDir":false}}`))}, nil
	})
	if err = m.CheckRoot(context.Background(), func(ctx context.Context) error { return ctx.Err() }); err == nil {
		t.Fatal("file/root readiness became directory connection proof")
	}
}

func TestRcloneSetupRootReusesItsOnlyListingForRawDiagnosticFile(t *testing.T) {
	budget := NewOperationBudget(context.Background(), 1)
	m, err := NewRcloneMetadata(12345, "fixture", "secret", budget, "leaf:base")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	calls := 0
	m.endpoint.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
		var input struct {
			FS, Remote string
			Opt        map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if r.URL.Path != "/operations/list" || input.FS != "leaf:base" || input.Remote != "" || input.Opt["recurse"] != false || input.Opt["showHash"] != false || input.Opt["noModTime"] != true || input.Opt["noMimeType"] != true {
			t.Fatal("setup added a metadata request", r.URL.Path, input)
		}
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"list":[{"Path":"raw.bin","Size":64},{"Path":"unused.bin","Size":32}]}`))}, nil
	})
	file, err := m.CheckRootFile(context.Background(), func(ctx context.Context) error { return ctx.Err() })
	if err != nil || calls != 1 || file == nil || file.Relative != "raw.bin" || file.Size != 64 || budget.Counts().Active != 0 {
		t.Fatal("setup root facts changed", file, calls, err)
	}
}

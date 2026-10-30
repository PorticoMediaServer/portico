package remotemedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRcloneDirectoryMetadataReadsActualListWithKnownHashCost(t *testing.T) {
	for _, hashCost := range []string{"fast", "slow", "unknown"} {
		t.Run(hashCost, func(t *testing.T) {
			m, err := NewRcloneMetadata(12345, "private", "secret", NewOperationBudget(context.Background(), 2), "media:")
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			m.endpoint.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
				body := `{"Name":"media","Features":{}}`
				if r.URL.Path == "/operations/fsinfo" {
					if hashCost == "fast" {
						body = `{"Name":"media","Features":{"SlowHash":false}}`
					} else if hashCost == "slow" {
						body = `{"Name":"media","Features":{"SlowHash":true}}`
					}
				} else if r.URL.Path == "/operations/list" {
					raw, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(raw), `"recurse":false`) || !strings.Contains(string(raw), `"noMimeType":true`) || strings.Contains(string(raw), `"showHash":true`) != (hashCost == "fast") {
						t.Fatal("whole-file hash or recursion escaped policy", string(raw))
					}
					body = `{"list":[{"Path":"disc/clip.m2ts","Size":8,"ModTime":"2026-10-23T10:00:00Z","ID":"provider-object"}]}`
				} else {
					t.Fatal("unexpected RC operation", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			count := 0
			err = m.VisitDirectory(context.Background(), "disc", func(context.Context) error { return nil }, func(value RcloneFileMetadata) error {
				if value.Path != "disc/clip.m2ts" || value.Size != 8 || value.ID != "provider-object" || value.Modified.IsZero() {
					t.Fatal("directory facts changed", value)
				}
				count++
				return nil
			})
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
		})
	}
}

func TestRcloneDirectoryMetadataRefusesPartialOrUnconfinedRows(t *testing.T) {
	for _, body := range []string{
		`{"list":[{"Path":"disc/clip","Size":1}]`,
		`{"list":[{"Path":"other/clip","Size":1}]}`,
		`{"list":[{"Path":"disc/../clip","Size":1}]}`,
		`{"list":[{"Path":"disc/sub/clip","Size":1}]}`,
		`{"list":[{"Path":"disc/clip","Size":-1}]}`,
		`{"list":[{"Path":"disc/clip"}]}`,
		`{"list":[{"Path":"disc/clip","Size":1,"ID":"` + strings.Repeat("x", 128<<10) + `"}]}`,
		`{"list":[{"Path":"disc/clip","Size":1,"Hashes":{"MD5":"bad\nvalue"}}]}`,
		`{"list":[]} trailing`,
		`{"list":[],"list":[]}`,
	} {
		if err := visitDirectoryMetadata(context.Background(), strings.NewReader(body), "disc", func(RcloneFileMetadata) error { return nil }); err == nil {
			t.Fatal("invalid metadata published", len(body))
		}
	}
	refused := errors.New("private sink refused")
	if err := visitDirectoryMetadata(context.Background(), strings.NewReader(`{"list":[{"Path":"disc/clip","Size":1}]}`), "disc", func(RcloneFileMetadata) error { return refused }); !errors.Is(err, refused) {
		t.Fatal("private sink error became success", err)
	}
}

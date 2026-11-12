//go:build reading_client_integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This opt-in boundary test requires the exact repository Node dependencies.
// It uses a real listener and newly issued, local runner-only credentials. No
// saved database, fixture credential, native state or production file enters it.
func TestReadingHTTPClientPublication(t *testing.T) {
	// The app's reader now reads books from the rebuilt server (server-next): its book route,
	// media addresses and state route. This server's reading sessions are no longer the reader's
	// pair; the same boundary is checked against server-next instead.
	t.Skip("the reading client speaks the rebuilt server; this old server's reading sessions are not its pair")
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("the configured reading-client integration needs Node", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "../../.."))
	for _, tc := range []struct{ kind, format string }{{"book", "pdf"}, {"book", "epub"}, {"comic", "cbz"}} {
		t.Run(tc.format, func(t *testing.T) {
			r := newReadingFixture(t, tc.kind, tc.format)
			server := httptest.NewServer(New(r.d))
			defer server.Close()
			input := struct {
				Origin    string `json:"origin"`
				Token     string `json:"token"`
				Viewer    any    `json:"viewer"`
				ItemID    string `json:"itemId"`
				SourceID  string `json:"sourceId"`
				LibraryID string `json:"libraryId"`
				Format    string `json:"format"`
			}{server.URL, r.f.owner.AccessToken, r.p.Viewer, r.item, r.source, r.library, tc.format}
			payload, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, node, "--experimental-strip-types", filepath.Join(root, "packages/client-core/test/reading-http.integration.mjs"))
			command.Dir = root
			command.Stdin = bytes.NewReader(payload)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err = command.Run(); err != nil {
				t.Fatalf("actual %s HTTP client/publication boundary: %v\n%s", tc.format, err, output.String())
			}
			t.Log(output.String())
		})
	}
}

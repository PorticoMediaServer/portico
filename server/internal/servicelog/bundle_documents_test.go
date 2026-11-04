package servicelog

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestBundleScrubsStructuredCredentialsAndPreservesPublicFacts(t *testing.T) {
	const privateURL = "https://doc-user:doc-pass@updates.example.invalid/releases.json?%74oken=short-token&channel=stable"
	const publicURL = "https://updates.example.invalid/releases.json?channel=stable"
	const counter uint64 = 1<<60 + 3
	digest := strings.Repeat("a", 64)
	settings := map[string]any{"nested": []any{map[string]any{"url": privateURL}}, "publicUrl": publicURL}
	before, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	truncated, err := WriteBundle(&out, BundleInputs{
		Settings:     settings,
		Report:       map[string]any{"buildDigest": digest, "counter": counter, "diagnostic": privateURL},
		Connectivity: map[string]any{"addresses": []string{privateURL, publicURL}},
		Records:      []Record{{Message: `Get "` + privateURL + `": unavailable`}},
	})
	if err != nil || truncated {
		t.Fatalf("bundle: truncated=%v err=%v", truncated, err)
	}
	archive, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.File {
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid(raw) {
			t.Errorf("invalid JSON in %s: %s", entry.Name, raw)
		}
		for _, secret := range []string{"doc-user", "doc-pass", "short-token"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Errorf("%s exposes %s", entry.Name, secret)
			}
		}
		if entry.Name == "system-report.json" {
			if !bytes.Contains(raw, []byte(digest)) || !bytes.Contains(raw, []byte("1152921504606846979")) {
				t.Errorf("public digest or integer precision lost: %s", raw)
			}
		}
		if entry.Name == "settings.json" || entry.Name == "connectivity.json" {
			if !bytes.Contains(raw, []byte(publicURL)) {
				t.Errorf("public diagnostic URL changed: %s", raw)
			}
		}
	}
	after, err := json.Marshal(settings)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("export mutated the input settings: %s %v", after, err)
	}
}

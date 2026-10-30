package servicelog

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

// MaxBundleBytes caps a diagnostics bundle. A support archive that grows without
// a ceiling is a denial-of-service against the server's own disk and against the
// person trying to send it, so the writer stops at the cap and records that it
// truncated rather than failing the download.
const MaxBundleBytes int64 = 16 << 20

var ErrBundleTooLarge = errors.New("diagnostics bundle exceeded its size cap")

// BundleInputs are the documents a bundle carries. Settings must already have
// had its secrets removed by the caller: this package does not know which fields
// are secret, and guessing would be worse than requiring the caller to say.
type BundleInputs struct {
	// Settings is the effective settings document with every field the registry
	// marks secret removed.
	Settings any
	// Report is the system report from internal/diagnostics.
	Report any
	// Connectivity is the connectivity status report.
	Connectivity any
	// Records is the recent message log.
	Records []Record
	// Files are rotating log file paths to include whole, newest first.
	Files []string
	Now   func() time.Time
}

// countingWriter refuses writes once the cap is reached, so a large rotated log
// cannot push the archive past its limit mid-entry.
type countingWriter struct {
	to        io.Writer
	written   int64
	cap       int64
	truncated bool
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.written+int64(len(p)) > c.cap {
		c.truncated = true
		return 0, ErrBundleTooLarge
	}
	n, err := c.to.Write(p)
	c.written += int64(n)
	return n, err
}

// WriteBundle streams a zip to w. It returns whether the cap forced it to stop
// early, so the caller can say so in a header.
func WriteBundle(w io.Writer, in BundleInputs) (truncated bool, err error) {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	counter := &countingWriter{to: w, cap: MaxBundleBytes}
	archive := zip.NewWriter(counter)
	add := func(name string, body []byte) bool {
		entry, e := archive.Create(name)
		if e != nil {
			return false
		}
		_, e = entry.Write(body)
		return e == nil
	}
	document := func(name string, v any) bool {
		body, e := json.MarshalIndent(v, "", "  ")
		if e != nil {
			body = []byte("{}")
		}
		return add(name, body)
	}
	manifest := map[string]any{
		"schemaVersion": 1,
		"createdAt":     now().UTC().Format(time.RFC3339),
		"contents":      []string{"manifest.json", "settings.json", "system-report.json", "connectivity.json", "messages.json", "logs/"},
		"note":          "Settings in this bundle have had every field the settings registry marks secret removed.",
	}
	for _, step := range []func() bool{
		func() bool { return document("manifest.json", manifest) },
		func() bool { return document("settings.json", in.Settings) },
		func() bool { return document("system-report.json", in.Report) },
		func() bool { return document("connectivity.json", in.Connectivity) },
		func() bool { return document("messages.json", in.Records) },
	} {
		if !step() {
			archive.Close()
			return counter.truncated, nil
		}
	}
	for _, path := range in.Files {
		raw, e := os.ReadFile(path)
		if e != nil {
			continue
		}
		if !add("logs/"+baseName(path), raw) {
			archive.Close()
			return true, nil
		}
	}
	if e := archive.Close(); e != nil {
		return true, nil
	}
	return counter.truncated, nil
}

func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

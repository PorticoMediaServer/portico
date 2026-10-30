package playback

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestWindowConverterDiagnosticKeepsBoundedSanitizedStderr(t *testing.T) {
	err := fmt.Errorf("window: %w", &storage.ProcessError{Err: errors.New("exit status 1"), Stderr: strings.Repeat("x", 4096) + "\x01 No such filter /private/output"})
	text := windowConverterDiagnostic(err, "/private/output")
	if !strings.Contains(text, "No such filter [path]") || strings.Contains(text, "\x01") || len(text) > 2000 {
		t.Fatal(text)
	}
	if classifyConverterFailure(text) != FailureFilterMissing {
		t.Fatal(text)
	}
	if text := windowConverterDiagnostic(errors.New("source changed")); text != "" {
		t.Fatal(text)
	}
}

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

// OPS-2: an archive the reader refuses (CBZ, CBR or CB7: encrypted, multi-volume,
// solid, compressed legacy RAR) answers a definite 422 the client can say, never 500.
func TestReadingUnsupportedArchiveIsA422(t *testing.T) {
	for _, format := range []string{"cbz", "cbr", "cb7"} {
		w := httptest.NewRecorder()
		readingFailure(w, fmt.Errorf("%w: %s", reading.ErrArchiveFormat, format))
		if w.Code != 422 {
			t.Fatalf("%s: status %d, want 422", format, w.Code)
		}
		var body struct{ Error struct{ Code, Message string } }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "comic_archive_unsupported" || body.Error.Message != "This comic's archive type isn't supported." {
			t.Fatalf("%s: body %s", format, w.Body.String())
		}
	}
}

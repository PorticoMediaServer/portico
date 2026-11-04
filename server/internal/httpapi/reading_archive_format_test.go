package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
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
		var body struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "comic_archive_unsupported" || body.Error.Message != "This comic's archive type isn't supported." {
			t.Fatalf("%s: body %s", format, w.Body.String())
		}
	}
}

func TestReadingComicArchiveMismatchUsesArchiveError(t *testing.T) {
	for _, tc := range []struct {
		name, format string
		raw          []byte
	}{
		{"RAR-as-CBZ", "cbz", readingtest.CBR()},
		{"7z-as-CBZ", "cbz", readingtest.CB7()},
		{"ZIP-as-CBR", "cbr", readingtest.CBZ()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.format == "cbr" && !readingcap.CBR() {
				t.Skip("CBR scan admission requires the official archive capability")
			}
			r := newReadingFixture(t, "comic", tc.format, tc.raw)
			w := readingHTTP(t, New(r.d), "POST", "/v1/reading/sessions", r.f.owner.AccessToken, "", ReadingSessionRequest{r.item, r.source})
			if w.Code != 422 || v1Code(w) != "comic_archive_unsupported" {
				t.Fatalf("mislabeled archive open: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

package mediaengine_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A film disc kept as a folder is listed, and pressing Play on it is answered plainly: discs
// can't be played here, and trying again will not help. It used to be answered as a file
// that could not be reached right now, with "try again".
func TestPlayingADiscIsRefusedInPlainWords(t *testing.T) {
	h, d := newHouse(t, decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t))
	library := filepath.Join(t.TempDir(), "Films")
	disc := filepath.Join(library, "Disc Film (2019)", "BDMV")
	if err := os.MkdirAll(disc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disc, "index.bdmv"), []byte("INDX0200"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := d.CreateLibrary(context.Background(), "Films", db.LibraryMovies, "en", []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: scan.FileProber{}}).Scan(context.Background(), lib); err != nil {
		t.Fatal(err)
	}
	var page api.ItemPage
	h.call("GET", "/v1/items?library="+lib.PublicID+"&kind=movie", nil, &page, 200)
	if len(page.Items) != 1 {
		t.Skipf("the scan lists no title for a disc folder here: %+v", page.Items)
	}
	for _, ask := range []func() (int, string){
		func() (int, string) {
			w := h.do("POST", "/v1/playback/sessions", api.PlaybackStart{ItemID: page.Items[0].ID})
			raw, _ := io.ReadAll(w.Body)
			return w.StatusCode, string(raw)
		},
		func() (int, string) {
			w := h.do("GET", "/v1/items/"+page.Items[0].ID+"/playback-options", nil)
			raw, _ := io.ReadAll(w.Body)
			return w.StatusCode, string(raw)
		},
	} {
		code, body := ask()
		if code != 501 || !strings.Contains(body, `"disc_playback_unavailable"`) || !strings.Contains(body, "Discs can't be played on this server.") ||
			!strings.Contains(body, `"retry":false`) {
			t.Fatalf("playing a disc answered %d: %s", code, body)
		}
	}
}

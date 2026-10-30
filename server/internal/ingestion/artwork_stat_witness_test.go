//go:build !windows

package ingestion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type countedCall struct {
	Operation string `json:"operation"`
	Inventory *struct {
		RelativePath string `json:"relativePath"`
	} `json:"inventory"`
}

// countedHelper records the request of every helper process the scan starts and
// then runs the real helper, so a test can name the exact inventory stats and
// blob reads one analysed candidate costs.
func countedHelper(t *testing.T) (*storage.Client, func(string) map[string]int) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "helper.log")
	script := filepath.Join(t.TempDir(), "counted-helper")
	body := fmt.Sprintf("#!/bin/sh\npayload=$(cat)\nprintf '%%s\\n' \"$payload\" >> %q\nprintf '%%s' \"$payload\" | exec %q \"$@\"\n", log, binary)
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	counts := func(relative string) map[string]int {
		t.Helper()
		raw, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		found := map[string]int{}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var call countedCall
			if err := json.Unmarshal([]byte(line), &call); err != nil {
				t.Fatalf("unrecorded helper request %q: %v", line, err)
			}
			if call.Inventory != nil && call.Inventory.RelativePath == relative {
				found[call.Operation]++
			}
		}
		return found
	}
	return storage.New(script), counts
}

// The scan's own analysis context is what a candidate consumes: a real scan of
// one film with one poster keeps independent fresh observations around candidate
// reads, including the warm verification after catalogue publication.
func TestScanAnalysisConsumesItsOwnArtworkObservation(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	library := filepath.Join(root, "films")
	if err := os.Mkdir(library, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:size=64x64:rate=1", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", filepath.Join(library, "Movie (2020).mp4")).CombinedOutput(); err != nil {
		t.Fatalf("fixture %v %s", err, output)
	}
	var poster bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 12))
	img.SetRGBA(0, 0, color.RGBA{R: 3, G: 255, A: 255})
	if err := png.Encode(&poster, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(library, "Movie (2020)-poster.png"), poster.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	store, counts := countedHelper(t)
	cat.SetStorage(store)
	meta, err := localmetadata.New(filepath.Join(root, "art"), ffmpeg, store)
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db, cat, assets.Probe{Binary: decodertest.QualifiedFFprobe(t), Supervisor: store.Supervisor})
	scanner.SetStorage(store)
	scanner.LocalMetadata = meta
	lib, err := cat.Create("Films", "movie", library)
	if err != nil {
		t.Fatal(err)
	}
	job, err := scanner.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 32; n++ {
		scanner.process(context.Background(), job.ID, lib.ID)
		// The derived worker runs beside ingestion in production; analysis
		// waits for its pending catalogue information changes to publish.
		drainDerivedCatalogue(t, db)
		state, err := scanner.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Status == "complete" || state.Status == "complete_with_warnings" {
			drainDerivedCatalogue(t, db)
			break
		}
		if state.Status == "failed" {
			t.Fatal(state)
		}
		if n == 31 {
			t.Fatal("scan unfinished:", state.Status)
		}
	}
	var local int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_item_details WHERE poster_url LIKE 'local:%'`).Scan(&local); err != nil {
		t.Fatal(err)
	}
	if local != 1 {
		t.Fatal("the scan did not publish the admitted poster")
	}
	// The item's artwork key takes one admission stat and one blob. Cold
	// candidate enumeration takes three fresh stats (pre-check, byte admission,
	// post-check) and one blob. Publication then causes a warm candidate check:
	// two fresh observations, no blob. No observation pays an extra explicit stat.
	calls := counts("Movie (2020)-poster.png")
	if calls["inventory-stat"] != 6 || calls["inventory-blob"] != 2 {
		t.Fatal("the scan did not consume its own observation around the admitted read", calls)
	}
}

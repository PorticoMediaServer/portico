package ingestion

import (
	"bytes"
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A poster that arrives after the scan listed its folder, but before the film
// beside it is analysed, is still found: what the scan remembers of a folder
// stands only while the folder is as it was listed.
func TestSidecarAddedAfterItsFolderWasListedIsStillRead(t *testing.T) {
	binary := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	library := filepath.Join(root, "films")
	if err := os.Mkdir(library, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "-v", "error", "-f", "lavfi", "-i", "color=c=blue:size=64x64:rate=1", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", filepath.Join(library, "Movie (2020).mp4")).CombinedOutput(); err != nil {
		t.Fatalf("fixture %v %s", err, output)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	helper, _ := os.Executable()
	store := storage.New(helper)
	cat.SetStorage(store)
	meta, err := localmetadata.New(filepath.Join(root, "art"), binary, store)
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db, cat, assets.Probe{Supervisor: store.Supervisor})
	scanner.SetStorage(store)
	scanner.LocalMetadata = meta
	lib, err := cat.Create("Films", "movie", library)
	if err != nil {
		t.Fatal(err)
	}
	finish := func(job Job) {
		t.Helper()
		for n := 0; n < 20; n++ {
			scanner.process(context.Background(), job.ID, lib.ID)
			// The derived worker runs beside ingestion in production; analysis
			// waits for its pending catalogue information changes to publish.
			drainDerivedCatalogue(t, db)
			state, err := scanner.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "complete" {
				drainDerivedCatalogue(t, db)
				return
			}
			if state.Status == "failed" {
				t.Fatal(state)
			}
		}
		t.Fatal("scan unfinished")
	}
	job, err := scanner.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Up to the point where the folder has been listed and the film waits to be analysed.
	for n := 0; ; n++ {
		var phase string
		if err = db.QueryRow(`SELECT phase FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&phase); err != nil {
			t.Fatal(err)
		}
		if phase == "basic_pending" {
			break
		}
		if n == 64 {
			t.Fatal("scan never reached analysis:", phase)
		}
		scanner.processQuantum(context.Background(), job.ID, lib.ID)
	}
	time.Sleep(50 * time.Millisecond)
	var poster bytes.Buffer
	if err = png.Encode(&poster, image.NewRGBA(image.Rect(0, 0, 8, 12))); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(library, "Movie (2020)-poster.png"), poster.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	finish(job)
	local := func() int {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM catalog_item_details WHERE poster_url LIKE 'local:%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if local() != 1 {
		t.Fatal("the poster added after the folder was listed was not read by this scan")
	}
	// An ordinary rescan leaves it in place.
	if job, err = scanner.Queue(lib.ID); err != nil {
		t.Fatal(err)
	}
	finish(job)
	if local() != 1 {
		t.Fatal("the poster was lost by the next scan")
	}
}

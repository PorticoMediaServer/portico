package writer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

func TestN1PairedCanonicalPopulationFunctional(t *testing.T) {
	if v := os.Getenv("PORTICO_PAIRED_CANONICAL_FUNCTIONAL"); v == "" {
		t.Skip("opt-in real-clock paired canonical component")
	} else if v != "1" {
		t.Fatal("functional flag must be1")
	}
	dir, pin := canonicalN1ControlArtifact(t)
	manifest := UseQualificationPopulation(t, dir, pin)
	data, _ := json.Marshal(firstLightConfig{RunID: "PAIRED_CANONICAL_CONTROL", HostEpoch: "CONTROL", SourceCommit: strings.Repeat("a", 40), OutputPath: filepath.Join(t.TempDir(), "raw.ndjson"), Fixtures: 1})
	// Tiny trusted artifact goes through the same actual constructor. The public
	// target still requires exact1M; this control is explicitly16 rows, never1M.
	c := newN1PairedPopulationComponent(t, data, 1, "timing", &manifest)
	if c.View.Library != 7 || c.PointView.Library != 1 {
		t.Fatal("wrong parent/overlay", c.View, c.PointView)
	}
	now := time.Now()
	live, request, output := readfixture.Requests("canonical-control", now)
	view := c.View
	view.Now = now
	_, err := read.ConsumeForeground(t.Context(), c.Governor, c.Authority, c.Snapshots, live, output, func(ctx context.Context, out read.BrowseOutput) error {
		page, _, err := c.Browse.Window(ctx, request, time.Second, out, view)
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			t.Error("canonical parent not browsed")
		}
		for _, item := range page.Items {
			if item.ID < manifest.Facts.FirstID {
				t.Error("tiny overlay substituted for canonical parent", item.ID)
			}
		}
		page = read.BrowsePage{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.RunOriginal(t, t.Context(), time.Now(), false, "timing"); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(nil); err != nil {
		t.Fatal(err)
	}
}

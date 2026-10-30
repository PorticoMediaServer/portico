package mediaanalysis

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestAnalysisPreviewCapturesCloseWithoutEarlyPublication(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	store, err := mediaartifact.New(filepath.Join(canonicalFixtureDir(t), "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db, err := persistence.Open(filepath.Join(canonicalFixtureDir(t), "database.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := &Service{db: db, artifacts: store, options: Options{MaxGeneratedBytes: 1 << 20, MaxPreviewFrames: 16}}
	retired := false
	run := func(ctx context.Context, key string, spec decoder.AnalysisSpec, consume func(io.Reader) error) error {
		if spec.Kind != "trickplay" {
			t.Fatal(spec.Kind)
		}
		// The callback sees complete frames, but decoder retirement is later.
		e := consume(bytes.NewReader(make([]byte, 320*180*3*16)))
		inventory, err := store.Inventory()
		if err != nil {
			t.Fatal(err)
		}
		defer inventory.Close()
		objects, err := inventory.Next(ctx, 128)
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if len(objects) != 0 {
			t.Fatal("decoder still alive: immutable objects published early")
		}
		retired = true
		return e
	}
	out, err := service.produce(context.Background(), binding{DurationUS: 160000000, VideoCodec: "h264"}, "trickplay", nil, run, token("test"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, a := range out.Artifacts {
		kinds[a.Kind]++
	}
	// The sixteen frames go straight into one sprite sheet (a frame is no
	// longer kept as an image of its own); the sheet and the set descriptor are
	// produced inside the same decode, and neither may be published before
	// retirement.
	if !retired || kinds["trickplay"] != 0 || kinds[trickplaySheetKind] != 1 || kinds[trickplayDescriptorKind] != 1 || len(out.Artifacts) != 2 {
		t.Fatal("incomplete producer", kinds)
	}
	if summary, ok := out.Summary.(trickplaySummary); !ok || summary.FrameCount != 16 {
		t.Fatalf("the sheet does not hold the sixteen frames decoded: %+v", out.Summary)
	}
	for _, a := range out.Artifacts {
		// Resume would fail if the original capture still held a writer descriptor.
		writer, err := store.ResumeRetained(context.Background(), a.RetainedKey, a.Object.Size, a.Object.Size)
		if err != nil {
			t.Fatal(err)
		}
		object, err := writer.Seal(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if object != a.Object {
			t.Fatal("retained bytes changed")
		}
		reader, err := store.Open(context.Background(), object)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.Remove(object); !errors.Is(err, mediaartifact.ErrLeased) {
			t.Fatalf("active-reader deletion: %v", err)
		}
		reader.Close()
		if err = store.Remove(object); err != nil {
			t.Fatal(err)
		}
	}
}

//go:build !windows

package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
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

// Every storage helper is a new process, which costs 0.1–0.4 s on a small VPS
// (OPS-1, 2 Nov: 31 helpers per film on a first import made a 14-library demo
// scan take 40+ minutes). These budgets hold the number of helpers a scan
// starts per file, so a change that starts one more per file fails here
// instead of on someone's server. Measured on this fixture (six films): before
// OPS-1 C, 36.5 per film on a first import and 7.8 on an unchanged rescan;
// after it, 10.3 and 4.0. Larger libraries amortise the folder pages further.
const (
	firstImportHelpersPerFile = 12
	rescanHelpersPerFile      = 5
)

func helperCounter(t *testing.T) (*storage.Client, func() map[string]int) {
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
	return storage.New(script), func() map[string]int {
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
			var call struct {
				Operation string `json:"operation"`
			}
			if err := json.Unmarshal([]byte(line), &call); err != nil {
				t.Fatalf("unrecorded helper request %q: %v", line, err)
			}
			found[call.Operation]++
		}
		return found
	}
}

func helperTotal(calls map[string]int) int {
	n := 0
	for op, c := range calls {
		if op != "inventory-watch" {
			n += c
		}
	}
	return n
}

func TestScanHelperProcessBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	library := filepath.Join(root, "films")
	const films = 6
	for i := 0; i < films; i++ {
		folder := filepath.Join(library, fmt.Sprintf("Film %d (20%02d)", i, i))
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=12", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", filepath.Join(folder, filepath.Base(folder)+".mp4")).CombinedOutput(); err != nil {
			t.Fatalf("fixture %v %s", err, output)
		}
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	store, counts := helperCounter(t)
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
	scan := func(what string) {
		t.Helper()
		job, err := scanner.Queue(lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 64; n++ {
			scanner.process(context.Background(), job.ID, lib.ID)
			drainDerivedCatalogue(t, db)
			state, err := scanner.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "complete" || state.Status == "complete_with_warnings" {
				return
			}
			if state.Status == "failed" || n == 63 {
				t.Fatalf("%s: scan ended %s", what, state.Status)
			}
		}
	}
	before := helperTotal(counts())
	scan("first import")
	first := counts()
	firstTotal := helperTotal(first) - before
	var analysed int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE analysis_state='complete'`).Scan(&analysed); err != nil || analysed != films {
		t.Fatalf("first import analysed %d of %d films (%v)", analysed, films, err)
	}
	scan("rescan")
	rescanTotal := helperTotal(counts()) - firstTotal - before
	t.Logf("helpers: first import %d (%.1f per film) %v; rescan %d (%.1f per film)", firstTotal, float64(firstTotal)/films, first, rescanTotal, float64(rescanTotal)/films)
	if firstTotal > firstImportHelpersPerFile*films {
		t.Fatalf("a first import of %d films started %d helper processes, over the budget of %d per film: %v", films, firstTotal, firstImportHelpersPerFile, first)
	}
	if rescanTotal > rescanHelpersPerFile*films {
		t.Fatalf("a rescan of %d unchanged films started %d helper processes, over the budget of %d per film", films, rescanTotal, rescanHelpersPerFile)
	}
}

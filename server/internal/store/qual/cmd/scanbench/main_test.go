package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestScanbenchTraversalKeepsAvailabilityUnknown(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("FIRST native-cookie harness")
	}
	scratch := t.TempDir()
	template := filepath.Join(scratch, "seed.mkv")
	if err := os.WriteFile(template, []byte("traversal fixture, no probe"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(scratch, "library")
	var out bytes.Buffer
	if err := run(t.Context(), []string{"-mode", "generate", "-root", root, "-template", template, "-files", "3"}, &out); err != nil {
		t.Fatal(err)
	}
	assertQualificationFalse(t, out.Bytes())
	var generated struct {
		Label, Mode string
		Fixture     struct{ Files int }
	}
	if err := json.Unmarshal(out.Bytes(), &generated); err != nil || generated.Label != "DIRECTIONAL" || generated.Mode != "generation-only" || generated.Fixture.Files != 3 {
		t.Fatal("unlabelled generator", generated, err)
	}
	out.Reset()
	if err := run(t.Context(), []string{"-root", root, "-source-sha", strings.Repeat("a", 40)}, &out); err != nil {
		t.Fatal(err)
	}
	assertQualificationFalse(t, out.Bytes())
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Label != "DIRECTIONAL" || r.Mode != "traversal-only" || !r.TraversalCompleted || r.Counts.Files != 3 || r.FilenameVisibleNS != nil || r.BasicNS != nil || r.Engine != nil || len(r.Unavailable) != 3 {
		t.Fatalf("incorrect claims: %+v", r)
	}
	// Do not turn a partial scan into successful timing evidence.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out.Reset()
	if err := run(ctx, []string{"-root", root, "-source-sha", strings.Repeat("a", 40)}, &out); err == nil {
		t.Fatal("canceled traversal succeeded")
	}
	assertQualificationFalse(t, out.Bytes())
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.TraversalCompleted || r.Error == "" || r.BasicNS != nil || r.FilenameVisibleNS != nil {
		t.Fatal("canceled scan reported completion")
	}
}

func assertQualificationFalse(t *testing.T, raw []byte) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	value, present := fields["qualification"]
	if !present || !bytes.Equal(value, []byte("false")) {
		t.Fatal("qualification must be present and false", string(raw))
	}
}

func TestScanbenchInvalidArgumentsNeverCreateFixture(t *testing.T) {
	root := filepath.Join(t.TempDir(), "library")
	for _, args := range [][]string{
		{"-mode", "unknown", "-root", root},
		{"-mode", "generate", "-root", root, "-files", "100000"},
		{"-mode", "traverse", "-root", root, "-source-sha", "bad"},
		{"-mode", "generate", "-root", root, "-files", "400001"},
	} {
		if err := run(t.Context(), args, &bytes.Buffer{}); err == nil {
			t.Fatal("accepted", args)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid run created fixture", err)
		}
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

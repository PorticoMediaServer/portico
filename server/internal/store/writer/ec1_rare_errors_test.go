package writer_test

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// EC1 rare-error tier: the 100ms ceiling covers refusal paths a normal command
// never takes. Each sample is one real facade call on the same native fixture;
// a refusal is the expected outcome and is never a product success mutation.
const rareErrorBudget = 100 * time.Millisecond

var ec1RareErrors = flag.Bool("ec1-rare-errors", false, "opt-in EC1 rare-error screening cell; never qualification")
var ec1RareIterations = flag.Int("ec1-rare-iterations", 10000, "measured calls per rare-error path,1..10000")

type rarePath struct {
	Name       string
	WantErr    bool
	Samples    []int64
	Error      int
	Unexpected int
}

type rareStats struct {
	Count               int
	ErrorCount          int
	Unexpected          int
	P50NS, P99NS, MaxNS time.Duration
	WithinTarget        bool
}

type rareErrorReport struct {
	Method        string
	BudgetNS      time.Duration
	Iterations    int
	Paths         map[string]rareStats
	Qualification bool
	Scope         string
}

func rareStatsFor(p rarePath) rareStats {
	out := rareStats{Count: len(p.Samples), ErrorCount: p.Error, Unexpected: p.Unexpected}
	if len(p.Samples) == 0 {
		return out
	}
	xs := slices.Clone(p.Samples)
	slices.Sort(xs)
	rank99 := (99*len(xs) + 99) / 100
	out.P50NS = time.Duration(xs[(len(xs)+1)/2-1])
	out.P99NS = time.Duration(xs[rank99-1])
	out.MaxNS = time.Duration(xs[len(xs)-1])
	out.WithinTarget = out.P99NS <= rareErrorBudget
	return out
}

func measureRare(paths map[string]*rarePath, name string, call func() error) {
	start := time.Now()
	err := call()
	elapsed := int64(time.Since(start))
	p := paths[name]
	p.Samples = append(p.Samples, elapsed)
	if err != nil {
		p.Error++
	}
	if p.WantErr != (err != nil) {
		p.Unexpected++
	}
}

// TestEC1RareErrorPaths is the opt-in screening cell for the common refusal
// paths. Every path uses the real native composition; no product code changes.
func TestEC1RareErrorPaths(t *testing.T) {
	enabled := *ec1RareErrors
	if v := os.Getenv("PORTICO_EC1_RARE_ERRORS"); v != "" {
		if v != "1" {
			t.Fatal("PORTICO_EC1_RARE_ERRORS must be 1")
		}
		enabled = true
	}
	if !enabled {
		t.Skip("opt-in EC1 rare-error screening")
	}
	iterations := *ec1RareIterations
	if v := os.Getenv("PORTICO_EC1_RARE_ITERATIONS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			t.Fatal("PORTICO_EC1_RARE_ITERATIONS must be 1..10000")
		}
		iterations = n
	}
	if iterations < 1 || iterations > 10000 {
		t.Fatal("rare-error iterations must be 1..10000")
	}
	path := filepath.Join(t.TempDir(), "rare.ndjson")
	if evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidence != "" {
		if err := os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(evidence, fmt.Sprintf("ec1-rare-%d.ndjson", time.Now().UnixNano()))
	}
	data, _ := json.Marshal(map[string]any{"run_id": "EC1_RARE", "host_epoch": "CONTROL", "source_commit": strings.Repeat("a", 40), "output_path": path, "fixtures": 1})
	c := writer.NewEC2PairedComponent(t, data, 1, "timing")
	driver, w := c.EC2Progress(), c.EC2Writer()
	if driver == nil || w == nil {
		t.Fatal("native progress composition unavailable")
	}
	paths := map[string]*rarePath{
		"replay":  {Name: "replay"},
		"stale":   {Name: "stale", WantErr: true},
		"unknown": {Name: "unknown", WantErr: true},
		"resolve": {Name: "resolve", WantErr: true},
	}
	for _, p := range paths {
		p.Samples = make([]int64, 0, iterations)
	}
	// One saved receipt: capture, accept, then replay it many times while it is
	// still the current saved original. ACK follows the measurement.
	command, err := driver.Capture(t.Context(), c.EC2ReportSequence(), 5, 100, "playing")
	if err != nil {
		t.Fatalf("progress capture: %v", err)
	}
	replayReference, err := writer.ProgressReference(command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AcceptProgress(t.Context(), driver.Disclosure(), command); err != nil {
		t.Fatalf("accept: %v", err)
	}
	before, err := w.CurrentProgress(t.Context(), driver.Disclosure())
	if err != nil {
		t.Fatal(err)
	}
	// Path 1: replay of the saved receipt. Every call must return the same saved
	// Applied result with zero new effects.
	for i := 0; i < iterations; i++ {
		measureRare(paths, "replay", func() error {
			result, err := w.ResolveProgress(t.Context(), driver.Disclosure(), replayReference)
			if err != nil {
				return err
			}
			if !result.Outcome.Replayed || result.Outcome.State != writer.Applied {
				return errors.New("saved receipt was not replayed")
			}
			return nil
		})
	}
	after, err := w.CurrentProgress(t.Context(), driver.Disclosure())
	if err != nil {
		t.Fatal(err)
	}
	if after.Last != before.Last || after.ReceiptRows != before.ReceiptRows || after.LastEvent != before.LastEvent {
		t.Fatal("replay changed native effects")
	}
	// Path 2: stale/fence refusal. A never-issued receipt digest must be refused
	// by the ACK fence and may never become a saved success.
	for i := 0; i < iterations; i++ {
		measureRare(paths, "stale", func() error {
			bogus := replayReference
			bogus.Receipt.RequestDigest = writer.RequestDigest{0xde, 0xad, 0xbe, 0xef}
			_, err := w.AckProgress(t.Context(), driver.Disclosure(), bogus)
			return err
		})
	}
	// Path 3: unknown/undeclared purpose refusal at the closed registry.
	for i := 0; i < iterations; i++ {
		measureRare(paths, "unknown", func() error {
			_, err := writer.Describe(writer.CommandID(0xFFFF))
			return err
		})
	}
	// Path 4: durable-resolution refusal. The retained saved original makes a
	// different capture report require resolution first; it must never create a
	// second uncommitted obligation.
	for i := 0; i < iterations; i++ {
		measureRare(paths, "resolve", func() error {
			_, err := driver.Capture(t.Context(), 99, 6, 100, "playing")
			if err != nil && !errors.Is(err, writer.ErrResolveRequired) {
				return fmt.Errorf("unexpected refusal kind: %w", err)
			}
			return err
		})
	}
	if _, err := w.AckProgress(t.Context(), driver.Disclosure(), replayReference); err != nil {
		t.Fatalf("final ack: %v", err)
	}
	if err := c.RunEC2Original(t, t.Context(), time.Now(), "timing"); err != nil {
		t.Fatal(err)
	}
	report := rareErrorReport{Method: "ec1-rare-error-paths-v1", BudgetNS: rareErrorBudget, Iterations: iterations, Paths: map[string]rareStats{}, Qualification: false, Scope: "refusal paths only; each sample is one real facade call; p99/max against the 100ms rare-error ceiling; uncertain-COMMIT resolution is not deterministically producible in this component cell"}
	for name, p := range paths {
		report.Paths[name] = rareStatsFor(*p)
	}
	raw, _ := json.Marshal(report)
	if err := c.EmitEC2("ec1_rare_error_report", raw); err != nil {
		t.Fatal(err)
	}
	for name, s := range report.Paths {
		if s.Unexpected > 0 {
			t.Fatalf("%s path had %d unexpected outcomes", name, s.Unexpected)
		}
	}
	if err := c.Close(nil); err != nil {
		t.Fatal(err)
	}
}

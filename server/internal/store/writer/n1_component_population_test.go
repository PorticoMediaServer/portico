package writer

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

var n1PopulationFlag = flag.String("n1-component-population", "first-use-reopen", "first-use-reopen, setup-retained-first-use, steady, full-prefix, or owner-window (closed separate cells)")
var n1WarmupFlag = flag.Int("n1-component-warmup", 0, "0 for first-use; 1..64 actual fresh originals before steady measured originals")

var errN1ProgressiveFixture = errors.New("N1 first-use population is not warmed steady state; positive warmup requires steady mode")
var errN1WarmupRequired = errors.New("N1 steady requires 1..64 retained fresh-original warmup commands")

func validateN1Population(mode string, warmup int) error {
	if warmup < 0 || warmup > 64 {
		return ErrInvalid
	}
	if mode != "first-use-reopen" && mode != "setup-retained-first-use" && mode != "steady" && mode != "full-prefix" && mode != "owner-window" {
		return ErrInvalid
	}
	if mode == "steady" {
		if warmup == 0 {
			return errN1WarmupRequired
		}
		return nil
	}
	if warmup > 0 {
		return errN1ProgressiveFixture
	}
	return nil
}

type n1AttemptOptions struct {
	population string
	ownerCell  string
	clock      func() time.Time
} // clock is internal protocol-control only, never a flag/config input

type n1ConnectionContinuity struct {
	Population             string             `json:"population"`
	BeforeID               *uint64            `json:"connection_before_id"`
	AfterID                *uint64            `json:"connection_after_id"`
	ConnectionsBefore      *uint64            `json:"connections_before"`
	ConnectionsAfter       *uint64            `json:"connections_after"`
	NativeClosesBefore     *int               `json:"native_closes_before"`
	NativeClosesAfter      *int               `json:"native_closes_after"`
	Same                   *bool              `json:"same_physical_connection"`
	ReplacementObserved    *bool              `json:"replacement_observed"`
	WarmupRequested        int                `json:"warmup_requested"`
	WarmupCompleted        int                `json:"warmup_completed"`
	WarmupOriginals        []ReceiptReference `json:"warmup_originals"`
	SteadyEligible         bool               `json:"steady_eligible"`
	DeliberateErrorControl bool               `json:"deliberate_error_control"`
	UnavailableReason      string             `json:"unavailable_reason"`
}

func (c *n1ConnectionContinuity) observeAfter(trace *n1Trace) {
	connections, closes := trace.connections, trace.closeCount
	c.ConnectionsAfter, c.NativeClosesAfter = &connections, &closes
	if trace.conn == nil {
		return
	}
	id := trace.conn.id
	c.AfterID = &id
	if c.BeforeID == nil || c.ConnectionsBefore == nil || c.NativeClosesBefore == nil {
		return
	}
	same := *c.BeforeID == id && *c.ConnectionsBefore == connections && *c.NativeClosesBefore == closes && !trace.conn.closed
	c.Same = &same
	replaced := *c.BeforeID != id || *c.ConnectionsBefore != connections
	c.ReplacementObserved = &replaced
}

// This brackets the actual native driver's Close, inside W's existing owned
// close boundary. It includes whatever SQLite does there (possibly checkpoint/
// sync), without claiming to isolate xSync or measuring the enclosing retirement
// interval as close. The owner still calls and contains Close exactly once.
type n1NativeClose struct {
	ConnectionID uint64 `json:"connection_id"`
	StartedNS    *int64 `json:"started_ns"`
	ReturnedNS   *int64 `json:"returned_ns"`
	UnwoundNS    *int64 `json:"unwound_ns"`
	ElapsedNS    *int64 `json:"elapsed_ns"`
	Error        string `json:"error"`
	Completed    bool   `json:"completed"`
}

type n1NativeCloseConn struct {
	writerDriverConn
	trace *n1Trace
	id    uint64
}

// Keep the optional native engine getter when observing physical Close.
// The values still come from this actual native owner, never the trace.
func (c *n1NativeCloseConn) SQLiteEngine() (string, string, error) {
	getter, ok := c.writerDriverConn.(nativeEngineGetter)
	if !ok {
		return "", "", invalid("physical_engine_getter")
	}
	return getter.SQLiteEngine()
}

func (c *n1NativeCloseConn) Close() (err error) {
	t := c.trace
	if t.closeCount == len(t.closes) {
		t.closeOverflow = true
		return c.writerDriverConn.Close()
	}
	out := &t.closes[t.closeCount]
	t.closeCount++
	out.ConnectionID = c.id
	start := time.Now()
	out.StartedNS = n1NS(t.origin, start)
	defer func() {
		if !out.Completed {
			out.UnwoundNS = n1NS(t.origin, time.Now())
		}
	}()
	err = c.writerDriverConn.Close()
	end := time.Now()
	out.Completed, out.Error = true, n1Error(err)
	out.ReturnedNS, out.ElapsedNS = n1NS(t.origin, end), n1Duration(start, end)
	return err
}

// Keep this compile-time preservation check alongside the observation wrapper.
var _ driver.Conn = (*n1NativeCloseConn)(nil)

func TestN1ComponentRetainedSetupConnectionAndNativeClose(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component control")
	}
	stream := n1ControlStream(t)
	var record *n1Attempt
	if !t.Run("first_original", func(t *testing.T) {
		runN1Attempt(t, t.Context(), stream, 1, n1Case{"active", "current", "present", "fixture"}, "timing", "", &record, n1AttemptOptions{population: "setup-retained-first-use"})
	}) {
		return
	}
	if !record.Complete {
		t.Fatal("component control incomplete", record.Outcome, record.Error)
	}
	c := record.Continuity
	if c.BeforeID == nil || c.AfterID == nil || c.Same == nil || !*c.Same || *c.BeforeID != 1 || *c.AfterID != 1 || *c.ConnectionsBefore != 1 || *c.ConnectionsAfter != 1 || *c.ReplacementObserved {
		t.Fatal("initial physical connection was not retained", c)
	}
	if c.SteadyEligible || c.WarmupRequested != 0 || c.WarmupCompleted != 0 || len(c.WarmupOriginals) != 0 {
		t.Fatal("first original masqueraded as warmed steady state")
	}
	if record.Settings.Actual == nil || !*record.Settings.PolicyVerified || record.Clock.XSyncNS != nil || record.Clock.PostCommitToPhysicalReturnNS == nil {
		t.Fatal("policy or interval presence lost")
	}
	if len(record.NativeCloses) != 1 || record.NativeCloseOverflow {
		t.Fatal("native close was omitted or duplicated")
	}
	close := record.NativeCloses[0]
	if close.ConnectionID != 1 || !close.Completed || close.Error != "" || close.StartedNS == nil || close.ReturnedNS == nil || close.ElapsedNS == nil || close.UnwoundNS != nil || *close.ElapsedNS != *close.ReturnedNS-*close.StartedNS {
		t.Fatal("native close interval missing", close)
	}
	if *close.StartedNS < *record.Clock.PhysicalReturnedNS {
		t.Fatal("healthy retained connection closed during component custody")
	}
}

func TestN1ComponentPhysicalReplacementIsObserved(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component control")
	}
	trace := newN1Trace(time.Now(), false)
	e, _, _ := testExecutor(t, trace.decorate)
	before, connections, closes := trace.conn.id, trace.connections, trace.closeCount
	c := n1ConnectionContinuity{Population: "deliberate-replacement-control", BeforeID: &before, ConnectionsBefore: &connections, NativeClosesBefore: &closes}
	// A real pool replacement, with no SQL reset/replay or second writer. This
	// control is explicitly separate from any future ordinary steady cohort.
	e.db.SetMaxIdleConns(0)
	e.db.SetMaxIdleConns(1)
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	actual, err := sqliteconn.Assert(t.Context(), conn, e.config)
	if err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	c.observeAfter(trace)
	if c.Same == nil || *c.Same || c.ReplacementObserved == nil || !*c.ReplacementObserved || *c.AfterID != 2 || actual.Engine == "" {
		t.Fatal("replacement hidden by logical pool continuity", c)
	}
	if trace.closeCount != 1 || trace.closes[0].ConnectionID != before || !trace.closes[0].Completed {
		t.Fatal("old native instance did not close")
	}
}

func TestN1ComponentSteadyRequiresPositiveWarmup(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component control")
	}
	for _, request := range []struct {
		mode   string
		warmup int
	}{{"first-use-reopen", 1}, {"setup-retained-first-use", 1}} {
		if !errors.Is(validateN1Population(request.mode, request.warmup), errN1ProgressiveFixture) {
			t.Fatal("warmup mixed into first-use population")
		}
	}
	if !errors.Is(validateN1Population("steady", 0), errN1WarmupRequired) || validateN1Population("steady", 2) != nil {
		t.Fatal("steady warmup contract")
	}
	if validateN1Population("first-use-reopen", 0) != nil || validateN1Population("setup-retained-first-use", 0) != nil {
		t.Fatal("first-use modes refused")
	}
	for _, request := range []struct {
		mode   string
		warmup int
	}{{"unknown", 0}, {"first-use-reopen", -1}, {"first-use-reopen", 65}} {
		if !errors.Is(validateN1Population(request.mode, request.warmup), ErrInvalid) {
			t.Fatal("invalid population accepted")
		}
	}
}

func TestN1ComponentSteadyPreflightDoesNotOpenOutput(t *testing.T) {
	if !*n1EnabledFlag {
		t.Skip("opt-in component control")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	output, config := filepath.Join(dir, "unavailable.ndjson"), filepath.Join(dir, "config.json")
	raw, err := json.Marshal(firstLightConfig{RunID: "CONTROL", HostEpoch: "CONTROL", SourceCommit: strings.Repeat("0", 40), OutputPath: output, Fixtures: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, raw, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-test.run=^TestN1ComponentMeasurement$", "-n1-component", "-n1-component-population=steady", "-n1-component-warmup=0")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PORTICO_WRITER_N1_COMPONENT=") && !strings.HasPrefix(entry, "PORTICO_WRITER_N1_COMPONENT_CONFIG=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "PORTICO_WRITER_N1_COMPONENT=1", "PORTICO_WRITER_N1_COMPONENT_CONFIG="+config)
	log, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(log), errN1WarmupRequired.Error()) {
		t.Fatalf("steady request did not fail missing warmup: %v %s", err, log)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("unavailable steady request emitted output", err)
	}
}

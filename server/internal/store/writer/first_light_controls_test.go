package writer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestFirstLightConfigAndExclusiveOutput(t *testing.T) {
	config := firstLightConfig{RunID: "run-1", HostEpoch: "epoch-1", SourceCommit: strings.Repeat("a", 40), OutputPath: filepath.Join(t.TempDir(), "raw.ndjson"), Fixtures: 1}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := decodeFirstLightConfig(data); err != nil || decoded != config {
		t.Fatal(decoded, err)
	}
	for _, count := range []int{1, 64, 1000} {
		copy := config
		copy.Fixtures = count
		encoded, _ := json.Marshal(copy)
		if decoded, err := decodeFirstLightConfig(encoded); err != nil || decoded.Fixtures != count {
			t.Fatal("bounded fixture count refused", count, err)
		}
	}
	for _, mutate := range []func(*firstLightConfig){
		func(c *firstLightConfig) { c.Fixtures = 0 }, func(c *firstLightConfig) { c.Fixtures = 1001 }, func(c *firstLightConfig) { c.OutputPath = "relative" },
		func(c *firstLightConfig) { c.SourceCommit = "wrong" }, func(c *firstLightConfig) { c.HostEpoch = "" }, func(c *firstLightConfig) { c.RunID = strings.Repeat("a", 129) },
	} {
		copy := config
		mutate(&copy)
		data, _ := json.Marshal(copy)
		if _, err := decodeFirstLightConfig(data); err == nil {
			t.Fatal("invalid config accepted", copy)
		}
	}
	for _, bad := range [][]byte{nil, append(data, []byte(" {}")...), []byte(strings.Repeat(" ", 16385)), []byte(strings.TrimSuffix(string(data), "}") + `,"budget":1}`), []byte(strings.TrimSuffix(string(data), "}") + `,"fixtures":2}`), []byte(strings.TrimSuffix(string(data), "}") + `,"RUN_ID":"shadow"}`), []byte{255}} {
		if _, err := decodeFirstLightConfig(bad); err == nil {
			t.Fatal("nonclosed config accepted")
		}
	}
	output, err := openFirstLightOutput(config.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := output.WriteString("retained original\n"); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if overwrite, err := openFirstLightOutput(config.OutputPath); err == nil {
		overwrite.Close()
		t.Fatal("output clobbered original")
	}
	retained, err := os.ReadFile(config.OutputPath)
	if err != nil || string(retained) != "retained original\n" {
		t.Fatal(string(retained), err)
	}
}

func TestFirstLightDerivesDurationsBeforeJSONAndRetainsPartial(t *testing.T) {
	start := time.Now()
	attempt, returned, physical := start.Add(time.Millisecond), start.Add(3*time.Millisecond), start.Add(5*time.Millisecond)
	e := &ExecutionEvidence{HoldStartedAt: start, ReturnedAt: physical.Add(time.Millisecond), CommitAttemptedAt: &attempt, CommitReturnedAt: &returned, PhysicalReturnedAt: &physical, Commit: CommitSucceeded,
		Policy: &sqliteconn.Settings{Engine: "component fixture"}, WALBefore: &WALObservation{}, WALAfter: &WALObservation{}}
	hold, commit, err := firstLightDurations(e)
	if err != nil || hold == nil || commit == nil || *hold != int64(5*time.Millisecond) || *commit != int64(2*time.Millisecond) {
		t.Fatal(hold, commit, err)
	}
	record := firstLightRecord{Evidence: e, HoldNS: hold, CommitNS: commit}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var decoded firstLightRecord
	if err := json.Unmarshal(encoded, &decoded); err != nil || *decoded.HoldNS != *hold || *decoded.CommitNS != *commit {
		t.Fatal(err)
	}
	e.Commit = CommitUnknown
	e.WALAfter = nil
	hold, commit, err = firstLightDurations(e)
	if err == nil || hold == nil || commit == nil || *hold != int64(5*time.Millisecond) {
		t.Fatal("partial evidence was erased or became complete", hold, commit, err)
	}
	if hold, commit, err := firstLightDurations(nil); err == nil || hold != nil || commit != nil {
		t.Fatal("missing owner became zero-duration evidence")
	}
}

func TestFirstLightStreamRetainsCommittedErrorAndRejectsStub(t *testing.T) {
	for _, mode := range []string{"committed_error", "stub"} {
		t.Run(mode, func(t *testing.T) {
			clock := time.Now().Add(time.Hour)
			fixture := newEffectiveFirstLightFixture(t, func() time.Time { return clock })
			if mode == "stub" {
				fixture.writer = &Writer{}
			} else {
				if err := fixture.executor.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(fixture.path, fixture.executor.config)
				if err != nil {
					t.Fatal(err)
				}
				fault := &faultConnector{Connector: connector, afterNativeCommit: func() { clock = clock.Add(MaxCommandSafetyHold + time.Millisecond) }}
				installFaultPool(fixture.executor, fault)
				primeEffectiveFixturePool(t, fixture.executor)
			}
			file, err := os.CreateTemp(t.TempDir(), "control-*.ndjson")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			stream := &firstLightStream{file: file, base: firstLightRecord{Label: "component virtual-clock control", Source: firstLightSourceLabel}}
			var restart uint64
			if stream.phase(t.Context(), fixture, 1, "BEGIN", &restart) {
				t.Fatal("incomplete phase succeeded")
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			var record firstLightRecord
			decoder := json.NewDecoder(file)
			if err := decoder.Decode(&record); err != nil {
				t.Fatal(err)
			}
			if record.Complete || record.Error == "" || record.Reference == nil {
				t.Fatal("failure was lost", record)
			}
			if mode == "stub" {
				if record.Evidence != nil || record.HoldNS != nil || !strings.Contains(record.Error, ErrNotImplemented.Error()) {
					t.Fatal("stub fabricated evidence", record)
				}
			} else if record.Result == nil || *record.Result != (EffectiveStageResult{}) || record.Evidence != nil || record.HoldNS != nil || record.CommitNS != nil || record.FrameFenced == nil || !*record.FrameFenced || !strings.Contains(record.Error, ErrOverrun.Error()) {
				t.Fatal("error release leaked a result/proof or lost private fence", record)
			}

			if _, err := closeEffectiveFirstLightFixture(fixture); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFirstLightExplicitOptInCannotSilentlySkip(t *testing.T) {
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, optIn := range []string{"wrong", "1"} {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		command := exec.CommandContext(ctx, program, "-test.run=^TestEffectiveStageFirstLightDirectional$", "-test.count=1", "-test.timeout=8s")
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "PORTICO_WRITER_FIRST_LIGHT=") && !strings.HasPrefix(value, "PORTICO_WRITER_FIRST_LIGHT_CONFIG=") {
				command.Env = append(command.Env, value)
			}
		}
		command.Env = append(command.Env, "PORTICO_WRITER_FIRST_LIGHT="+optIn, "PORTICO_WRITER_FIRST_LIGHT_CONFIG=")
		output, err := command.CombinedOutput()
		cancel()
		var failure *exec.ExitError
		if !errors.As(err, &failure) || !strings.Contains(string(output), "incomplete:") || strings.Contains(string(output), "--- SKIP:") {
			t.Fatal("explicit opt-in did not fail incomplete", optIn, err, string(output))
		}
	}
}

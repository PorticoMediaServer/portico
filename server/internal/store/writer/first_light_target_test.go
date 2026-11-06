package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

const firstLightCooperativeTimeout = 5 * time.Minute
const firstLightRecordVersion = "portico-writer-first-light-ndjson-v1"

type firstLightConfig struct {
	RunID        string `json:"run_id"`
	HostEpoch    string `json:"host_epoch"`
	SourceCommit string `json:"source_commit"`
	OutputPath   string `json:"output_path"`
	Fixtures     int    `json:"fixtures"`
}

func openFirstLightOutput(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}

func decodeFirstLightConfig(data []byte) (firstLightConfig, error) {
	var config firstLightConfig
	if len(data) == 0 || len(data) > 16384 || !utf8.Valid(data) {
		return config, errors.New("incomplete: config size")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return config, errors.New("incomplete: config object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		value, err := decoder.Token()
		if err != nil {
			return config, err
		}
		key, ok := value.(string)
		if !ok || seen[key] {
			return config, errors.New("incomplete: duplicate config field")
		}
		switch key {
		case "run_id", "host_epoch", "source_commit", "output_path", "fixtures":
		default:
			return config, errors.New("incomplete: unknown config field")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return config, err
		}
	}
	if ending, err := decoder.Token(); err != nil || ending != json.Delim('}') {
		return config, errors.New("incomplete: config object end")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return config, errors.New("incomplete: trailing config")
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, err
	}
	if config.RunID == "" || len(config.RunID) > 128 || strings.ContainsRune(config.RunID, 0) || config.HostEpoch == "" || len(config.HostEpoch) > 128 || strings.ContainsRune(config.HostEpoch, 0) ||
		len(config.SourceCommit) != 40 || !filepath.IsAbs(config.OutputPath) || len(config.OutputPath) > 4096 || strings.ContainsRune(config.OutputPath, 0) || config.Fixtures < 1 || config.Fixtures > 64 {
		return config, errors.New("incomplete: config shape")
	}
	if raw, err := hex.DecodeString(config.SourceCommit); err != nil || len(raw) != 20 {
		return config, errors.New("incomplete: declared source commit")
	}
	return config, nil
}

type firstLightRecord struct {
	Version              string                          `json:"version"`
	Kind                 string                          `json:"kind"`
	Label                string                          `json:"label"`
	Source               string                          `json:"source"`
	WCanonicalTestOnly   bool                            `json:"WCanonicalTestOnly"`
	ProductionSealProof  bool                            `json:"ProductionSealProof"`
	Population           string                          `json:"population"`
	RunID                string                          `json:"run_id"`
	HostEpoch            string                          `json:"host_epoch"`
	DeclaredSourceCommit string                          `json:"declared_source_commit"`
	ConfigSHA256         string                          `json:"config_sha256"`
	BinarySHA256         string                          `json:"binary_sha256"`
	ManifestSHA256       string                          `json:"manifest_sha256"`
	Manifest             *firstLightManifest             `json:"manifest"`
	ObservedHost         string                          `json:"observed_host"`
	ObservedBootID       string                          `json:"observed_boot_id"`
	FixtureOrdinal       int                             `json:"fixture_ordinal"`
	DatabaseID           string                          `json:"database_id"`
	DatabasePath         string                          `json:"database_path"`
	Phase                string                          `json:"phase"`
	Reference            *ReceiptReference               `json:"reference"`
	Result               *EffectiveStageResult           `json:"result"`
	Ack                  *AckResult                      `json:"ack"`
	Error                string                          `json:"error"`
	Panic                string                          `json:"panic"`
	Evidence             *ExecutionEvidence              `json:"evidence"`
	HoldNS               *int64                          `json:"hold_ns"`
	CommitNS             *int64                          `json:"commit_ns"`
	CallStartedAt        *time.Time                      `json:"call_started_at"`
	CallReturnedAt       *time.Time                      `json:"call_returned_at"`
	CallUnwoundAt        *time.Time                      `json:"call_unwound_at"`
	HarnessWallNS        *int64                          `json:"harness_wall_ns"`
	FrameFenced          *bool                           `json:"frame_fenced"`
	DispatcherFenced     *bool                           `json:"dispatcher_fenced"`
	CommitAmbiguous      *bool                           `json:"commit_ambiguous"`
	PoolInUse            *int                            `json:"pool_in_use"`
	NativeJoined         *bool                           `json:"native_joined"`
	Restart              *governor.WALRestartObservation `json:"new_restart_observation"`
	CompleteSamples      int                             `json:"complete_samples"`
	Samples              int                             `json:"samples"`
	Complete             bool                            `json:"complete"`
}

type firstLightManifest struct {
	Fixture             string            `json:"fixture"`
	Schema              string            `json:"schema_sha256"`
	Population          string            `json:"population"`
	ProductionQualified bool              `json:"production_qualified"`
	Profile             firstLightProfile `json:"experimental_profile"`
}

type firstLightProfile struct {
	Limit, Setup, Terminal, HeaderSync Work
	Need                               governor.Amounts
	IO                                 []governor.DeviceIO
	Steps                              []firstLightStep
}

type firstLightStep struct {
	ID   string
	Work Work
}

func firstLightDeclaredManifest() firstLightManifest {
	p := effectiveFixturePlan()
	out := firstLightManifest{Fixture: firstLightFixtureVersion, Schema: schema.SHA256, Population: "fresh_zero_wal", ProductionQualified: false,
		Profile: firstLightProfile{Limit: p.recipe.limit, Setup: p.recipe.setup, Terminal: p.recipe.terminal, HeaderSync: p.headerSync, Need: p.need, IO: p.io}}
	for _, step := range p.steps {
		out.Profile.Steps = append(out.Profile.Steps, firstLightStep{string(step.id), step.work})
	}
	return out
}

type firstLightStream struct {
	file *os.File
	base firstLightRecord
	err  error
}

func (s *firstLightStream) append(record firstLightRecord) error {
	if s.err != nil {
		return s.err
	}
	if err := json.NewEncoder(s.file).Encode(record); err != nil {
		s.err = err
		return err
	}
	s.err = s.file.Sync() // outside HOLD, inside the host/cgroup measurement window
	return s.err
}

func firstLightDurations(e *ExecutionEvidence) (hold, commit *int64, err error) {
	if e == nil {
		return nil, nil, errors.New("incomplete: missing owner evidence")
	}
	if e.Policy == nil || e.Policy.Engine == "" || e.ReturnedAt.IsZero() || e.WALBefore == nil || e.WALAfter == nil {
		err = errors.Join(err, errors.New("incomplete: missing policy/return/WAL observation"))
	}
	if e.HoldStartedAt.IsZero() || e.PhysicalReturnedAt == nil {
		err = errors.Join(err, errors.New("incomplete: missing physical boundary"))
	} else {
		h := e.PhysicalReturnedAt.Sub(e.HoldStartedAt).Nanoseconds()
		if h < 0 || (!e.ReturnedAt.IsZero() && e.ReturnedAt.Before(*e.PhysicalReturnedAt)) {
			err = errors.Join(err, errors.New("incomplete: physical time order"))
		} else {
			hold = &h
		}
	}
	if e.CommitAttemptedAt != nil || e.CommitReturnedAt != nil {
		if e.CommitAttemptedAt == nil || e.CommitReturnedAt == nil {
			err = errors.Join(err, errors.New("incomplete: partial COMMIT observation"))
		} else {
			c := e.CommitReturnedAt.Sub(*e.CommitAttemptedAt).Nanoseconds()
			if c < 0 || (!e.HoldStartedAt.IsZero() && e.CommitAttemptedAt.Before(e.HoldStartedAt)) || (e.PhysicalReturnedAt != nil && e.PhysicalReturnedAt.Before(*e.CommitReturnedAt)) {
				err = errors.Join(err, errors.New("incomplete: COMMIT time order"))
			} else {
				commit = &c
			}
		}
	}
	if e.Commit == CommitSucceeded && commit == nil {
		err = errors.Join(err, errors.New("incomplete: missing successful COMMIT interval"))
	}
	if e.Commit == CommitUnknown || e.Commit == 0 {
		err = errors.Join(err, errors.New("incomplete: unknown COMMIT"))
	}
	return hold, commit, err
}

func firstLightCustody(record *firstLightRecord, f *effectiveFirstLightFixture) {
	frame, ambiguous, inUse := f.executor.fenced, f.executor.ambiguous, f.executor.db.Stats().InUse
	record.FrameFenced, record.CommitAmbiguous, record.PoolInUse = &frame, &ambiguous, &inUse
	if f.writer != nil && f.writer.effective != nil {
		dispatcher := f.writer.effective.fenced
		record.DispatcherFenced = &dispatcher
	}
}

func (s *firstLightStream) phase(ctx context.Context, f *effectiveFirstLightFixture, ordinal int, phase string, lastRestart *uint64) (complete bool) {
	record := s.base
	record.Kind = "phase"
	record.FixtureOrdinal = ordinal
	record.DatabaseID = fmt.Sprintf("%s:%d", record.RunID, ordinal)
	record.DatabasePath = f.path
	record.Phase = phase
	request := effectiveReference(f.command)
	record.Reference = &request.Receipt
	defer func() {
		if primary := recover(); primary != nil {
			record.Panic = fmt.Sprint(primary)
			record.Error = "incomplete: API panic"
			record.Complete = false
			unwound := time.Now()
			record.CallUnwoundAt = &unwound
			if record.CallStartedAt != nil {
				wall := unwound.Sub(*record.CallStartedAt).Nanoseconds()
				record.HarnessWallNS = &wall
			}
			firstLightCustody(&record, f)
			_ = s.append(record)
			complete = false
		}
	}()
	started := time.Now()
	record.CallStartedAt = &started
	var callErr error
	switch phase {
	case "BEGIN":
		result, err := f.writer.StageEffective(ctx, f.disclosure, f.command)
		record.Result = &result
		record.Evidence = result.Outcome.Evidence
		callErr = err
	case "RESOLVE":
		result, err := f.writer.ResolveEffective(ctx, f.disclosure, request)
		record.Result = &result
		record.Evidence = result.Outcome.Evidence
		callErr = err
	case "ACK":
		ack, err := f.writer.AckEffective(ctx, f.disclosure, request)
		record.Ack = &ack
		record.Evidence = ack.Evidence
		callErr = err
	default:
		callErr = errors.New("incomplete: unknown phase")
	}
	returned := time.Now()
	record.CallReturnedAt = &returned
	wall := returned.Sub(started).Nanoseconds()
	record.HarnessWallNS = &wall
	firstLightCustody(&record, f)
	var evidenceErr error
	record.HoldNS, record.CommitNS, evidenceErr = firstLightDurations(record.Evidence)
	if phase == "ACK" {
		if record.Ack == nil || record.Ack.AcknowledgedThrough != request.Receipt.Identity.Sequence {
			evidenceErr = errors.Join(evidenceErr, errors.New("incomplete: ACK floor"))
		}
	} else if record.Result == nil || record.Result.Outcome.State != Applied || record.Result.Value.ImageID <= 0 || record.Result.Outcome.Replayed != (phase == "RESOLVE") {
		evidenceErr = errors.Join(evidenceErr, errors.New("incomplete: hidden BEGIN/replay result"))
	}
	if record.Evidence != nil && (record.Evidence.Purpose != EffectiveStage || record.Evidence.Class != governor.Background || record.Evidence.Durability != Normal || record.Evidence.Commit != CommitSucceeded) {
		evidenceErr = errors.Join(evidenceErr, errors.New("incomplete: purpose/class/policy"))
	}
	if phase == "BEGIN" && record.Evidence != nil && record.Evidence.WALBefore != nil {
		before := record.Evidence.WALBefore.State
		if before.Frames != 0 || before.Backfill != 0 || before.ActiveBytes != 0 {
			evidenceErr = errors.Join(evidenceErr, errors.New("incomplete: population is not fresh zero-WAL"))
		}
	}
	if record.DispatcherFenced == nil || *record.FrameFenced || *record.DispatcherFenced || *record.CommitAmbiguous || *record.PoolInUse != 0 {
		evidenceErr = errors.Join(evidenceErr, errors.New("incomplete: fenced/unreturned custody"))
	}
	if f.writer != nil && f.writer.effective != nil {
		restart := f.writer.effective.wal.LastRestartObservation()
		if restart.Sequence != 0 && restart.Sequence != *lastRestart {
			record.Restart = &restart
			*lastRestart = restart.Sequence
		}
	}
	if err := errors.Join(callErr, evidenceErr); err != nil {
		record.Error = err.Error()
	} else {
		record.Complete = true
	}
	if err := s.append(record); err != nil {
		return false
	}
	return record.Complete
}

// Opt-in only. No shipping server can compile this constructor or target.
// SIGINT/SIGTERM or the5-minute cooperative deadline cancel the active call;
// return waits for native custody. A hard test/process kill can only leave a
// partial NDJSON prefix and is never a successful/complete run (use >=7m grace).
func TestEffectiveStageFirstLightDirectional(t *testing.T) {
	optIn := os.Getenv("PORTICO_WRITER_FIRST_LIGHT")
	if optIn == "" {
		t.Skip("opt-in DIRECTIONAL first light")
	}
	if optIn != "1" {
		t.Fatal("incomplete: invalid explicit opt-in")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("incomplete: target requires observed Linux boot identity")
	}
	configPath := os.Getenv("PORTICO_WRITER_FIRST_LIGHT_CONFIG")
	configFile, err := os.Open(configPath)
	if err != nil {
		t.Fatal("incomplete: config", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(configFile, 16385))
	closeErr := configFile.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal("incomplete: config read", err)
	}
	config, err := decodeFirstLightConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	output, err := openFirstLightOutput(config.OutputPath)
	if err != nil {
		t.Fatal("incomplete: exclusive output", err)
	}
	stream := &firstLightStream{file: output}
	complete, samples, attempts, joined, joinKnown := false, 0, 0, true, true
	stream.base = firstLightRecord{Version: firstLightRecordVersion, Label: "DIRECTIONAL", Source: firstLightSourceLabel, WCanonicalTestOnly: true, ProductionSealProof: false, Population: "fresh_zero_wal", RunID: config.RunID, HostEpoch: config.HostEpoch, DeclaredSourceCommit: config.SourceCommit}
	defer func() {
		record := stream.base
		record.Kind = "final"
		record.Samples = attempts
		record.CompleteSamples = samples
		record.Complete = complete && joined && joinKnown && !t.Failed() && samples == config.Fixtures*3 && samples > 0
		if joinKnown {
			record.NativeJoined = &joined
		}
		if !record.Complete {
			record.Error = "incomplete: stopped/failed/missing samples"
		}
		if err := errors.Join(stream.append(record), output.Close()); err != nil {
			t.Error(err)
		}
		if !record.Complete {
			t.Error("DIRECTIONAL first light incomplete; retain NDJSON prefix")
		}
	}()
	configSum := sha256.Sum256(data)
	stream.base.ConfigSHA256 = hex.EncodeToString(configSum[:])
	declaredManifest := firstLightDeclaredManifest()
	stream.base.Manifest = &declaredManifest
	manifest, err := json.Marshal(declaredManifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(manifest)
	stream.base.ManifestSHA256 = hex.EncodeToString(manifestSum[:])
	binary, err := os.Open("/proc/self/exe")
	if err != nil {
		t.Fatal("incomplete: running binary", err)
	}
	hash := sha256.New()
	_, readErr = io.Copy(hash, binary)
	closeErr = binary.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	stream.base.BinarySHA256 = hex.EncodeToString(hash.Sum(nil))
	stream.base.ObservedHost, err = os.Hostname()
	if err != nil || stream.base.ObservedHost == "" {
		t.Fatal("incomplete: observed host", err)
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal("incomplete: boot identity", err)
	}
	stream.base.ObservedBootID = strings.TrimSpace(string(boot))
	if stream.base.ObservedBootID == "" || len(boot) > 128 {
		t.Fatal("incomplete: boot identity shape")
	}
	start := stream.base
	start.Kind = "start"
	if err := stream.append(start); err != nil {
		t.Fatal(err)
	}
	signalContext, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalContext, firstLightCooperativeTimeout)
	defer cancel()
	for ordinal := 1; ordinal <= config.Fixtures; ordinal++ {
		if ctx.Err() != nil {
			break
		}
		fixtureComplete := false
		var fixtureJoined *bool
		var databasePath string
		var reference *ReceiptReference
		before := samples
		ok := t.Run(fmt.Sprintf("fixture_%03d", ordinal), func(t *testing.T) {
			var fixture *effectiveFirstLightFixture
			defer func() {
				if primary := recover(); primary != nil {
					t.Errorf("incomplete fixture panic: %v", primary)
				}
				if fixture != nil {
					value, err := closeEffectiveFirstLightFixture(fixture)
					fixtureJoined = &value
					if err != nil {
						t.Error(err)
					}
				}
			}()
			fixture = newEffectiveFirstLightFixture(t, time.Now)
			databasePath = fixture.path
			original := effectiveReference(fixture.command).Receipt
			reference = &original
			if ctx.Err() != nil {
				return
			}
			var lastRestart uint64
			for _, phase := range []string{"BEGIN", "RESOLVE", "ACK"} {
				if ctx.Err() != nil {
					return
				}
				attempts++
				if !stream.phase(ctx, fixture, ordinal, phase, &lastRestart) {
					return
				}
				samples++
			}
			fixtureComplete = true
		})
		// t.Run's return joins ALL registered setup/fixture cleanup, including a
		// FailNow before the constructor returned its handle to this function.
		closeRecord := stream.base
		closeRecord.Kind = "fixture_close"
		closeRecord.FixtureOrdinal = ordinal
		closeRecord.DatabaseID = fmt.Sprintf("%s:%d", config.RunID, ordinal)
		closeRecord.NativeJoined = fixtureJoined
		closeRecord.DatabasePath = databasePath
		closeRecord.Reference = reference
		closeRecord.Complete = ok && fixtureComplete && fixtureJoined != nil && *fixtureJoined && samples-before == 3
		if !closeRecord.Complete {
			closeRecord.Error = "incomplete fixture; no further command issued"
		}
		if err := stream.append(closeRecord); err != nil {
			t.Error(err)
			break
		}
		if fixtureJoined == nil {
			joinKnown = false
			joined = false
		} else {
			joined = joined && *fixtureJoined
		}
		if !closeRecord.Complete {
			break
		}
	}
	complete = ctx.Err() == nil && samples == config.Fixtures*3 && stream.err == nil
}

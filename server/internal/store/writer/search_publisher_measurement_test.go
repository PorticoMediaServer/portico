package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var publisherQualificationOptIn = flag.Bool("search-publisher-qualification", false, "QL only: real-clock publisher workload; also requires PORTICO_SEARCH_PUBLISHER_QUALIFICATION_CONFIG")

const publisherQualificationConfigEnv = "PORTICO_SEARCH_PUBLISHER_QUALIFICATION_CONFIG"
const publisherMeasurementVersion = "portico-search-publisher-jsonl-v1"

// Each invocation selects ONE closed class. fresh_count excludes warmup.
// background_count adds sealed, physically indexed publisher graphs, never a
// fake catalog count. Qualification-scale execution awaits reviewed merge/QL.
type publisherMeasurementConfig struct {
	RunID           string `json:"run_id"`
	SourceCommit    string `json:"source_commit"`
	HostProfile     string `json:"host_profile"`
	OutputPath      string `json:"output_path"`
	Command         string `json:"command"`
	FreshCount      int    `json:"fresh_count"`
	WarmupCount     int    `json:"warmup_count"`
	BackgroundCount int    `json:"background_count"`
}

func (c publisherMeasurementConfig) purpose() CommandID {
	if c.Command == "SearchFragmentStage" {
		return SearchFragmentStage
	}
	return SearchHeadPublish
}

func decodePublisherMeasurementConfig(data []byte) (publisherMeasurementConfig, error) {
	var c publisherMeasurementConfig
	if len(data) == 0 || len(data) > 16384 {
		return c, errors.New("config must contain 1..16384 bytes")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return c, errors.New("config must be an object")
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return c, err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return c, errors.New("duplicate config key")
		}
		seen[key] = true
		var v json.RawMessage
		if err := d.Decode(&v); err != nil {
			return c, err
		}
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return c, errors.New("config object end")
	}
	if _, err := d.Token(); err != io.EOF {
		return c, errors.New("trailing config")
	}
	d = json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	source, err := hex.DecodeString(c.SourceCommit)
	if err != nil || len(source) != 20 || c.SourceCommit != strings.ToLower(c.SourceCommit) {
		return c, errors.New("source_commit must be full lowercase SHA")
	}
	if c.RunID == "" || len(c.RunID) > 128 || c.HostProfile == "" || len(c.HostProfile) > 256 || strings.ContainsRune(c.RunID+c.HostProfile+c.OutputPath, 0) || !filepath.IsAbs(c.OutputPath) || len(c.OutputPath) > 4096 {
		return c, errors.New("run_id, host_profile and absolute output_path required")
	}
	if c.Command != "SearchFragmentStage" && c.Command != "SearchHeadPublish" {
		return c, errors.New("unknown command class")
	}
	if c.FreshCount < 1 || c.FreshCount > 1000000 || c.WarmupCount < 0 || c.WarmupCount > 10000 || c.FreshCount+c.WarmupCount > 1000000 || c.BackgroundCount < 0 || c.BackgroundCount > 1000000 {
		return c, errors.New("fresh/warmup/background count bounds")
	}
	return c, nil
}

type publisherMeasurementMetadata struct {
	Config              publisherMeasurementConfig `json:"config"`
	ConfigSHA256        string                     `json:"config_sha256"`
	SourceArchiveSHA256 string                     `json:"source_archive_sha256"`
	BinarySHA256        string                     `json:"binary_sha256"`
	CompiledGo          string                     `json:"compiled_go"`
	BuildInfo           *debug.BuildInfo           `json:"build_info"`
	Host                string                     `json:"host"`
	GOOS                string                     `json:"goos"`
	GOARCH              string                     `json:"goarch"`
	GOMAXPROCS          int                        `json:"gomaxprocs"`
	SchemaSHA256        string                     `json:"schema_sha256"`
	CompiledProfile     searchpolicy.Profile       `json:"compiled_profile"`
	CapturedEnvironment plan.Environment           `json:"captured_environment"`
	CaptureSHA256       string                     `json:"capture_sha256"`
	Profile             firstLightProfile          `json:"command_profile"`
	Memory              commandMemoryBudgetExport  `json:"command_memory"`
	Population          map[string]int64           `json:"physical_population_before_binding"`
	PopulationModel     string                     `json:"population_model"`
	StartingWAL         string                     `json:"starting_wal"`
	BodyBoundary        string                     `json:"body_boundary"`
}

type commandMemoryBudgetExport struct{ Preparation, Reserved uint64 }

type publisherMeasurementRecord struct {
	Version           string                        `json:"version"`
	Kind              string                        `json:"kind"`
	RunID             string                        `json:"run_id"`
	Command           string                        `json:"command"`
	Purpose           CommandID                     `json:"purpose"`
	Class             governor.Class                `json:"class"`
	Metadata          *publisherMeasurementMetadata `json:"metadata,omitempty"`
	Warmup            bool                          `json:"warmup"`
	Ordinal           int                           `json:"ordinal"`
	Phase             string                        `json:"phase,omitempty"`
	Request           *PublisherReceiptRequest      `json:"request,omitempty"`
	ImageID           int64                         `json:"image_id,omitempty"`
	FragmentID        int64                         `json:"fragment_id,omitempty"`
	ExpectedFragments int64                         `json:"expected_fragments,omitempty"`
	ExpectedBytes     int64                         `json:"expected_bytes,omitempty"`
	PreparedBytes     uint64                        `json:"prepared_bytes"`
	OfferedAt         *time.Time                    `json:"offered_at,omitempty"`
	ReturnedAt        *time.Time                    `json:"returned_at,omitempty"`
	Outcome           *ReceiptOutcome               `json:"outcome,omitempty"`
	Fragment          *searchFragmentStageResult    `json:"fragment,omitempty"`
	Head              *searchHeadPublishResult      `json:"head,omitempty"`
	Ack               *AckResult                    `json:"ack,omitempty"`
	Evidence          *ExecutionEvidence            `json:"execution_evidence"`
	PrecommitNS       *int64                        `json:"precommit_ns"`
	CommitNS          *int64                        `json:"commit_ns"`
	PhysicalHoldNS    *int64                        `json:"physical_hold_ns"`
	Error             string                        `json:"error,omitempty"`
	Refusal           *Refusal                      `json:"refusal,omitempty"`
	ErrorType         string                        `json:"error_type,omitempty"`
	CallError         string                        `json:"call_error,omitempty"`
	Complete          bool                          `json:"complete"`
	FreshCompleted    int                           `json:"fresh_completed"`
	CallsAttempted    int                           `json:"calls_attempted"`
	NativeJoined      *bool                         `json:"native_joined,omitempty"`
}

// There is no sample/result slice. Every call is flushed before any next offer,
// including failures. Sync and packet preparation are outside the call window.
type publisherMeasurementStream struct {
	file *os.File
	base publisherMeasurementRecord
}

func (s *publisherMeasurementStream) append(r publisherMeasurementRecord) error {
	if err := json.NewEncoder(s.file).Encode(r); err != nil {
		return err
	}
	return s.file.Sync()
}

func publisherMeasurementHash(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Pin the actual clean checkout, not just a supplied label. This runs before
// native setup/binding and cannot substitute SQL inside a command.
func publisherMeasurementSource(ctx context.Context, declared string) (string, error) {
	git := func(args ...string) (string, error) {
		b, err := exec.CommandContext(ctx, "git", args...).Output()
		return strings.TrimSpace(string(b)), err
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil || head != declared {
		return "", fmt.Errorf("source HEAD %q differs from %q: %w", head, declared, err)
	}
	status, err := git("status", "--porcelain", "--untracked-files=normal")
	if err != nil || status != "" {
		return "", fmt.Errorf("source checkout must be clean: %s: %w", status, err)
	}
	cmd := exec.CommandContext(ctx, "git", "archive", "--format=tar", head)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	sum, readErr := publisherMeasurementHash(pipe)
	return sum, errors.Join(readErr, cmd.Wait())
}

func publisherMeasurementOnMini1(host string) bool {
	return strings.Contains(host, "mini-1") || host == "ehler-personal" || host == "mini1"
}

func publisherMeasurementMetadataFor(t *testing.T, c publisherMeasurementConfig, raw []byte, host, sourceHash string, p *publisherPopulation, f *publisherGovernedFixture) publisherMeasurementMetadata {
	t.Helper()
	m := publisherMeasurementMetadata{Config: c, SourceArchiveSHA256: sourceHash, CompiledGo: runtime.Version(), Host: host, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GOMAXPROCS: runtime.GOMAXPROCS(0), SchemaSHA256: schema.SHA256, CompiledProfile: searchpolicy.Compiled(), CapturedEnvironment: f.golden.Environment, Population: p.counts,
		PopulationModel: "uniform title-only native graphs: source/job, 2 images, 1 seal, 2 reclaimers; sealed graphs have 3 fragments in each of 3 FTS indexes and original own-prefix; fixed canonical seed rows included in counts",
		StartingWAL:     "TRUNCATE checkpoint completed before FULL Capture/binding; no per-offer reset or checkpoint",
		BodyBoundary:    "owner exposes hold start and COMMIT attempt, not an independent body boundary; precommit_ns includes acquisition/setup/body"}
	sum := sha256.Sum256(raw)
	m.ConfigSHA256 = hex.EncodeToString(sum[:])
	binary, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	m.BinarySHA256, err = publisherMeasurementHash(binary)
	if err = errors.Join(err, binary.Close()); err != nil {
		t.Fatal(err)
	}
	m.BuildInfo, _ = debug.ReadBuildInfo()
	capture, err := json.Marshal(f.golden)
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(capture)
	m.CaptureSHA256 = hex.EncodeToString(sum[:])
	cp := publisherQualificationPlan(c.purpose(), f.golden.Receipt())
	m.Profile = firstLightProfile{Limit: cp.recipe.limit, Setup: cp.recipe.setup, Terminal: cp.recipe.terminal, HeaderSync: cp.headerSync, Need: cp.need, IO: cp.io}
	for _, step := range cp.steps {
		m.Profile.Steps = append(m.Profile.Steps, firstLightStep{string(step.id), step.work})
	}
	m.Memory = commandMemoryBudgetExport{cp.publisherMemory.preparationBytes, cp.publisherMemory.reservedBytes}
	return m
}

func publisherMeasurementEvidence(e *ExecutionEvidence, purpose CommandID, offered, returned time.Time) (precommit, commit, hold *int64, err error) {
	// Reuse the owner-boundary validator; nil/partial observations stay nil.
	hold, commit, err = firstLightDurations(e)
	if e == nil {
		return
	}
	if e.Purpose != purpose || e.Class != governor.Background || e.Durability != Normal || e.Commit != CommitSucceeded {
		err = errors.Join(err, errors.New("wrong purpose/class/durability or uncertain commit"))
	}
	if e.HoldStartedAt.Before(offered) || e.ReturnedAt.After(returned) {
		err = errors.Join(err, errors.New("owner observations outside real call window"))
	}
	if e.CommitAttemptedAt != nil && !e.HoldStartedAt.IsZero() && !e.CommitAttemptedAt.Before(e.HoldStartedAt) {
		n := e.CommitAttemptedAt.Sub(e.HoldStartedAt).Nanoseconds()
		precommit = &n
	}
	return
}

func publisherMeasurementReference(f *searchFragmentStageCommand, h *searchHeadPublishCommand) (q PublisherReceiptRequest, s searchFragmentStageSnapshot) {
	var identity CommandIdentity
	var digest RequestDigest
	if f != nil {
		s, identity, digest = f.request.snapshot, f.identity, f.digest
	} else {
		s, identity, digest = h.request.snapshot.stage, h.identity, h.digest
	}
	return PublisherReceiptRequest{s.job.id, s.source.libraryID, s.source.entityID, ReceiptReference{identity, digest}}, s
}

// Example (config/output/scratch outside the clean checkout):
//
//	PORTICO_SEARCH_PUBLISHER_QUALIFICATION_CONFIG=/abs/config.json GOTOOLCHAIN=go1.26.10 \
//	  go test -count=1 -timeout=60m -tags=portico_rar7z,portico_cb7 ./internal/store/writer \
//	  -run '^TestSearchPublisherQualification$' -args -search-publisher-qualification
//
// Config: {"run_id":"ql-001","source_commit":"<full checkout HEAD>",
// "host_profile":"<QL host profile>","output_path":"/abs/new.jsonl",
// "command":"SearchFragmentStage","fresh_count":100,"warmup_count":5,
// "background_count":1000}. SearchHeadPublish is the other closed class.
// Large-population qualification requires the owner's reviewed merge gate.
//
// TestSearchPublisherQualification is QL's opt-in runnable target. This helper
// does not establish production qualification, percentiles or passing bounds.
func TestSearchPublisherQualification(t *testing.T) {
	path, configured := os.LookupEnv(publisherQualificationConfigEnv)
	if !*publisherQualificationOptIn && !configured {
		t.Skip("QL opt-in: -search-publisher-qualification and " + publisherQualificationConfigEnv)
	}
	if !*publisherQualificationOptIn || path == "" {
		t.Fatal("both explicit qualification flag and config environment are required")
	}
	if runtime.Version() != "go1.26.10" {
		t.Fatal("requires compiled Go1.26.10, got", runtime.Version())
	}
	if !filepath.IsAbs(path) {
		t.Fatal("config path must be absolute")
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(io.LimitReader(input, 16385))
	if err = errors.Join(err, input.Close()); err != nil {
		t.Fatal(err)
	}
	c, err := decodePublisherMeasurementConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if publisherMeasurementOnMini1(host) && c.FreshCount+c.WarmupCount+c.BackgroundCount >= 100000 {
		t.Fatal("mini-1 native population >=100k is prohibited; use reviewed QL host")
	}
	output, err := os.OpenFile(c.OutputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("exclusive output", err)
	}
	stream := publisherMeasurementStream{file: output, base: publisherMeasurementRecord{Version: publisherMeasurementVersion, RunID: c.RunID, Command: c.Command, Purpose: c.purpose(), Class: governor.Background}}
	complete, completed, attempts := false, 0, 0
	var e *executor
	var joined *bool
	defer func() {
		r := stream.base
		r.Kind = "final"
		r.NativeJoined = joined
		r.FreshCompleted = completed
		r.CallsAttempted = attempts
		r.Complete = complete && !t.Failed() && completed == c.FreshCount && joined != nil && *joined
		if !r.Complete {
			r.Error = "incomplete: retain JSONL prefix; no retry or quantiles"
		}
		if err := errors.Join(stream.append(r), output.Close()); err != nil {
			t.Error(err)
		}
		if !r.Complete {
			t.Error("publisher measurement incomplete")
		}
	}()
	r := stream.base
	r.Kind = "start"
	if err := stream.append(r); err != nil {
		t.Fatal(err)
	}
	sourceHash, err := publisherMeasurementSource(t.Context(), c.SourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	// Keep setup under a subtest so its FailNow still joins native cleanup and
	// leaves the parent's final incomplete record. No commands survive in arrays.
	if !t.Run("native_population_and_calls", func(t *testing.T) {
		p := preparePublisherPopulation(t, c.purpose(), c.FreshCount+c.WarmupCount, c.BackgroundCount)
		e = p.e
		// Observe the FIRST close result before idempotent registered cleanup;
		// a later no-op close must never turn a failed join into evidence.
		defer func() {
			closeErr := e.close()
			ok := closeErr == nil
			joined = &ok
			if closeErr != nil {
				t.Error(closeErr)
			}
		}()
		firstF, firstH, err := p.packet(0, c.purpose(), 0)
		if err != nil {
			t.Fatal(err)
		}
		f := bindPublisherGovernedFixture(t, e, c.purpose(), firstF, firstH, true)
		m := publisherMeasurementMetadataFor(t, c, raw, host, sourceHash, p, f)
		r := stream.base
		r.Kind = "metadata"
		r.Metadata = &m
		if err := stream.append(r); err != nil {
			t.Fatal(err)
		}
		census := publisherCallCensus{}
		defer func() { completed, attempts = census.FreshCompleted, census.CallsAttempted }()
		if err := runPublisherMeasurementCalls(t, c, p, f, &stream, stream.base, &census); err != nil {
			t.Fatal("stopped at first incomplete original; retain call record:", err)
		}
		complete = true
	}) {
		return
	}
}

// Default functional smoke: virtual clock, real native graph/dispatcher/receipt
// protocol. It emits no measurement record and makes no latency claim.
func TestSearchPublisherMeasurementSmoke(t *testing.T) {
	for _, purpose := range []CommandID{SearchFragmentStage, SearchHeadPublish} {
		t.Run(fmt.Sprint(purpose), func(t *testing.T) {
			p := preparePublisherPopulation(t, purpose, 2, 1)
			var matched int
			if err := p.e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM search_fragment_lexical WHERE search_fragment_lexical MATCH '東京' AND rowid=?`, publisherPopulationID(2)+6).Scan(&matched); err != nil || matched != 1 {
				t.Fatal("background native FTS posting missing", matched, err)
			}
			fragment, head, err := p.packet(0, purpose, 0)
			if err != nil {
				t.Fatal(err)
			}
			f := bindPublisherGovernedFixture(t, p.e, purpose, fragment, head, false)
			for n := 0; n < 2; n++ {
				fragment, head, err := p.packet(n, purpose, n)
				if err != nil {
					t.Fatal(err)
				}
				q, s := publisherMeasurementReference(fragment, head)
				var nativeInc []byte
				if err := p.e.db.QueryRowContext(t.Context(), `SELECT metadata_incarnation FROM catalog_entities WHERE id=?`, q.EntityID).Scan(&nativeInc); err != nil || !reflect.DeepEqual(nativeInc, s.source.incarnation[:]) {
					t.Fatal("native source witness", err)
				}
				cap := publisherFixtureCapability(t, f.w, q)
				out, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, q, purpose, fragment, head, false)
				if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.Outcome.Reference != q.Receipt {
					t.Fatal("fresh", out, err)
				}
				replay, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, q, purpose, nil, nil, false)
				if err != nil || !replay.Outcome.Replayed || replay.Fragment != out.Fragment || replay.Head != out.Head {
					t.Fatal("saved replay", replay, err)
				}
				_, ack, err := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, q, purpose, nil, nil, true)
				if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence || ack.Evidence == nil || f.a.Usage() != ([4]governor.Amounts{}) || p.e.db.Stats().InUse != 0 {
					t.Fatal("ACK/custody", ack, err)
				}
			}
		})
	}
}

func TestSearchPublisherMeasurementConfig(t *testing.T) {
	c := publisherMeasurementConfig{RunID: "functional", SourceCommit: strings.Repeat("a", 40), HostProfile: "functional-only", OutputPath: filepath.Join(t.TempDir(), "calls.jsonl"), Command: "SearchHeadPublish", FreshCount: 1, BackgroundCount: 1000000}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decodePublisherMeasurementConfig(data); err != nil || got != c {
		t.Fatal(got, err)
	}
	// Two material controls: accidental duplicate config, destructive overwrite.
	duplicate := append([]byte(`{"fresh_count":2,`), data[1:]...)
	if _, err := decodePublisherMeasurementConfig(duplicate); err == nil {
		t.Fatal("duplicate config accepted")
	}
	f, err := os.OpenFile(c.OutputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if again, err := os.OpenFile(c.OutputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
		again.Close()
		t.Fatal("overwrote evidence")
	}
}

// Exercise the real-clock CLI and source/output binding with one original per
// class. This is functional only: a genuine deadline/quantum overrun is retained
// as incomplete evidence, never converted into a passing timing sample.
func TestSearchPublisherRealClockSurface(t *testing.T) {
	if runtime.Version() != "go1.26.10" {
		t.Skip("real-clock surface uses the pinned Go1.26.10 runner")
	}
	head, err := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{"SearchFragmentStage", "SearchHeadPublish"} {
		t.Run(class, func(t *testing.T) {
			dir := t.TempDir()
			c := publisherMeasurementConfig{RunID: "unmerged-functional-real-clock", SourceCommit: strings.TrimSpace(string(head)), HostProfile: "functional-only-not-qualification", OutputPath: filepath.Join(dir, "calls.jsonl"), Command: class, FreshCount: 1, BackgroundCount: 1}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(dir, "config.json")
			if err := os.WriteFile(config, data, 0600); err != nil {
				t.Fatal(err)
			}
			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSearchPublisherQualification$", "-test.timeout=2m", "-search-publisher-qualification")
			for _, v := range os.Environ() {
				if !strings.HasPrefix(v, publisherQualificationConfigEnv+"=") {
					child.Env = append(child.Env, v)
				}
			}
			child.Env = append(child.Env, publisherQualificationConfigEnv+"="+config)
			log, runErr := child.CombinedOutput()
			file, err := os.Open(c.OutputPath)
			if err != nil {
				t.Fatal(string(log), err)
			}
			defer file.Close()
			// Keep the exact prefix, including a real timing failure, before
			// test scratch is removed. The runner owns this evidence directory.
			if evidenceDir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidenceDir != "" {
				if err := os.MkdirAll(evidenceDir, 0700); err != nil {
					t.Fatal(err)
				}
				saved, err := os.CreateTemp(evidenceDir, "publisher-real-clock-"+class+"-*.jsonl")
				if err != nil {
					t.Fatal(err)
				}
				_, copyErr := io.Copy(saved, file)
				_, seekErr := file.Seek(0, io.SeekStart)
				if err := errors.Join(copyErr, saved.Close(), seekErr); err != nil {
					t.Fatal(err)
				}
			}
			d := json.NewDecoder(file)
			var calls int
			var failed *publisherMeasurementRecord
			var final publisherMeasurementRecord
			for {
				var r publisherMeasurementRecord
				if err := d.Decode(&r); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if r.Kind == "call" {
					calls++
					if !r.Complete {
						v := r
						failed = &v
					}
				}
				if r.Kind == "final" {
					final = r
				}
			}
			if calls == 0 || final.Kind != "final" {
				t.Fatal("target never reached real dispatcher", string(log))
			}
			if runErr == nil {
				if !final.Complete || final.FreshCompleted != 1 || calls != 3 {
					t.Fatal("false completion", final, calls)
				}
				return
			}
			if final.Complete || failed == nil || failed.Error == "" {
				t.Fatal("lost incomplete call", final, string(log))
			}
			if !strings.Contains(failed.Error, ErrQuantum.Error()) && !strings.Contains(failed.Error, ErrOverrun.Error()) && !strings.Contains(failed.Error, context.DeadlineExceeded.Error()) {
				t.Fatal("unexpected real-clock execution failure", failed.Error, string(log))
			}
			t.Logf("retained directional functional timing failure; NOT qualification: %s", failed.Error)
		})
	}
}

// Test-only record sinks hold values only; they never run in writer custody.
type publisherRecordSink interface {
	append(publisherMeasurementRecord) error
}
type publisherCallCensus struct{ FreshCompleted, CallsAttempted int }

func runPublisherMeasurementCalls(t *testing.T, c publisherMeasurementConfig, p *publisherPopulation, f *publisherGovernedFixture, stream publisherRecordSink, base publisherMeasurementRecord, census *publisherCallCensus) error {
	t.Helper()
	e := p.e
	for n := 0; n < c.FreshCount+c.WarmupCount; n++ {
		fragment, head, err := p.packet(n, c.purpose(), n)
		if err != nil {
			return err
		}
		q, s := publisherMeasurementReference(fragment, head)
		cap := publisherFixtureCapability(t, f.w, q)
		var original searchPublisherCommandResult
		for _, phase := range []string{"fresh", "resolve", "ack"} {
			r := base
			r.Kind = "call"
			r.Ordinal = n
			r.Warmup = n < c.WarmupCount
			r.Phase = phase
			r.Request = &q
			r.ImageID = s.image.id
			r.ExpectedFragments = s.seal.expectedFragments
			r.ExpectedBytes = s.seal.expectedBytes
			if fragment != nil {
				r.PreparedBytes = fragment.payload.bytes
			} else {
				r.PreparedBytes = uint64(len(head.request.old.prefix) + len(head.request.old.initial) + len(head.request.next.prefix) + len(head.request.next.initial))
			}
			ff, hh := fragment, head
			if phase != "fresh" {
				ff, hh = nil, nil
			}
			offered := time.Now()
			if starts, ok := stream.(interface {
				beforeCall(publisherMeasurementRecord) error
			}); ok {
				if err := starts.beforeCall(r); err != nil {
					return err
				}
			}
			census.CallsAttempted++
			offered = time.Now() // preserve original facade timing; fsync is pre-custody
			out, ack, callErr := f.w.effective.runSearchPublisher(t.Context(), f.w, cap, q, c.purpose(), ff, hh, phase == "ack")
			returned := time.Now()
			r.OfferedAt, r.ReturnedAt = &offered, &returned
			r.Outcome, r.Fragment, r.Head, r.Ack = &out.Outcome, &out.Fragment, &out.Head, &ack
			r.Evidence = out.Outcome.Evidence
			if phase == "ack" {
				r.Evidence = ack.Evidence
			}
			r.PrecommitNS, r.CommitNS, r.PhysicalHoldNS, err = publisherMeasurementEvidence(r.Evidence, c.purpose(), offered, returned)
			if callErr != nil {
				r.CallError = callErr.Error()
				r.ErrorType = fmt.Sprintf("%T", callErr)
				errors.As(callErr, &r.Refusal)
			}
			err = errors.Join(callErr, err)
			if phase == "fresh" {
				original = out
			}
			if phase == "ack" {
				if ack.AcknowledgedThrough != q.Receipt.Identity.Sequence {
					err = errors.Join(err, errors.New("ACK floor differs from original"))
				}
			} else if out.Outcome.State != Applied || out.Outcome.Reference != q.Receipt || out.Outcome.Replayed != (phase == "resolve") {
				err = errors.Join(err, errors.New("missing exact applied original or saved replay"))
			}
			if phase == "resolve" && (out.Fragment != original.Fragment || out.Head != original.Head) {
				err = errors.Join(err, errors.New("saved result differs from fresh original"))
			}
			r.FragmentID = original.Fragment.FragmentID
			if f.a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 || e.fenced || e.ambiguous || f.w.effective.fenced {
				err = errors.Join(err, errors.New("physical custody not cleanly returned"))
			}
			r.Complete = err == nil
			if err != nil {
				r.Error = err.Error()
			}
			if appendErr := stream.append(r); appendErr != nil {
				return appendErr
			}
			if err != nil {
				return err
			}
		}
		if n >= c.WarmupCount {
			census.FreshCompleted++
		}
	}
	return nil
}

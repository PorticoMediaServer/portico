package writer

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Closed test targets only. No CLI callback/SQL/profile/grant opens a binding.
// QL owns the ONE canonical 1M seed and its provenance/physical attachment.
const canonicalHeldSchema = "portico-canonical-held-class-v1"
const canonicalHeldItems = 1000000

var canonicalHeldEnabled = flag.Bool("canonical-held", false, "opt-in canonical 1M per-class target; missing owner bindings fail")
var errCanonicalHeldBinding = errors.New("canonical held target unavailable: source-bound seed/owner Action required")

type canonicalHeldConfig struct {
	RunID             string `json:"run_id"`
	HostEpoch         string `json:"host_epoch"`
	SourceCommit      string `json:"source_commit"`
	OutputPath        string `json:"output_path"`
	Class             string `json:"class"`
	Profile           string `json:"profile"`
	Attempts          int    `json:"attempts"`
	ArtifactDirectory string `json:"artifact_directory"`
	ManifestSHA256    string `json:"manifest_sha256"`
}

func decodeCanonicalHeldConfig(raw []byte) (canonicalHeldConfig, error) {
	var c canonicalHeldConfig
	if len(raw) == 0 || len(raw) > 16384 {
		return c, errors.New("bounded canonical config required")
	}
	// Reject duplicate fields as well as unknown fields; pins are not last-wins.
	d := json.NewDecoder(strings.NewReader(string(raw)))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return c, errors.New("canonical config object required")
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return c, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return c, errors.New("duplicate canonical config field")
		}
		seen[name] = true
		var v json.RawMessage
		if err := d.Decode(&v); err != nil {
			return c, err
		}
	}
	if _, err := d.Token(); err != nil {
		return c, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, errors.New("trailing canonical config")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return c, err
	}
	if c.RunID == "" || len(c.RunID) > 128 || c.HostEpoch == "" || len(c.HostEpoch) > 256 || !filepath.IsAbs(c.OutputPath) || len(c.SourceCommit) != 40 || c.Attempts < 10000 || c.Attempts > 1000000 {
		return c, errors.New("canonical identity/output or10000..1000000 planned originals invalid")
	}
	for _, b := range c.SourceCommit {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return c, errors.New("full lower-hex source commit required")
		}
	}
	if c.Profile != "standard" && c.Profile != "legacy8" && c.Profile != "ram2gb" && c.Profile != "reference" {
		return c, errors.New("closed host profile required")
	}
	if c.ArtifactDirectory != "" && !filepath.IsAbs(c.ArtifactDirectory) {
		return c, errors.New("declared canonical artifact directory must be absolute")
	}
	if c.ManifestSHA256 != "" {
		if len(c.ManifestSHA256) != 64 {
			return c, errors.New("trusted producer manifest pin requires64hex")
		}
		for _, b := range c.ManifestSHA256 {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				return c, errors.New("trusted producer manifest pin requires lower hex")
			}
		}
	}
	if _, err := canonicalHeldClassFor(c.Class); err != nil {
		return c, err
	}
	return c, nil
}

// Closed compiled runner variants keep workload imports outside writer (the
// Actions themselves import writer). Neither variant is selectable by a factory
// or callback from config; external parents receive only opaque typed drivers.
type canonicalClassRun interface{ isCanonicalClassRun() }
type canonicalInternalRun func(*testing.T, context.Context, canonicalHeldConfig, *n1Stream, QualificationPopulationManifest) error

func (canonicalInternalRun) isCanonicalClassRun() {}

// This runner owns attachment at its actual child constructor, rather than
// leaving a parent-t attachment unconsumed when setup is a contained subtest.
type canonicalFixtureRun func(*testing.T, context.Context, canonicalHeldConfig, *n1Stream) error

func (canonicalFixtureRun) isCanonicalClassRun() {}

type canonicalWCFactory func(*testing.T, context.Context, canonicalHeldConfig, *n1Stream, QualificationPopulationManifest) *CanonicalWCComponent

func (canonicalWCFactory) isCanonicalClassRun() {}

type canonicalHeldClass struct {
	name, owner, kind string
	purpose           *CommandID // compiled INTENT only, never actual grant/policy evidence
	reason            string
	run               canonicalClassRun
}

// The merged transport owns the next canonical clone for this exact t.
func attachCanonicalHeldPopulation(t *testing.T, cfg canonicalHeldConfig) QualificationPopulationManifest {
	t.Helper()
	m, err := readQualificationPopulation(cfg.ArtifactDirectory, cfg.ManifestSHA256)
	if err != nil || m.Facts.Residents != canonicalHeldItems {
		t.Fatal("canonical target requires exact1M artifact", err)
	}
	return UseQualificationPopulation(t, cfg.ArtifactDirectory, cfg.ManifestSHA256)
}

func canonicalHeldClasses() []canonicalHeldClass {
	n1, basic, enrich, progress, security, fragment, flip, reclaim := InventoryAccept, BasicAccept, EffectiveStage, ProgressAccept, SecurityAccept, SearchFragmentStage, SearchHeadPublish, ProgressReceiptReclaim
	return []canonicalHeldClass{
		{name: "inventory_n1", owner: "SC/W", kind: "normal_write", purpose: &n1, run: canonicalInternalRun(runCanonicalHeldN1), reason: "timing cohort does not expose complete observed statement-byte totals; inspect nullable per-attempt profile facts"},
		{name: "basic_accept", owner: "SC/W", kind: "normal_write", purpose: &basic, reason: "canonical Basic owner Action not attached"},
		{name: "staged_enrich", owner: "EN/W", kind: "normal_write", purpose: &enrich, reason: "canonical staged-enrich owner Action not attached"},
		{name: "progress", owner: "WC", kind: "normal_write", purpose: &progress, run: canonicalWCFactory(newCanonicalWCComponent), reason: "source-bound WC progress; complete observed counters remain nullable"},
		{name: "security", owner: "WC", kind: "normal_write", purpose: &security, run: canonicalWCFactory(newCanonicalWCComponent), reason: "source-bound WC FULL security; complete observed counters remain nullable"},
		{name: "wc_reclaim", owner: "WC", kind: "normal_write", purpose: &reclaim, run: canonicalWCFactory(newCanonicalWCComponent), reason: "source-bound WC retained Progress receipt reclaim; not publisher reclaim"},
		{name: "publisher_fragment", owner: "PB", kind: "normal_write", purpose: &fragment, run: canonicalFixtureRun(runCanonicalPublisher), reason: "PB fixture/loop attached; sustained keeper composition remains explicitly gated"},
		{name: "publisher_flip", owner: "PB", kind: "normal_write", purpose: &flip, run: canonicalFixtureRun(runCanonicalPublisher), reason: "PB fixture/loop attached; sustained keeper composition remains explicitly gated"},
		{name: "checkpoint", owner: "GV", kind: "maintenance", reason: "canonical governed checkpointer owner Action not attached; no normal-write budget"},
		{name: "mp.queue_head", owner: "P2-MP", kind: "foreground_read", reason: "required reviewed/merged MediaReader/ConsumeForeground +canonical device-owned queue fixture not attached; never a writer HOLD/COMMIT"},
		{name: "mp.session", owner: "P2-MP", kind: "foreground_read", reason: "required reviewed/merged SessionHead/ConsumeForeground +canonical session fixture not attached"},
		{name: "mp.preparation", owner: "P2-MP", kind: "foreground_read", reason: "required reviewed/merged PreparedJob/ConsumeForeground +canonical prepared-job fixture not attached"},
		{name: "uo.personal", owner: "P2-UO", kind: "foreground_read", reason: "merged ForegroundReader.PersonalState/UOPersonalDefinitions available; canonical profile/current-authority/source-publication/ConsumeForeground composition not attached"},
		{name: "rb.sorted", owner: "P2-RB", kind: "foreground_read", reason: "required reviewed/merged SortedWindow +actual RBBrowseDefinitions Capture and canonical source-order cohorts not attached"},
		{name: "rb.chronology", owner: "P2-RB", kind: "foreground_read", reason: "required reviewed/merged ChronologyWindow +actual RBChronologyDefinitions Capture and dated/NULL/content cohorts not attached"},
		{name: "rb.numeric_advance", owner: "P2-RB", kind: "foreground_read", reason: "canonical library7 full cold NumericFacetPreparation.Advance1024 /original F04 lifetime composition/ConsumeForeground and source-bound Capture not attached"},
		{name: "rb.numeric_first_page", owner: "P2-RB", kind: "foreground_read", reason: "canonical library7 full first exact numeric Page /fresh LIVE/publication/cache handoff composition/ConsumeForeground and source-bound Capture not attached"},
		{name: "rb.numeric_cached_page", owner: "P2-RB", kind: "foreground_read", reason: "canonical library7 full cached exact numeric Page /original expiry and fresh authorization composition/ConsumeForeground and source-bound Capture not attached"},
		{name: "rb.group_window", owner: "P2-RB", kind: "foreground_read", reason: "canonical library7 full RBGroupWindow ten-family64raw /source-order witness composition/ConsumeForeground and source-bound Capture not attached"},
		{name: "rb.relationship_exact", owner: "P2-RB", kind: "foreground_read", reason: "canonical library7 full RBRelationshipExact complete<=256 /cap+1 zero ErrNotReady composition/ConsumeForeground and source-bound Capture not attached"},
		{name: "rb.resolution", owner: "P2-RB", kind: "foreground_read", reason: "canonical library7 full ResolutionWindow128fetched/64consumed/40output /NULL-zero continuation composition/ConsumeForeground and source-bound Capture not attached"},
		{name: "rs.listening", owner: "P2-RS", kind: "foreground_read", reason: "canonical library7 full RSChildrenReader.Window512raw /native lookup+Go sort+fresh publication composition/ConsumeForeground and source-bound Capture not attached"},
		{name: "IA-membership-audio-artist", owner: "P2-IA", kind: "foreground_read", reason: "IAAudioMembership/artist: reviewed native API, exact IAAudioHarnessDefinitions Capture and canonical library7 trusted audio fixture/ConsumeForeground composition not attached"},
		{name: "IA-membership-audio-album", owner: "P2-IA", kind: "foreground_read", reason: "IAAudioMembership/album: reviewed native API, exact IAAudioHarnessDefinitions Capture and canonical library7 trusted audio fixture/ConsumeForeground composition not attached"},
		{name: "IA-membership-audio-song", owner: "P2-IA", kind: "foreground_read", reason: "IAAudioMembership/song: reviewed native API, exact IAAudioHarnessDefinitions Capture and canonical library7 trusted audio fixture/ConsumeForeground composition not attached"},
		{name: "IA-membership-audiobook-book", owner: "P2-IA", kind: "foreground_read", reason: "IAAudioMembership/book: reviewed native API, exact IAAudioHarnessDefinitions Capture and canonical library7 trusted audio fixture/ConsumeForeground composition not attached"},
		{name: "IA-membership-audiobook-file", owner: "P2-IA", kind: "foreground_read", reason: "IAAudioMembership/audiobook_file: reviewed native API, exact IAAudioHarnessDefinitions Capture and canonical library7 trusted audio fixture/ConsumeForeground composition not attached"},
	}
}
func canonicalHeldClassFor(name string) (canonicalHeldClass, error) {
	for _, c := range canonicalHeldClasses() {
		if c.name == name {
			return c, nil
		}
	}
	return canonicalHeldClass{}, errors.New("unknown closed canonical class")
}
func canonicalHeldMissing(c canonicalHeldClass) []string {
	missing := []string{}
	if c.run == nil {
		missing = append(missing, c.owner+": "+c.reason)
	}
	return missing
}
func canonicalHeldIntent(c canonicalHeldClass) any {
	if c.purpose == nil {
		return nil
	}
	descriptor, err := Describe(*c.purpose)
	if err != nil {
		return nil
	}
	return descriptor
}
func canonicalHeldStart(t *testing.T, class string) (canonicalHeldConfig, *n1Stream, canonicalHeldClass) {
	t.Helper()
	if !*canonicalHeldEnabled {
		if os.Getenv("PORTICO_CANONICAL_HELD") != "" {
			t.Fatal("canonical-held companion flag required")
		}
		t.Skip("opt-in source-bound canonical per-class target")
	}
	if os.Getenv("PORTICO_CANONICAL_HELD") != "1" {
		t.Fatal("explicit canonical-held flag requires PORTICO_CANONICAL_HELD=1")
	}
	file, err := os.Open(os.Getenv("PORTICO_CANONICAL_HELD_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, 16385))
	if err := errors.Join(readErr, file.Close()); err != nil {
		t.Fatal(err)
	}
	cfg, err := decodeCanonicalHeldConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Class != class {
		t.Fatal("selected parent/class/config mismatch")
	}
	output, err := openFirstLightOutput(cfg.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	handedOff := false
	defer func() {
		if !handedOff {
			_ = output.Close()
		}
	}()
	stream := &n1Stream{file: output, origin: time.Now(), base: map[string]any{"schema": canonicalHeldSchema, "label": "CANONICAL PER-CLASS RAW TARGET", "qualification": false, "qualified_profile_ready": false, "run_id": cfg.RunID, "host_epoch": cfg.HostEpoch, "declared_source_commit": cfg.SourceCommit, "profile": cfg.Profile, "class": class, "process_epoch": fmt.Sprintf("%s/%d/%d", cfg.RunID, os.Getpid(), time.Now().UnixNano())}}
	definition, _ := canonicalHeldClassFor(class)
	if err := emitCanonicalHeldPrefix(stream, cfg, definition); err != nil {
		t.Fatal(err)
	}
	if definition.run == nil {
		t.Fatal(errCanonicalHeldBinding)
	}
	handedOff = true
	return cfg, stream, definition
}
func canonicalHeldRun(t *testing.T, class string) {
	t.Helper()
	cfg, stream, definition := canonicalHeldStart(t, class)
	defer stream.file.Close()
	if run, ok := definition.run.(canonicalFixtureRun); ok {
		if err := run(t, t.Context(), cfg, stream); err != nil {
			t.Fatal(err)
		}
		return
	}
	run, ok := definition.run.(canonicalInternalRun)
	if !ok {
		t.Fatal("external WC parent required")
	}
	manifest := attachCanonicalHeldPopulation(t, cfg)
	if err := run(t, t.Context(), cfg, stream, manifest); err != nil {
		t.Fatal(err)
	}
}
func emitCanonicalHeldPrefix(stream *n1Stream, cfg canonicalHeldConfig, definition canonicalHeldClass) error {
	var normalP99, quietMax, errorCeiling *int64
	if definition.kind == "normal_write" {
		a, b, c := int64(10*time.Millisecond), int64(25*time.Millisecond), int64(100*time.Millisecond)
		normalP99, quietMax, errorCeiling = &a, &b, &c
	}
	if err := stream.append("start", map[string]any{"owner": definition.owner, "target_kind": definition.kind, "compiled_intent": canonicalHeldIntent(definition), "actual_class": nil, "actual_durability": nil, "requested_originals": cfg.Attempts, "expected_library_items": canonicalHeldItems, "actual_library_items": nil, "actual_library_items_unavailable_reason": "canonical seed has not been attached/observed", "declared_artifact_directory": cfg.ArtifactDirectory, "declared_manifest_sha256": cfg.ManifestSHA256, "expected_library_numeric_id": 7, "expected_library_key": "qualification-movies", "expected_root": "/qualification-media", "expected_shape": "canonical-movies-v1", "observed_statement_bytes": nil, "statement_bytes_unavailable_reason": definition.reason, "normal_hosted_p99_ns": normalP99, "quiet_max_ns": quietMax, "rare_error_ceiling_ns": errorCeiling, "maintenance_budget_ns": nil, "maintenance_budget_unavailable_reason": "owner-supplied maintenance budget required; never normal25ms", "physical_owner_intervals": nil, "read_recipe_intent": canonicalReadRecipeFor(definition.name), "reads_are_writer_holds": false, "phase1a_lease_enabled": false}); err != nil {
		return err
	}
	missing := canonicalHeldMissing(definition)
	for _, reason := range missing {
		if err := stream.append("failed_binding", map[string]any{"reason": reason, "hold_ns": nil, "commit_ns": nil, "queue_ns": nil, "original_reference": nil, "actual_physical_id": nil}); err != nil {
			return err
		}
	}
	// Each missing owner binding remains an explicit nonpassing target.
	if len(missing) > 0 {
		if err := stream.append("final", map[string]any{"complete": false, "native_calls_started": 0, "report": nil, "missing_bindings": missing, "qualification": false}); err != nil {
			return err
		}
		return fmt.Errorf("%w: %s", errCanonicalHeldBinding, strings.Join(missing, "; "))
	}

	return nil
}

func TestCanonicalHeldInventoryN1(t *testing.T)       { canonicalHeldRun(t, "inventory_n1") }
func TestCanonicalHeldBasic(t *testing.T)             { canonicalHeldRun(t, "basic_accept") }
func TestCanonicalHeldStagedEnrich(t *testing.T)      { canonicalHeldRun(t, "staged_enrich") }
func TestCanonicalHeldPublisherFragment(t *testing.T) { canonicalHeldRun(t, "publisher_fragment") }
func TestCanonicalHeldPublisherFlip(t *testing.T)     { canonicalHeldRun(t, "publisher_flip") }
func TestCanonicalHeldCheckpoint(t *testing.T)        { canonicalHeldRun(t, "checkpoint") }
func TestCanonicalQueueHeadRead(t *testing.T)         { canonicalHeldRun(t, "mp.queue_head") }

func TestCanonicalSessionHeadRead(t *testing.T) { canonicalHeldRun(t, "mp.session") }
func TestCanonicalPreparationRead(t *testing.T) { canonicalHeldRun(t, "mp.preparation") }

func TestCanonicalRBSortedRead(t *testing.T)     { canonicalHeldRun(t, "rb.sorted") }
func TestCanonicalRBChronologyRead(t *testing.T) { canonicalHeldRun(t, "rb.chronology") }

func TestCanonicalRBNumericAdvance(t *testing.T)    { canonicalHeldRun(t, "rb.numeric_advance") }
func TestCanonicalRBNumericFirstPage(t *testing.T)  { canonicalHeldRun(t, "rb.numeric_first_page") }
func TestCanonicalRBNumericCachedPage(t *testing.T) { canonicalHeldRun(t, "rb.numeric_cached_page") }
func TestCanonicalRBGroupWindow(t *testing.T)       { canonicalHeldRun(t, "rb.group_window") }
func TestCanonicalRBRelationshipExact(t *testing.T) { canonicalHeldRun(t, "rb.relationship_exact") }
func TestCanonicalRBResolution(t *testing.T)        { canonicalHeldRun(t, "rb.resolution") }
func TestCanonicalRSListening(t *testing.T)         { canonicalHeldRun(t, "rs.listening") }

func TestCanonicalUOPersonalRead(t *testing.T) { canonicalHeldRun(t, "uo.personal") }

func TestCanonicalIAAudioArtistRead(t *testing.T) { canonicalHeldRun(t, "IA-membership-audio-artist") }
func TestCanonicalIAAudioAlbumRead(t *testing.T)  { canonicalHeldRun(t, "IA-membership-audio-album") }
func TestCanonicalIAAudioSongRead(t *testing.T)   { canonicalHeldRun(t, "IA-membership-audio-song") }
func TestCanonicalIAAudiobookBookRead(t *testing.T) {
	canonicalHeldRun(t, "IA-membership-audiobook-book")
}
func TestCanonicalIAAudiobookFileRead(t *testing.T) {
	canonicalHeldRun(t, "IA-membership-audiobook-file")
}

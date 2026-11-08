package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"os"
	"runtime"
	"testing"
	"time"
)

var errCanonicalPublisherSustained = errors.New("canonical publisher sustained composition unavailable: actual funded same-DB keeper or its pressure-recovery hook not attached; WAL refusals are harness gaps")

func canonicalPublisherConfig(c canonicalHeldConfig) (publisherMeasurementConfig, string, error) {
	command, recipe := "SearchFragmentStage", "canonical-publisher-fragment-title-overlay-v1"
	switch c.Class {
	case "publisher_fragment":
	case "publisher_flip":
		command, recipe = "SearchHeadPublish", "canonical-publisher-head-title-overlay-v1"
	default:
		return publisherMeasurementConfig{}, "", errCanonicalHeldBinding
	}
	// Closed first recipe: eight warmup graphs, requested measured graphs, ZERO
	// additional background graphs. Canonical1M is a distinct catalogue baseline,
	// not one million physically indexed publisher graphs.
	p := publisherMeasurementConfig{RunID: c.RunID, SourceCommit: c.SourceCommit, HostProfile: c.Profile, OutputPath: c.OutputPath, Command: command, FreshCount: c.Attempts, WarmupCount: 8, BackgroundCount: 0}
	raw, err := json.Marshal(p)
	if err != nil {
		return p, recipe, err
	}
	_, err = decodePublisherMeasurementConfig(raw)
	return p, recipe, err
}

type canonicalPublisherSummary struct {
	Calls              publisherCallCensus  `json:"calls"`
	NativeJoined       *bool                `json:"native_joined"`
	Usage              *[4]governor.Amounts `json:"authority_usage_after_close"`
	CheckpointCalls    *int                 `json:"checkpoint_calls"`
	CheckpointComplete *int                 `json:"checkpoint_complete"`
	CheckpointOverflow *bool                `json:"checkpoint_overflow"`
	Complete           bool                 `json:"complete"`
	Error              string               `json:"error"`
}

type canonicalPublisherSink struct {
	stream          *n1Stream
	fixture         *publisherGovernedFixture
	calls, physical int
}

func (s *canonicalPublisherSink) ids(r publisherMeasurementRecord) (string, string) {
	original := fmt.Sprintf("%s/publisher-original/%d", s.stream.base["process_epoch"], r.Ordinal+1)
	return original, original + "/" + r.Phase
}
func (s *canonicalPublisherSink) beforeCall(r publisherMeasurementRecord) error {
	original, call := s.ids(r)
	return s.stream.append("attempt_start", map[string]any{"original_id": original, "attempt_id": call, "offered_original_ordinal": r.Ordinal + 1, "turn_role": r.Phase, "original_population": publisherCanonicalPopulation(r), "requested_reference": r.Request, "scope": "before dispatcher custody; original phase/reference retained even if invocation never returns"})
}
func (s *canonicalPublisherSink) afterOriginal() error {
	if s.fixture.maintenance != nil {
		return s.fixture.maintenance.drain(s.stream)
	}
	return errCanonicalPublisherSustained
}

func publisherCanonicalPopulation(r publisherMeasurementRecord) string {
	if r.Warmup {
		return "warmup"
	}
	return "measured"
}
func (s *canonicalPublisherSink) append(r publisherMeasurementRecord) error {
	s.calls++
	original, call := s.ids(r)
	clock := n1Clock{}
	if r.OfferedAt != nil {
		clock.InvocationStartedNS = n1NS(s.stream.origin, *r.OfferedAt)
	}
	if r.ReturnedAt != nil {
		clock.InvocationReturnedNS = n1NS(s.stream.origin, *r.ReturnedAt)
	}
	if r.OfferedAt != nil && r.ReturnedAt != nil {
		clock.InvocationNS = n1Duration(*r.OfferedAt, *r.ReturnedAt)
	}
	if e := r.Evidence; e != nil {
		clock.HoldStartedNS = n1NS(s.stream.origin, e.HoldStartedAt)
		if e.PhysicalReturnedAt != nil {
			clock.PhysicalReturnedNS = n1NS(s.stream.origin, *e.PhysicalReturnedAt)
			clock.HoldNS = n1Duration(e.HoldStartedAt, *e.PhysicalReturnedAt)
		}
		if e.CommitAttemptedAt != nil {
			clock.CommitAttemptedNS = n1NS(s.stream.origin, *e.CommitAttemptedAt)
		}
		if e.CommitReturnedAt != nil {
			clock.CommitReturnedNS = n1NS(s.stream.origin, *e.CommitReturnedAt)
		}
		if e.CommitAttemptedAt != nil && e.CommitReturnedAt != nil {
			clock.CommitNS = n1Duration(*e.CommitAttemptedAt, *e.CommitReturnedAt)
		}
	}
	var window *n1OwnerWindow
	if s.fixture != nil && r.OfferedAt != nil && r.ReturnedAt != nil {
		if view, ok := s.fixture.e.lastOwnerWindow(); ok {
			window = n1BindOwnerWindowForPurpose(view, s.stream.origin, *r.OfferedAt, *r.ReturnedAt, r.Purpose)
		}
	}
	var ordinal *int
	if (r.Evidence != nil && clock.HoldNS != nil && *clock.HoldNS > 0) || (window != nil && window.SetupComplete) {
		s.physical++
		value := s.physical
		ordinal = &value
	}
	return s.stream.append("attempt", map[string]any{"original_id": original, "attempt_id": call, "offered_original_ordinal": r.Ordinal + 1, "call_ordinal": s.calls, "actual_physical_attempt_ordinal": ordinal, "turn_role": r.Phase, "original_population": publisherCanonicalPopulation(r), "requested_reference": r.Request, "owner_clock": clock, "private_owner_window": window, "publisher": r, "scope": "original PB raw packet; missing physical phases stay null, no failed public Evidence resurrection or later Resolve reconstruction"})
}

func runCanonicalPublisher(t *testing.T, ctx context.Context, cfg canonicalHeldConfig, stream *n1Stream) (primary error) {
	t.Helper()
	c, recipe, err := canonicalPublisherConfig(cfg)
	if err != nil {
		return err
	}
	result := canonicalPublisherSummary{}
	defer func() {
		result.Error = checkpointError(primary)
		primary = errors.Join(primary, stream.append("final", map[string]any{"complete": result.Complete, "recipe": recipe, "publisher": result, "report": nil, "qualification": false, "qualified_profile_ready": false}))
	}()
	if runtime.Version() != "go1.26.10" {
		return errors.New("canonical publisher requires Go1.26.10")
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	if publisherMeasurementOnMini1(host) {
		return errors.New("canonical1M native publisher population prohibited on mini-1; use reviewed QL host")
	}
	sourceHash, err := publisherMeasurementSource(ctx, c.SourceCommit)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	// Attachment belongs to THIS child, which invokes the actual constructor.
	// A child Fatal still returns to the parent's durable incomplete final.
	if !t.Run("native_population_and_calls", func(owner *testing.T) {
		manifest := attachCanonicalHeldPopulation(owner, cfg)
		primary = runCanonicalPublisherOwner(owner, c, recipe, manifest, stream, raw, host, sourceHash, &result)
	}) {
		primary = errors.Join(primary, errors.New("publisher fixture failed; retain raw prefix"))
	}
	return primary
}

func runCanonicalPublisherOwner(t *testing.T, c publisherMeasurementConfig, recipe string, manifest QualificationPopulationManifest, stream *n1Stream, raw []byte, host, sourceHash string, result *canonicalPublisherSummary) (primary error) {
	t.Helper()
	if err := stream.append("preparation_start", map[string]any{"recipe": recipe, "manifest": manifest, "requested_measured_originals": c.FreshCount, "requested_warmup_originals": c.WarmupCount, "requested_background_graphs": c.BackgroundCount, "scope": "same-t canonical constructor, then unchanged PB fixture overlay; not offered command/HOLD count"}); err != nil {
		return err
	}
	p := preparePublisherPopulation(t, c.purpose(), c.FreshCount+c.WarmupCount, c.BackgroundCount)
	var authority *governor.Authority
	var maintenance *canonicalN1MaintenanceOwner
	defer func() {
		var closeErr error
		if maintenance != nil {
			closeErr = maintenance.close() // same stop/join -> W.Retired -> keeper shutdown chain
			primary = errors.Join(primary, maintenance.drain(stream))
			started, completed, overflow := maintenance.started, maintenance.completed, maintenance.overflow
			result.CheckpointCalls, result.CheckpointComplete, result.CheckpointOverflow = &started, &completed, &overflow
			if overflow {
				primary = errors.Join(primary, errors.New("publisher checkpoint ledger overflow"))
			}
		} else {
			closeErr = p.e.close()
		} // preserve first actual close result
		p.e.physicalFaults.mu.Lock()
		fault := p.e.physicalFaults.failed
		p.e.physicalFaults.mu.Unlock()
		stats := p.e.db.Stats()
		ok := closeErr == nil && p.e.closed && !fault && stats.InUse == 0 && stats.OpenConnections == 0
		if authority != nil {
			usage := authority.Usage()
			result.Usage = &usage
			ok = ok && usage == ([4]governor.Amounts{})
		} else {
			ok = false
		}
		result.NativeJoined = &ok
		primary = errors.Join(primary, closeErr)
		if !ok {
			primary = errors.Join(primary, errors.New("publisher physical close/retirement incomplete"))
		}
		result.Complete = primary == nil && ok && result.Calls.FreshCompleted == c.FreshCount && result.Calls.CallsAttempted == 3*(c.FreshCount+c.WarmupCount)
	}()
	firstF, firstH, err := p.packet(0, c.purpose(), 0)
	if err != nil {
		return err
	}
	// Reuse N1's attested opening after the ONE seeded executor joins. Same
	// canonical copy/native path; no new opening implementation or SQL reset.
	path := publisherFixturePath(t, p.e)
	seedConfig := p.e.config
	if err := p.e.close(); err != nil {
		return err
	}
	p.e.physicalFaults.mu.Lock()
	failed := p.e.physicalFaults.failed
	p.e.physicalFaults.mu.Unlock()
	if stats := p.e.db.Stats(); failed || stats.InUse != 0 || stats.OpenConnections != 0 {
		return errors.New("publisher seed executor did not physically join")
	}
	opening := openScanFixture(t, path, scanOpeningOwner(t, path))
	p.e = opening.executor
	if p.e.config != seedConfig {
		return errors.New("publisher reopen changed actual connection config")
	}
	spec := canonicalPublisherMaintenance()
	f := bindPublisherGovernedFixture(t, p.e, c.purpose(), firstF, firstH, true, spec)
	authority = f.a
	maintenance = newCanonicalCheckpointOwner(t, opening, f.w.effective.governor, f.a, f.w.effective.wal, path, canonicalCheckpointShape{c.purpose(), "canonical-publisher-cp", spec.policy.HardBytes})
	f.maintenance = maintenance
	// Pressure recovery is optional trusted composition. Attach the SAME funded
	// keeper so this stream recovers before admission instead of refusing on
	// observed WAL state. Caps, serial order and the maintenance partition are
	// unchanged; this is the WC component's existing wiring.
	f.w.effective.maintenance = &checkpointMaintenance{request: maintenance.request("passive"), results: maintenance.results}
	metadata := publisherMeasurementMetadataFor(t, c, raw, host, sourceHash, p, f)
	if err := stream.append("population_attached", map[string]any{"recipe": recipe, "publisher_metadata": metadata, "canonical_manifest_residents": manifest.Facts.Residents, "native_census_before_graphs": p.baseline, "native_census_after_graphs": p.counts, "prepared_measured_graphs": c.FreshCount, "prepared_warmup_graphs": c.WarmupCount, "prepared_background_graphs": c.BackgroundCount, "catalogue_scope": "canonical baseline library7 plus unchanged PB fixed seed and uniform title-only graph owners in library3, SAME database; no exact1M total or1M dense publisher claim", "consumer": "not exercised by held publisher class", "seed_connection_config": seedConfig, "accepting_connection_config": p.e.config, "standalone_max_grants_per_class": 2, "actual_max_grants_per_class": f.grantLimit, "grant_limit_scope": "lead-approved N1 test-only keeper configuration: global2-to4 metadata slots; publisher stream remains serial, command/WAL/resource caps unchanged; no production profile authority", "maintenance_policy": spec.policy, "maintenance_additional_partition": spec.need}); err != nil {
		return err
	}
	if p.baseline["catalog_entities"] < int64(manifest.Facts.Residents) {
		return errors.New("native baseline smaller than attached canonical population")
	}
	// Initial PASSIVE and the worker use the SAME actual g/a/WAL/native binding;
	// failed/refused maintenance remains raw and stops the run without a retry.
	if gap := canonicalPublisherSustainedGate(f); gap != nil {
		if err := stream.append("failed_binding", map[string]any{"reason": gap.Error(), "native_calls_started": 0, "hold_ns": nil, "commit_ns": nil, "scope": "no accepting command attempted; constructor census is not throughput"}); err != nil {
			return err
		}
		return gap
	}
	maintenance.results <- maintenance.step(t.Context())
	if err := maintenance.drain(stream); err != nil {
		return err
	}
	maintenance.start(t.Context())
	sink := &canonicalPublisherSink{stream: stream, fixture: f}
	base := publisherMeasurementRecord{Version: publisherMeasurementVersion, RunID: c.RunID, Command: c.Command, Purpose: c.purpose(), Class: governor.Background}
	return runPublisherMeasurementCalls(t, c, p, f, sink, base, &result.Calls)
}

func canonicalPublisherSustainedGate(f *publisherGovernedFixture) error {
	if f == nil || f.e == nil || f.maintenance == nil || f.w == nil || f.w.effective == nil ||
		f.w.effective.maintenance == nil || f.e.keeper.Load() == nil {
		return errCanonicalPublisherSustained
	}
	return f.e.keeper.Load().Check()
}

var _ publisherRecordSink = (*canonicalPublisherSink)(nil)

func canonicalPublisherMaintenance() governedFixtureMaintenance {
	policy := publisherFixtureWALPolicy()
	policy.ConcurrentPassive = true
	policy.CheckpointMaxHold = 5 * time.Second
	return governedFixtureMaintenance{policy: policy, need: governor.Amounts{governor.Reader: 2, governor.MemoryBytes: 8 << 20, governor.CPUMilli: 2000, governor.IOBytes: 4 * policy.HardBytes}}
}

package writer

import (
	"context"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/walobserve"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opaque TEST-ONLY composition for the external workload.Action parents. Only
// existing sealed facades/drivers cross this boundary, never SQL/native/grants.
type CanonicalWCComponent struct {
	Writer                                      *Writer
	Progress                                    *WCProgressDriver
	Security                                    *WCSecurityDriver
	Reclaim                                     *WCReclaimDriver
	Class                                       string
	Attempts                                    int
	Warmup                                      int
	stream                                      *n1Stream
	manifest                                    QualificationPopulationManifest
	keeper                                      *canonicalN1MaintenanceOwner
	trace                                       *n1Trace
	target                                      string
	currentID, population, operation            string
	offerOrdinal, physicalOrdinal, observations int
	observedNative                              uint64
	measured, warmup, auxiliary                 int
	accepted                                    bool
	closed                                      bool
	failed                                      error
	lastProgress                                ProgressSnapshot
	lineage                                     any
	variants                                    map[string]int
	purpose                                     CommandID
	offers                                      *canonicalOfferSchedule
	turnsAtFirstOffer                           uint64
}

type CanonicalWCObservation struct {
	Phase             string
	Started, Returned *time.Time
	Evidence          *ExecutionEvidence
	Requested         *ReceiptReference
	Outcome           *ReceiptOutcome
	Error             string
	Refused           bool
	Raw               any
}

func NewCanonicalWCComponent(t *testing.T, class string) *CanonicalWCComponent {
	t.Helper()
	cfg, stream, definition := canonicalHeldStart(t, class)
	factory, ok := definition.run.(canonicalWCFactory)
	if !ok {
		_ = stream.file.Close()
		t.Fatal("closed external WC parent required")
	}
	manifest := attachCanonicalHeldPopulation(t, cfg)
	return factory(t, t.Context(), cfg, stream, manifest)
}
func newCanonicalWCComponent(t *testing.T, ctx context.Context, cfg canonicalHeldConfig, stream *n1Stream, manifest QualificationPopulationManifest) *CanonicalWCComponent {
	return newCanonicalWCFixtureComponent(t, ctx, cfg, stream, manifest, 8)
}

// Functional controls use the identical composition against a tiny artifact;
// neither this helper nor its synthetic recipe is a canonical1M measurement.
func CanonicalWCFunctionalComponent(t *testing.T, class string) *CanonicalWCComponent {
	t.Helper()
	if value := os.Getenv("PORTICO_CANONICAL_WC_FUNCTIONAL"); value == "" {
		t.Skip("opt-in real-clock canonical WC/funded-keeper component control")
	} else if value != "1" {
		t.Fatal("PORTICO_CANONICAL_WC_FUNCTIONAL must be1")
	}
	definition, err := canonicalHeldClassFor(class)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := definition.run.(canonicalWCFactory); !ok {
		t.Fatal("closed WC class required")
	}
	dir, pin := canonicalN1ControlArtifact(t)
	outputDirectory := t.TempDir()
	if evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidence != "" {
		if !filepath.IsAbs(evidence) {
			t.Fatal("absolute functional evidence directory required")
		}
		if err := os.MkdirAll(evidence, 0700); err != nil {
			t.Fatal(err)
		}
		outputDirectory = evidence
	}
	streamFile, err := openFirstLightOutput(filepath.Join(outputDirectory, class+"-wc-functional.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	stream := &n1Stream{file: streamFile, origin: time.Now(), base: map[string]any{"schema": canonicalHeldSchema, "process_epoch": "FUNCTIONAL_ONLY/" + class, "qualification": false}}
	manifest := UseQualificationPopulation(t, dir, pin)
	return newCanonicalWCFixtureComponent(t, t.Context(), canonicalHeldConfig{Class: class, Attempts: 3}, stream, manifest, 1)
}
func newCanonicalWCFixtureComponent(t *testing.T, ctx context.Context, cfg canonicalHeldConfig, stream *n1Stream, manifest QualificationPopulationManifest, warmup int) *CanonicalWCComponent {
	t.Helper()
	c := &CanonicalWCComponent{Class: cfg.Class, Attempts: cfg.Attempts, Warmup: warmup, stream: stream, manifest: manifest, trace: newN1Trace(stream.origin, false), variants: map[string]int{}}
	offers, scheduleErr := newCanonicalOfferSchedule(cfg.OfferedRate, nil, nil)
	if scheduleErr != nil {
		t.Fatal(scheduleErr)
	}
	c.offers = offers
	t.Cleanup(func() {
		if !c.closed {
			if err := c.Finish(errors.New("parent exited before explicit joined finish")); err != nil && !t.Failed() {
				t.Error(err)
			}
		}
	})
	// The next canonical fixture clone belongs to EXACTLY this t. The real birth
	// checks the actual persisted public ID through wc.fixture.existing_catalog.
	catalog := wcProgressExistingCatalog{7, manifest.Facts.FirstID, "qualification-movies", qualificationMovieBlob("QMP1", manifest.Facts.Seed, 1)}
	tier := "member"
	if cfg.Class == "security" {
		tier = "owner"
	}
	w, pd, _, keeper := wcComponentFixture(t, wcFixtureOptions{kind: cfg.Class, actorTier: tier, extraMemory: 2 << 20, maxGrants: 5, catalog: &catalog, decorate: c.trace.decorate, maintenance: true, realClock: true})
	c.Writer, c.Progress, c.keeper = w, pd, keeper
	w.effective.maintenance = &checkpointMaintenance{request: keeper.request("passive"), results: keeper.results}
	// The ONE class start: the gate refuses a class that is not kit-registered or
	// that lacks its keeper recovery hook, pressure-recovery hook or WAL policy.
	binding, err := beginClassTarget(t, cfg.Class, classTargetParts{Writer: w, Keeper: keeper})
	if err != nil {
		t.Fatal(err)
	}
	c.trace.useRegistry(w.effective.registry)
	switch cfg.Class {
	case "progress":
		c.purpose = ProgressAccept
	case "security":
		c.purpose = SecurityAccept
		c.Security = wcSecurityDriverForFixture(t, w, pd)
	case "wc_reclaim":
		c.purpose = ProgressReceiptReclaim
		c.Reclaim = wcReclaimForProgressFixture(t, w, pd)
	default:
		t.Fatal("unknown compiled WC class")
	}
	c.trace.useRegistry(w.effective.registry)
	if err := stream.append("population_attached", map[string]any{"manifest": manifest, "recipe": c.Recipe(), "database_id": fmt.Sprintf("%s/wc-db1", stream.base["process_epoch"]), "native_file_binding": map[string]any{"path": keeper.opening.binding.path, "device": keeper.opening.binding.device, "inode": keeper.opening.binding.inode}, "requested_warmup_originals": c.Warmup, "profile_scope": "unchanged WC synthetic component command profile; separately funded keeper/CP; not production issuer", "library_numeric_id": catalog.libraryID, "library_key": catalog.libraryKey, "subject_id": catalog.subjectID, "actual_native_catalog_binding_checked": true, "initial_fixture_native_begin_attempts": c.trace.nativeBegins, "retained_preparation_deadline": pd.expected.Deadline, "nullable_native_scope": "timing cohort: attribution DBStatus/VM/WALIO snapshots omitted inside HOLD; examined rows/pages/xSync/device attribution/aggregate LIVE peak unavailable, never default0", "initial_fixture_interval_scope": "native birth/registration precedes sampled originals; no measured HOLD inferred for setup", "maintenance_policy": canonicalN1Maintenance().policy, "maintenance_additional_partition": canonicalN1Maintenance().need, "class_target_kit": true, "wal_policy": binding.WAL.Policy, "wal_policy_explicit": binding.WAL.Explicit, "consumer": "not exercised by a held-command class", "qualification": false}); err != nil {
		t.Fatal(err)
	}
	c.keeper.results <- c.keeper.step(ctx)
	if err := c.keeper.drain(stream); err != nil {
		t.Fatal(err)
	}
	c.keeper.start(ctx)
	c.observedNative = c.trace.nativeBegins
	c.trace.nextTurn()
	c.trace.armed = true
	return c
}
func (c *CanonicalWCComponent) Recipe() string {
	return "canonical-wc-" + c.Class + "-original-chain-v1"
}

// PaceOffer waits for the next original's OPEN-LOOP scheduled instant (no wait
// when the offered rate is unlimited) and returns it for the Offer. Call it once
// before each original, outside every command HOLD.
func (c *CanonicalWCComponent) PaceOffer(ctx context.Context) (time.Time, error) {
	if c.offers == nil {
		return time.Now(), nil
	}
	if c.offers.n == 0 {
		c.turnsAtFirstOffer = c.trace.nativeBegins
	}
	return c.offers.wait(ctx)
}
func (c *CanonicalWCComponent) IsPending() bool       { return c.lastProgress.Pending }
func (c *CanonicalWCComponent) ReportSequence() int64 { return c.lastProgress.LastAcceptedSequence + 1 }
func (c *CanonicalWCComponent) Target() string        { return c.target }
func (c *CanonicalWCComponent) RecordProgressInput(sequence int64, position, duration float64, state string) error {
	return c.stream.append("input", map[string]any{"original_id": c.currentID, "operation": c.operation, "declared_report_sequence": sequence, "position_seconds": position, "duration_seconds": duration, "state": state, "scope": "offered synthetic report data, not observed native acceptance or authority"})
}

// Real native births, outside measured originals. Each target is DISTINCT and
// receives one fresh revoke; already-revoked families are never throughput.
func (c *CanonicalWCComponent) NextSecurityTarget(t *testing.T, ordinal int) error {
	if c.failed != nil {
		return c.failed
	}
	if c.Security == nil {
		return ErrInvalid
	}
	id := fmt.Sprintf("%s/target-birth/%d", c.stream.base["process_epoch"], ordinal)
	if err := c.stream.append("preparation_start", map[string]any{"preparation_id": id, "scope": "actual fresh native target birth; excluded from measured security originals"}); err != nil {
		return err
	}
	c.trace.nextTurn()
	c.trace.armed = true
	before := c.trace.nativeBegins
	start := time.Now()
	target, err := wcSecurityTargetForFixture(t, c.Writer, c.Progress, "member", fmt.Sprintf("canonical-target-%d", ordinal))
	returned := time.Now()
	c.target = target
	var window *n1OwnerWindow
	if view, ok := c.Writer.effective.executor.lastOwnerWindow(); ok {
		window = n1BindOwnerWindowForPurpose(view, c.stream.origin, start, returned, ProgressAccept)
	}
	c.failed = errors.Join(c.failed, err, c.stream.append("preparation", map[string]any{"preparation_id": id, "scope": "actual target birth and joined preparation grant retirement; no public Evidence manufactured", "started_ns": n1NS(c.stream.origin, start), "returned_ns": n1NS(c.stream.origin, returned), "native_begins_before": before, "native_begins_after": c.trace.nativeBegins, "owner_window": window, "native_units": c.trace.units[:c.trace.used], "hold_ns": nil, "public_evidence": nil, "target_family": target, "error": checkpointError(err)}))
	if c.trace.overflow {
		c.failed = errors.Join(c.failed, errors.New("target birth observation overflow"))
	}
	c.observedNative = c.trace.nativeBegins
	c.observedNative = c.trace.nativeBegins
	c.trace.nextTurn()
	c.trace.armed = true
	return c.failed
}
func (c *CanonicalWCComponent) Begin(population, operation string, purpose CommandID) error {
	if c.closed || c.failed != nil {
		return errors.Join(ErrClosed, c.failed)
	}
	c.offerOrdinal++
	c.currentID = fmt.Sprintf("%s/original/%d", c.stream.base["process_epoch"], c.offerOrdinal)
	c.population, c.operation, c.purpose = population, operation, purpose
	c.lineage = nil
	c.observeLineage()
	c.accepted = false
	c.observedNative = c.trace.nativeBegins
	c.trace.nextTurn()
	c.trace.armed = true
	return c.stream.append("attempt_start", map[string]any{"original_id": c.currentID, "offered_ordinal": c.offerOrdinal, "original_population": population, "operation": operation, "requested_purpose": purpose, "input_reference": nil, "reference_unavailable_reason": "sealed by the actual owner during Action preparation; join later calls by original_id"})
}
func (c *CanonicalWCComponent) Observe(v CanonicalWCObservation) error {
	c.observations++
	clock := n1Clock{}
	if v.Started != nil {
		clock.InvocationStartedNS = n1NS(c.stream.origin, *v.Started)
	}
	if v.Returned != nil {
		clock.InvocationReturnedNS = n1NS(c.stream.origin, *v.Returned)
	}
	if v.Started != nil && v.Returned != nil {
		clock.InvocationNS = n1Duration(*v.Started, *v.Returned)
	}
	if e := v.Evidence; e != nil {
		clock.HoldStartedNS = n1NS(c.stream.origin, e.HoldStartedAt)
		if e.PhysicalReturnedAt != nil {
			clock.PhysicalReturnedNS = n1NS(c.stream.origin, *e.PhysicalReturnedAt)
			clock.HoldNS = n1Duration(e.HoldStartedAt, *e.PhysicalReturnedAt)
		}
		if e.CommitAttemptedAt != nil {
			clock.CommitAttemptedNS = n1NS(c.stream.origin, *e.CommitAttemptedAt)
		}
		if e.CommitReturnedAt != nil {
			clock.CommitReturnedNS = n1NS(c.stream.origin, *e.CommitReturnedAt)
		}
		if e.CommitAttemptedAt != nil && e.CommitReturnedAt != nil {
			clock.CommitNS = n1Duration(*e.CommitAttemptedAt, *e.CommitReturnedAt)
		}
	}
	var window *n1OwnerWindow
	var diagnostic any
	if v.Started != nil && v.Returned != nil {
		if view, ok := c.Writer.effective.executor.lastOwnerWindow(); ok {
			window = n1BindOwnerWindowForPurpose(view, c.stream.origin, *v.Started, *v.Returned, c.purpose)
			if window != nil {
				var actual any
				var verified *bool
				if view.policyVerified {
					actual = view.settings
					value := true
					verified = &value
				}
				diagnostic = map[string]any{"physical_state": view.state, "policy_verified": verified, "actual_settings": actual, "scope": "PRIVATE call-bound copied owner observation; not public Evidence"}
			}
		}
	}
	began := int(c.trace.nativeBegins - c.observedNative)
	c.observedNative = c.trace.nativeBegins
	var physical *int
	if began > 0 {
		c.physicalOrdinal = int(c.trace.nativeBegins)
		n := c.physicalOrdinal
		physical = &n
	}
	if began > 1 {
		c.failed = errors.Join(c.failed, errors.New("multiple native turns inside one public observation; physical coverage unavailable"))
	}
	if c.trace.overflow {
		c.failed = errors.Join(c.failed, errors.New("native observation ledger overflow"))
	}
	if v.Error != "" {
		c.failed = errors.Join(c.failed, fmt.Errorf("%s %s %s: %s", c.currentID, c.operation, v.Phase, v.Error))
	}
	if v.Refused {
		c.failed = errors.Join(c.failed, errors.New("actual Action refusal"))
	}
	if v.Error == "" {
		class, classErr := commandClass(c.purpose)
		wantSync := 1
		wantDurability := Normal
		if c.purpose == SecurityAccept {
			wantSync = 2
			wantDurability = Full
		}
		e := v.Evidence
		if classErr != nil || e == nil || e.Purpose != c.purpose || e.Class != class || e.Durability != wantDurability || e.Policy == nil || e.Policy.Synchronous != wantSync || e.Commit != CommitSucceeded || clock.HoldNS == nil || *clock.HoldNS <= 0 || v.Started == nil || v.Returned == nil || e.HoldStartedAt.Before(*v.Started) || e.PhysicalReturnedAt == nil || e.PhysicalReturnedAt.After(*v.Returned) || began != 1 || c.trace.activeRows != 0 {
			c.failed = errors.Join(c.failed, errors.New("physical interval/class/durability binding unavailable"))
		}

	}
	if v.Phase == "ACCEPT" {
		if c.accepted || v.Outcome == nil || v.Requested == nil || v.Outcome.State != Applied || v.Outcome.Replayed || v.Outcome.Reference != *v.Requested {
			c.failed = errors.Join(c.failed, ErrResolveRequired)
		} else {
			c.accepted = true
		}
	}
	c.observeLineage()
	walStarted := time.Now()
	wal, walErr := walobserve.Observe(c.Writer.effective.path, c.Writer.effective.wal)
	walReturned := time.Now()
	err := c.stream.append("attempt", map[string]any{"original_id": c.currentID, "offered_ordinal": c.offerOrdinal, "original_population": c.population, "operation": c.operation, "call_id": fmt.Sprintf("%s/call/%d", c.currentID, c.observations), "phase": v.Phase, "actual_physical_attempt_ordinal": physical, "native_begins_in_call": began, "requested_reference": v.Requested, "owner_clock": clock, "owner_window": window, "private_diagnostic": diagnostic, "public_evidence": v.Evidence, "raw": v.Raw, "capture_lineage": c.lineage, "native_units": c.trace.units[:c.trace.used], "unit_ledger_overflow": c.trace.overflow, "wal_after": wal, "wal_error": checkpointError(walErr), "wal_observer_started_ns": n1NS(c.stream.origin, walStarted), "wal_observer_returned_ns": n1NS(c.stream.origin, walReturned), "wal_scope": "post-physical-return advisory probe may overlap concurrent CP; not atomic/reset/admission/command IO attribution proof", "error": v.Error, "qualification": false})
	c.trace.nextTurn()
	c.trace.armed = true
	return errors.Join(c.failed, err)
}
func (c *CanonicalWCComponent) ProgressSnapshot(snapshot ProgressSnapshot) { c.lastProgress = snapshot }
func (c *CanonicalWCComponent) End(err error) error {
	c.failed = errors.Join(c.failed, err)
	if c.failed == nil && !c.accepted {
		c.failed = ErrResolveRequired
	}
	if c.failed == nil {
		switch c.population {
		case "measured":
			c.measured++
			c.variants[c.operation]++
		case "warmup":
			c.warmup++
		default:
			c.auxiliary++
		}
	}
	c.failed = errors.Join(c.failed, c.keeper.drain(c.stream))
	return c.failed
}
func (c *CanonicalWCComponent) Finish(primary error) error {
	if c.closed {
		return c.failed
	}
	c.failed = errors.Join(c.failed, primary)
	c.trace.armed = false
	if c.keeper != nil {
		c.keeper.joinWorker()
	}
	if c.Reclaim != nil {
		c.failed = errors.Join(c.failed, c.Reclaim.Close(context.Background()))
	}
	if c.Security != nil {
		c.failed = errors.Join(c.failed, c.Security.Close(context.Background()))
	}
	if c.Progress != nil {
		c.failed = errors.Join(c.failed, c.Progress.Close(context.Background()))
	}
	if c.keeper != nil {
		c.failed = errors.Join(c.failed, c.keeper.close(), c.keeper.drain(c.stream))
	}
	var usage any
	joined := false
	if c.Writer != nil {
		r := c.Writer.effective
		u := r.authority.Usage()
		usage = u
		stats := r.executor.db.Stats()
		r.executor.physicalFaults.mu.Lock()
		fault := r.executor.physicalFaults.failed
		r.executor.physicalFaults.mu.Unlock()
		joined = r.executor.closed && stats.InUse == 0 && stats.OpenConnections == 0 && !fault && c.trace.activeRows == 0 && u == ([4]governor.Amounts{})
	}
	complete := c.failed == nil && joined && c.measured == c.Attempts && c.warmup == c.Warmup && c.keeper != nil && !c.keeper.overflow && !c.trace.closeOverflow
	c.failed = errors.Join(c.failed, c.stream.append("final", map[string]any{"complete": complete, "recipe": c.Recipe(), "measured_originals_complete": c.measured, "measured_originals_by_variant": c.variants, "warmup_originals_complete": c.warmup, "auxiliary_originals_complete": c.auxiliary, "all_offered_originals": c.offerOrdinal, "known_actual_physical_turns": c.trace.nativeBegins, "offered_rate": c.offerReport(), "physical_attempt_ordinal_scope": "one actual W native BEGIN-attempt stream, includes fixture/auxiliary and failed attempts; no success renumbering", "native_closes": c.trace.closes[:c.trace.closeCount], "native_close_overflow": c.trace.closeOverflow, "authority_usage_after_close": usage, "native_joined": joined, "error": checkpointError(c.failed), "report": nil, "qualification": false, "qualified_profile_ready": false}))
	c.closed = true
	c.failed = errors.Join(c.failed, c.stream.file.Close())
	if !complete && c.failed == nil {
		c.failed = errors.New("WC class incomplete")
	}
	return c.failed
}

func (c *CanonicalWCComponent) observeLineage() {
	if c.Progress == nil {
		return
	}
	if p := c.Progress.pending.Load(); p != nil {
		var reference any
		if p.root.capture.Receipt.Identity.Sequence > 0 {
			reference = p.root.capture.Receipt
		}
		c.lineage = map[string]any{"actual_capture_reference": reference, "playback_id": c.Progress.scope.playbackID, "command_incarnation": c.Progress.scope.incarnation, "clock": p.source.proof.clockIncarnation, "accepted_report_sequence": p.root.source.lastAcceptedSequence, "source_from_ms": p.root.source.fromMS, "source_until_ms": p.root.source.untilMS, "prepared_cursor_ms": p.source.cursorMS, "last_observed_progress_snapshot": c.lastProgress, "scope": "actual retained root at this offer; split/drain progress remains in raw before/result/after snapshots"}
	}
}

func (c *CanonicalWCComponent) offerReport() any {
	if c.offers == nil {
		return nil
	}
	r := c.offers.report()
	// ACTUAL physical write turns per second from the first offer to this joined
	// finish: includes warmup, target births and failed attempts (all real native
	// BEGINs), so it is the achieved commit load, not a per-original constant.
	if c.offers.n > 0 {
		r["physical_turns_in_window"] = c.trace.nativeBegins - c.turnsAtFirstOffer
		r["physical_window_ns"], r["achieved_physical_turns_per_sec"] = physicalTurnRate(c.trace.nativeBegins-c.turnsAtFirstOffer, time.Since(c.offers.firstStart))
	}
	return r
}

func physicalTurnRate(turns uint64, window time.Duration) (int64, any) {
	if window <= 0 {
		return window.Nanoseconds(), nil
	}
	return window.Nanoseconds(), float64(turns) / window.Seconds()
}

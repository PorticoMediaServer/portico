package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func validateN1SteadyCase(cell n1Case) error {
	if cell != (n1Case{"active", "current", "present", "fixture"}) {
		return errors.New("steady component currently supports only active/current/present/fixture; no reset to maintain absent/other cells")
	}
	return nil
}

type n1OriginalCensus struct {
	Offered     int `json:"offered"`
	FreshBegins int `json:"fresh_begins"`
	Complete    int `json:"complete"`
}

type n1TurnCensus struct {
	Started    int `json:"call_started"`
	Physical   int `json:"known_physical_intervals"`
	Returned   int `json:"physically_returned"`
	Committed  int `json:"committed"`
	RolledBack int `json:"rolled_back"`
	Unknown    int `json:"unknown"`
	Unobserved int `json:"unobserved"`
	Errors     int `json:"errors"`
	Refused    int `json:"known_refusals"`
	Fenced     int `json:"known_fenced"`
}

type n1CurrentSnapshot struct {
	RunRevision          int64  `json:"run_revision"`
	LastSequence         int64  `json:"last_sequence"`
	AcknowledgedSequence int64  `json:"acknowledged_sequence"`
	Cursor               string `json:"cursor"`
	Pages                int64  `json:"pages"`
	Discovered           int64  `json:"discovered"`
	Processed            int64  `json:"processed"`
}

var n1FullPrefixCase = n1Case{"dual", "current", "absent", "domain-cap"}

type n1SteadySummary struct {
	CanonicalPopulation *QualificationPopulationManifest `json:"canonical_population,omitempty"`
	ActualLibraryItems  *int64                           `json:"actual_target_library_items_before_warmup"`
	CheckpointCalls     int                              `json:"checkpoint_calls"`
	CheckpointComplete  int                              `json:"checkpoint_complete"`
	CheckpointOverflow  bool                             `json:"checkpoint_ledger_overflow"`
	Restart             *n1ComponentRestart              `json:"component_restart"`
	FullPrefix          *bool                            `json:"complete_escaped_domain_cap_full_prefix"`
	RequestedWarmup     int                              `json:"requested_warmup_originals"`
	RequestedMeasured   int                              `json:"requested_measured_originals"`
	OpenedDatabases     *int                             `json:"opened_databases"`
	Warmup              n1OriginalCensus                 `json:"warmup"`
	Measured            n1OriginalCensus                 `json:"measured"`
	Turns               map[string]*n1TurnCensus         `json:"turns_by_role"`
	KnownPhysicalTurns  int                              `json:"known_physical_turns"`
	WarmupOriginals     []ReceiptReference               `json:"completed_warmup_originals"`
	DatabaseID          string                           `json:"database_id"`
	RegistrySHA256      string                           `json:"captured_registry_manifest_sha256"`
	FixtureSetup        *n1FixtureSetup                  `json:"fixture_setup_facts"`
	PoolStats           *sql.DBStats                     `json:"pool_stats_after_fixture_close"`
	Usage               *[4]governor.Amounts             `json:"authority_usage_after_fixture_close"`
	ConnectionsOpened   *uint64                          `json:"observed_physical_connections_opened"`
	ConnectionsClosed   *uint64                          `json:"observed_physical_connections_closed"`
	NativeCloses        []n1NativeClose                  `json:"native_closes"`
	NativeCloseOverflow bool                             `json:"native_close_overflow"`
	NativeJoined        *bool                            `json:"native_joined_after_fixture_close"`
	BeforeFixture       *n1MemorySample                  `json:"memory_before_fixture"`
	AfterFixture        *n1MemorySample                  `json:"memory_after_fixture_setup"`
	BeforeClose         *n1MemorySample                  `json:"memory_before_fixture_close"`
	AfterClose          *n1MemorySample                  `json:"memory_after_fixture_close"`
	Complete            bool                             `json:"complete"`
	StopReason          string                           `json:"stop_reason"`
}

type n1FixtureSetup struct {
	AgeState       []int64           `json:"age_input_published_pending"`
	StoredProfiles []n1StoredProfile `json:"stored_search_profiles"`
	Cardinalities  map[string]int64  `json:"cardinalities"`
}

type n1SteadyControls struct {
	initialConnections  *uint64
	initialCloses       *int
	canonical           *QualificationPopulationManifest // compiled canonical target; never an alternate opener/profile
	restart             bool                             // component-only canonical close/reopen, no production authority
	fullPrefix          bool                             // closed target cell, not a recipe/limit selector
	clock               func() time.Time                 // target ALWAYS passes time.Now; controls may freeze
	failOriginal        int
	failRole, failQuery string // no config/flag accepts these fault controls
}

// This retains the reviewed owner fixture; it neither opens a second writer nor
// reimplements grants, Resolve, ACK, snapshot or original progression.
func runN1Steady(t *testing.T, ctx context.Context, stream *n1Stream, measured, warmup int, cohort string, control n1SteadyControls, summary *n1SteadySummary) {
	t.Helper()
	*summary = n1SteadySummary{RequestedWarmup: warmup, RequestedMeasured: measured, Turns: map[string]*n1TurnCensus{}, WarmupOriginals: []ReceiptReference{}}
	for _, role := range []string{"fresh_begin", "resolve", "ack"} {
		summary.Turns[role] = &n1TurnCensus{}
	}
	summary.BeforeFixture = n1Memory(stream.origin)
	trace := newN1Trace(stream.origin, cohort == "native-counters")
	var fixture *inventoryProgressiveFixture
	var maintenance *canonicalN1MaintenanceOwner
	defer func() {
		trace.armed = false
		if fixture != nil {
			if fixture.turn != nil && !fixture.turn.transaction.done {
				_, err := fixture.turn.transaction.rollback(nil)
				summary.StopReason = n1Join(summary.StopReason, err)
			}
			summary.BeforeClose = n1Memory(stream.origin)
			var err error
			var physicalCloseErr error
			if maintenance != nil {
				physicalCloseErr = maintenance.close()
				err = errors.Join(physicalCloseErr, maintenance.drain(stream))
				summary.CheckpointCalls, summary.CheckpointComplete, summary.CheckpointOverflow = maintenance.started, maintenance.completed, maintenance.overflow
				if maintenance.overflow {
					err = errors.Join(err, errors.New("checkpoint raw ledger overflow; harness gap"))
				}
			} else {
				physicalCloseErr = fixture.owner.executor.close()
				err = physicalCloseErr
			}
			summary.StopReason = n1Join(summary.StopReason, err)
			w := fixture.owner.executor
			w.physicalFaults.mu.Lock()
			failed := w.physicalFaults.failed
			w.physicalFaults.mu.Unlock()
			stats := w.db.Stats()
			usage, opened, closed := fixture.owner.authority.Usage(), trace.connections, trace.closed
			summary.PoolStats, summary.Usage, summary.ConnectionsOpened, summary.ConnectionsClosed = &stats, &usage, &opened, &closed
			joined := physicalCloseErr == nil && w.closed && !failed && stats.InUse == 0 && stats.OpenConnections == 0 && trace.connections == trace.closed && fixture.owner.authority.Usage() == ([4]governor.Amounts{})
			summary.NativeJoined = &joined
			summary.AfterClose = n1Memory(stream.origin)
		}
		summary.NativeCloses, summary.NativeCloseOverflow = trace.closes[:trace.closeCount], trace.closeOverflow
		summary.StopReason = n1Join(summary.StopReason, ctx.Err())
		if t.Failed() {
			summary.StopReason = n1Join(summary.StopReason, errors.New("fixture/control failed; retain partial wire and test log"))
		}
		summary.Complete = summary.StopReason == "" && stream.err == nil && summary.NativeJoined != nil && *summary.NativeJoined && !summary.NativeCloseOverflow && summary.Warmup.Complete == warmup && summary.Measured.Complete == measured && summary.Warmup.FreshBegins == warmup && summary.Measured.FreshBegins == measured
	}()
	if (control.fullPrefix || control.restart) && (measured != 1 || warmup != 0) {
		summary.StopReason = ErrInvalid.Error()
		return
	}
	if err := validateN1Population("steady", warmup); err != nil && !control.fullPrefix && !control.restart {
		summary.StopReason = err.Error()
		return
	}
	maxMeasured, maxTotal := 64, inventoryProgressiveLimit
	if control.canonical != nil {
		maxMeasured, maxTotal = 1000000, 1000000+canonicalN1Warmup
	}
	if measured < 1 || measured > maxMeasured || warmup+measured > maxTotal || control.clock == nil {
		summary.StopReason = ErrInvalid.Error()
		return
	}
	if err := ctx.Err(); err != nil {
		summary.StopReason = err.Error()
		return
	}
	if control.canonical != nil {
		fixture, maintenance = newCanonicalN1Fixture(t, warmup+measured, trace)
		summary.CanonicalPopulation = control.canonical
		var count int64
		if err := fixture.owner.executor.db.QueryRowContext(ctx, `SELECT count(*) FROM catalog_entities WHERE library_id=7 AND kind=1`).Scan(&count); err != nil {
			summary.StopReason = err.Error()
			return
		}
		summary.ActualLibraryItems = &count
		if count != int64(control.canonical.Facts.Residents) {
			summary.StopReason = "actual target library differs from pinned population"
			return
		}
	} else {
		fixture = newInventoryProgressiveFixture(t, warmup+measured, control.clock, trace.decorate)
	}
	if control.fullPrefix {
		fixture.base = n1SeedCase(t, fixture.owner.executor, fixture.base, n1FullPrefixCase)
	}
	opened := 1
	summary.OpenedDatabases = &opened
	summary.AfterFixture = n1Memory(stream.origin)
	summary.DatabaseID = fmt.Sprintf("%s/steady-db", stream.base["process_epoch"])
	raw, err := json.Marshal(fixture.owner.registry.Manifest())
	if err != nil {
		summary.StopReason = err.Error()
		return
	}
	hash := sha256.Sum256(raw)
	summary.RegistrySHA256 = hex.EncodeToString(hash[:])
	trace.useRegistry(fixture.owner.registry)
	if trace.conn == nil {
		summary.StopReason = "setup physical connection unavailable"
		return
	}
	baseline := trace.conn.id
	facts := &n1Attempt{}
	n1ReadFixtureFacts(t, ctx, fixture.owner.executor, facts, fixture.base.request.scope.libraryKey)
	summary.FixtureSetup = &n1FixtureSetup{facts.AgeState, facts.StoredProfiles, facts.Cardinalities}
	if maintenance != nil {
		if err := stream.append("population_attached", map[string]any{"manifest": control.canonical, "actual_library_items": summary.ActualLibraryItems, "recipe": canonicalN1Recipe, "requested_warmup_originals": warmup, "cohort": "timing", "maintenance_policy": canonicalN1Maintenance().policy, "recovery_partition": canonicalN1Maintenance().need, "initial_opening_outside_trace": true, "observer": "existing n1 owner/decorator; native counters inside HOLD omitted", "qualified_profile_ready": false}); err != nil {
			summary.StopReason = err.Error()
			return
		}
		maintenance.results <- maintenance.step(ctx) // real startup PASSIVE, separate maintenance record
		if err := maintenance.drain(stream); err != nil {
			summary.StopReason = err.Error()
			return
		}
		maintenance.start(ctx)
	}
	attemptOrdinal := 0
	for original := 1; original <= warmup+measured; original++ {
		if err := ctx.Err(); err != nil {
			summary.StopReason = err.Error()
			return
		}
		census, population := &summary.Warmup, "warmup"
		if original > warmup {
			census, population = &summary.Measured, "measured"
		}
		census.Offered++
		command, err := fixture.NextOriginal()
		if err != nil {
			summary.StopReason = err.Error()
			return
		}
		if control.fullPrefix {
			command, err = n1DomainCapCommand(command)
			if err != nil {
				summary.StopReason = err.Error()
				return
			}
			fixture.current = command // before custody; this frozen original is used by ALL three roles
		}
		for _, role := range []string{"fresh_begin", "resolve", "ack"} {
			attemptOrdinal++
			summary.Turns[role].Started++
			record := runN1SteadyTurn(t, ctx, stream, fixture, trace, summary, original, attemptOrdinal, population, role, cohort, baseline, control)
			if control.restart && role == "fresh_begin" && record.Complete && stream.err == nil {
				n1RestartComponent(t, fixture, trace, stream.origin, &summary.Restart)
				if !summary.Restart.Complete {
					summary.StopReason = summary.Restart.Error
					return
				}
				baseline = trace.conn.id
			}
			if !record.Complete || stream.err != nil {
				summary.StopReason = n1Join("incomplete "+population+" "+role, errors.Join(errors.New(record.Error), stream.err))
				return
			}
		}
		if maintenance != nil {
			if err := maintenance.drain(stream); err != nil {
				summary.StopReason = err.Error()
				return
			}
		}
		if control.fullPrefix {
			complete := fixture.ready && fixture.resolved != nil && fixture.acknowledged != nil && *fixture.acknowledged == command.identity.Sequence && fixture.joined() == nil
			summary.FullPrefix = &complete
			if !complete {
				summary.StopReason = "full-prefix actual ACK/join gate incomplete"
				return
			}
		}
		census.Complete++
		if population == "warmup" {
			summary.WarmupOriginals = append(summary.WarmupOriginals, ReceiptReference{command.identity, command.digest})
		}
	}
}

func runN1SteadyTurn(t *testing.T, ctx context.Context, stream *n1Stream, f *inventoryProgressiveFixture, trace *n1Trace, summary *n1SteadySummary, original, ordinal int, population, role, cohort string, baseline uint64, control n1SteadyControls) (record *n1Attempt) {
	t.Helper()
	f.owner.lastAdmission = nil // early guards must not borrow the previous call's receipt
	trace.nextTurn()
	trace.failQuery = ""
	if control.failOriginal == original && control.failRole == role {
		trace.failQuery = control.failQuery
	}
	originalID := fmt.Sprintf("%s/original/%d", stream.base["process_epoch"], original)
	q, err := f.current.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	record = &n1Attempt{ID: originalID + "/" + role, Ordinal: ordinal, OriginalID: originalID, OriginalOrdinal: &original, OriginalPopulation: population, TurnRole: role, Reference: &q.Receipt, DatabaseID: summary.DatabaseID, DatabasePath: f.owner.path, RegistrySHA256: summary.RegistrySHA256, Case: n1Case{"active", "current", "present", "fixture"}, Cohort: cohort, Outcome: "unobserved", Phase: "prepared", Memory: map[string]*n1MemorySample{}, Unavailable: map[string]string{}}
	if control.restart {
		record.OwnerCell = "restart"
		record.OriginalPopulation = "component-restart"
	}
	if control.fullPrefix {
		record.Case = n1FullPrefixCase
		record.OriginalPopulation = "full-prefix"
	}
	for key, value := range n1Missing {
		record.Unavailable[key] = value
	}
	delete(record.Unavailable, "gate_token")
	record.Unavailable["native_joined"] = "persistent pool stays alive across turns; final fixture close carries native join observations"
	record.Unavailable["native_closes"] = "final.steady.native_closes is emitted after fixture teardown; per-turn continuity counts end before teardown"
	record.Continuity = n1ConnectionContinuity{Population: "steady", BeforeID: &baseline, WarmupRequested: summary.RequestedWarmup, WarmupCompleted: summary.Warmup.Complete, WarmupOriginals: append([]ReceiptReference{}, summary.WarmupOriginals...), DeliberateErrorControl: control.failRole != ""}
	if control.fullPrefix {
		record.Continuity.Population = "full-prefix"
	}
	if control.restart {
		record.Continuity.Population = "component-restart"
	}
	beforeID, connections, closes := trace.conn.id, trace.connections, trace.closeCount
	record.Continuity.BeforeID, record.Continuity.ConnectionsBefore, record.Continuity.NativeClosesBefore = &beforeID, &connections, &closes
	var turn *commandTurn
	var ownerStarted time.Time
	returned := false
	defer func() {
		if turn == nil {
			switch role {
			case "fresh_begin":
				turn = f.turn
			case "resolve":
				turn = f.resolveTurn
			case "ack":
				turn = f.ackTurn
			}
		}
		if !returned && record.Error == "" {
			record.Error = "helper did not return; partial owner view only"
		}
		trace.armed = false
		if turn != nil {
			rowsErr := turn.closeRows()
			closed := rowsErr == nil
			record.RowsClosed = &closed
			if !turn.transaction.done {
				_, rollbackErr := turn.transaction.rollback(rowsErr)
				record.Error = n1Join(record.Error, rollbackErr)
			}
			record.Error = n1Join(record.Error, f.owner.observeReturned(turn.transaction))
			n1ObserveOwner(record, turn, stream.origin)
			// owner.begin returns a commandTurn only after native BEGIN succeeds.
			// tx.begun is the CURRENT open state, cleared by commit/rollback.
			began := true
			record.BeginSucceeded = &began
			if !turn.transaction.start.IsZero() {
				summary.KnownPhysicalTurns++
				physical := summary.KnownPhysicalTurns
				record.PhysicalAttemptOrdinal = &physical
			}
		}
		record.Admission = f.owner.lastAdmission
		if turn != nil && f.owner.observedReturn == turn.transaction {
			record.PostReturnObservation = f.owner.postReturn
		}
		n1ObserveOwnerWindow(record, f.owner.executor, stream.origin, ownerStarted)
		record.Continuity.observeAfter(trace)
		expectedConnections, expectedCloses := uint64(1), 0
		if control.initialConnections != nil && control.initialCloses != nil {
			expectedConnections, expectedCloses = *control.initialConnections, *control.initialCloses
		}
		if control.restart && role != "fresh_begin" {
			expectedConnections, expectedCloses = 2, 1
		}
		retained := record.Continuity.Same != nil && *record.Continuity.Same && trace.conn.id == baseline && trace.connections == expectedConnections && trace.closeCount == expectedCloses && !trace.closeOverflow
		if !retained {
			record.Error = n1Join(record.Error, errors.New("persistent physical connection interrupted; no replacement success population"))
		}
		record.Continuity.SteadyEligible = !control.fullPrefix && !control.restart && population == "measured" && summary.Warmup.Complete == summary.RequestedWarmup && retained && control.failRole == ""
		if !record.Continuity.SteadyEligible {
			record.Continuity.UnavailableReason = "warmup or deliberate-error cohort, or physical continuity gap"
			if control.fullPrefix || control.restart {
				record.Continuity.UnavailableReason = "separate source-bound component cell, not a warm steady population; inspect physical lifetime facts"
			}
		}
		record.Units, record.LedgerOverflow = trace.units[:trace.used], trace.overflow
		for i := range record.Units {
			u := &record.Units[i]
			if record.Clock.PhysicalReturnedNS != nil && u.StartedNS != nil && *u.StartedNS > *record.Clock.PhysicalReturnedNS {
				u.Phase = "post_return_observer"
			}
			if u.Phase == "rollback" {
				record.Clock.RollbackStartedNS, record.Clock.RollbackReturnedNS, record.Clock.RollbackNS = u.StartedNS, u.ReturnedNS, u.ElapsedNS
			}
			if u.UnitID == "BEGIN IMMEDIATE" {
				record.Clock.BeginNS = u.ElapsedNS
			}
			if u.Error != "" && u.ID == "" && u.Phase == "setup" {
				record.Settings.AssertError = n1Join(record.Settings.AssertError, errors.New(u.Error))
			}
		}
		if trace.conn != nil {
			id := trace.conn.id
			record.Settings.ConnectionID = &id
		}
		if trace.conn != nil && !trace.conn.closed {
			record.NativeAfter = &n1NativeSnapshot{}
			trace.conn.snapshot(record.NativeAfter, false)
		} else {
			record.Unavailable["native_after"] = "physical connection unavailable or closed before external getter"
		}
		record.WALAfter = n1ReadWAL(stream.origin, f.owner.path, f.owner.wal)
		fenced, ambiguous := f.owner.executor.fenced, f.owner.executor.ambiguous
		record.Fenced, record.Ambiguous = &fenced, &ambiguous
		usage := f.owner.authority.Usage()
		record.Usage = &usage
		stats := f.owner.executor.db.Stats()
		record.PoolStats = &stats
		record.Memory["after_physical_return"] = n1Memory(stream.origin)
		if record.Clock.PhysicalReturnedNS != nil {
			record.Phase = "physical_return"
		}
		record.Complete = record.Error == "" && record.Outcome == "committed" && record.Clock.HoldNS != nil && record.Clock.PhysicalReturnedNS != nil && record.Settings.Actual != nil && record.ConnectionReturned != nil && *record.ConnectionReturned && record.Retired != nil && *record.Retired && f.joinedUsage(usage) && !fenced && !ambiguous && retained && !trace.overflow
		if control.fullPrefix && role == "ack" && record.Complete {
			complete := f.ready && f.resolved != nil && f.acknowledged != nil && f.joined() == nil
			record.FullPrefix = &complete
		}
		record.Profile = n1BuildProfile(record, trace)
		if err := stream.append("attempt", map[string]any{"attempt_id": record.ID, "attempt_ordinal": ordinal, "attempt": record}); err != nil {
			record.Error = n1Join(record.Error, err)
			record.Complete = false
		}
		c := summary.Turns[role]
		if record.PhysicalAttemptOrdinal != nil {
			c.Physical++
		}
		if record.Clock.PhysicalReturnedNS != nil {
			c.Returned++
			record.Phase = "physical_return"
		}
		switch record.Outcome {
		case "committed":
			c.Committed++
		case "rolled_back":
			c.RolledBack++
		case "unknown":
			c.Unknown++
		default:
			c.Unobserved++
		}
		if record.Error != "" {
			c.Errors++
		}
		if record.Refusal != nil && *record.Refusal {
			c.Refused++
		}
		if record.Fenced != nil && *record.Fenced {
			c.Fenced++
		}
		if role == "fresh_begin" && record.BeginSucceeded != nil && *record.BeginSucceeded {
			if population == "warmup" {
				summary.Warmup.FreshBegins++
			} else {
				summary.Measured.FreshBegins++
			}
		}

	}()
	for i, text := range inventoryRequestTexts(&f.current.request) {
		if record.InputLengths == nil {
			record.InputLengths = map[string]int{}
		}
		record.InputLengths[fmt.Sprintf("text_%02d", i)] = len(*text)
	}
	required, err := inventoryFreshRequiredBytes(f.current)
	if err != nil {
		record.Error = err.Error()
		return
	}
	record.RequiredBytes = &required
	record.InputProfile = n1InputProfile(f.current, record.Case)
	record.NativeBefore = &n1NativeSnapshot{}
	trace.conn.snapshot(record.NativeBefore, false)
	record.WALBefore = n1ReadWAL(stream.origin, f.owner.path, f.owner.wal)
	record.Memory["before_turn"] = n1Memory(stream.origin)
	if err := stream.append("attempt_start", map[string]any{"attempt_id": record.ID, "attempt_ordinal": ordinal, "original_id": originalID, "original_ordinal": original, "original_population": record.OriginalPopulation, "turn_role": role, "original_reference": record.Reference, "database_id": summary.DatabaseID, "database_path": f.owner.path, "declared_warmup": summary.RequestedWarmup}); err != nil {
		record.Error = err.Error()
		return
	}
	if err := ctx.Err(); err != nil {
		record.Error = err.Error()
		return
	}
	trace.armed = true
	started := time.Now()
	ownerStarted = started
	record.Phase = role
	switch role {
	case "fresh_begin":
		record.Clock.TurnSetupStartedNS = n1NS(stream.origin, started)
		turn, err = f.Begin(t)
		if turn == nil {
			turn = f.turn
		} // preserve a begun frame whose fresh guards failed
		returned := time.Now()
		record.Clock.TurnSetupReturnedNS = n1NS(stream.origin, returned)
		record.Clock.TurnSetupNS = n1Duration(started, returned)
		if err == nil {
			invoke := time.Now()
			record.Clock.InvocationStartedNS = n1NS(stream.origin, invoke)
			out, callErr := applyInventoryAccept(ctx, turn, f.current)
			end := time.Now()
			record.Clock.InvocationReturnedNS = n1NS(stream.origin, end)
			record.Clock.InvocationNS = n1Duration(invoke, end)
			record.Result = &out
			err = callErr
			if err == nil {
				err = f.finish(turn)
			} else {
				_, err = turn.transaction.rollback(err)
			}
		}
	case "resolve":
		record.Clock.InvocationStartedNS = n1NS(stream.origin, started)
		err = f.Resolve(t)
		turn = f.resolveTurn
		if f.resolved != nil {
			value, digest := f.resolved.result, f.resolved.digest
			record.HistoricalResult, record.HistoricalDigest = &value, &digest
		}
	case "ack":
		record.Clock.InvocationStartedNS = n1NS(stream.origin, started)
		err = f.AckAndSnapshot(t)
		turn = f.ackTurn
		record.AcknowledgedThrough = f.acknowledged
		if f.ready {
			s := f.snapshot
			record.CurrentSnapshot = &n1CurrentSnapshot{s.run.revision, s.run.lastSequence, s.run.acknowledgedSequence, s.directory.expectedCursor, s.directory.pages, s.discovered, s.processed}
		}
	}
	if role != "fresh_begin" {
		returned := time.Now()
		record.Clock.InvocationReturnedNS = n1NS(stream.origin, returned)
		record.Clock.InvocationNS = n1Duration(started, returned)
	}
	if err != nil {
		record.Error = err.Error()
		var refusal *Refusal
		refused := errors.As(err, &refusal) || errors.Is(err, ErrQuantum)
		record.Refusal = &refused
		f.stop(err)
	}
	record.Phase = "call_return"
	returned = true
	return
}

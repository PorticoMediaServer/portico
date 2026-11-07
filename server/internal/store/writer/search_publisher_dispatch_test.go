package writer

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

type publisherGovernedFixture struct {
	maintenance *canonicalN1MaintenanceOwner
	grantLimit  int
	w           *Writer
	e           *executor
	a           *governor.Authority
	cap         *PublisherDisclosure
	q           PublisherReceiptRequest
	fragment    *searchFragmentStageCommand
	head        *searchHeadPublishCommand
	reclaim     *searchFragmentReclaimCommand
	rq          searchReclaimerReceiptRequest
	golden      plan.Golden
}

// Compiled candidate envelope for qualification, NOT measured passing bounds.
// Native module amplification is charged explicitly in addition to ordinary
// points, original retained payloads, receipt writes, setup and worst terminal.
// QL must report refusals/overruns unchanged; this never enlarges the 25ms hold.
func publisherQualificationPlan(purpose CommandID, registration stmt.Receipt) effectivePlan {
	p := effectivePlan{registration: registration,
		recipe: turnRecipe{limit: Work{65536, 16 << 20, 8192, 32 << 20, MaxCommandHold},
			setup:    Work{16, 2000, 32, 128 << 10, time.Millisecond},
			terminal: Work{16, 2000, 32, 128 << 10, 8 * time.Millisecond}},
		headerSync:      Work{WALBytes: 32, Hold: 5 * time.Millisecond},
		publisherMemory: commandMemoryBudget{4 << 20, 12 << 20}}
	for _, d := range searchPublisherCommandDefinitions(purpose) {
		work := Work{16, 2000, 16, 64 << 10, 100 * time.Microsecond}
		if d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
			work = Work{8192, 1 << 20, 256, 1 << 20, 500 * time.Microsecond}
		}
		p.steps = append(p.steps, statementStep{d.ID, work})
	}
	p.need[governor.MemoryBytes], p.need[governor.CPUMilli] = 16<<20, 100
	p.need[governor.WALBytes], p.need[governor.IOBytes] = int64(p.recipe.limit.WALBytes), 64<<20
	p.io = []governor.DeviceIO{{DeviceID: "publisher-qualification-volume", Class: governor.Seeking, Outstanding: 1, Bytes: 64 << 20}}
	return p
}

func newPublisherGovernedFixture(t *testing.T, purpose CommandID, realClock bool) *publisherGovernedFixture {
	t.Helper()
	var e *executor
	var fragment *searchFragmentStageCommand
	var head *searchHeadPublishCommand
	if purpose == SearchFragmentStage {
		e, fragment = searchFragmentNativeSeed(t)
	} else if purpose == SearchHeadPublish {
		e, head = searchHeadNativeSeed(t)
	} else {
		e, reclaim := searchReclaimNativeSeed(t)
		return bindPublisherReclaimerGovernedFixture(t, e, reclaim, realClock)
	}
	return bindPublisherGovernedFixture(t, e, purpose, fragment, head, realClock)
}

// Uses the existing canonical executor and ONE W runtime. Population setup may
// occur before this binding/Capture; no test callback enters an accepting turn.
func bindPublisherGovernedFixture(t *testing.T, e *executor, purpose CommandID, fragment *searchFragmentStageCommand, head *searchHeadPublishCommand, realClock bool, maintenance ...governedFixtureMaintenance) *publisherGovernedFixture {
	t.Helper()
	return bindPublisherFixture(t, e, purpose, fragment, head, nil, realClock, maintenance...)
}

// Independent reclaimer scope: same dispatcher and grant machinery, separate
// receipt namespace. It never mints publisher-job authority.
func bindPublisherReclaimerGovernedFixture(t *testing.T, e *executor, reclaim *searchFragmentReclaimCommand, realClock bool, maintenance ...governedFixtureMaintenance) *publisherGovernedFixture {
	t.Helper()
	if reclaim == nil {
		t.Fatal("reclaimer fixture requires a closed reclaimer")
	}
	return bindPublisherFixture(t, e, SearchFragmentReclaim, nil, nil, reclaim, realClock, maintenance...)
}

func bindPublisherFixture(t *testing.T, e *executor, purpose CommandID, fragment *searchFragmentStageCommand, head *searchHeadPublishCommand, reclaim *searchFragmentReclaimCommand, realClock bool, maintenance ...governedFixtureMaintenance) *publisherGovernedFixture {
	t.Helper()
	if realClock {
		e.now = time.Now
	}
	path := publisherFixturePath(t, e)
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	purposes := []CommandID{SearchFragmentStage, SearchHeadPublish}
	if reclaim != nil {
		purposes = append(purposes, SearchFragmentReclaim)
	}
	for _, p := range purposes {
		for _, d := range searchPublisherCommandDefinitions(p) {
			found := false
			for _, old := range defs {
				if old.ID == d.ID {
					if !reflect.DeepEqual(old, d) {
						t.Fatal("conflicting definition", d.ID)
					}
					found = true
					break
				}
			}
			if !found {
				defs = append(defs, d)
			}
		}
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, defs, "search_publisher_dispatch_test.go", "search_fragment_stage_test.go", "search_head_test.go")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	base := effectiveFixturePlan()
	base.registration = golden.Receipt()
	fp, hp := publisherQualificationPlan(SearchFragmentStage, golden.Receipt()), publisherQualificationPlan(SearchHeadPublish, golden.Receipt())
	policy := publisherFixtureWALPolicy()
	if len(maintenance) > 1 {
		t.Fatal("one compiled publisher maintenance composition")
	}
	if len(maintenance) == 1 {
		actual := maintenance[0].policy
		if actual.HardBytes != policy.HardBytes || actual.ReserveBytes != policy.ReserveBytes || actual.SoftDebtBytes != policy.SoftDebtBytes || actual.ActivePressureBytes != policy.ActivePressureBytes || actual.MaxSampleAge != policy.MaxSampleAge {
			t.Fatal("publisher maintenance changed command WAL caps")
		}
		policy = actual
	}
	wal, err := governor.NewWALGate(policy)
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = base.need
	}
	maxGrants := 2
	if len(maintenance) == 1 {
		partitions[governor.Recovery] = maintenance[0].need
		maxGrants = 4
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: maxGrants, Now: e.now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, base)
	if err != nil {
		t.Fatal(err)
	}
	var extra []effectivePlan
	if reclaim != nil {
		extra = append(extra, publisherQualificationPlan(SearchFragmentReclaim, golden.Receipt()))
	}
	if err := composeSearchPublisherCommands(t.Context(), r, fp, hp, extra...); err != nil {
		t.Fatal(err)
	}
	w := &Writer{effective: r}
	var q PublisherReceiptRequest
	if fragment != nil {
		s := fragment.request.snapshot
		q = PublisherReceiptRequest{s.job.id, s.source.libraryID, s.source.entityID, ReceiptReference{fragment.identity, fragment.digest}}
	}
	if head != nil {
		s := head.request.snapshot.stage
		q = PublisherReceiptRequest{s.job.id, s.source.libraryID, s.source.entityID, ReceiptReference{head.identity, head.digest}}
	}
	var rq searchReclaimerReceiptRequest
	var extraScope []searchReclaimerReceiptRequest
	if reclaim != nil {
		s := reclaim.request.snapshot
		rq = searchReclaimerReceiptRequest{s.owner.id, s.owner.publicationID, s.fragment.libraryID, s.fragment.entityID, ReceiptReference{reclaim.identity, reclaim.digest}}
		extraScope = append(extraScope, rq)
	}
	cap := publisherFixtureCapability(t, w, q, extraScope...)
	return &publisherGovernedFixture{grantLimit: maxGrants, w: w, e: e, a: a, cap: cap, q: q, fragment: fragment, head: head, reclaim: reclaim, rq: rq, golden: golden}
}

func publisherFixtureCapability(t *testing.T, w *Writer, q PublisherReceiptRequest, reclaims ...searchReclaimerReceiptRequest) *PublisherDisclosure {
	t.Helper()
	if len(reclaims) > 1 {
		t.Fatal("multiple reclaimer scopes")
	}
	if len(reclaims) == 1 {
		if q != (PublisherReceiptRequest{}) {
			t.Fatal("publisher ID cannot mint reclaimer scope")
		}
		r := reclaims[0]
		return &PublisherDisclosure{issuer: w, scope: &publisherScope{kind: publisherReclaimerScope, reclaimer: publisherReclaimerBinding{r.ReclaimerID, r.PublicationID, r.Receipt.Identity.Incarnation}, libraryID: r.LibraryID, entityID: r.EntityID, libraryKey: "publisher-fixture", lifetime: t.Context()}}
	}
	return &PublisherDisclosure{issuer: w, scope: &publisherScope{jobID: q.JobID, libraryID: q.LibraryID, entityID: q.EntityID, libraryKey: "publisher-fixture", incarnation: q.Receipt.Identity.Incarnation, lifetime: t.Context()}}
}

func TestSearchPublisherGovernedCommands(t *testing.T) {
	for _, purpose := range []CommandID{SearchFragmentStage, SearchHeadPublish} {
		t.Run(fmt.Sprint(purpose), func(t *testing.T) {
			f := newPublisherGovernedFixture(t, purpose, false)
			out, ack, err := f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, f.q, purpose, f.fragment, f.head, false)
			if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.Outcome.Reference != f.q.Receipt || ack != (AckResult{}) {
				t.Fatal(out, ack, err)
			}
			ev := out.Outcome.Evidence
			if ev == nil || ev.Purpose != purpose || ev.Class != governor.Background || ev.Durability != Normal || ev.Commit != CommitSucceeded || ev.PhysicalReturnedAt == nil || ev.WALBefore == nil || ev.WALAfter == nil {
				t.Fatal("not real same-purpose physical execution", ev)
			}
			if out.Fragment.Outcome != (ReceiptOutcome{}) || out.Head.Outcome != (ReceiptOutcome{}) {
				t.Fatal("domain manufactured physical outcome")
			}
			if _, err := f.e.db.ExecContext(t.Context(), `UPDATE search_publish_jobs SET job_lease_expires_ms=0,job_revision=9223372036854775807,input_generation=99 WHERE id=29`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.e.db.ExecContext(t.Context(), `UPDATE search_generations SET unicode_fingerprint='stale' WHERE id=7`); err != nil {
				t.Fatal(err)
			}
			replay, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, f.q, purpose, nil, nil, false)
			if err != nil || !replay.Outcome.Replayed || replay.Outcome.State != Applied || replay.Fragment != out.Fragment || replay.Head != out.Head {
				t.Fatal("saved replay depended on fresh engine/lease/source", replay, err)
			}
			_, ack, err = f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, f.q, purpose, nil, nil, true)
			if err != nil || ack.AcknowledgedThrough != f.q.Receipt.Identity.Sequence || ack.Evidence == nil {
				t.Fatal(ack, err)
			}
			retired, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, f.q, purpose, nil, nil, false)
			if err != nil || retired.Outcome.State != ResultRetired || retired.Fragment != (searchFragmentStageResult{}) || retired.Head != (searchHeadPublishResult{}) {
				t.Fatal("retired resurrected result", retired, err)
			}
			if f.a.Usage() != ([4]governor.Amounts{}) || f.e.db.Stats().InUse != 0 {
				t.Fatal("physical custody leaked")
			}
		})
	}
}

func TestSearchPublisherNativeDisclosureAndPartition(t *testing.T) {
	f := newPublisherGovernedFixture(t, SearchFragmentStage, false)
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET library_id='replacement' WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	out, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, f.q, SearchFragmentStage, f.fragment, nil, false)
	if !errors.Is(err, ErrDisclosureDenied) || out != (searchPublisherCommandResult{}) {
		t.Fatal("native replacement disclosed", out, err)
	}
	if f.a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("denial leaked grant")
	}
	if _, err := f.e.db.ExecContext(t.Context(), `UPDATE catalog_libraries SET library_id='publisher-fixture' WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	f.w.effective.publisherPlans[0].publisherMemory = commandMemoryBudget{}
	out, _, err = f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, f.q, SearchFragmentStage, f.fragment, nil, false)
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || out != (searchPublisherCommandResult{}) {
		t.Fatal("borrowed another purpose's partition", out, err)
	}
}

func TestSearchPublisherClosedPurposeAndCompleteInventory(t *testing.T) {
	for _, p := range []CommandID{SearchFragmentStage, SearchHeadPublish} {
		defs := searchPublisherCommandDefinitions(p)
		if len(defs) == 0 {
			t.Fatal("empty")
		}
		for _, d := range defs {
			if searchPublisherMaxUses(p, d.ID) == 0 {
				t.Fatal("unreserved", d.ID)
			}
		}
		if searchPublisherMaxUses(p, "unknown") != 0 || searchPublisherMaxUses(SearchImageStage, searchPublisherOwnerID) != 0 {
			t.Fatal("open inventory")
		}
	}
	f := newPublisherGovernedFixture(t, SearchFragmentStage, false)
	out, _, err := f.w.effective.runSearchPublisher(t.Context(), f.w, f.cap, f.q, SearchHeadPublish, f.fragment, nil, false)
	if !errors.Is(err, ErrInvalid) || out != (searchPublisherCommandResult{}) || f.a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("purpose substitution", out, err)
	}
}

func publisherFixtureWALPolicy() governor.WALPolicy {
	return governor.WALPolicy{HardBytes: 1 << 30, ReserveBytes: 64 << 20, SoftDebtBytes: 1 << 29, ActivePressureBytes: 3 << 28, MaxSampleAge: time.Second}
}
func (f *publisherGovernedFixture) joinedUsage() bool {
	usage := f.a.Usage()
	if f.maintenance != nil {
		usage[governor.Recovery] = governor.Amounts{}
	} // ONLY this owned keeper, joined separately
	return usage == ([4]governor.Amounts{})
}

func publisherFixturePath(t *testing.T, e *executor) string {
	t.Helper()
	var seq int64
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

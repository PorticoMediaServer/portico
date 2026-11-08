package writer

import (
	"context"
	"crypto/rand"
	"database/sql/driver"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"testing"
	"time"
)

// One factored trusted fixture builder. Options are compiled tests only; no
// worker SQL, policy, grant, permit or opening can enter through the target ABI.
type wcFixtureOptions struct {
	attached      *wcFixtureAttachment
	kind          string
	actorTier     string
	extraMemory   int64
	maxGrants     int
	principal     context.Context
	catalog       *wcProgressExistingCatalog
	decorate      func(driver.Conn) driver.Conn
	maintenance   bool
	pressureBytes int64 // tiny functional-control profile only; targets leave zero
	realClock     bool
}

// Test-only reuse of an already opened SAME executor/governor/registry. No
// caller of a production API can supply this attachment.
type wcFixtureAttachment struct {
	opening   *scanOpening
	governor  governor.Governor
	authority *governor.Authority
	wal       *governor.WALGate
	receipt   stmt.Receipt
	keeper    *canonicalN1MaintenanceOwner
}

func wcComponentFixture(t *testing.T, opts wcFixtureOptions) (*Writer, *WCProgressDriver, *governor.Authority, *canonicalN1MaintenanceOwner) {
	t.Helper()
	var opening *scanOpening
	var e *executor
	var now func() time.Time
	var warmup func()
	if opts.attached != nil {
		opening = opts.attached.opening
		e = opening.executor
		now = e.now
		warmup = func() {}
		if opts.decorate != nil || !opts.realClock || opts.attached.receipt == nil {
			t.Fatal("invalid existing WC fixture attachment")
		}
	} else {
		initial, seededPath := testCanonicalExecutor(t)
		if err := initial.close(); err != nil {
			t.Fatal(err)
		}
		opening = openScanFixture(t, seededPath, scanOpeningOwner(t, seededPath))
		e = opening.executor
		now, warmup = wcCompositionFixtureClock(t, opts.realClock)
		e.now = now
		if opts.decorate != nil {
			if err := e.db.Close(); err != nil {
				t.Fatal(err)
			}
			if stats := e.db.Stats(); stats.InUse != 0 || stats.OpenConnections != 0 {
				t.Fatal("opening pool not joined")
			}
			e.physicalFaults.mu.Lock()
			failed := e.physicalFaults.failed
			e.physicalFaults.mu.Unlock()
			if failed {
				t.Fatal("opening native close fault")
			}
			connector, err := sqliteconn.Connector(seededPath, e.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			e.openPool(connector, opts.decorate)
		}
	}
	path := opening.binding.path
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	defs = append(defs, wcProgressCommandDefinitions()...)
	defs = append(defs, wcProgressBirthDefinitions()...)
	extras := [][]stmt.Definition{}
	if opts.kind == "security" || opts.kind == "mixed" {
		extras = append(extras, wcSecurityCommandDefinitions())
	}
	if opts.kind == "wc_reclaim" || opts.kind == "mixed" {
		extras = append(extras, wcReclaimCommandDefinitions())
	}
	for _, definitions := range extras {
		for _, def := range definitions {
			found := false
			for _, old := range defs {
				if old.ID == def.ID {
					if !reflect.DeepEqual(old, def) {
						t.Fatal("conflicting definition", def.ID)
					}
					found = true
					break
				}
			}
			if !found {
				defs = append(defs, def)
			}
		}
	}
	p := effectiveFixturePlan()
	var err error
	if opts.attached != nil {
		p.registration = opts.attached.receipt
	} else {
		conn, err := e.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })

		source := "wc_progress_composition_test.go"
		switch opts.kind {
		case "security":
			source = "wc_security_composition_test.go"
		case "wc_reclaim":
			source = "wc_reclaim_composition_test.go"
		}
		golden := captureEffectiveFixture(t, conn, e.config, defs, source, "wc_progress_fixture.go", "wc_component_fixture_test.go", "wc_fixture_clock_test.go")
		if err = conn.Close(); err != nil {
			t.Fatal(err)
		}
		p.registration = golden.Receipt()
	}
	wc := p
	wc.steps = nil
	wc.wcProgressMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range wcProgressCommandDefinitions() {
		wc.steps = append(wc.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	var g governor.Governor
	var a *governor.Authority
	var wal *governor.WALGate
	if opts.attached != nil {
		g, a, wal = opts.attached.governor, opts.attached.authority, opts.attached.wal
		if err = a.VerifyComposition(g, wal); err != nil {
			t.Fatal(err)
		}
	} else {
		walPolicy := governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second}
		if opts.maintenance {
			walPolicy = canonicalN1Maintenance().policy
			if opts.pressureBytes > 0 {
				walPolicy.ActivePressureBytes = opts.pressureBytes
			}
		}
		wal, err = governor.NewWALGate(walPolicy)
		if err != nil {
			t.Fatal(err)
		}
		var partitions [4]governor.Amounts
		for i := range partitions {
			partitions[i] = p.need
			partitions[i][governor.MemoryBytes] += opts.extraMemory
		}
		if opts.maintenance {
			for resource, n := range canonicalN1Maintenance().need {
				partitions[governor.Recovery][resource] += n
			}
		}
		g, a, err = governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: opts.maxGrants, Now: now, WAL: wal})
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	birth := wc
	birth.steps = nil
	for _, d := range wcProgressBirthDefinitions() {
		birth.steps = append(birth.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	if err = composeWCProgress(t.Context(), r, wc, birth); err != nil {
		t.Fatal(err)
	}
	w := &Writer{effective: r}
	var keeper *canonicalN1MaintenanceOwner
	if opts.attached != nil {
		keeper = opts.attached.keeper
	} else if opts.maintenance {
		keeper = newCanonicalCheckpointOwner(t, opening, g, a, wal, path)
	}
	member, err := wcRandomBirth()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := wcRandomBirth()
	if err != nil {
		t.Fatal(err)
	}
	scope := wcProgressScope{primary: 1, primaryProfile: "profile-one", membershipRole: opts.actorTier, horizon: now().Add(time.Hour).UTC().Format(time.RFC3339Nano), expires: now().Add(time.Hour).UTC().Format(time.RFC3339Nano), playbackID: "playback-音", profileKey: "viewer-one", accountID: "account-one", profileID: "profile-one", memberBirth: member, profileBirth: profile, libraryKey: "library-one", libraryID: 7, subjectID: 11, serverID: "server", familyID: "wc-family", tokenHash: "wc-token", epoch: 1, tokenGeneration: 1, memberRevision: 1, profileRevision: 1, role: opts.actorTier, memberLibraries: `["library-one"]`}
	if opts.catalog != nil {
		scope.libraryID = opts.catalog.libraryID
		scope.libraryKey = opts.catalog.libraryKey
		scope.subjectID = opts.catalog.subjectID
		scope.subjectPublicID = opts.catalog.publicID
		encoded, _ := json.Marshal([]string{scope.libraryKey})
		scope.memberLibraries = string(encoded)
	}
	scope.profileKey = wcFixturePersonalOwnerKey("local", scope.accountID, scope.profileID)
	scope.expires = scope.horizon
	for _, b := range []*[16]byte{&scope.incarnation, &scope.clock} {
		if _, err = rand.Read(b[:]); err != nil {
			t.Fatal(err)
		}
	}
	if opts.catalog == nil {
		if _, err = rand.Read(scope.subjectPublicID[:]); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := wcSnapshot{}
	text := func(i int, s string) { snapshot[i] = wcSnapshotCell{Kind: 2, Text: s} }
	integer := func(i int, n int64) { snapshot[i] = wcSnapshotCell{Kind: 1, Integer: n} }
	text(0, now().UTC().Format(time.RFC3339Nano))
	text(1, "started")
	text(2, scope.accountID)
	text(3, scope.profileID)
	text(4, scope.playbackID)
	integer(5, 1)
	integer(6, 0)
	snapshot[7] = wcSnapshotCell{Kind: 3, Blob: append([]byte(nil), scope.subjectPublicID[:]...)}
	text(8, scope.libraryKey)
	integer(9, 1)
	for _, i := range []int{21, 22, 23, 25, 26} {
		integer(i, 0)
	}
	integer(24, 100000)
	integer(32, scope.subjectID)
	integer(33, 1)
	integer(34, now().UnixMilli())
	text(39, member)
	text(40, profile)
	preparation := governor.Request{Key: governor.JobKey{Domain: uint16(ProgressAccept), ID: scope.playbackID}, Owner: r.owner, Class: governor.Foreground, Deadline: now().Add(time.Minute), Footprint: []governor.Scope{{Library: scope.libraryID, Domain: uint16(ProgressAccept)}}}
	// The retained preparation lifetime is the same actual token horizon in a
	// streaming component, not a per-command quantum or a fresh authority lease.
	if opts.maintenance {
		preparation.Deadline, err = time.Parse(time.RFC3339Nano, scope.horizon)
		if err != nil {
			t.Fatal(err)
		}
	}
	preparation.Need[governor.MemoryBytes] = 1 << 20
	decision := g.Try(preparation)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	principal := t.Context()
	if opts.principal != nil {
		principal = opts.principal
	}
	d, err := newWCProgressDriver(t.Context(), principal, w, scope, snapshot, wcProgressPolicy{StartedPercent: 5, PlayedPercent: 95, VideoCompletion: "earliest"}, preparation, decision.Grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	catalog := []wcProgressExistingCatalog{}
	if opts.catalog != nil {
		catalog = append(catalog, *opts.catalog)
	}
	if err = initializeWCProgressFixture(t.Context(), d, catalog...); err != nil {
		t.Fatal(err)
	}
	warmup()
	return w, d, a, keeper
}

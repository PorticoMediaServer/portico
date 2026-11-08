//go:build linux && portico_ec4_screening

package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

// Only this opt-in binary links the actual storage Client/helper, whose legacy
// registrations must not change ordinary writer-test function-set goldens.
func init() {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if err := storage.Helper(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
}

type ec4ScreenReport struct {
	Label                                                                                     string `json:"label"`
	Qualification                                                                             bool   `json:"qualification"`
	Features                                                                                  string
	Capabilities                                                                              map[string]bool
	Source, Profile, Phase, Error                                                             string
	Requested, Accepted, ReaderVisible, Renewals, Checkpoints, Recoveries, HistoryResolutions int
	GenerationNS, ScanNS, MaintenanceNS                                                       int64
	FirstFilenameVisibleNS, AllFilenameVisibleNS, FirstBasicNS, AllBasicNS                    *int64
	CheckpointOverlappingCommands, AcceptOver25MS, AcceptOverlapOver25MS                      int
	AcceptMaxNS, AcceptMaxOverlapNS, CheckpointMaxNativeNS, CheckpointMaxCallNS               int64
	DispatcherRecoveries                                                                      int
	WALPolicy                                                                                 ec4WALPolicy
	Fixture                                                                                   ec4ScreenFixture
	NativeEOFObserved                                                                         bool
	TraversalCommitted                                                                        bool
	Missing                                                                                   []string
	Cgroup                                                                                    string
}

type ec4WALPolicy struct {
	HardBytes           int64 `json:"hard_bytes"`
	ReserveBytes        int64 `json:"reserve_bytes"`
	SoftDebtBytes       int64 `json:"soft_debt_bytes"`
	ActivePressureBytes int64 `json:"active_pressure_bytes"`
	ConcurrentPassive   bool  `json:"concurrent_passive"`
}

func ec4WriteReport(path string, r ec4ScreenReport) error {
	if path == "" {
		return nil
	}
	data, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	tmp := path + ".tmp"
	if e = os.WriteFile(tmp, append(data, '\n'), 0600); e != nil {
		return e
	}
	return os.Rename(tmp, path)
}
func ec4Journal(file *os.File, v any) error {
	if e := json.NewEncoder(file).Encode(v); e != nil {
		return e
	}
	return file.Sync()
}

func ec4ConsumeLifecycle(ctx context.Context, op *ScanRunOperation, j *os.File) (ScanRunReference, *ScanRunLease, error) {
	q, e := op.Reference()
	if e != nil {
		return q, nil, e
	}
	if e = ec4Journal(j, map[string]any{"phase": "lifecycle_original", "original": q}); e != nil {
		return q, nil, e
	}
	result, e := op.Apply(ctx)
	if e != nil {
		applyErr := e
		if e = ec4Journal(j, map[string]any{"phase": "lifecycle_apply_error", "original": q, "at": time.Now(), "error": applyErr.Error()}); e != nil {
			return q, nil, errors.Join(applyErr, e)
		}
		result, e = op.Resolve(ctx)
		if e != nil {
			return q, nil, errors.Join(applyErr, e)
		}
	}
	if e != nil {
		return q, nil, e
	}
	value, e := result.Snapshot(ctx)
	if e != nil {
		return q, nil, e
	}
	if value.Outcome.State != Applied {
		return q, nil, ErrResolveRequired
	}
	if e = ec4Journal(j, map[string]any{"phase": "lifecycle_consumed", "original": q, "result": value}); e != nil {
		return q, nil, e
	}
	if e = result.Close(); e != nil {
		return q, nil, e
	}
	if _, e = op.Ack(ctx); e != nil {
		return q, nil, e
	}
	var lease *ScanRunLease
	if q.Purpose == InventoryClaim {
		lease, e = op.Lease(ctx)
		if e != nil {
			return q, nil, e
		}
	}
	return q, lease, op.Close(ctx)
}

type ec4ScreenReader struct {
	cp       *checkpointComponent
	pool     *governor.Snapshots
	reader   *read.BrowseReader
	database string
}

func ec4AttachReader(t *testing.T, cp *checkpointComponent) *ec4ScreenReader {
	t.Helper()
	if cp.reader != nil {
		if e := errors.Join(cp.reader.Close(), cp.a.Retired(cp.readerGrant)); e != nil {
			t.Fatal(e)
		}
		cp.reader, cp.readerGrant = nil, nil
	}
	conn, e := cp.w.db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	readfixture.ForegroundOn(t, cp.path, conn)
	if _, e = conn.ExecContext(t.Context(), `UPDATE direct_memberships SET allowed_libraries='["movies","lifecycle-library"]' WHERE account_id='a'`); e != nil {
		t.Fatal(e)
	}
	if e = conn.Close(); e != nil {
		t.Fatal(e)
	}
	cfg := readfixture.ReaderConfig()
	db, _, e := sqliteconn.Open(t.Context(), cp.path, cfg, sqliteconn.ExistingFile)
	if e != nil {
		t.Fatal(e)
	}
	rc, e := db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	registry := nativeFixtureRegistry(t, rc, cfg, read.HarnessDefinitions(), "../internal/readfixture/source.go", "../read/foreground.go")
	if e = errors.Join(rc.Close(), db.Close()); e != nil {
		t.Fatal(e)
	}
	connector, e := sqliteconn.Connector(cp.path, cfg, sqliteconn.ExistingFile)
	if e != nil {
		t.Fatal(e)
	}
	// SAME governor/authority and existing source-bound native reader policy.
	identity := &n1PairedReaderIdentity{device: cp.opening.binding.device, inode: cp.opening.binding.inode}
	pool, e := governor.NewSnapshots(&faultConnector{Connector: connector, observeNative: identity.observe}, cfg, cp.g, cp.a, registry)
	if e != nil {
		t.Fatal(e)
	}
	reader, e := read.NewBrowseReader(pool)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := pool.Close(); e != nil {
			t.Error(e)
		}
	})
	return &ec4ScreenReader{cp: cp, pool: pool, reader: reader, database: fmt.Sprintf("ec4-%d", time.Now().UnixNano())}
}
func (r *ec4ScreenReader) page(ctx context.Context, after *read.BrowseCursor) (read.BrowsePage, error) {
	var page read.BrowsePage
	at := time.Now()
	live, request, output := readfixture.Requests("ec4-visible", at)
	_, e := read.ConsumeForeground(ctx, r.cp.g, r.cp.a, r.pool, live, output, func(c context.Context, out read.BrowseOutput) error {
		var e error
		page, _, e = r.reader.Window(c, request, time.Second, out, read.BrowseInput{Database: r.database, TokenHash: "token", Library: 7, Kind: 1, Limit: 40, Now: at, After: after})
		return e
	})
	return page, e
}

func ec4MapObserved(s InventoryPreparationSnapshot, page storage.NativeInventoryPage) (InventoryAcceptInput, error) {
	in := s.Input
	if page.Version != 1 || page.ErrorCode != "" || page.Unstable || page.Complete || len(page.Issues) != 0 || len(page.Entries) != 1 || page.Entries[0].Directory {
		return in, ErrInvalid
	}
	e := page.Entries[0]
	p, err := ProjectOrdinaryFilename(InventoryOrdinaryFilenameInput{IdentityRoot: s.IdentityRoot, AssetPath: e.Path, RelativePath: e.Name, MetadataLanguage: "en-US"})
	if err != nil {
		return in, err
	}
	m := p.Metadata
	revision, err := (mediasource.InventoryEvidence{Kind: "local", Scope: in.Source.ID + ":" + in.Source.Incarnation, Object: objectidentity.StableObjectIdentity(e.ObjectIdentity, in.Source.RootIdentity), Size: e.Size, ModifiedNS: e.ModifiedNS, ChangeToken: e.ChangeToken}).Revision()
	if err != nil {
		return in, err
	}
	in.Directory.NextCursor, in.Directory.RawNames = page.NextCursor, 1
	in.Files = []InventoryFilenameInput{{RelativePath: e.Name, AssetPath: e.Path, ObjectIdentity: e.ObjectIdentity, Revision: revision, ChangeToken: e.ChangeToken, Size: e.Size, ModifiedNS: e.ModifiedNS, SourceKey: p.SourceKey, Year: p.Year, Part: p.Part, Title: m.Title, SortTitle: m.SortTitle, SortKey: m.SortKey, SortHead: m.SortHead, SortKeyKeepArticles: m.SortKeyKeepArticles, SortHeadKeepArticles: m.SortHeadKeepArticles, MetadataLanguage: m.MetadataLanguage}}
	return in, nil
}

// ec4RecoveryGate refuses the screening start unless the trusted pressure
// recovery hook is attached to the same funded keeper the writer uses.
func ec4RecoveryGate(d *ScanDriver) error {
	if d == nil || d.writer == nil || d.writer.effective == nil || d.writer.effective.executor == nil ||
		d.writer.effective.maintenance == nil || d.writer.effective.maintenance.request.Key.ID == "" ||
		d.writer.effective.maintenance.results == nil || d.writer.effective.executor.keeper.Load() == nil {
		return errors.New("screening pressure recovery unavailable: funded keeper hook not attached")
	}
	return nil
}

const ec4CheckpointCadence = 50 * time.Millisecond

// ec4ConcurrentCheckpoints runs the SAME owned keeper PASSIVE turn on its own
// goroutine at the existing cadence, matching production where checkpoints are
// concurrent with the writer instead of an inline delay in the scan loop. It
// serialises Step/RecoverWAL on one goroutine, keeps the maintenance journal and
// counters race-free, and records whether a PASSIVE turn coincided with the
// writer's Accept call so a slow checkpoint can be compared with command time.
type ec4ConcurrentCheckpoints struct {
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	cp       *checkpointComponent
	journal  *os.File
	records  <-chan checkpointMaintenanceRecord
	progress chan struct{}
	stepMu   sync.Mutex
	ticks    atomic.Int64

	mu            sync.Mutex
	checkpoints   int
	recoveries    int
	dispatcherRec int
	totalNS       int64
	nativeMax     int64
	callMax       int64
	acceptMax     int64
	overlapCount  int
	overlapMax    int64
	over25        int
	overlapOver25 int
	err           error
}

func newEC4ConcurrentCheckpoints(ctx context.Context, cp *checkpointComponent, journal *os.File, records <-chan checkpointMaintenanceRecord) *ec4ConcurrentCheckpoints {
	c, cancel := context.WithCancel(ctx)
	return &ec4ConcurrentCheckpoints{ctx: c, cancel: cancel, cp: cp, journal: journal, records: records, progress: make(chan struct{}, 1)}
}

func (c *ec4ConcurrentCheckpoints) start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-timer.C:
			}
			if err := c.step(false); err != nil {
				if c.ctx.Err() == nil {
					c.mu.Lock()
					if c.err == nil {
						c.err = err
					}
					c.mu.Unlock()
				}
				return
			}
			timer.Reset(ec4CheckpointCadence)
		}
	}()
	// The writer's trusted pressure-recovery hook publishes on a bounded channel;
	// drain it continuously so recovery is never refused for capacity and the
	// observation is retained in the maintenance journal.
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for {
			select {
			case <-c.ctx.Done():
				for {
					select {
					case record := <-c.records:
						c.recordRecovery(record)
					default:
						return
					}
				}
			case record := <-c.records:
				c.recordRecovery(record)
			}
		}
	}()
}

func (c *ec4ConcurrentCheckpoints) recordRecovery(record checkpointMaintenanceRecord) {
	if err := ec4Journal(c.journal, map[string]any{"phase": "dispatcher_recovery", "recovery": record.Result.Recovery, "result": record.Result, "error": record.Error, "deferral": record.Deferral}); err != nil {
		c.mu.Lock()
		if c.err == nil {
			c.err = err
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	c.dispatcherRec++
	c.mu.Unlock()
}

// stop cancels any in-flight PASSIVE turn and joins the one owned goroutine.
func (c *ec4ConcurrentCheckpoints) stop() {
	c.cancel()
	c.wg.Wait()
}

func (c *ec4ConcurrentCheckpoints) ticksSnapshot() int64 { return c.ticks.Load() }

func (c *ec4ConcurrentCheckpoints) errValue() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *ec4ConcurrentCheckpoints) recordAccept(ns int64, overlapped bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ns > c.acceptMax {
		c.acceptMax = ns
	}
	if ns > int64(25*time.Millisecond) {
		c.over25++
	}
	if overlapped {
		c.overlapCount++
		if ns > c.overlapMax {
			c.overlapMax = ns
		}
		if ns > int64(25*time.Millisecond) {
			c.overlapOver25++
		}
	}
}

func (c *ec4ConcurrentCheckpoints) report(r ec4ScreenReport) ec4ScreenReport {
	c.mu.Lock()
	defer c.mu.Unlock()
	r.Checkpoints, r.Recoveries = c.checkpoints, c.recoveries
	r.MaintenanceNS = c.totalNS
	r.CheckpointOverlappingCommands = c.overlapCount
	r.AcceptMaxNS = c.acceptMax
	r.AcceptMaxOverlapNS = c.overlapMax
	r.AcceptOver25MS = c.over25
	r.AcceptOverlapOver25MS = c.overlapOver25
	r.CheckpointMaxNativeNS = c.nativeMax
	r.CheckpointMaxCallNS = c.callMax
	r.DispatcherRecoveries = c.dispatcherRec
	return r
}

// step issues ONE existing owned PASSIVE (or recovery) turn. Step and RecoverWAL
// share stepMu, so the one goroutine plus a rare recovery request can never
// overlap the keeper.
func (c *ec4ConcurrentCheckpoints) step(recovery bool) error {
	c.stepMu.Lock()
	defer c.stepMu.Unlock()
	if c.ctx.Err() != nil {
		return nil
	}
	started := time.Now()
	c.ticks.Add(1)
	var result governor.CheckpointResult
	var err error
	if recovery {
		result, err = c.cp.cp.RecoverWAL(c.ctx, c.cp.cpRequest())
	} else {
		result, err = c.cp.cp.Step(c.ctx, c.cp.cpRequest())
	}
	c.ticks.Add(1)
	callNS := time.Since(started).Nanoseconds()
	var nativeNS int64
	if !result.NativeStarted.IsZero() && result.NativeEnded.After(result.NativeStarted) {
		nativeNS = result.NativeEnded.Sub(result.NativeStarted).Nanoseconds()
	}
	// The keeper's own record distinguishes a cadence/turn deferral (no grant or
	// native call, not a failure) from a real refusal. Never end the scan because
	// the concurrent PASSIVE or a writer pressure turn owned the same keeper.
	record := checkpointRecord(result, err)
	if journalErr := ec4Journal(c.journal, map[string]any{"recovery": recovery, "result": result, "error": record.Error, "deferral": record.Deferral}); journalErr != nil {
		return journalErr
	}
	c.mu.Lock()
	if record.Deferral == "" {
		if recovery {
			c.recoveries++
		} else {
			c.checkpoints++
		}
	}
	c.totalNS += callNS
	if nativeNS > c.nativeMax {
		c.nativeMax = nativeNS
	}
	if callNS > c.callMax {
		c.callMax = callNS
	}
	c.mu.Unlock()
	select {
	case c.progress <- struct{}{}:
	default:
	}
	if !record.failed() {
		return nil
	}
	if record.Error != "" {
		return fmt.Errorf("screening maintenance: %s", record.Error)
	}
	return fmt.Errorf("screening maintenance refused: %v physical_safe=%v", result.Refusal, result.PhysicalSafe)
}

// waitStep waits for the next completed concurrent PASSIVE turn instead of
// running an inline one, so the scan loop never blocks on checkpoint work.
func (c *ec4ConcurrentCheckpoints) waitStep(ctx context.Context) error {
	select {
	case <-c.progress:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.ctx.Done():
		if err := c.errValue(); err != nil {
			return err
		}
		return ErrClosed
	}
}

// recoverNow is the rare WAL-space recovery path, serialised with the owned
// PASSIVE turn on the same keeper.
func (c *ec4ConcurrentCheckpoints) recoverNow() error { return c.step(true) }

func TestEC4NativeScreening(t *testing.T) {
	if os.Getenv("PORTICO_EC4_SCREENING") != "1" {
		t.Skip("opt-in SCREENING only")
	}
	count, e := strconv.Atoi(os.Getenv("PORTICO_EC4_FILES"))
	if e != nil || count < 1 || count > 400000 {
		t.Fatal("invalid screening count")
	}
	out := os.Getenv("PORTICO_EC4_OUTPUT")
	qualification := os.Getenv("PORTICO_EC4_QUALIFICATION") == "1"
	label := "SCREENING"
	missing := []string{
		"Basic readiness null: no end-to-end Basic producer exists; the lock row derives it from the qualified Basic write class times the population",
		"sparse metadata fixture, not media-read qualification",
		"generated directory metadata is warm; applied io.max is not cold-HDD qualification",
		"single-writer isolated browse observation; formal current asset/info_epoch witness not claimed",
	}
	if qualification {
		label = "QUALIFICATION"
		missing = append(missing, "qualification runs the adopted governor.Phase1aWALPolicy")
	} else {
		missing = append(missing, "screening uses the 96 MiB stress WAL fixture, not the adopted Phase1a policy")
	}
	r := ec4ScreenReport{Features: os.Getenv("PORTICO_EC4_FEATURES"), Capabilities: map[string]bool{"inventory": true, "Basic_binding": false, "folder_binding": false}, Label: label, Qualification: qualification, Source: os.Getenv("PORTICO_EC4_SOURCE"), Profile: os.Getenv("PORTICO_EC4_STORAGE"), Requested: count, Phase: "fixture", Missing: missing}
	if data, e := os.ReadFile("/proc/self/cgroup"); e == nil {
		r.Cgroup = string(data)
	}
	var checkpoints *ec4ConcurrentCheckpoints
	defer func() {
		if t.Failed() && r.Error == "" {
			r.Error = "screening setup failed; see raw test output"
		}
		report := r
		if checkpoints != nil {
			report = checkpoints.report(report)
		}
		if e := ec4WriteReport(out, report); e != nil {
			t.Error(e)
		}
	}()
	root := os.Getenv("PORTICO_EC4_MEDIA_ROOT")
	if root == "" {
		root = filepath.Join(t.TempDir(), "library")
	}
	started := time.Now()
	fixture, e := ec4MakeScreenFixture(t, root, count, ec4ScreenSeed)
	if e != nil {
		r.Error = e.Error()
		t.Fatal(e)
	}
	r.Fixture, r.GenerationNS = fixture, time.Since(started).Nanoseconds()
	r.Phase = "composition"
	d, _, cp := n1ComposedDriverFixtureState(t, n1EC4ScreeningFixture)
	r.WALPolicy = ec4WALPolicy{HardBytes: cp.policy.HardBytes, ReserveBytes: cp.policy.ReserveBytes, SoftDebtBytes: cp.policy.SoftDebtBytes, ActivePressureBytes: cp.policy.ActivePressureBytes, ConcurrentPassive: cp.policy.ConcurrentPassive}
	// #1620's pressure recovery only runs when trusted composition attaches the
	// SAME funded keeper's request/results hook (the WC and publisher targets do
	// this); without it a steady scan refuses on observed WAL pressure forever.
	recoveryRecords := make(chan checkpointMaintenanceRecord, 256)
	d.writer.effective.maintenance = &checkpointMaintenance{request: cp.cpRequest(), results: recoveryRecords}
	if e := ec4RecoveryGate(d); e != nil {
		t.Fatal(e)
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	client := storage.New(binary)
	d.observer = client.NativeInventoryObserver()
	directory, e := os.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	rootIdentity, e := storage.FileObjectIdentity(directory)
	closeErr := directory.Close()
	if e = errors.Join(e, closeErr); e != nil {
		t.Fatal(e)
	}
	if _, e = cp.w.db.ExecContext(t.Context(), `UPDATE library_sources SET root=?,root_identity=? WHERE id='lifecycle-new-source'`, root, rootIdentity); e != nil {
		t.Fatal(e)
	}
	r.Phase = "reader_setup"
	serving := ec4AttachReader(t, cp)
	journalPath := filepath.Join(filepath.Dir(out), "originals.ndjson")
	journal, e := os.OpenFile(journalPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer journal.Close()
	maintenanceJournal, e := os.OpenFile(filepath.Join(filepath.Dir(out), "maintenance.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer maintenanceJournal.Close()
	r.Phase = "create"
	origin := time.Now()
	// The owned keeper PASSIVE turn runs on its own goroutine, concurrent with
	// the writer like production; the scan loop never steps checkpoints inline.
	checkpoints = newEC4ConcurrentCheckpoints(t.Context(), cp, maintenanceJournal, recoveryRecords)
	run := func() error {
		create, e := d.PrepareCreate(t.Context(), ScanRunInput{7, "lifecycle-library", "lifecycle-new-source", "first-import"})
		if e != nil {
			return e
		}
		createRef, _, e := ec4ConsumeLifecycle(t.Context(), create, journal)
		if e != nil {
			return e
		}
		claim, e := d.PrepareClaim(t.Context(), createRef)
		if e != nil {
			return e
		}
		claimRef, lease, e := ec4ConsumeLifecycle(t.Context(), claim, journal)
		if e != nil {
			return e
		}
		last := claimRef
		seen := make([]bool, count)
		checkpoints.start()
		defer checkpoints.stop()
		for r.Accepted < count {
			if e = checkpoints.errValue(); e != nil {
				return e
			}
			// Renew only when the current lease is close to expiry, at the top of
			// the turn with no preparation/work/unsettled result live. The owner
			// path is PrepareClaim(previous settled ref) -> consume/ACK -> Lease.
			if lease != nil && time.Until(time.UnixMilli(lease.expiresMS)) < 20*time.Second {
				r.Phase = "renew"
				claim, e = d.PrepareClaim(t.Context(), claimRef)
				if e != nil {
					return e
				}
				claimRef, lease, e = ec4ConsumeLifecycle(t.Context(), claim, journal)
				if e != nil {
					return e
				}
				last = claimRef
				r.Renewals++
			}
			r.Phase = "observe"
			preparedAt := time.Now()
			preparation, e := d.PrepareInventory(t.Context(), lease, last, []byte("."))
			if e != nil {
				return e
			}
			snapshot, e := preparation.Snapshot(t.Context())
			if e != nil {
				return e
			}
			page, e := preparation.ObserveInventory(t.Context())
			if e != nil {
				return e
			}
			if page.Complete && len(page.Entries) == 0 {
				r.NativeEOFObserved = true
				_ = preparation.Close(t.Context())
				return fmt.Errorf("native EOF after %d/%d accepted", r.Accepted, count)
			}
			in, e := ec4MapObserved(snapshot, page)
			if e != nil {
				return e
			}
			name := string(in.Files[0].RelativePath)
			if !strings.HasPrefix(name, "EC4 ") || len(name) < 14 {
				return fmt.Errorf("unexpected native name")
			}
			ordinal, e := strconv.Atoi(name[4:13])
			if e != nil || ordinal < 0 || ordinal >= count || seen[ordinal] {
				return fmt.Errorf("duplicate/outside fixture ordinal")
			}
			r.Phase = "freeze"
			command, e := preparation.Freeze(t.Context(), in)
			if e != nil {
				return e
			}
			original, e := InventoryAcceptReference(command)
			if e != nil {
				return e
			}
			saveStarted := time.Now()
			if e = ec4Journal(journal, map[string]any{"phase": "original", "original": original, "input": in, "prepared_at": preparedAt, "save_started_at": saveStarted}); e != nil {
				return e
			}
			r.Phase = "accept"
			acceptStarted := time.Now()
			acceptTicks := checkpoints.ticksSnapshot()
			historyOnly := false
			result, e := preparation.Accept(t.Context(), command)
			checkpoints.recordAccept(time.Since(acceptStarted).Nanoseconds(), checkpoints.ticksSnapshot() != acceptTicks)
			if e != nil {
				acceptErr := e
				if e = ec4Journal(journal, map[string]any{"phase": "accept_error", "original": original, "at": time.Now(), "error": acceptErr.Error(), "prepare_to_accept_ns": acceptStarted.Sub(preparedAt).Nanoseconds(), "original_save_ns": acceptStarted.Sub(saveStarted).Nanoseconds(), "accept_ns": time.Since(acceptStarted).Nanoseconds()}); e != nil {
					return errors.Join(acceptErr, e)
				}
				var admission *governor.AdmissionError
				if errors.As(acceptErr, &admission) && (admission.Cause == governor.CauseWALDebt || admission.Cause == governor.CauseWALActive || admission.Cause == governor.CauseWALSpace) {
					// Wait for the concurrent owned PASSIVE turn instead of
					// stepping inline; only WAL-space recovery runs synchronously.
					if admission.Cause == governor.CauseWALSpace {
						if e = checkpoints.recoverNow(); e != nil {
							return errors.Join(acceptErr, e)
						}
					} else if e = checkpoints.waitStep(t.Context()); e != nil {
						return errors.Join(acceptErr, e)
					}
				}
				// A refused or ambiguous ORIGINAL is never replaced/replayed.
				// Only its saved Applied proof can proceed to consumption and ACK.
				// The old ORIGINAL lifetime cannot fund history after expiry.
				// Join/Close it, then let W retain current history custody.
				result, e = ec4ResolveOriginal(t.Context(), d, preparation, original)
				historyOnly = true
				r.HistoryResolutions++
				if e != nil {
					return errors.Join(acceptErr, e)
				}
			}
			if result.Outcome.State != Applied || result.Outcome.Reference != original.Receipt || len(result.Members) != 1 {
				if e = ec4Journal(journal, map[string]any{"phase": "unapplied_original", "original": original, "result": result}); e != nil {
					return e
				}
				if historyOnly {
					if e = d.ReleasePending(t.Context()); e != nil {
						return e
					}
				}
				return ErrResolveRequired
			}
			if e = ec4Journal(journal, map[string]any{"phase": "consumed", "original": original, "result": result}); e != nil {
				return e
			}
			if _, e = d.AckScan(t.Context(), original); e != nil {
				return e
			}
			if historyOnly {
				e = d.ReleasePending(t.Context())
			} else {
				e = preparation.Close(t.Context())
			}
			if e != nil {
				return e
			}
			seen[ordinal] = true
			r.Accepted++
			last = ScanRunReference{Scan: original, Purpose: InventoryAccept}
			if r.FirstFilenameVisibleNS == nil {
				r.Phase = "first_browse"
				browse, e := serving.page(t.Context(), nil)
				if e != nil {
					return e
				}
				for _, item := range browse.Items {
					if item.ID == result.Members[0].SubjectID && item.Title == in.Files[0].Title {
						n := time.Since(origin).Nanoseconds()
						r.FirstFilenameVisibleNS = &n
						break
					}
				}
				if r.FirstFilenameVisibleNS == nil {
					return fmt.Errorf("accepted first filename was not visible through Browse")
				}
			}
			if r.Accepted%100 == 0 {
				r.ScanNS = time.Since(origin).Nanoseconds()
				if e = ec4WriteReport(out, checkpoints.report(r)); e != nil {
					return e
				}
			}
		}
		r.Phase = "all_browse"
		var after *read.BrowseCursor
		visible := make([]bool, count)
		for {
			page, e := serving.page(t.Context(), after)
			if e != nil {
				return e
			}
			for _, item := range page.Items {
				if !strings.HasPrefix(item.Title, "EC4 ") || len(item.Title) < 14 {
					continue
				}
				ordinal, e := strconv.Atoi(item.Title[4:13])
				if e != nil || ordinal < 0 || ordinal >= count || visible[ordinal] {
					return fmt.Errorf("reader duplicate/outside fixture title")
				}
				visible[ordinal] = true
				r.ReaderVisible++
			}
			if page.Exhausted {
				break
			}
			if page.Next == nil {
				return fmt.Errorf("reader missing continuation")
			}
			after = page.Next
		}
		if r.ReaderVisible != count {
			return fmt.Errorf("reader exposed %d/%d accepted filenames", r.ReaderVisible, count)
		}
		n := time.Since(origin).Nanoseconds()
		r.AllFilenameVisibleNS = &n
		r.Phase = "inventory_filename_visible"
		return nil
	}
	e = run()
	r.ScanNS = time.Since(origin).Nanoseconds()
	if e != nil {
		r.Error = e.Error()
		_ = ec4WriteReport(out, checkpoints.report(r))
		t.Fatal(r.Phase, e)
	}
	t.Logf("%s profile=%s accepted=%d reader_visible=%d first_filename=%s all_filename=%s Basic=null(derived) folder=null qualification=%v", r.Label, r.Profile, r.Accepted, r.ReaderVisible, time.Duration(*r.FirstFilenameVisibleNS), time.Duration(*r.AllFilenameVisibleNS), r.Qualification)
}

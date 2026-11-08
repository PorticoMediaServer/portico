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
	Fixture                                                                                   ec4ScreenFixture
	NativeEOFObserved                                                                         bool
	TraversalCommitted                                                                        bool
	Missing                                                                                   []string
	Cgroup                                                                                    string
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

func TestEC4NativeScreening(t *testing.T) {
	if os.Getenv("PORTICO_EC4_SCREENING") != "1" {
		t.Skip("opt-in SCREENING only")
	}
	count, e := strconv.Atoi(os.Getenv("PORTICO_EC4_FILES"))
	if e != nil || count < 1 || count > 400000 {
		t.Fatal("invalid screening count")
	}
	out := os.Getenv("PORTICO_EC4_OUTPUT")
	r := ec4ScreenReport{Features: os.Getenv("PORTICO_EC4_FEATURES"), Capabilities: map[string]bool{"inventory": true, "Basic_binding": false, "folder_binding": false}, Label: "SCREENING", Source: os.Getenv("PORTICO_EC4_SOURCE"), Profile: os.Getenv("PORTICO_EC4_STORAGE"), Requested: count, Phase: "fixture", Missing: []string{"Basic accepting/reader timing awaits SC harness binding", "folder completion timing awaits SC harness binding", "sparse metadata fixture, not media-read qualification", "generated directory metadata is warm; applied io.max is not cold-HDD qualification", "single-writer isolated browse observation; formal current asset/info_epoch witness not claimed"}}
	if data, e := os.ReadFile("/proc/self/cgroup"); e == nil {
		r.Cgroup = string(data)
	}
	defer func() {
		if t.Failed() && r.Error == "" {
			r.Error = "screening setup failed; see raw test output"
		}
		if e := ec4WriteReport(out, r); e != nil {
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
	maintenance := func(recovery bool) error {
		started := time.Now()
		var result governor.CheckpointResult
		var err error
		if recovery {
			result, err = cp.cp.RecoverWAL(t.Context(), cp.cpRequest())
			r.Recoveries++
		} else {
			result, err = cp.cp.Step(t.Context(), cp.cpRequest())
			r.Checkpoints++
		}
		r.MaintenanceNS += time.Since(started).Nanoseconds()
		if journalErr := ec4Journal(maintenanceJournal, map[string]any{"recovery": recovery, "result": result, "error": checkpointError(err)}); journalErr != nil {
			return errors.Join(err, journalErr)
		}
		if err != nil {
			return err
		}
		if result.Refusal != nil || !result.PhysicalSafe {
			return fmt.Errorf("screening maintenance refused: %v physical_safe=%v", result.Refusal, result.PhysicalSafe)
		}
		return nil
	}
	r.Phase = "create"
	origin := time.Now()
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
		renewed := time.Now()
		seen := make([]bool, count)
		lastMaintenance := time.Now()
		for r.Accepted < count {
			// Keep is resident custody, not a scheduler. Offer the SAME owner's
			// normal bounded PASSIVE turn between commands, at its existing cadence.
			if time.Since(lastMaintenance) >= 50*time.Millisecond {
				r.Phase = "maintenance"
				if e = maintenance(false); e != nil {
					return e
				}
				lastMaintenance = time.Now()
			}
			if time.Since(renewed) > 250*time.Millisecond {
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
				renewed = time.Now()
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
			historyOnly := false
			result, e := preparation.Accept(t.Context(), command)
			if e != nil {
				acceptErr := e
				if e = ec4Journal(journal, map[string]any{"phase": "accept_error", "original": original, "at": time.Now(), "error": acceptErr.Error(), "prepare_to_accept_ns": acceptStarted.Sub(preparedAt).Nanoseconds(), "original_save_ns": acceptStarted.Sub(saveStarted).Nanoseconds(), "accept_ns": time.Since(acceptStarted).Nanoseconds()}); e != nil {
					return errors.Join(acceptErr, e)
				}
				var admission *governor.AdmissionError
				if errors.As(acceptErr, &admission) && (admission.Cause == governor.CauseWALDebt || admission.Cause == governor.CauseWALActive || admission.Cause == governor.CauseWALSpace) {
					if e = maintenance(false); e != nil {
						return e
					}
					if admission.Cause == governor.CauseWALSpace {
						if e = maintenance(true); e != nil {
							return e
						}
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
				if e = ec4WriteReport(out, r); e != nil {
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
		_ = ec4WriteReport(out, r)
		t.Fatal(r.Phase, e)
	}
	t.Logf("SCREENING profile=%s accepted=%d reader_visible=%d first_filename=%s all_filename=%s Basic=null folder=null qualification=false", r.Profile, r.Accepted, r.ReaderVisible, time.Duration(*r.FirstFilenameVisibleNS), time.Duration(*r.AllFilenameVisibleNS))
}

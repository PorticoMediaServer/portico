//go:build linux

package writer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func init() { registerCrashDomain("inventory_batch", crashInventoryBatchChild) }

type inventoryBatchCrashSource struct {
	Input    InventoryAcceptInput
	Identity CommandIdentity
	Original ScanReceiptRequest
	Baseline n1DriverBaseline
	Initial  map[string][32]byte
	Trace    []string
}

// Serialize the frozen original before any native effect. These are original
// values only; no expected catalogue row is taken from the writer's result.
func batchOriginalInput(c *inventoryAcceptCommand) InventoryAcceptInput {
	r, s, u, d := c.request, c.request.source, c.request.run, c.request.directory
	in := InventoryAcceptInput{Scope: InventoryScopeInput{r.scope.runID, r.scope.libraryKey, r.scope.libraryID},
		Source:    InventorySourceInput{ID: s.id, Incarnation: s.incarnation, RootIdentity: s.rootIdentity, ResolvedRoot: bytes.Clone(s.resolvedRoot), Generation: s.generation, FollowSymlinks: s.followSymlinks},
		Run:       InventoryRunInput{Revision: u.revision, PolicyRevision: u.policyRevision, LeaseOwner: u.leaseOwner, LeaseGeneration: u.leaseGeneration, LeaseExpiresMS: u.leaseExpiresMS, LastSequence: u.lastSequence, AcknowledgedSequence: u.acknowledgedSequence, Phase: u.phase, ScanMode: u.scanMode},
		Directory: InventoryDirectoryInput{Path: []byte(d.path), Identity: d.identity, Revision: d.revision, ExpectedCursor: d.expectedCursor, NextCursor: d.nextCursor, CursorKind: d.cursorKind, Pages: d.pages, Skipped: d.skipped, RawNames: d.rawNames, Complete: d.complete, Unstable: d.unstable}}
	for _, f := range r.files {
		in.Files = append(in.Files, InventoryFilenameInput{RelativePath: []byte(f.relativePath), AssetPath: []byte(f.assetPath), SourceKey: f.sourceKey, ObjectIdentity: f.objectIdentity, Revision: f.revision, ChangeToken: f.changeToken, Directory: f.directory, Size: f.size, ModifiedNS: f.modifiedNS, Title: f.title, SortTitle: f.sortTitle, SortKey: f.sortKey, SortHead: f.sortHead, SortKeyKeepArticles: f.sortKeyKeepArticles, SortHeadKeepArticles: f.sortHeadKeepArticles, Year: f.year, Part: f.part, MetadataLanguage: f.metadataLanguage})
	}
	return in
}

func crashInventoryBatchChild(t *testing.T, w *crashWitness) {
	width, err := strconv.Atoi(w.Case.Operation)
	if err != nil || (width != 2 && width != maxInventoryBatchFiles) {
		t.Fatal("unknown batch width")
	}
	d, p, command, q := inventoryComposedDriverPrepared(t, width, n1BatchForecastFixture)
	e := d.opening.executor
	c := command.(*inventoryAcceptCommand)
	clock := e.now()
	source := inventoryBatchCrashSource{Input: batchOriginalInput(c), Identity: c.identity, Original: q}
	source.Baseline = n1DriverCaptureBaseline(t, t.Context(), e.db, n1DriverInputFrom(c))
	source.Baseline.ClockMS = clock.UnixMilli()
	source.Initial, err = n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	w.Path, w.Reference = e.fileBinding.path, q.Receipt
	ids := map[string]string{}
	for _, entry := range d.writer.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	counts := map[string]int{}
	point := func(phase string) {
		w.Payload, err = json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
		crashPoint(t, w, phase)
	}
	armed := false
	installCrashDriverAt(t, e, w.Path, w, clock, func(query string) {
		if !armed {
			return
		}
		id, ok := ids[strings.TrimSpace(query)]
		if !ok {
			return
		}
		counts[id]++
		phase := fmt.Sprintf("native_%s_%03d", id, counts[id])
		source.Trace = append(source.Trace, phase)
		point(phase)
	})
	// Terminal hooks live in the shared driver; keep the original payload
	// installed even before the first SQL callback (before_begin/after_begin).
	w.Payload, err = json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	armed = true
	out, err := p.Accept(t.Context(), command)
	armed = false
	if w.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || !reflect.DeepEqual(out, InventoryResult{}) || !e.ambiguous || !d.writer.effective.fenced {
			t.Fatal("uncertain batch COMMIT reported success", out, err)
		}
		w.Unknown = true
		point("uncertain_return")
		t.Fatal("missing unknown boundary")
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = checkInventoryBatchCrashCommitted(t, e, source); err != nil {
		t.Fatal(err)
	}
	if w.Case.Boundary == "trace" {
		w.Payload, err = json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	point("api_return")
	t.Fatal("batch crash boundary not reached")
}

func checkInventoryBatchCrashCommitted(t *testing.T, e *executor, s inventoryBatchCrashSource) (InventoryResult, error) {
	command, err := NewInventoryAcceptCommand(s.Identity, s.Input)
	if err != nil {
		return InventoryResult{}, err
	}
	c := command.(*inventoryAcceptCommand)
	q, err := InventoryAcceptReference(command)
	if err != nil || q != s.Original {
		return InventoryResult{}, errors.New("original batch changed")
	}
	in := n1DriverInputFrom(c)
	journal, err := readN1DriverJournal(t.Context(), e.db, in)
	if err != nil {
		return InventoryResult{}, err
	}
	v := inventoryAcceptResult{RunID: q.RunID, AcceptedRunRevision: s.Baseline.RunRevision + 1, AppliedMS: s.Baseline.ClockMS, SourceID: s.Input.Source.ID, SourceIncarnation: s.Input.Source.Incarnation, SourceRevision: s.Input.Files[0].Revision, NextCursor: s.Input.Directory.NextCursor, ResultCount: int64(len(s.Input.Files))}
	if len(journal.Children) != len(s.Input.Files) {
		return InventoryResult{}, errors.New("partial batch receipt")
	}
	out := InventoryResult{Outcome: ReceiptOutcome{State: Applied, Reference: q.Receipt}, RunID: v.RunID, AcceptedRunRevision: v.AcceptedRunRevision, AppliedMS: v.AppliedMS, SourceID: v.SourceID, SourceIncarnation: v.SourceIncarnation, SourceRevision: v.SourceRevision, NextCursor: v.NextCursor, ResultCount: v.ResultCount}
	for i, ch := range journal.Children {
		if ch.InputOrdinal != int64(i) || ch.ChildKind != 0 || ch.ChildOrdinal != 0 {
			return InventoryResult{}, errors.New("batch ordinal changed")
		}
		m := inventoryResultMember{ch.InputOrdinal, ch.ChildKind, ch.ChildOrdinal, ch.ObjectID, ch.LibraryID, ch.SubjectID, ch.AssetID, ch.AcceptedAssetRevision}
		v.Members = append(v.Members, m)
		out.Members = append(out.Members, InventoryMember(m))
	}
	// CR's independent byte encoder, not the production result codec.
	digest, err := n1DriverResultDigest(s.Identity, q.Receipt.RequestDigest, v)
	if err != nil {
		return InventoryResult{}, err
	}
	out.Digest = digest
	want := inventoryCrashHeader{RunID: q.RunID, Incarnation: bytes.Clone(s.Identity.Incarnation[:]), Sequence: s.Identity.Sequence, OperationID: bytes.Clone(s.Identity.OperationID[:]), RequestDigest: bytes.Clone(q.Receipt.RequestDigest[:]), Kind: "inventory_accept", AcceptedRevision: v.AcceptedRunRevision, AppliedMS: v.AppliedMS, SourceID: v.SourceID, SourceIncarnation: v.SourceIncarnation, SourceRevision: v.SourceRevision, NextCursor: v.NextCursor, ResultCount: v.ResultCount, ResultDigest: bytes.Clone(digest[:])}
	if !reflect.DeepEqual(journal.Header, want) {
		return InventoryResult{}, errors.New("batch receipt header differs from input/raw oracle")
	}
	if err = checkInventoryBatchEffects(t, e, c, s.Baseline, out); err != nil {
		return InventoryResult{}, err
	}
	current, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		return InventoryResult{}, err
	}
	unchanged := map[string][32]byte{}
	for name, digest := range s.Initial {
		unchanged[name] = digest
	}
	for _, name := range strings.Fields(`catalog_subjects catalog_identities catalog_assets catalog_entities catalog_asset_links inventory_objects catalog_scalar_contributions catalog_asset_contributions scan_command_receipts scan_command_results search_publish_jobs catalog_age_item_counts inventory_directories jobs inventory_runs libraries library_sources catalog_membership_clock search_revision search_generations`) {
		delete(unchanged, name)
		delete(current, name)
	}
	if err = compareN1DriverCheckpoint(unchanged, current); err != nil {
		return InventoryResult{}, err
	}
	return out, nil
}

func TestCrashReplayInventoryBatchEveryStep(t *testing.T) {
	if raceDetector {
		t.Skip("SIGKILL matrix runs without race; run deterministic batch controls under race")
	}
	for _, width := range []int{2, maxInventoryBatchFiles} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			trace := crashWitness{Case: crashCase{Domain: "inventory_batch", Operation: strconv.Itoa(width), Boundary: "trace"}}
			crashInventoryBatchChild(t, &trace)
			var original inventoryBatchCrashSource
			if err := json.Unmarshal(trace.Payload, &original); err != nil {
				t.Fatal(err)
			}
			if len(original.Trace) < width*20 {
				t.Fatal("batch trace missed repeated members", len(original.Trace))
			}
			boundaries := append([]string{"before_begin", "after_begin"}, original.Trace...)
			boundaries = append(boundaries, "before_commit", "after_commit", "physical_return", "api_return")
			for _, phase := range boundaries {
				t.Run(phase, func(t *testing.T) {
					verifyInventoryBatchCrash(t, runCrashChild(t, crashCase{"inventory_batch", strconv.Itoa(width), phase, ""}))
				})
			}
			for _, fault := range []string{"before", "after"} {
				t.Run("uncertain_"+fault, func(t *testing.T) {
					verifyInventoryBatchCrash(t, runCrashChild(t, crashCase{"inventory_batch", strconv.Itoa(width), "uncertain_return", fault}))
				})
			}
		})
	}
}

func verifyInventoryBatchCrash(t *testing.T, w crashWitness) {
	var s inventoryBatchCrashSource
	if err := json.Unmarshal(w.Payload, &s); err != nil {
		t.Fatal(err)
	}
	if w.Reference != s.Original.Receipt || w.Unknown != (w.Case.CommitFault != "") {
		t.Fatal("lost original/unknown state")
	}
	d := n1ComposedDriverOnExisting(t, w.Path)
	e := d.opening.executor
	var integrity string
	if err := e.db.QueryRowContext(t.Context(), `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	rows, err := e.db.QueryContext(t.Context(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("batch foreign key violation")
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if !crashCommitted(w.Case) {
		state, err := n1DriverCheckpoint(t.Context(), e.db)
		if err != nil {
			t.Fatal(err)
		}
		if err = compareN1DriverCheckpoint(s.Initial, state); err != nil {
			t.Fatal("partial batch survived", err)
		}
		pending, err := d.ResolveScan(t.Context(), s.Original)
		if err != nil || pending.Outcome.State != Pending || len(pending.Members) != 0 {
			t.Fatal("rollback became success", pending, err)
		}
		return
	}
	want, err := checkInventoryBatchCrashCommitted(t, e, s)
	if err != nil {
		t.Fatal(err)
	}
	before, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.ResolveScan(t.Context(), s.Original)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Outcome.Replayed || got.Digest != want.Digest || !reflect.DeepEqual(got.Members, want.Members) {
		t.Fatal("restart replay lost cohort", got, want)
	}
	after, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("restart replay wrote", err)
	}
	ack, err := d.AckScan(t.Context(), s.Original)
	if err != nil || ack.AcknowledgedThrough != s.Identity.Sequence {
		t.Fatal(ack, err)
	}
	before, err = n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.AckScan(t.Context(), s.Original); err != nil {
		t.Fatal(err)
	}
	after, err = n1DriverCheckpoint(t.Context(), e.db)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("double ACK wrote", err)
	}
	if err = d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryBatchOracleRejectsLaterMemberCorruption(t *testing.T) {
	for _, mutation := range []string{"child", "title", "progress"} {
		t.Run(mutation, func(t *testing.T) {
			d, p, command, q := inventoryComposedDriverPrepared(t, 2, n1BatchForecastFixture)
			e := d.opening.executor
			c := command.(*inventoryAcceptCommand)
			s := inventoryBatchCrashSource{Input: batchOriginalInput(c), Identity: c.identity, Original: q}
			s.Baseline = n1DriverCaptureBaseline(t, t.Context(), e.db, n1DriverInputFrom(c))
			s.Baseline.ClockMS = e.now().UnixMilli()
			var err error
			s.Initial, err = n1DriverCheckpoint(t.Context(), e.db)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Accept(t.Context(), command); err != nil {
				t.Fatal(err)
			}
			if _, err = checkInventoryBatchCrashCommitted(t, e, s); err != nil {
				t.Fatal("control failed", err)
			}
			switch mutation {
			case "child":
				_, err = e.db.ExecContext(t.Context(), `UPDATE scan_command_results SET object_id='corrupted' WHERE run_id=? AND command_sequence=? AND input_ordinal=1`, q.RunID, c.identity.Sequence)
			case "title":
				_, err = e.db.ExecContext(t.Context(), `UPDATE catalog_entities SET title='corrupted' WHERE id=(SELECT subject_id FROM catalog_identities WHERE library_id=7 AND source_key=?)`, c.request.files[1].sourceKey)
			case "progress":
				_, err = e.db.ExecContext(t.Context(), `UPDATE jobs SET processed=processed-1 WHERE id=?`, q.RunID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = checkInventoryBatchCrashCommitted(t, e, s); err == nil {
				t.Fatal("later-member corruption escaped oracle")
			}
		})
	}
}

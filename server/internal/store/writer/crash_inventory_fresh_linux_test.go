package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Before the fixture's captured 4000000000000ms lease, and after real runtime
// deadlines. Virtual time and W/SC's numerical fixtures are NOT qualification.
var freshCrashClock = time.Date(2090, 11, 6, 10, 30, 0, 0, time.UTC)

type freshCrashIdentity struct {
	Public, Metadata, Artwork, EntityPlayback, AssetPlayback []byte
	Random                                                   int64
	AssetToken                                               string
}

func readFreshCrashIdentity(ctx context.Context, reader inventoryCrashReader) (out freshCrashIdentity, err error) {
	err = reader.QueryRowContext(ctx, `SELECT s.public_id,e.metadata_incarnation,e.artwork_incarnation,e.playback_origin_incarnation,
 e.random_key,a.token,a.playback_origin_incarnation FROM catalog_subjects s JOIN catalog_entities e ON e.id=s.id
 JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id`).Scan(
		&out.Public, &out.Metadata, &out.Artwork, &out.EntityPlayback, &out.Random, &out.AssetToken, &out.AssetPlayback)
	return out, err
}

func compareFreshCrashIdentity(want, got freshCrashIdentity) error {
	if !reflect.DeepEqual(want, got) {
		return fmt.Errorf("fresh generated identity changed: got %+v, want %+v", got, want)
	}
	return nil
}

type freshCrashTrace struct {
	events []string
	armed  bool
}

func freshCrashFixture(t *testing.T, witness *crashWitness) (*executor, *inventoryAcceptCommand, *commandTurn, *governor.Authority, *freshCrashTrace) {
	t.Helper()
	e, path, command, registry := inventoryFreshFixture(t)
	q, err := command.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	witness.Path, witness.Reference = path, q.Receipt
	ids := map[string]stmt.ID{}
	for _, d := range scanInventoryFreshDefinitions() {
		ids[strings.TrimSpace(d.SQL)] = d.ID
	}
	trace := &freshCrashTrace{armed: true}
	uses := map[stmt.ID]int{}
	installCrashDriverAt(t, e, path, witness, freshCrashClock, func(query string) {
		if !trace.armed {
			return
		}
		if id, ok := ids[strings.TrimSpace(query)]; ok {
			uses[id]++
			if uses[id] > inventoryFreshMaxUses(id) {
				t.Fatal("fresh component exceeded finite native uses", id, uses[id])
			}
			phase := fmt.Sprintf("native_%02d_%s", len(trace.events), id)
			trace.events = append(trace.events, phase)
			crashPoint(t, witness, phase)
		}
	})
	turn, authority := inventoryFreshTestTurn(t, e, path, registry)
	return e, command, turn, authority, trace
}

func crashFreshInventoryChild(t *testing.T, witness *crashWitness) {
	t.Helper()
	if witness.Case.Operation != "component" {
		t.Fatal("unsupported fresh component operation", witness.Case)
	}
	e, command, turn, authority, trace := freshCrashFixture(t, witness)
	out, err := applyInventoryAccept(t.Context(), turn, command)
	if err != nil {
		t.Fatal("private body failed", out, err)
	}
	trace.armed = false // raw verdict reads are not command phases
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	// Observe the original raw receipt on this SAME transaction, independently
	// of the body result. It witnesses generated identities, not source proof.
	journal, err := checkFreshCrashProjection(t.Context(), turn.transaction.conn, command, out)
	if err != nil {
		t.Fatal(err)
	}
	witness.InventoryJournal = &journal
	identity, err := readFreshCrashIdentity(t.Context(), turn.transaction.conn)
	if err != nil {
		t.Fatal(err)
	}
	witness.FreshIdentity = &identity
	physical, err := turn.transaction.commit()
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || physical.state != commitUnknown || !e.ambiguous || !e.fenced {
			t.Fatal("uncertain component COMMIT reported success", physical, err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
	} else if err != nil || physical.state != committed {
		t.Fatal(physical, err)
	}
	if authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("component did not physically retire")
	}
	crashPoint(t, witness, "api_return") // private frame return, NOT public acceptance
	t.Fatal("fresh component boundary was never reached", witness.Case)
}

func freshCrashRow(ctx context.Context, db inventoryCrashReader, query string, args ...any) (out []any, err error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out = make([]any, len(columns))
	dest := make([]any, len(out))
	for i := range out {
		dest[i] = &out[i]
	}
	if !rows.Next() {
		return nil, errors.Join(sql.ErrNoRows, rows.Err())
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	if rows.Next() {
		return nil, errors.New("fresh oracle expected exactly one raw row")
	}
	return out, rows.Err()
}

// Expected scalar values come from the ORIGINAL fixed source journal and the
// witnessed COMMIT boundary, not applyInventoryAccept or its receipt decoder.
// Returned generated IDs are observed only through their native rows and must
// agree across all references and the exact raw pre-COMMIT receipt.
func checkFreshCrashState(ctx context.Context, reader inventoryCrashReader, c *inventoryAcceptCommand, committed, acknowledged bool) (inventoryCrashJournal, error) {
	var zero inventoryCrashJournal
	n, floor := int64(0), int64(18)
	if committed {
		n = 1
	}
	if acknowledged {
		floor = 19
	}
	check := func(name, query string, want []any, args ...any) error {
		got, err := freshCrashRow(ctx, reader, query, args...)
		if err != nil {
			return fmt.Errorf("fresh %s: %w", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			return fmt.Errorf("fresh %s: got %#v, want %#v", name, got, want)
		}
		return nil
	}
	for _, table := range []string{"catalog_subjects", "catalog_identities", "catalog_assets", "catalog_entities", "catalog_asset_links", "inventory_objects", "catalog_scalar_contributions", "catalog_asset_contributions", "scan_command_receipts", "scan_command_results", "search_publish_jobs"} {
		if err := check(table, "SELECT count(*) FROM "+table, []any{n}); err != nil {
			return zero, err
		}
	}
	for _, table := range []string{"search_publications", "search_fragments"} {
		if err := check(table, "SELECT count(*) FROM "+table, []any{int64(0)}); err != nil {
			return zero, err
		}
	}
	cursor := ""
	var progress any
	if committed {
		cursor, progress = "42", freshCrashClock.Format("2006-01-02T15:04:05.000000000Z")
	}
	for _, point := range []struct {
		name, query string
		want        []any
	}{
		{"library clocks", `SELECT data_revision,data_order_revision,data_metadata_revision,age_input_revision,age_published_revision,age_pending_operations FROM libraries WHERE id='fresh-library'`, []any{71 + n, 73 + n, 79 + n, 83 + n, 83 + n, int64(0)}},
		{"run", `SELECT job_revision,discovered,last_command_sequence,acknowledged_command_sequence,command_incarnation,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms FROM inventory_runs WHERE job_id='fresh-run'`, []any{41 + n, 59 + n, 18 + n, floor, c.identity.Incarnation[:], "fresh-source", int64(23), "fresh-incarnation", "dev:7:ino:1", int64(7), "inventory", "first-import", int64(13), int64(17), int64(4000000000000)}},
		{"job", `SELECT processed,status FROM jobs WHERE id='fresh-run'`, []any{67 + n, "running"}},
		{"directory", `SELECT cursor,pages,skipped,state,identity,revision FROM inventory_directories WHERE job_id='fresh-run' AND relative_path='.'`, []any{cursor, 5 + n, int64(3), "pending", "directory:11", "directory:013"}},
		{"source progress", `SELECT progress_present,progress_last_progress_at FROM library_sources WHERE id='fresh-source'`, []any{n, progress}},
		{"membership clock", `SELECT sequence FROM catalog_membership_clock WHERE singleton=1`, []any{89 + n}},
		{"unrated count", `SELECT total FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=0 AND admission_age=-1`, []any{97 + n}},
		{"other rating count", `SELECT total FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=1 AND admission_age=7`, []any{int64(101)}},
		{"blocked unrated absent", `SELECT count(*) FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=1 AND admission_age=-1`, []any{int64(0)}},
		{"search clocks", `SELECT revision,input_sequence,active_generation_id,building_generation_id FROM search_revision WHERE singleton=1`, []any{int64(103), 107 + n, int64(1), nil}},
		{"bank", `SELECT pending_subjects,state,bank_key FROM search_generations WHERE id=1`, []any{n, "active", "raw-active"}},
	} {
		if err := check(point.name, point.query, point.want); err != nil {
			return zero, err
		}
	}
	if !committed {
		return zero, nil
	}
	journal, err := readInventoryCrashJournal(ctx, reader, c.request.scope.runID)
	if err != nil {
		return zero, err
	}
	h := journal.Header
	if h.RunID != "fresh-run" || h.Sequence != 19 || !bytes.Equal(h.Incarnation, c.identity.Incarnation[:]) || !bytes.Equal(h.OperationID, c.identity.OperationID[:]) || !bytes.Equal(h.RequestDigest, c.digest[:]) ||
		h.Kind != "inventory_accept" || h.AcceptedRevision != 42 || h.AppliedMS != freshCrashClock.UnixMilli() || h.SourceID != "fresh-source" || h.SourceIncarnation != "fresh-incarnation" || h.SourceRevision != c.request.files[0].revision || h.NextCursor != "42" || h.ResultCount != 1 || len(h.ResultDigest) != 32 || bytes.Equal(h.ResultDigest, make([]byte, 32)) || len(journal.Children) != 1 {
		return zero, fmt.Errorf("fresh receipt differs from original source facts: %+v", journal)
	}
	wantHeader := inventoryCrashHeader{RunID: "fresh-run", Incarnation: c.identity.Incarnation[:], Sequence: 19,
		OperationID: c.identity.OperationID[:], RequestDigest: c.digest[:], Kind: "inventory_accept", AcceptedRevision: 42,
		AppliedMS: freshCrashClock.UnixMilli(), SourceID: "fresh-source", SourceIncarnation: "fresh-incarnation",
		SourceRevision: c.request.files[0].revision, NextCursor: "42", ResultCount: 1, ResultDigest: h.ResultDigest}
	if !reflect.DeepEqual(wantHeader, h) {
		return zero, fmt.Errorf("fresh header has unsupported carrier fields: %+v", h)
	}
	m := journal.Children[0]
	if m.RunID != h.RunID || m.Sequence != h.Sequence || !bytes.Equal(m.Incarnation, h.Incarnation) || m.InputOrdinal != 0 || m.ChildKind != 0 || m.ChildOrdinal != 0 || m.LibraryID != 7 || m.SubjectID <= 0 || m.AssetID <= 0 || len(m.ObjectID) != 32 || m.AcceptedAssetRevision != 1 {
		return zero, fmt.Errorf("fresh receipt member identity: %+v", m)
	}
	for _, point := range []struct {
		name, query string
		want        []any
		args        []any
	}{
		{"identity", `SELECT s.id,s.library_id,s.kind,length(s.public_id),i.source_key,e.public_id=s.public_id FROM catalog_subjects s JOIN catalog_identities i ON i.subject_id=s.id JOIN catalog_entities e ON e.id=s.id`, []any{m.SubjectID, int64(7), int64(1), int64(16), "source-key:001", int64(1)}, nil},
		{"filename publication", `SELECT id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,year,added_at,metadata_language,retired,revision,length(metadata_incarnation),length(artwork_incarnation),length(playback_origin_incarnation),playback_origin_revision FROM catalog_entities`, []any{m.SubjectID, int64(7), int64(1), "Arrival", "Arrival", "arrival", "a", "arrival", "a", int64(2016), progress, "en-US", int64(0), int64(1), int64(16), int64(16), int64(16), int64(1)}, nil},
		{"leaf keys", `SELECT admission_allow_age,admission_known_age,admission_own_age,admission_member_count,admission_unrated_members,admission_min_known_age,resolution_key,duration_key,community_rating_key,critic_rating_key,file_size_key,latest_aired_key,episode_count,aggregate_publication_id,credit_image_id FROM catalog_entities`, []any{int64(-1), nil, nil, int64(0), int64(0), nil, int64(0), float64(0), float64(0), float64(0), int64(42), "", int64(0), nil, nil}, nil},
		{"asset", `SELECT id,path,size,modified_ns,available,info_epoch,file_size_revision,info_owner_object,info_owner_revision,container,video_codec,audio_codec,width,height,duration FROM catalog_assets`, []any{m.AssetID, "/media/Arrival.mkv", int64(42), int64(-11), int64(1), int64(1), int64(1), nil, nil, "", "", "", int64(0), int64(0), float64(0)}, nil},
		{"object", `SELECT o.id,o.asset_id=a.token,o.source_id,o.root_incarnation,o.relative_path,o.object_identity,o.revision,o.evidence_json,o.size,o.modified_ns,o.seen_job,o.directory_path,o.state,o.retired,o.analysis_state,o.analysis_revision,o.has_analysis_probe_bindings FROM inventory_objects o JOIN catalog_assets a ON a.id=?`, []any{m.ObjectID, int64(1), "fresh-source", "fresh-incarnation", "Arrival.mkv", "ino:31", c.request.files[0].revision, `{"kind":"local","scope":"fresh-source:fresh-incarnation","object":"ino:31","size":42,"modifiedNs":-11,"changeToken":"change:29"}`, int64(42), int64(-11), "fresh-run", ".", "available", int64(0), "pending", "", int64(0)}, []any{m.AssetID}},
		{"link", `SELECT library_id,entity_id,asset_id,part_index,available,cue_ordinal,start_seconds,end_seconds FROM catalog_asset_links`, []any{int64(7), m.SubjectID, m.AssetID, int64(0), int64(1), nil, float64(0), nil}, nil},
		{"scalar contribution", `SELECT c.parent_id,c.contributor_id,c.source,c.membership_sequence,c.contributor_revision=e.scalar_source_revision,c.duration,c.community_rating,c.critic_rating,c.resolution,c.latest_aired_key,c.admission_age FROM catalog_scalar_contributions c JOIN catalog_entities e ON e.id=c.parent_id`, []any{m.SubjectID, m.SubjectID, int64(0), int64(90), int64(1), nil, nil, nil, nil, nil, nil}, nil},
		{"physical contribution", `SELECT c.parent_id,c.asset_id,c.membership_refs,c.authored_refs,c.file_size,c.file_size_revision=a.file_size_revision FROM catalog_asset_contributions c JOIN catalog_assets a ON a.id=c.asset_id`, []any{m.SubjectID, m.AssetID, int64(1), int64(0), int64(42), int64(1)}, nil},
		{"publisher intent", `SELECT j.entity_id,j.search_generation_id,j.input_generation,j.published_generation,j.source_incarnation=e.metadata_incarnation,j.source_revision,j.phase,j.job_next_attempt_ms,j.recipe_version=g.recipe_version FROM search_publish_jobs j JOIN catalog_entities e ON e.id=j.entity_id JOIN search_generations g ON g.id=j.search_generation_id`, []any{m.SubjectID, int64(1), int64(1), int64(0), int64(1), int64(1), "capture", freshCrashClock.UnixMilli(), int64(1)}, nil},
	} {
		if err := check(point.name, point.query, point.want, point.args...); err != nil {
			return zero, err
		}
	}
	return journal, nil
}

// Check the complete private return before discarding it. The expected payload
// comes only from independently validated raw state and original input; the
// body has no public Outcome or per-call Evidence before the central frame.
func checkFreshCrashProjection(ctx context.Context, reader inventoryCrashReader, command *inventoryAcceptCommand, got InventoryResult) (inventoryCrashJournal, error) {
	journal, err := checkFreshCrashState(ctx, reader, command, true, false)
	if err != nil {
		return inventoryCrashJournal{}, err
	}
	want, err := inventoryCrashProjection(journal, false)
	if err != nil {
		return inventoryCrashJournal{}, err
	}
	want.Outcome = ReceiptOutcome{}
	if !reflect.DeepEqual(want, got) {
		return inventoryCrashJournal{}, fmt.Errorf("fresh private projection changed: got %+v, want %+v", got, want)
	}
	return journal, nil
}

func verifyFreshInventoryCrash(t *testing.T, command *inventoryAcceptCommand, profile effectivePlan, witness crashWitness) {
	t.Helper()
	q, err := command.receiptRequest()
	if err != nil || witness.Reference != q.Receipt || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("fresh original identity/outcome changed", witness, err)
	}
	e := reopenCrashExecutor(t, witness.Path)
	committed := crashCommitted(witness.Case)
	journal, err := checkFreshCrashState(t.Context(), e.db, command, committed, false)
	if err != nil {
		t.Fatal(err)
	}
	if committed {
		if witness.InventoryJournal == nil {
			t.Fatal("committed component lost original raw journal")
		}
		if err := compareInventoryCrashJournal(*witness.InventoryJournal, journal); err != nil {
			t.Fatal(err)
		}
		identity, err := readFreshCrashIdentity(t.Context(), e.db)
		if err != nil || witness.FreshIdentity == nil {
			t.Fatal("fresh generated identity changed", identity, err)
		}
		if err := compareFreshCrashIdentity(*witness.FreshIdentity, identity); err != nil {
			t.Fatal(err)
		}
	}
	w := bindCrashInventory(t, e, witness.Path, profile)
	cap := inventoryTestDisclosure(t, w, q)
	for range 2 {
		got, err := w.ResolveScan(t.Context(), cap, q)
		if err != nil {
			t.Fatal(err)
		}
		want := InventoryResult{Outcome: ReceiptOutcome{State: Pending, Reference: q.Receipt}}
		if committed {
			want, err = inventoryCrashProjection(journal, false)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := compareInventoryCrashProjection(want, got); err != nil {
			t.Fatal(err)
		}
	}
	if committed {
		for range 2 {
			ack, err := w.AckScan(t.Context(), cap, q)
			if err != nil || ack.AcknowledgedThrough != 19 {
				t.Fatal(ack, err)
			}
		}
		after, err := checkFreshCrashState(t.Context(), e.db, command, true, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareInventoryCrashJournal(journal, after); err != nil {
			t.Fatal(err)
		}
		identity, err := readFreshCrashIdentity(t.Context(), e.db)
		if err != nil {
			t.Fatal("recovery rewrote generated identity", identity, err)
		}
		if err := compareFreshCrashIdentity(*witness.FreshIdentity, identity); err != nil {
			t.Fatal(err)
		}
		want, err := inventoryCrashProjection(after, true)
		if err != nil {
			t.Fatal(err)
		}
		got, err := w.ResolveScan(t.Context(), cap, q)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareInventoryCrashProjection(want, got); err != nil {
			t.Fatal(err)
		}
	} else {
		ack, err := w.AckScan(t.Context(), cap, q)
		if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal(ack, err)
		}
		out, err := w.AcceptInventory(t.Context(), cap, command)
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, InventoryResult{}) {
			t.Fatal("restart enabled public fresh work", out, err)
		}
		if _, err := checkFreshCrashState(t.Context(), e.db, command, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if w.effective.authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("fresh recovery stranded custody")
	}
}

func TestCrashReplayFreshInventoryComponent(t *testing.T) {
	t.Helper()
	if raceDetector {
		// This sequential child matrix exceeds one shard's race budget. The
		// same cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	witness := crashWitness{Case: crashCase{Domain: "inventory_fresh_component", Operation: "component", Boundary: "trace"}}
	e, command, turn, authority, trace := freshCrashFixture(t, &witness)
	out, err := applyInventoryAccept(t.Context(), turn, command)
	if err != nil {
		t.Fatal(out, err)
	}
	trace.armed = false
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := checkFreshCrashProjection(t.Context(), turn.transaction.conn, command, out); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if authority.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("trace fixture did not retire")
	}
	if _, err := checkFreshCrashState(t.Context(), e.db, command, false, false); err != nil {
		t.Fatal(err)
	}
	t.Logf("fresh native trace (%d): %q", len(trace.events), trace.events)
	// Pin every executed statement/use for this N1 existing-age/one-bank
	// fixture. A removed/reordered phase cannot silently shrink the kill matrix.
	expected := []stmt.ID{scanFreshRun, scanFreshSource, scanFreshLibrary, scanFreshPolicy, scanFreshJob, scanFreshDirectory,
		scanInventoryIdentity, scanInventoryAsset, scanFreshPath, scanFreshMembership, scanFreshAge,
		scanInventorySubject, scanInventoryAlias, scanInventoryNewAsset, scanInventoryEntity, scanInventoryObject, scanInventoryLink,
		scanFreshAssetRevision, scanFreshEntityFacts, scanFreshEntityInitial, scanFreshMembershipCAS, scanFreshScalarInsert, scanFreshPhysicalInsert, scanFreshAgeCAS, scanFreshLibraryCAS,
		searchInputHead, searchInputBank, searchInputEntity, searchInputSubject, searchInputExisting, searchInputInsert, searchInputPending, searchInputSequence,
		scanFreshFinalLinks, scanFreshFinalOwners, scanFreshDirectoryCAS, scanFreshJobCAS, scanFreshProgressCAS, scanFreshRunCAS, scanReceiptInsert, scanResultInsert}
	var phases []string
	for i, id := range expected {
		phases = append(phases, fmt.Sprintf("native_%02d_%s", i, id))
	}
	if !reflect.DeepEqual(phases, trace.events) {
		t.Fatal("fresh phase inventory changed", trace.events, phases)
	}
	if err := e.close(); err != nil {
		t.Fatal(err)
	}
	prototype, _, other, _ := inventoryFacadeFixture(t)
	profile := *prototype.effective.inventoryPlan
	if err := other.close(); err != nil {
		t.Fatal(err)
	}
	boundaries := append([]string{"before_begin", "after_begin"}, trace.events...)
	boundaries = append(boundaries, "before_commit", "after_commit", "physical_return", "api_return")
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			got := runCrashChild(t, crashCase{"inventory_fresh_component", "component", boundary, ""})
			verifyFreshInventoryCrash(t, command, profile, got)
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("uncertain_return/"+fault, func(t *testing.T) {
			got := runCrashChild(t, crashCase{"inventory_fresh_component", "component", "uncertain_return", fault})
			verifyFreshInventoryCrash(t, command, profile, got)
		})
	}
}

func TestCrashFreshInventoryOracleRejectsCorruption(t *testing.T) {
	witness := crashWitness{Case: crashCase{Domain: "inventory_fresh_component", Operation: "component", Boundary: "verdict_control"}}
	e, c, turn, a, trace := freshCrashFixture(t, &witness)
	out, err := applyInventoryAccept(t.Context(), turn, c)
	if err != nil {
		t.Fatal(out, err)
	}
	trace.armed = false
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := checkFreshCrashProjection(t.Context(), turn.transaction.conn, c, out); err != nil {
		t.Fatal(err)
	}
	if physical, err := turn.transaction.commit(); err != nil || physical.state != committed {
		t.Fatal(physical, err)
	}
	if a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("control fixture did not retire")
	}
	if _, err := checkFreshCrashState(t.Context(), e.db, c, true, false); err != nil {
		t.Fatal(err)
	}
	original, err := readFreshCrashIdentity(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	policy := e.config
	policy.Synchronous = sqliteconn.Normal
	if _, err := sqliteconn.Assert(t.Context(), conn, policy); err != nil {
		t.Fatal(err)
	}
	for name, mutations := range map[string][]string{
		"duplicate_run_progress":    {`UPDATE inventory_runs SET discovered=discovered+1 WHERE job_id='fresh-run'`},
		"directory_double_advance":  {`UPDATE inventory_directories SET pages=pages+1 WHERE job_id='fresh-run'`},
		"membership_double_advance": {`UPDATE catalog_membership_clock SET sequence=sequence+1 WHERE singleton=1`},
		"unrated_double_count":      {`UPDATE catalog_age_item_counts SET total=total+1 WHERE block_unrated=0`},
		"filename_projection":       {`UPDATE catalog_entities SET title='WRONG'`},
		"search_serving_changed":    {`UPDATE search_revision SET revision=revision+1 WHERE singleton=1`},
		"fake_basic":                {`UPDATE inventory_objects SET analysis_state='ready'`},
		"original_request_lost":     {`UPDATE scan_command_receipts SET request_digest=zeroblob(32)`},
		"spurious_header_carrier":   {`UPDATE scan_command_receipts SET change_id=1`},
		"member_object_lost":        {`UPDATE scan_command_results SET object_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'`},
		"identity_remint":           {`UPDATE catalog_entities SET metadata_incarnation=randomblob(16)`, `UPDATE search_publish_jobs SET source_incarnation=(SELECT metadata_incarnation FROM catalog_entities)`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), "SAVEPOINT crash_oracle"); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range mutations {
				result, err := conn.ExecContext(t.Context(), mutation)
				if err != nil {
					t.Fatal(err)
				}
				if rows, err := result.RowsAffected(); err != nil || rows != 1 {
					t.Fatal("control did not mutate one row", rows, err)
				}
			}
			_, stateErr := checkFreshCrashState(t.Context(), conn, c, true, false)
			identity, err := readFreshCrashIdentity(t.Context(), conn)
			if err != nil {
				t.Fatal(err)
			}
			if stateErr == nil && compareFreshCrashIdentity(original, identity) == nil {
				t.Fatal("fresh corruption escaped independent oracle", name)
			}
			if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO crash_oracle"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(t.Context(), "RELEASE crash_oracle"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

// EnrichAccept process-crash cells. Test-only: no production hook, permit,
// profile or constructor is added. The accept command is the existing frozen
// fixture request over the real BEGIN/seal lineage; the child ships its exact
// exported receipt request/identity plus the pre-command raw checkpoint.
//
// The donor fixture MUST be built inside a synctest bubble: the native evidence
// and source seal receipts capture appliedMS from the executor clock (2000-01-01
// + testExecutor's hour), and those result digests are bound into the BEGIN v2
// digest and therefore the accept digest. A fixed hook clock alone runs too
// late, so two plain donors differ; the bubble makes the seal epoch identical
// across processes.
const enrichCrashDomain = "enrich_accept_crash"

// Explicit acceptance instant, inside the fixture's 2100 lease window and fixed
// for the control and every killed child. Protocol input, not latency.
var enrichCrashClock = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func init() { registerCrashDomain(enrichCrashDomain, enrichCrashChild) }

// Payload is test-only data. It carries the exact original exported receipt
// request, the fixed clock, the image ID and the raw pre-command checkpoint. It
// is never a permit, proof, reconstructed result or retry authority.
type enrichCrashPayload struct {
	Purpose CommandID
	Request EnrichmentReceiptRequest
	ClockMS int64
	ImageID int64
	Config  sqliteconn.Config
	Pre     map[string][32]byte
}

type enrichCrashDonor struct {
	e      *executor
	begin  *effectiveStageBeginCommand
	cmd    *enrichAcceptCommand
	w      *Writer
	cap    *EnrichmentDisclosure
	q      EnrichmentReceiptRequest
	image  effectiveBeginImage
	result EffectiveStageBeginResult
}

// The one deterministic donor composition. Every caller runs inside a synctest
// bubble so the seal-clock-derived digests match the killed child byte-exactly.
func enrichCrashWithDonor(t *testing.T, fn func(*testing.T, *enrichCrashDonor)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		clock := enrichCrashClock
		e, f, begin, image, result := acceptBeginFixtureWith(t, func(e *executor) {
			e.now = func() time.Time { return clock }
		})
		enrichCrashPinLibraries(t, e)
		enrichCrashPinImage(t, e, image.id)
		cmd := acceptRequestFixture(t, e, f, begin, image, result)
		w, cap, q, _ := acceptRuntime(t, e, begin, cmd)
		fn(t, &enrichCrashDonor{e: e, begin: begin, cmd: cmd, w: w, cap: cap, q: q, image: image, result: result})
	})
}

// The canonical schema gives libraries.playback_origin_incarnation a randomblob
// default. Pin it in fixture setup for the control and every killed child so the
// raw checkpoint compares byte-exactly. The accept command never reads it.
func enrichCrashPinLibraries(t *testing.T, e *executor) {
	t.Helper()
	pinned := make([]byte, 16)
	for i := range pinned {
		pinned[i] = byte(0xa0 + i)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE libraries SET playback_origin_incarnation=?`, pinned); err != nil {
		t.Fatal(err)
	}
}

// The canonical schema gives catalog_credit_images a randomblob command
// incarnation. Pin it in fixture setup for the control and every killed child so
// the raw image row compares byte-exactly across processes. Neither the BEGIN
// nor the accept request/statement reads this column.
func enrichCrashPinImage(t *testing.T, e *executor, imageID int64) {
	t.Helper()
	pinned := make([]byte, 16)
	for i := range pinned {
		pinned[i] = byte(0xe0 + i)
	}
	result, err := e.db.ExecContext(t.Context(), `UPDATE catalog_credit_images SET command_incarnation=? WHERE id=?`, pinned, imageID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("pin hidden image incarnation", n, err)
	}
}

func enrichCrashPath(t *testing.T, e *executor) string {
	t.Helper()
	var seq int
	var name, path string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func enrichCrashApply(t *testing.T, d *enrichCrashDonor) (enrichmentCommandResult, AckResult, error) {
	t.Helper()
	return d.w.effective.runEnrichment(t.Context(), d.w, acceptInvocation(d.cmd, d.cap, d.q))
}

func enrichCrashResolve(t *testing.T, w *Writer, cap *EnrichmentDisclosure, q EnrichmentReceiptRequest) (enrichmentCommandResult, AckResult, error) {
	t.Helper()
	return w.effective.runEnrichment(t.Context(), w, &enrichmentInvocation{action: enrichmentResolve,
		purpose: EnrichAccept, disclosure: cap, request: q})
}

func enrichCrashAcknowledge(t *testing.T, w *Writer, cap *EnrichmentDisclosure, q EnrichmentReceiptRequest) (enrichmentCommandResult, AckResult, error) {
	t.Helper()
	return w.effective.runEnrichment(t.Context(), w, &enrichmentInvocation{action: enrichmentAcknowledge,
		purpose: EnrichAccept, disclosure: cap, request: q})
}

// Child: build the REAL accept fixture, arm the crash driver and execute the
// real command body. Each distinct executed statement ID is one boundary; the
// fixed BEGIN/COMMIT, physical/API return and both ambiguous replies are
// explicit.
func enrichCrashChild(t *testing.T, witness *crashWitness) {
	t.Helper()
	if witness.Case.Operation != "accept" {
		t.Fatal("unsupported enrich crash operation", witness.Case)
	}
	enrichCrashWithDonor(t, func(t *testing.T, d *enrichCrashDonor) {
		e, path := d.e, enrichCrashPath(t, d.e)
		pre, err := n1DriverCheckpoint(t.Context(), e.db)
		if err != nil {
			t.Fatal(err)
		}
		witness.Path, witness.Reference = path, d.q.Receipt
		payload, err := json.Marshal(enrichCrashPayload{Purpose: EnrichAccept, Request: d.q,
			ClockMS: enrichCrashClock.UnixMilli(), ImageID: d.cmd.request.image.id, Config: e.config, Pre: pre})
		if err != nil {
			t.Fatal(err)
		}
		witness.Payload = payload
		ids := map[string]string{}
		for _, entry := range d.w.effective.registry.Manifest() {
			ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
		}
		seen := map[string]bool{}
		armed := false
		installCrashDriverAt(t, e, path, witness, enrichCrashClock, func(query string) {
			if !armed {
				return
			}
			id, ok := ids[strings.TrimSpace(query)]
			if !ok || seen[id] {
				return
			}
			seen[id] = true
			crashPoint(t, witness, "native_"+id)
		})
		armed = true
		got, ack, runErr := enrichCrashApply(t, d)
		armed = false
		if witness.Case.CommitFault != "" {
			// A lost COMMIT reply must return a zero value, quarantine the
			// runtime and never report success before the uncertain boundary.
			if !errors.Is(runErr, ErrResolveRequired) || !errors.Is(runErr, errCrashCommitReply) ||
				!reflect.DeepEqual(got, enrichmentCommandResult{}) || !e.ambiguous || !d.w.effective.fenced {
				t.Fatal("enrich uncertain COMMIT reported success or escaped quarantine", got, runErr)
			}
			witness.Unknown = true
			crashPoint(t, witness, "uncertain_return")
			t.Fatal("enrich uncertain boundary was never reached", witness.Case)
		}
		if runErr != nil {
			t.Fatal("actual enrich accept refused", runErr)
		}
		if got.Outcome.State != Applied || got.Outcome.Replayed || got.Outcome.Reference != d.q.Receipt ||
			got.Value.accepted == nil || got.Digest == (ResultDigest{}) || ack != (AckResult{}) {
			t.Fatal("actual enrich accept did not apply the original", got, ack)
		}
		crashPoint(t, witness, "api_return")
		t.Fatal("enrich boundary was never reached", witness.Case)
	})
}

// Oracle freezes the complete saved result, raw receipt/image/owner/entity rows
// and post-COMMIT full-table checkpoint from one in-process real execution,
// before any child spawns. It never supplies the child's pre-command state.
type enrichCrashOracle struct {
	Result                        enrichmentCommandResult
	Post                          map[string][32]byte
	Receipt, Image, Owner, Entity map[string]effectiveRawCell
	ImageID                       int64
	Phases                        []string
}

func enrichCrashReadRow(t *testing.T, ctx context.Context, reader effectiveJournalReader, query string, args ...any) (map[string]effectiveRawCell, bool) {
	t.Helper()
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return nil, false
	}
	values, dest := make([]any, len(columns)), make([]any, len(columns))
	for i := range dest {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	row, err := effectiveRawRow(columns, values)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("non-singleton enrich crash row", query)
	}
	return row, true
}

func enrichCrashAcceptReceipt(t *testing.T, ctx context.Context, reader effectiveJournalReader, q EnrichmentReceiptRequest) (map[string]effectiveRawCell, bool) {
	t.Helper()
	return enrichCrashReadRow(t, ctx, reader,
		`SELECT * FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='enrich_accept'`,
		q.ProviderOperationID, q.Receipt.Identity.Incarnation[:], q.Receipt.Identity.Sequence)
}

func enrichCrashCount(t *testing.T, ctx context.Context, reader inventoryCrashReader, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := reader.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func enrichCrashState(t *testing.T, ctx context.Context, reader effectiveJournalReader, q EnrichmentReceiptRequest, imageID int64) (receipt, image, owner, entity map[string]effectiveRawCell, found bool) {
	t.Helper()
	receipt, found = enrichCrashAcceptReceipt(t, ctx, reader, q)
	image, _ = enrichCrashReadRow(t, ctx, reader, `SELECT * FROM catalog_credit_images WHERE id=?`, imageID)
	owner, _ = enrichCrashReadRow(t, ctx, reader, `SELECT * FROM metadata_provider_operations WHERE id=?`, q.ProviderOperationID)
	entity, _ = enrichCrashReadRow(t, ctx, reader, `SELECT * FROM catalog_entities WHERE id=?`, q.EntityID)
	if image == nil || owner == nil || entity == nil {
		t.Fatal("missing enrich crash image/owner/entity", imageID)
	}
	return receipt, image, owner, entity, found
}

func enrichCrashOracleFor(t *testing.T) enrichCrashOracle {
	t.Helper()
	var out enrichCrashOracle
	enrichCrashWithDonor(t, func(t *testing.T, d *enrichCrashDonor) {
		e, path := d.e, enrichCrashPath(t, d.e)
		ids := map[string]string{}
		for _, entry := range d.w.effective.registry.Manifest() {
			ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
		}
		seen := map[string]bool{}
		armed := false
		witness := crashWitness{Case: crashCase{enrichCrashDomain, "accept", "trace", ""}}
		installCrashDriverAt(t, e, path, &witness, enrichCrashClock, func(query string) {
			if !armed {
				return
			}
			if id, ok := ids[strings.TrimSpace(query)]; ok {
				seen[id] = true
			}
		})
		armed = true
		got, ack, err := enrichCrashApply(t, d)
		armed = false
		if err != nil || got.Outcome.State != Applied || got.Outcome.Replayed || got.Value.accepted == nil ||
			got.Digest == (ResultDigest{}) || ack != (AckResult{}) {
			t.Fatal("trace control did not apply the original", got, ack, err)
		}
		post, err := n1DriverCheckpoint(t.Context(), e.db)
		if err != nil {
			t.Fatal(err)
		}
		receipt, found := enrichCrashAcceptReceipt(t, t.Context(), e.db, d.q)
		if !found {
			t.Fatal("trace control lost its committed accept receipt")
		}
		image, owner, entity := map[string]effectiveRawCell{}, map[string]effectiveRawCell{}, map[string]effectiveRawCell{}
		image, _ = enrichCrashReadRow(t, t.Context(), e.db, `SELECT * FROM catalog_credit_images WHERE id=?`, d.cmd.request.image.id)
		owner, _ = enrichCrashReadRow(t, t.Context(), e.db, `SELECT * FROM metadata_provider_operations WHERE id=?`, d.q.ProviderOperationID)
		entity, _ = enrichCrashReadRow(t, t.Context(), e.db, `SELECT * FROM catalog_entities WHERE id=?`, d.q.EntityID)
		if image == nil || owner == nil || entity == nil {
			t.Fatal("trace control lost image/owner/entity")
		}
		got.Outcome.Evidence = nil
		phases := make([]string, 0, len(seen))
		for id := range seen {
			phases = append(phases, "native_"+id)
		}
		out = enrichCrashOracle{Result: got, Post: post, Receipt: receipt, Image: image, Owner: owner,
			Entity: entity, ImageID: d.cmd.request.image.id, Phases: phases}
	})
	return out
}

// Reopen only the killed canonical file under the existing ownership opening.
// The payload's connection policy is asserted; the opening already uses it.
func enrichCrashReopen(t *testing.T, path string, config sqliteconn.Config) *scanOpening {
	t.Helper()
	owner := scanOpeningOwner(t, path)
	opening, err := openScanStore(t.Context(), owner, path, 1024)
	if err != nil {
		t.Fatal("open killed enrich file:", err)
	}
	t.Cleanup(func() {
		if err := opening.close(); err != nil {
			t.Error(err)
		}
	})
	e := opening.executor
	if e.config != config {
		t.Fatalf("killed file connection policy changed: have %+v, payload %+v", e.config, config)
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := schema.AssertExisting(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("integrity_check", integrity, err)
	}
	rows, err := conn.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		t.Fatal("foreign_key_check", rows.Err())
	}
	return opening
}

// acceptRuntime captures its plans with the command's NORMAL connection policy.
// The killed child left its single pooled connection at NORMAL after the last
// fixture transaction, so the existing capture asserted NORMAL there. A pool
// freshly opened on the survivor is FULL; reproduce the child's resting
// connection state on the same single connection. This grants nothing, writes
// no row and changes no executor.config assertion.
func enrichCrashRestPool(t *testing.T, e *executor) {
	t.Helper()
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), "PRAGMA synchronous=NORMAL"); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func enrichCrashCommitted(cell crashCase) bool {
	switch cell.Boundary {
	case "after_commit", "physical_return", "api_return":
		return true
	case "uncertain_return":
		return cell.CommitFault == "after"
	default:
		return false
	}
}

func enrichCrashWantReplay(want enrichmentCommandResult) enrichmentCommandResult {
	want.Outcome.Replayed = true
	return want
}

func verifyEnrichCrash(t *testing.T, oracle enrichCrashOracle, witness crashWitness, retry bool) {
	t.Helper()
	if witness.Case.Domain != enrichCrashDomain || witness.Case.Operation != "accept" ||
		witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("enrich witness lost its original operation", witness.Case)
	}
	var payload enrichCrashPayload
	if err := json.Unmarshal(witness.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Purpose != EnrichAccept || payload.ClockMS != enrichCrashClock.UnixMilli() ||
		witness.Reference != payload.Request.Receipt || payload.Request.ProviderOperationID == "" ||
		payload.Request.Receipt.Identity.Sequence <= 0 {
		t.Fatalf("witness original request/identity changed: %+v", payload)
	}
	opening := enrichCrashReopen(t, witness.Path, payload.Config)
	e := opening.executor
	e.now = func() time.Time { return time.UnixMilli(payload.ClockMS) }
	var begin *effectiveStageBeginCommand
	var cmd *enrichAcceptCommand
	enrichCrashWithDonor(t, func(t *testing.T, d *enrichCrashDonor) {
		begin, cmd = d.begin, d.cmd
	})
	enrichCrashRestPool(t, e)
	w, cap, q, _ := acceptRuntime(t, e, begin, cmd)
	if q != payload.Request || cmd.request.image.id != payload.ImageID {
		t.Fatalf("rebuilt donor request is not the child's original:\n child %+v\n parent %+v", payload.Request, q)
	}
	committed := enrichCrashCommitted(witness.Case)
	reopened, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	receipt, image, owner, entity, found := enrichCrashState(t, t.Context(), e.db, payload.Request, payload.ImageID)
	if !committed {
		if found {
			t.Fatal("pre-COMMIT enrich kill left an accept receipt")
		}
		if err := compareN1DriverCheckpoint(payload.Pre, reopened); err != nil {
			t.Fatal("pre-COMMIT enrich kill changed native state", err)
		}
		if image["sealed"].Int != 0 {
			t.Fatal("pre-COMMIT enrich kill sealed the image", image["sealed"])
		}
		if n := enrichCrashCount(t, t.Context(), e.db,
			`SELECT count(*) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_kind='enrich_accept'`,
			payload.Request.ProviderOperationID); n != 0 {
			t.Fatal("pre-COMMIT enrich kill minted a receipt", n)
		}
		if retry {
			enrichCrashRetry(t, w, cap, cmd, payload.Request, oracle)
			return
		}
		pending, ack, err := enrichCrashResolve(t, w, cap, payload.Request)
		if err != nil || pending.Outcome.State != Pending || pending.Outcome.Replayed ||
			pending.Outcome.Reference != payload.Request.Receipt || pending.Value.accepted != nil ||
			pending.Digest != (ResultDigest{}) || ack != (AckResult{}) {
			t.Fatal("missing enrich receipt did not resolve Pending", pending, ack, err)
		}
		settled, err := n1DriverCheckpoint(t.Context(), e.db)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareN1DriverCheckpoint(payload.Pre, settled); err != nil {
			t.Fatal("missing-receipt resolve wrote state", err)
		}
		return
	}
	if !found {
		t.Fatal("post-COMMIT enrich crash lost its accept receipt")
	}
	if !reflect.DeepEqual(receipt, oracle.Receipt) || !reflect.DeepEqual(image, oracle.Image) ||
		!reflect.DeepEqual(owner, oracle.Owner) || !reflect.DeepEqual(entity, oracle.Entity) {
		t.Fatal("reopened raw receipt/image/owner/entity state differs from the control")
	}
	if err := compareN1DriverCheckpoint(oracle.Post, reopened); err != nil {
		t.Fatal("post-COMMIT reopened checkpoint differs from the control", err)
	}
	if n := enrichCrashCount(t, t.Context(), e.db,
		`SELECT count(*) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='effective_stage'`,
		payload.Request.ProviderOperationID, begin.identity.Incarnation[:], begin.identity.Sequence); n != 0 {
		t.Fatal("accept did not drop the BEGIN receipt", n)
	}
	want := enrichCrashWantReplay(oracle.Result)
	first, ack, err := enrichCrashResolve(t, w, cap, payload.Request)
	if err != nil || first.Outcome.Evidence == nil || ack != (AckResult{}) {
		t.Fatal("restart resolve did not serve the original", first, ack, err)
	}
	first.Outcome.Evidence = nil
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("restart resolve changed the saved result:\n got %#v\nwant %#v", first, want)
	}
	second, _, err := enrichCrashResolve(t, w, cap, payload.Request)
	if err != nil || second.Outcome.Evidence == nil {
		t.Fatal("second restart resolve did not serve the original", second, err)
	}
	second.Outcome.Evidence = nil
	if !reflect.DeepEqual(second, want) {
		t.Fatalf("second restart resolve changed the saved result:\n got %#v\nwant %#v", second, want)
	}
	afterReplay, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(reopened, afterReplay); err != nil {
		t.Fatal("second replay wrote state", err)
	}
	enrichCrashCheckAck(t, "restart ACK", w, cap, payload.Request)
	settled, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	enrichCrashCheckAck(t, "repeated restart ACK", w, cap, payload.Request)
	afterAck, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(settled, afterAck); err != nil {
		t.Fatal("repeated ACK wrote state", err)
	}
	if n := enrichCrashCount(t, t.Context(), e.db,
		`SELECT count(*) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_kind='enrich_accept'`,
		payload.Request.ProviderOperationID); n != 1 {
		t.Fatal("restart lost or duplicated the accept receipt", n)
	}
	retired, _, err := enrichCrashResolve(t, w, cap, payload.Request)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Outcome.Replayed ||
		retired.Value.accepted != nil || retired.Digest != (ResultDigest{}) {
		t.Fatal("ACKed accept still served its saved result", retired, err)
	}
	final, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(afterAck, final); err != nil {
		t.Fatal("retired resolve wrote state", err)
	}
}

func enrichCrashCheckAck(t *testing.T, operation string, w *Writer, cap *EnrichmentDisclosure, q EnrichmentReceiptRequest) {
	t.Helper()
	got, ack, err := enrichCrashAcknowledge(t, w, cap, q)
	if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence || ack.Evidence == nil ||
		!reflect.DeepEqual(got, enrichmentCommandResult{}) {
		t.Fatal(operation, got, ack, err)
	}
}

// Fresh retry after the accept was killed before COMMIT: no receipt exists, so
// the SAME deterministic original executes exactly once on the reopened file and
// reaches the byte-identical control state; a duplicated apply or resolve only
// replays and writes nothing.
func enrichCrashRetry(t *testing.T, w *Writer, cap *EnrichmentDisclosure, cmd *enrichAcceptCommand, q EnrichmentReceiptRequest, oracle enrichCrashOracle) {
	t.Helper()
	got, ack, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q))
	if err != nil || got.Outcome.State != Applied || got.Outcome.Replayed || got.Outcome.Reference != q.Receipt ||
		got.Value.accepted == nil || got.Digest != oracle.Result.Digest || ack != (AckResult{}) {
		t.Fatal("fresh retry did not execute exactly once", got, ack, err)
	}
	if got.Outcome.Evidence == nil {
		t.Fatal("fresh retry lacked actual frame evidence")
	}
	got.Outcome.Evidence = nil
	if !reflect.DeepEqual(got, oracle.Result) {
		t.Fatalf("fresh retry changed the control result:\n got %#v\nwant %#v", got, oracle.Result)
	}
	receipt, image, owner, entity, found := enrichCrashState(t, t.Context(), w.effective.executor.db, q, oracle.ImageID)
	if !found || !reflect.DeepEqual(receipt, oracle.Receipt) || !reflect.DeepEqual(image, oracle.Image) ||
		!reflect.DeepEqual(owner, oracle.Owner) || !reflect.DeepEqual(entity, oracle.Entity) {
		t.Fatal("fresh retry raw receipt/image/owner/entity state differs from the control")
	}
	if n := enrichCrashCount(t, t.Context(), w.effective.executor.db,
		`SELECT count(*) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_kind='enrich_accept'`, q.ProviderOperationID); n != 1 {
		t.Fatal("fresh retry produced duplicate or missing receipts", n)
	}
	retried, err := n1DriverCheckpoint(t.Context(), w.effective.executor.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(oracle.Post, retried); err != nil {
		t.Fatal("fresh retry did not reach the exact control state", err)
	}
	want := enrichCrashWantReplay(oracle.Result)
	dup, dupAck, err := w.effective.runEnrichment(t.Context(), w, acceptInvocation(cmd, cap, q))
	if err != nil || dupAck != (AckResult{}) || dup.Outcome.State != Applied || !dup.Outcome.Replayed || dup.Outcome.Evidence == nil {
		t.Fatal("duplicated retry did not replay the saved result", dup, dupAck, err)
	}
	dup.Outcome.Evidence = nil
	if !reflect.DeepEqual(dup, want) {
		t.Fatalf("duplicated retry changed the saved result:\n got %#v\nwant %#v", dup, want)
	}
	resolved, resolvedAck, err := enrichCrashResolve(t, w, cap, q)
	if err != nil || resolvedAck != (AckResult{}) || resolved.Outcome.State != Applied || !resolved.Outcome.Replayed {
		t.Fatal("duplicated resolve did not replay the saved result", resolved, resolvedAck, err)
	}
	afterDup, err := n1DriverCheckpoint(t.Context(), w.effective.executor.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(retried, afterDup); err != nil {
		t.Fatal("duplicated retry wrote state", err)
	}
	enrichCrashCheckAck(t, "retry ACK", w, cap, q)
	settled, err := n1DriverCheckpoint(t.Context(), w.effective.executor.db)
	if err != nil {
		t.Fatal(err)
	}
	enrichCrashCheckAck(t, "repeated retry ACK", w, cap, q)
	afterAck, err := n1DriverCheckpoint(t.Context(), w.effective.executor.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(settled, afterAck); err != nil {
		t.Fatal("repeated retry ACK wrote state", err)
	}
	if n := enrichCrashCount(t, t.Context(), w.effective.executor.db,
		`SELECT count(*) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_kind='enrich_accept'`, q.ProviderOperationID); n != 1 {
		t.Fatal("retry lost or duplicated the accept receipt", n)
	}
}

// One boundary per traced distinct statement plus the explicit BEGIN/COMMIT,
// return and ambiguous-COMMIT boundaries. The accept before-COMMIT cell is the
// single receipt-miss/fresh-retry cell; no exhaustive statement matrix.
func TestCrashReplayEnrichAccept(t *testing.T) {
	if raceDetector {
		// The sequential child matrix exceeds one shard's race budget. The same
		// cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	oracle := enrichCrashOracleFor(t)
	phases := append([]string{"before_begin", "after_begin"}, oracle.Phases...)
	phases = append(phases, "before_commit", "after_commit", "physical_return", "api_return")
	for _, phase := range phases {
		retry := phase == "before_commit"
		name := phase
		if retry {
			name += "/fresh_retry"
		}
		t.Run(name, func(t *testing.T) {
			verifyEnrichCrash(t, oracle, runCrashChild(t, crashCase{enrichCrashDomain, "accept", phase, ""}), retry)
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run(fmt.Sprint("uncertain_return/", fault), func(t *testing.T) {
			verifyEnrichCrash(t, oracle, runCrashChild(t, crashCase{enrichCrashDomain, "accept", "uncertain_return", fault}), false)
		})
	}
}

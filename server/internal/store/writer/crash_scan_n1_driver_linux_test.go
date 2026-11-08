package writer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// These are real public N1 path process-crash controls over W/SC's merged
// composed driver fixture. They are not source/lease authority, production
// startup, power loss, fsync timing or resource qualification. Every expected
// value is derived from the ORIGINAL frozen command input and raw rows captured
// before Accept; no writer result, reducer, decoder or result codec supplies
// expected state.
type n1DriverInput struct {
	RunID, LibraryKey string
	LibraryID         int64

	SourceID, SourceIncarnation, SourceRootIdentity string
	SourceResolvedRoot                              string
	SourceGeneration                                int64
	SourceFollowSymlinks                            bool

	RunRevision, PolicyRevision                     int64
	LeaseOwner, LeaseGeneration, LeaseExpiresMS     int64
	LastSequence, AcknowledgedSequence              int64
	Phase, ScanMode                                 string
	DirectoryPath, DirectoryIdentity                string
	DirectoryRevision, ExpectedCursor, NextCursor   string
	CursorKind                                      string
	Pages, Skipped, RawNames                        int64
	DirectoryComplete, DirectoryUnstable            bool
	FileRelativePath, FileAssetPath                 string
	FileSourceKey, FileObjectIdentity, FileRevision string
	FileChangeToken                                 string
	FileSize, FileModifiedNS                        int64
	FileTitle, FileSortTitle, FileSortKey           string
	FileSortHead, FileSortKeyKeepArticles           string
	FileSortHeadKeepArticles, FileMetadataLanguage  string
	FileYear, FilePart                              int64
	IdentityIncarnation, IdentityOperationID        []byte
	IdentitySequence                                int64
	RequestDigest                                   []byte
	ClockMS                                         int64
}

type n1DriverBaseline struct {
	ClockMS                                             int64
	RunRevision, RunDiscovered, RunLastSequence, RunAck int64
	RunLeaseOwner, RunLeaseGeneration                   int64
	RunLeaseExpiresMS                                   int64
	JobProcessed                                        int64
	JobStatus                                           string
	DirectoryCursor, DirectoryState                     string
	DirectoryPages, DirectorySkipped                    int64
	LibraryData, LibraryOrder, LibraryMetadata          int64
	LibraryAgeInput, LibraryAgePublished                int64
	LibraryAgePending                                   int64
	SourceProgressPresent                               int64
	SourceProgressAt                                    string
	SourceProgressAtValid                               bool
	Membership                                          int64
	AgeUnratedTotal, AgeOtherTotal, AgeBlockedUnrated   int64
	SearchRevision, SearchInputSequence                 int64
	SearchActiveGeneration, SearchBuildingGeneration    int64
	SearchBuildingGenerationValid                       bool
	BankPending                                         int64
	BankState, BankKey                                  string
	Counts                                              map[string]int64
}

// n1DriverCrashSource is the parent/child witness of the ORIGINAL command and
// the exact initial raw checkpoint. It never carries a grant, permit, source
// retry or reconstructed production result.
type n1DriverCrashSource struct {
	Input    n1DriverInput
	Baseline n1DriverBaseline
	Initial  map[string][32]byte
	Journal  *inventoryCrashJournal
	Trace    []string
}

var n1DriverCountTables = []string{
	"catalog_subjects", "catalog_identities", "catalog_assets", "catalog_entities",
	"catalog_asset_links", "inventory_objects", "catalog_scalar_contributions",
	"catalog_asset_contributions", "scan_command_receipts", "scan_command_results",
	"search_publish_jobs", "search_publications", "search_fragments",
	"catalog_age_item_counts",
}

func n1DriverInputFrom(command *inventoryAcceptCommand) n1DriverInput {
	r, f := command.request, command.request.files[0]
	return n1DriverInput{
		RunID: r.scope.runID, LibraryKey: r.scope.libraryKey, LibraryID: r.scope.libraryID,
		SourceID: r.source.id, SourceIncarnation: r.source.incarnation, SourceRootIdentity: r.source.rootIdentity,
		SourceResolvedRoot: string(r.source.resolvedRoot), SourceGeneration: r.source.generation, SourceFollowSymlinks: r.source.followSymlinks,
		RunRevision: r.run.revision, PolicyRevision: r.run.policyRevision,
		LeaseOwner: r.run.leaseOwner, LeaseGeneration: r.run.leaseGeneration, LeaseExpiresMS: r.run.leaseExpiresMS,
		LastSequence: r.run.lastSequence, AcknowledgedSequence: r.run.acknowledgedSequence,
		Phase: r.run.phase, ScanMode: r.run.scanMode,
		DirectoryPath: r.directory.path, DirectoryIdentity: r.directory.identity, DirectoryRevision: r.directory.revision,
		ExpectedCursor: r.directory.expectedCursor, NextCursor: r.directory.nextCursor, CursorKind: r.directory.cursorKind,
		Pages: r.directory.pages, Skipped: r.directory.skipped, RawNames: r.directory.rawNames,
		DirectoryComplete: r.directory.complete, DirectoryUnstable: r.directory.unstable,
		FileRelativePath: f.relativePath, FileAssetPath: f.assetPath,
		FileSourceKey: f.sourceKey, FileObjectIdentity: f.objectIdentity, FileRevision: f.revision, FileChangeToken: f.changeToken,
		FileSize: f.size, FileModifiedNS: f.modifiedNS,
		FileTitle: f.title, FileSortTitle: f.sortTitle, FileSortKey: f.sortKey, FileSortHead: f.sortHead,
		FileSortKeyKeepArticles: f.sortKeyKeepArticles, FileSortHeadKeepArticles: f.sortHeadKeepArticles,
		FileYear: f.year, FilePart: f.part, FileMetadataLanguage: f.metadataLanguage,
		IdentityIncarnation: append([]byte(nil), command.identity.Incarnation[:]...),
		IdentitySequence:    command.identity.Sequence,
		IdentityOperationID: append([]byte(nil), command.identity.OperationID[:]...),
		RequestDigest:       append([]byte(nil), command.digest[:]...),
	}
}

func n1DriverCaptureBaseline(t *testing.T, ctx context.Context, reader inventoryCrashReader, in n1DriverInput) n1DriverBaseline {
	t.Helper()
	b := n1DriverBaseline{ClockMS: in.ClockMS, Counts: map[string]int64{}}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(reader.QueryRowContext(ctx, `SELECT job_revision,discovered,last_command_sequence,acknowledged_command_sequence,
 job_lease_owner,job_lease_generation,job_lease_expires_ms FROM inventory_runs WHERE job_id=?`, in.RunID).Scan(
		&b.RunRevision, &b.RunDiscovered, &b.RunLastSequence, &b.RunAck,
		&b.RunLeaseOwner, &b.RunLeaseGeneration, &b.RunLeaseExpiresMS))
	must(reader.QueryRowContext(ctx, `SELECT processed,status FROM jobs WHERE id=?`, in.RunID).Scan(&b.JobProcessed, &b.JobStatus))
	must(reader.QueryRowContext(ctx, `SELECT cursor,pages,skipped,state FROM inventory_directories WHERE job_id=? AND relative_path=?`,
		in.RunID, in.DirectoryPath).Scan(&b.DirectoryCursor, &b.DirectoryPages, &b.DirectorySkipped, &b.DirectoryState))
	must(reader.QueryRowContext(ctx, `SELECT data_revision,data_order_revision,data_metadata_revision,age_input_revision,
 age_published_revision,age_pending_operations FROM libraries WHERE id=?`, in.LibraryKey).Scan(
		&b.LibraryData, &b.LibraryOrder, &b.LibraryMetadata, &b.LibraryAgeInput, &b.LibraryAgePublished, &b.LibraryAgePending))
	var progressAt sql.NullString
	must(reader.QueryRowContext(ctx, `SELECT progress_present,progress_last_progress_at FROM library_sources WHERE id=?`, in.SourceID).Scan(
		&b.SourceProgressPresent, &progressAt))
	b.SourceProgressAt, b.SourceProgressAtValid = progressAt.String, progressAt.Valid
	must(reader.QueryRowContext(ctx, `SELECT sequence FROM catalog_membership_clock WHERE singleton=1`).Scan(&b.Membership))
	must(reader.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT total FROM catalog_age_item_counts WHERE library_id=? AND kind=1 AND block_unrated=0 AND admission_age=-1),-1),
 COALESCE((SELECT total FROM catalog_age_item_counts WHERE library_id=? AND kind=1 AND block_unrated=1 AND admission_age=7),-1),
 (SELECT count(*) FROM catalog_age_item_counts WHERE library_id=? AND kind=1 AND block_unrated=1 AND admission_age=-1)`,
		in.LibraryID, in.LibraryID, in.LibraryID).Scan(&b.AgeUnratedTotal, &b.AgeOtherTotal, &b.AgeBlockedUnrated))
	var active, building sql.NullInt64
	must(reader.QueryRowContext(ctx, `SELECT revision,input_sequence,active_generation_id,building_generation_id FROM search_revision WHERE singleton=1`).Scan(
		&b.SearchRevision, &b.SearchInputSequence, &active, &building))
	b.SearchActiveGeneration = active.Int64
	b.SearchBuildingGeneration, b.SearchBuildingGenerationValid = building.Int64, building.Valid
	must(reader.QueryRowContext(ctx, `SELECT pending_subjects,state,bank_key FROM search_generations WHERE id=?`, active.Int64).Scan(
		&b.BankPending, &b.BankState, &b.BankKey))
	for _, table := range n1DriverCountTables {
		var n int64
		must(reader.QueryRowContext(ctx, "SELECT count(*) FROM "+n1DriverQuote(table)).Scan(&n))
		b.Counts[table] = n
	}
	return b
}

func n1DriverQuote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// The checkpoint hashes every user table, including generated identities and
// owner/search tables, directly from raw SQL. It is the before-COMMIT retention
// oracle, not a writer result or a production codec.
func n1DriverCheckpoint(ctx context.Context, reader inventoryCrashReader) (map[string][32]byte, error) {
	names, err := reader.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			names.Close()
			return nil, err
		}
		tables = append(tables, name)
	}
	err = errors.Join(err, names.Err(), names.Close())
	if err != nil {
		return nil, err
	}
	out := map[string][32]byte{}
	for _, name := range tables {
		rows, err := reader.QueryContext(ctx, "SELECT * FROM "+n1DriverQuote(name))
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		var encoded []string
		for rows.Next() {
			values, dest := make([]any, len(columns)), make([]any, len(columns))
			for i := range dest {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, err
			}
			cells := make([]string, len(values))
			for i, value := range values {
				cells[i], err = inventoryHistoryCell(value)
				if err != nil {
					rows.Close()
					return nil, err
				}
			}
			row, err := json.Marshal(cells)
			if err != nil {
				rows.Close()
				return nil, err
			}
			encoded = append(encoded, string(row))
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return nil, err
		}
		sort.Strings(encoded)
		data, err := json.Marshal(struct{ Columns, Rows []string }{columns, encoded})
		if err != nil {
			return nil, err
		}
		out[name] = sha256.Sum256(data)
	}
	return out, nil
}

func compareN1DriverCheckpoint(want, got map[string][32]byte) error {
	if len(want) == 0 || len(got) != len(want) {
		return fmt.Errorf("N1 checkpoint table inventory changed: got %d, want %d", len(got), len(want))
	}
	for name, before := range want {
		after, ok := got[name]
		if !ok {
			return fmt.Errorf("N1 checkpoint lost table %s", name)
		}
		if before != after {
			return fmt.Errorf("N1 checkpoint changed table %s", name)
		}
	}
	return nil
}

func n1DriverIdentity(in n1DriverInput) (CommandIdentity, RequestDigest, error) {
	if len(in.IdentityIncarnation) != 16 || len(in.IdentityOperationID) != 16 || len(in.RequestDigest) != 32 || in.IdentitySequence <= 0 {
		return CommandIdentity{}, RequestDigest{}, errors.New("unsupported original N1 identity")
	}
	var identity CommandIdentity
	var request RequestDigest
	copy(identity.Incarnation[:], in.IdentityIncarnation)
	copy(identity.OperationID[:], in.IdentityOperationID)
	identity.Sequence = in.IdentitySequence
	copy(request[:], in.RequestDigest)
	return identity, request, nil
}

func n1DriverObjectFacts(in n1DriverInput) (stable, evidence string, revision string, err error) {
	// This fixture deliberately uses a non-Unix root identity. Its observed
	// Unix child keeps the device and inode verbatim; reject unsupported inputs.
	if in.SourceRootIdentity != "source-root" || in.FileObjectIdentity != "dev:7:ino:31" {
		return "", "", "", errors.New("unsupported N1 fixture object identity")
	}
	stable = in.FileObjectIdentity
	// Independent documented inventory-v1 JSON wire, without the production
	// evidence struct, revision helper or object-identity projection.
	e := struct {
		Kind        string `json:"kind"`
		Scope       string `json:"scope"`
		Object      string `json:"object"`
		Size        int64  `json:"size"`
		ModifiedNS  int64  `json:"modifiedNs"`
		ChangeToken string `json:"changeToken"`
	}{"local", in.SourceID + ":" + in.SourceIncarnation, stable, in.FileSize, in.FileModifiedNS, in.FileChangeToken}
	raw, err := json.Marshal(e)
	if err != nil {
		return "", "", "", err
	}
	digest := sha256.Sum256(append([]byte("portico.inventory.v1\x00"), raw...))
	return stable, string(raw), hex.EncodeToString(digest[:]), nil
}

// Independent fixed-octet, big-endian v1 result wire. No writer encoder,
// decoder, reducer or canonical codec constant supplies expected bytes.
func n1DriverResultDigest(identity CommandIdentity, request RequestDigest, result inventoryAcceptResult) (ResultDigest, error) {
	h := sha256.New()
	put := func(p []byte) { _, _ = h.Write(p) }
	number := func(n int64) { var p [8]byte; binary.BigEndian.PutUint64(p[:], uint64(n)); put(p[:]) }
	text := func(v string) { number(int64(len(v))); put([]byte(v)) }
	put([]byte("portico-writer-result-sha256-v1\x00"))
	put([]byte{0, 1})
	text("inventory_accept")
	text("ordinary_batch")
	text("applied")
	text(result.RunID)
	put(identity.Incarnation[:])
	number(identity.Sequence)
	put(identity.OperationID[:])
	put(request[:])
	number(result.AcceptedRunRevision)
	number(result.AppliedMS)
	text(result.SourceID)
	text(result.SourceIncarnation)
	text(result.SourceRevision)
	text(result.NextCursor)
	number(result.ResultCount)
	number(int64(len(result.Members)))
	for _, m := range result.Members {
		number(m.InputOrdinal)
		number(m.ChildKind)
		number(m.ChildOrdinal)
		text(m.ObjectID)
		number(m.LibraryID)
		number(m.SubjectID)
		number(m.AssetID)
		number(m.AcceptedAssetRevision)
	}
	var digest ResultDigest
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func n1DriverAddedAt(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func n1DriverExpectedResult(source n1DriverCrashSource, journal inventoryCrashJournal) (InventoryResult, error) {
	h := journal.Header
	var reference ReceiptReference
	copy(reference.Identity.Incarnation[:], h.Incarnation)
	reference.Identity.Sequence = h.Sequence
	copy(reference.Identity.OperationID[:], h.OperationID)
	copy(reference.RequestDigest[:], h.RequestDigest)
	if err := checkN1DriverReference(source.Input, reference); err != nil {
		return InventoryResult{}, err
	}
	out := InventoryResult{Outcome: ReceiptOutcome{State: Applied, Reference: reference},
		RunID: h.RunID, AcceptedRunRevision: h.AcceptedRevision, AppliedMS: h.AppliedMS,
		SourceID: h.SourceID, SourceIncarnation: h.SourceIncarnation, SourceRevision: h.SourceRevision,
		NextCursor: h.NextCursor, ResultCount: h.ResultCount}
	copy(out.Digest[:], h.ResultDigest)
	for _, c := range journal.Children {
		out.Members = append(out.Members, InventoryMember{InputOrdinal: c.InputOrdinal, ChildKind: c.ChildKind, ChildOrdinal: c.ChildOrdinal,
			ObjectID: c.ObjectID, LibraryID: c.LibraryID, SubjectID: c.SubjectID, AssetID: c.AssetID, AcceptedAssetRevision: c.AcceptedAssetRevision})
	}
	return out, nil
}

func compareN1DriverResult(want, got InventoryResult) error {
	e := got.Outcome.Evidence
	if e == nil || e.Purpose != InventoryAccept || e.Class != governor.ScanMinimum || e.Durability != Normal || e.ReturnedAt.IsZero() || e.Commit != CommitSucceeded || e.Policy == nil || e.CommitAttemptedAt == nil || e.CommitReturnedAt == nil || e.PhysicalReturnedAt == nil {
		return errors.New("N1 public return lacks actual frame evidence")
	}
	got.Outcome.Evidence = nil // actual per-call frame evidence is output, not immutable result data
	if !reflect.DeepEqual(want, got) {
		return fmt.Errorf("N1 public return changed: got %+v, want %+v", got, want)
	}
	return nil
}

// readN1DriverJournal reads only the ORIGINAL accepting receipt. The run also
// has earlier create/claim receipts on this fixture; selecting by run alone
// would silently return another lifecycle command.
func readN1DriverJournal(ctx context.Context, reader inventoryCrashReader, in n1DriverInput) (out inventoryCrashJournal, err error) {
	h := &out.Header
	err = reader.QueryRowContext(ctx, `SELECT run_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
 accepted_revision,applied_ms,source_id,source_incarnation,source_revision,change_id,next_cursor,
 next_family,next_copy_phase,next_copy_id,next_copy_aux,next_copy_key,next_copy_key2,next_copy_key3,
 after_entity,expected_epoch,result_count,result_digest,refused_relative_path,refusal_code,cursor_kind,
 cursor_directory_path,cursor_directory_identity,cursor_directory_revision,cursor_expected_payload,cursor_next_payload
 FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?`,
		in.RunID, in.IdentityIncarnation, in.IdentitySequence).Scan(
		&h.RunID, &h.Incarnation, &h.Sequence, &h.OperationID, &h.RequestDigest, &h.Kind,
		&h.AcceptedRevision, &h.AppliedMS, &h.SourceID, &h.SourceIncarnation, &h.SourceRevision, &h.ChangeID, &h.NextCursor,
		&h.NextFamily, &h.NextCopyPhase, &h.NextCopyID, &h.NextCopyAux, &h.NextCopyKey, &h.NextCopyKey2, &h.NextCopyKey3,
		&h.AfterEntity, &h.ExpectedEpoch, &h.ResultCount, &h.ResultDigest, &h.RefusedRelativePath, &h.RefusalCode, &h.CursorKind,
		&h.CursorDirectoryPath, &h.CursorDirectoryIdentity, &h.CursorDirectoryRevision, &h.CursorExpectedPayload, &h.CursorNextPayload)
	if err != nil {
		return out, err
	}
	rows, err := reader.QueryContext(ctx, `SELECT run_id,command_incarnation,command_sequence,input_ordinal,child_kind,child_ordinal,
 object_id,library_id,subject_id,asset_id,accepted_asset_revision FROM scan_command_results
 WHERE run_id=? AND command_incarnation=? AND command_sequence=?
 ORDER BY input_ordinal,child_kind,child_ordinal`, in.RunID, in.IdentityIncarnation, in.IdentitySequence)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var child inventoryCrashChild
		if err = rows.Scan(&child.RunID, &child.Incarnation, &child.Sequence, &child.InputOrdinal, &child.ChildKind, &child.ChildOrdinal,
			&child.ObjectID, &child.LibraryID, &child.SubjectID, &child.AssetID, &child.AcceptedAssetRevision); err != nil {
			return out, err
		}
		out.Children = append(out.Children, child)
	}
	return out, rows.Err()
}

func checkN1DriverReference(in n1DriverInput, got ReceiptReference) error {
	identity, request, err := n1DriverIdentity(in)
	if err != nil {
		return err
	}
	if got.Identity != identity || got.RequestDigest != request {
		return errors.New("N1 original reference changed")
	}
	return nil
}

func n1DriverEqual(name string, got, want any) error {
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("N1 %s: got %#v, want %#v", name, got, want)
	}
	return nil
}

func checkN1DriverCommitted(ctx context.Context, reader inventoryCrashReader, source n1DriverCrashSource) (inventoryCrashJournal, error) {
	in, b := source.Input, source.Baseline
	var zero inventoryCrashJournal
	if in.SourceFollowSymlinks || b.RunRevision != in.RunRevision || b.ClockMS != in.ClockMS || in.LastSequence+1 != in.IdentitySequence || in.AcknowledgedSequence != in.LastSequence {
		return zero, fmt.Errorf("N1 baseline/original command disagreement: %+v %+v", in, b)
	}
	identity, request, err := n1DriverIdentity(in)
	if err != nil {
		return zero, err
	}
	journal, err := readN1DriverJournal(ctx, reader, in)
	if err != nil {
		return zero, err
	}
	if len(journal.Children) != 1 {
		return zero, fmt.Errorf("N1 receipt child count %d, want 1", len(journal.Children))
	}
	child := journal.Children[0]
	if child.RunID != in.RunID || !bytes.Equal(child.Incarnation, in.IdentityIncarnation) || child.Sequence != in.IdentitySequence ||
		child.InputOrdinal != 0 || child.ChildKind != 0 || child.ChildOrdinal != 0 || child.LibraryID != in.LibraryID ||
		child.SubjectID <= 0 || child.AssetID <= 0 || len(child.ObjectID) != 32 || child.AcceptedAssetRevision != 1 {
		return zero, fmt.Errorf("N1 receipt member identity: %+v", child)
	}
	result := inventoryAcceptResult{RunID: in.RunID, AcceptedRunRevision: b.RunRevision + 1, AppliedMS: b.ClockMS,
		SourceID: in.SourceID, SourceIncarnation: in.SourceIncarnation, SourceRevision: in.FileRevision, NextCursor: in.NextCursor,
		ResultCount: 1, Members: []inventoryResultMember{{InputOrdinal: 0, ChildKind: 0, ChildOrdinal: 0, ObjectID: child.ObjectID,
			LibraryID: in.LibraryID, SubjectID: child.SubjectID, AssetID: child.AssetID, AcceptedAssetRevision: child.AcceptedAssetRevision}}}
	digest, err := n1DriverResultDigest(identity, request, result)
	if err != nil {
		return zero, err
	}
	wantHeader := inventoryCrashHeader{RunID: in.RunID, Incarnation: append([]byte(nil), in.IdentityIncarnation...),
		Sequence: in.IdentitySequence, OperationID: append([]byte(nil), in.IdentityOperationID...), RequestDigest: append([]byte(nil), in.RequestDigest...),
		Kind: "inventory_accept", AcceptedRevision: b.RunRevision + 1, AppliedMS: b.ClockMS,
		SourceID: in.SourceID, SourceIncarnation: in.SourceIncarnation, SourceRevision: in.FileRevision,
		NextCursor: in.NextCursor, ResultCount: 1, ResultDigest: append([]byte(nil), digest[:]...)}
	if err := n1DriverCheckWholeHeader(ctx, reader, in, wantHeader); err != nil {
		return zero, err
	}
	if !reflect.DeepEqual(wantHeader, journal.Header) {
		return zero, fmt.Errorf("N1 receipt header changed: got %+v, want %+v", journal.Header, wantHeader)
	}
	if err := checkN1DriverEffects(ctx, reader, in, b, child, journal); err != nil {
		return zero, err
	}
	// Everything outside the explicitly modeled N1 write set is immutable.
	// This checks real native state, never a candidate-supplied mutation list.
	current, err := n1DriverCheckpoint(ctx, reader)
	if err != nil {
		return zero, err
	}
	unchanged := make(map[string][32]byte, len(source.Initial))
	for name, digest := range source.Initial {
		unchanged[name] = digest
	}
	for _, name := range strings.Fields(`catalog_subjects catalog_identities catalog_assets catalog_entities catalog_asset_links inventory_objects catalog_scalar_contributions catalog_asset_contributions scan_command_receipts scan_command_results search_publish_jobs catalog_age_item_counts inventory_directories jobs inventory_runs libraries library_sources catalog_membership_clock search_revision search_generations`) {
		delete(unchanged, name)
		delete(current, name)
	}
	if err := compareN1DriverCheckpoint(unchanged, current); err != nil {
		return zero, err
	}
	return journal, nil
}

// Check every canonical header column, including NULL lifecycle/directory
// projections omitted by the historical shared header struct. Schema growth
// fails closed until its new column is explicitly modeled here.
func n1DriverCheckWholeHeader(ctx context.Context, reader inventoryCrashReader, in n1DriverInput, h inventoryCrashHeader) error {
	want := map[string]any{
		"run_id": h.RunID, "command_incarnation": h.Incarnation, "command_sequence": h.Sequence,
		"operation_id": h.OperationID, "request_digest": h.RequestDigest, "command_kind": h.Kind,
		"accepted_revision": h.AcceptedRevision, "applied_ms": h.AppliedMS,
		"source_id": h.SourceID, "source_incarnation": h.SourceIncarnation, "source_revision": h.SourceRevision,
		"change_id": nil, "next_cursor": h.NextCursor, "next_family": int64(0), "next_copy_phase": int64(0),
		"next_copy_id": int64(0), "next_copy_aux": int64(0), "next_copy_key": "", "next_copy_key2": "", "next_copy_key3": "",
		"after_entity": int64(0), "expected_epoch": int64(0), "result_count": int64(1), "result_digest": h.ResultDigest,
	}
	for _, name := range strings.Fields(`refused_relative_path refusal_code cursor_kind cursor_directory_path cursor_directory_identity cursor_directory_revision cursor_expected_payload cursor_next_payload
 result_lease_owner result_lease_generation result_lease_expires_ms result_acknowledged_sequence result_discovered result_processed result_root_identity result_root_revision result_root_cursor result_root_pages result_root_skipped
 result_directory_path result_directory_identity result_directory_revision result_directory_cursor result_directory_state result_directory_pages result_directory_skipped result_parent_pages result_parent_skipped result_inventory_completed_at result_traversal_blocked result_traversal_protected refused_entry_kind refused_directory_identity refused_directory_revision`) {
		want[name] = nil
	}
	rows, err := reader.QueryContext(ctx, "SELECT * FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?", in.RunID, in.IdentityIncarnation, in.IdentitySequence)
	if err != nil {
		return err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	if len(columns) != len(want) {
		return fmt.Errorf("N1 header column inventory: got %d want %d", len(columns), len(want))
	}
	if !rows.Next() {
		return errors.Join(sql.ErrNoRows, rows.Err())
	}
	values, dest := make([]any, len(columns)), make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if err = rows.Scan(dest...); err != nil {
		return err
	}
	for i, name := range columns {
		v, ok := want[name]
		if !ok {
			return fmt.Errorf("N1 unmodeled header column %s", name)
		}
		if err := n1DriverEqual("header "+name, values[i], v); err != nil {
			return err
		}
	}
	if rows.Next() {
		return errors.New("N1 duplicate original header")
	}
	return rows.Err()
}

func checkN1DriverEffects(ctx context.Context, reader inventoryCrashReader, in n1DriverInput, b n1DriverBaseline, child inventoryCrashChild, journal inventoryCrashJournal) error {
	stable, evidence, revision, err := n1DriverObjectFacts(in)
	if err != nil {
		return err
	}
	if revision != in.FileRevision {
		return fmt.Errorf("N1 expected object revision %q differs from original %q", revision, in.FileRevision)
	}
	addedAt := n1DriverAddedAt(b.ClockMS)
	ageUnrated := int64(1)
	if b.AgeUnratedTotal >= 0 {
		ageUnrated = b.AgeUnratedTotal + 1
	}
	agePublished := b.LibraryAgePublished
	if b.LibraryAgeInput == b.LibraryAgePublished && b.LibraryAgePending == 0 {
		agePublished = b.LibraryAgeInput + 1
	}
	ageRows := b.Counts["catalog_age_item_counts"]
	if b.AgeUnratedTotal < 0 {
		ageRows++
	}
	for _, point := range []struct {
		name, query string
		want        []any
		args        []any
	}{
		{"subject", `SELECT s.id,s.library_id,s.kind,length(s.public_id),i.source_key,e.public_id=s.public_id
 FROM catalog_subjects s JOIN catalog_identities i ON i.subject_id=s.id JOIN catalog_entities e ON e.id=s.id`,
			[]any{child.SubjectID, in.LibraryID, int64(1), int64(16), in.FileSourceKey, int64(1)}, nil},
		{"filename", `SELECT id,library_id,kind,title,sort_title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,
 year,added_at,metadata_language,retired,revision,length(metadata_incarnation),length(artwork_incarnation),
 length(playback_origin_incarnation),playback_origin_revision FROM catalog_entities`,
			[]any{child.SubjectID, in.LibraryID, int64(1), in.FileTitle, in.FileSortTitle, in.FileSortKey, in.FileSortHead,
				in.FileSortKeyKeepArticles, in.FileSortHeadKeepArticles, in.FileYear, addedAt, in.FileMetadataLanguage, int64(0), int64(1),
				int64(16), int64(16), int64(16), int64(1)}, nil},
		{"asset", `SELECT id,path,size,modified_ns,available,info_epoch,file_size_revision,token,library_id FROM catalog_assets`,
			[]any{child.AssetID, in.FileAssetPath, in.FileSize, in.FileModifiedNS, int64(1), child.AcceptedAssetRevision, nil, nil, in.LibraryID}, nil},
		{"object", `SELECT o.id,o.asset_id,a.token,o.source_id,o.root_incarnation,o.relative_path,o.object_identity,o.revision,
 o.evidence_json,o.size,o.modified_ns,o.seen_job,o.directory_path,o.state,o.retired,o.analysis_state,o.analysis_revision,
 o.has_analysis_probe_bindings FROM inventory_objects o JOIN catalog_assets a ON a.id=? WHERE o.id=?`,
			[]any{child.ObjectID, nil, nil, in.SourceID, in.SourceIncarnation, in.FileRelativePath, stable, in.FileRevision,
				evidence, in.FileSize, in.FileModifiedNS, in.RunID, in.DirectoryPath, "available", int64(0), "pending", "", int64(0)},
			[]any{child.AssetID, child.ObjectID}},
		{"link", `SELECT library_id,entity_id,asset_id,part_index,available,cue_ordinal,start_seconds,end_seconds FROM catalog_asset_links`,
			[]any{in.LibraryID, child.SubjectID, child.AssetID, in.FilePart, int64(1), nil, float64(0), nil}, nil},
		{"scalar", `SELECT c.parent_id,c.contributor_id,c.source,c.membership_sequence,c.contributor_revision=e.scalar_source_revision,
 c.duration,c.community_rating,c.critic_rating,c.resolution,c.latest_aired_key,c.admission_age
 FROM catalog_scalar_contributions c JOIN catalog_entities e ON e.id=c.parent_id`,
			[]any{child.SubjectID, child.SubjectID, int64(0), b.Membership + 1, int64(1), nil, nil, nil, nil, nil, nil}, nil},
		{"physical", `SELECT c.parent_id,c.asset_id,c.membership_refs,c.authored_refs,c.file_size,c.file_size_revision=a.file_size_revision
 FROM catalog_asset_contributions c JOIN catalog_assets a ON a.id=c.asset_id`,
			[]any{child.SubjectID, child.AssetID, int64(1), int64(0), in.FileSize, int64(1)}, nil},
		{"publisher", `SELECT j.entity_id,j.search_generation_id,j.input_generation,j.published_generation,
 j.source_incarnation=e.metadata_incarnation,j.source_revision,j.phase,j.job_next_attempt_ms,j.recipe_version=g.recipe_version
 FROM search_publish_jobs j JOIN catalog_entities e ON e.id=j.entity_id JOIN search_generations g ON g.id=j.search_generation_id`,
			[]any{child.SubjectID, b.SearchActiveGeneration, int64(1), int64(0), int64(1), int64(1), "capture", b.ClockMS, int64(1)}, nil},
		{"run", `SELECT job_revision,discovered,last_command_sequence,acknowledged_command_sequence,source_id,source_generation,
 root_incarnation,root_identity,policy_revision,phase,scan_mode,job_lease_owner,job_lease_generation,job_lease_expires_ms
 FROM inventory_runs WHERE job_id=?`,
			[]any{b.RunRevision + 1, b.RunDiscovered + 1, in.IdentitySequence, b.RunAck, in.SourceID, in.SourceGeneration,
				in.SourceIncarnation, in.SourceRootIdentity, in.PolicyRevision, in.Phase, in.ScanMode, b.RunLeaseOwner,
				b.RunLeaseGeneration, b.RunLeaseExpiresMS}, []any{in.RunID}},
		{"job", `SELECT processed,status FROM jobs WHERE id=?`, []any{b.JobProcessed + 1, b.JobStatus}, []any{in.RunID}},
		{"directory", `SELECT cursor,pages,skipped,state FROM inventory_directories WHERE job_id=? AND relative_path=?`,
			[]any{in.NextCursor, b.DirectoryPages + 1, b.DirectorySkipped, b.DirectoryState}, []any{in.RunID, in.DirectoryPath}},
		{"library", `SELECT data_revision,data_order_revision,data_metadata_revision,age_input_revision,age_published_revision,
 age_pending_operations FROM libraries WHERE id=?`,
			[]any{b.LibraryData + 1, b.LibraryOrder + 1, b.LibraryMetadata + 1, b.LibraryAgeInput + 1, agePublished, b.LibraryAgePending},
			[]any{in.LibraryKey}},
		{"source original", `SELECT library_id,generation,incarnation,root_identity,root,follow_symlinks,kind,classification,enabled,identity_confirmed,health,remote_source_id FROM library_sources WHERE id=?`,
			[]any{in.LibraryKey, in.SourceGeneration, in.SourceIncarnation, in.SourceRootIdentity, in.SourceResolvedRoot, int64(0), "local", "local", int64(1), int64(1), "ready", nil}, []any{in.SourceID}},
		{"progress", `SELECT progress_present,progress_last_progress_at FROM library_sources WHERE id=?`,
			[]any{int64(1), addedAt}, []any{in.SourceID}},
		{"membership", `SELECT sequence FROM catalog_membership_clock WHERE singleton=1`, []any{b.Membership + 1}, nil},
		{"age", `SELECT
 COALESCE((SELECT total FROM catalog_age_item_counts WHERE library_id=? AND kind=1 AND block_unrated=0 AND admission_age=-1),-1),
 COALESCE((SELECT total FROM catalog_age_item_counts WHERE library_id=? AND kind=1 AND block_unrated=1 AND admission_age=7),-1),
 (SELECT count(*) FROM catalog_age_item_counts WHERE library_id=? AND kind=1 AND block_unrated=1 AND admission_age=-1)`,
			[]any{ageUnrated, b.AgeOtherTotal, b.AgeBlockedUnrated}, []any{in.LibraryID, in.LibraryID, in.LibraryID}},
		{"search clocks", `SELECT revision,input_sequence,active_generation_id,building_generation_id FROM search_revision WHERE singleton=1`,
			[]any{b.SearchRevision, b.SearchInputSequence + 1, b.SearchActiveGeneration, n1DriverNullable(b.SearchBuildingGenerationValid, b.SearchBuildingGeneration)}, nil},
		{"bank", `SELECT pending_subjects,state,bank_key FROM search_generations WHERE id=?`,
			[]any{b.BankPending + 1, b.BankState, b.BankKey}, []any{b.SearchActiveGeneration}},
	} {
		got, err := n1DriverRow(ctx, reader, point.query, point.args...)
		if err != nil {
			return fmt.Errorf("N1 %s: %w", point.name, err)
		}
		if len(got) != len(point.want) {
			return fmt.Errorf("N1 %s: got %d columns, want %d", point.name, len(got), len(point.want))
		}
		for i, want := range point.want {
			if want == nil && (point.name == "object" && (i == 1 || i == 2) || point.name == "asset" && (i == 6 || i == 7)) {
				continue // generated token/value relationships are checked explicitly below
			}
			if point.name == "search clocks" && i == 3 {
				if err := n1DriverNullableEqual("building generation", got[i], b.SearchBuildingGenerationValid, b.SearchBuildingGeneration); err != nil {
					return err
				}
				continue
			}
			if err := n1DriverEqual(point.name, got[i], want); err != nil {
				return err
			}
		}
		switch point.name {
		case "asset":
			var sizeRevision int64
			if err := reader.QueryRowContext(ctx, `SELECT file_size_revision FROM catalog_assets WHERE id=?`, child.AssetID).Scan(&sizeRevision); err != nil || sizeRevision <= 0 {
				return fmt.Errorf("N1 asset file size revision: %v %w", sizeRevision, err)
			}
			var token string
			if err := reader.QueryRowContext(ctx, `SELECT token FROM catalog_assets WHERE id=?`, child.AssetID).Scan(&token); err != nil || len(token) != 32 {
				return fmt.Errorf("N1 asset token: %q %w", token, err)
			}
		case "object":
			var objectAsset, assetToken string
			if err := reader.QueryRowContext(ctx, `SELECT o.asset_id,a.token FROM inventory_objects o JOIN catalog_assets a ON a.id=? WHERE o.id=?`,
				child.AssetID, child.ObjectID).Scan(&objectAsset, &assetToken); err != nil || objectAsset != assetToken {
				return fmt.Errorf("N1 object asset token: %q %q %w", objectAsset, assetToken, err)
			}
		}
	}
	expectedCounts := map[string]int64{}
	for _, table := range n1DriverCountTables {
		expectedCounts[table] = b.Counts[table]
	}
	for _, table := range []string{"catalog_subjects", "catalog_identities", "catalog_assets", "catalog_entities",
		"catalog_asset_links", "inventory_objects", "catalog_scalar_contributions", "catalog_asset_contributions",
		"scan_command_receipts", "scan_command_results", "search_publish_jobs"} {
		expectedCounts[table]++
	}
	expectedCounts["catalog_age_item_counts"] = ageRows
	for _, table := range n1DriverCountTables {
		var n int64
		if err := reader.QueryRowContext(ctx, "SELECT count(*) FROM "+n1DriverQuote(table)).Scan(&n); err != nil {
			return err
		}
		if err := n1DriverEqual("count "+table, n, expectedCounts[table]); err != nil {
			return err
		}
	}
	return nil
}

func n1DriverNullable(valid bool, value int64) any {
	if !valid {
		return nil
	}
	return value
}

func n1DriverNullableEqual(name string, got any, valid bool, value int64) error {
	if !valid {
		if got != nil {
			return fmt.Errorf("N1 %s: got %#v, want NULL", name, got)
		}
		return nil
	}
	return n1DriverEqual(name, got, value)
}

func n1DriverRow(ctx context.Context, reader inventoryCrashReader, query string, args ...any) ([]any, error) {
	rows, err := reader.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		return nil, errors.Join(sql.ErrNoRows, rows.Err())
	}
	out := make([]any, len(columns))
	dest := make([]any, len(out))
	for i := range out {
		dest[i] = &out[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	if rows.Next() {
		return nil, errors.New("N1 oracle expected exactly one raw row")
	}
	return out, rows.Err()
}

// crashN1DriverFixture is the child body for the merged N1 public path. It uses
// W/SC's real merged n1ComposedDriverPrepared, the actual prepared command and
// p.Accept; it never mints a grant, permit, lease, source or retry identity.
func crashN1DriverFixture(t *testing.T, witness *crashWitness) {
	t.Helper()
	d, preparation, command, original := n1ComposedDriverPrepared(t)
	c, ok := command.(*inventoryAcceptCommand)
	if !ok || c == nil || !c.valid {
		t.Fatal("N1 child did not receive the compiled original command")
	}
	e, path := d.opening.executor, d.opening.binding.path
	// Freeze this fixture's real captured clock BEFORE the crash wrapper. The
	// original real lease stays valid; a synthetic 2100 clock would expire it.
	clock := e.now()
	source := n1DriverCrashSource{Input: n1DriverInputFrom(c)}
	source.Input.ClockMS = clock.UnixMilli()
	source.Baseline = n1DriverCaptureBaseline(t, t.Context(), e.db, source.Input)
	initial, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	source.Initial = initial
	witness.Path, witness.Reference, witness.N1Driver = path, original.Receipt, &source
	ids := map[string]string{}
	for _, entry := range d.writer.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	seen := map[string]bool{}
	armed := false
	installCrashDriverAt(t, e, path, witness, clock, func(query string) {
		if !armed {
			return
		}
		id, ok := ids[strings.TrimSpace(query)]
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		phase := "native_" + id
		source.Trace = append(source.Trace, phase)
		crashPoint(t, witness, phase)
	})
	armed = true
	out, err := preparation.Accept(t.Context(), command)
	armed = false
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || !reflect.DeepEqual(out, InventoryResult{}) ||
			!e.ambiguous || !d.writer.effective.fenced {
			t.Fatal("N1 uncertain COMMIT reported success or escaped quarantine", out, err)
		}
		// The uncertain COMMIT quarantines and closes the writer pool, so the
		// child cannot read the file here. The parent verifies the reopened raw
		// state: committed-after against its independent journal model and
		// ambiguous-before against the frozen initial checkpoint with zero
		// receipts/children.
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
		t.Fatal("N1 uncertain boundary was never reached", witness.Case)
	}
	if err != nil {
		t.Fatal("actual public N1 Accept refused", err)
	}
	journal, err := checkN1DriverCommitted(t.Context(), e.db, source)
	if err != nil {
		t.Fatal(err)
	}
	source.Journal = &journal
	want, err := n1DriverExpectedResult(source, journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverResult(want, out); err != nil {
		t.Fatal(err)
	}
	if e.db.Stats().InUse != 0 {
		t.Fatal("N1 public return retained a physical connection")
	}
	if witness.Case.Boundary == "trace" {
		return
	}
	crashPoint(t, witness, "api_return")
	t.Fatal("N1 public boundary was never reached", witness.Case)
}

func verifyN1DriverCrash(t *testing.T, witness crashWitness) {
	t.Helper()
	source := witness.N1Driver
	if source == nil {
		t.Fatal("N1 crash witness lost the original source checkpoint")
	}
	if err := checkN1DriverReference(source.Input, witness.Reference); err != nil {
		t.Fatal(err)
	}
	if witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("N1 uncertain outcome changed", witness.Unknown, witness.Case)
	}
	// Canonical existing-file opening and the actual owner lock. The complete
	// 84-ID public-driver rebind is not factored yet; raw recovery is verified
	// here and public-history rebind remains an explicit parent-owned gap.
	owner := scanOpeningOwner(t, witness.Path)
	opening := openScanFixture(t, witness.Path, owner)
	e := opening.executor
	var integrity string
	if err := e.db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal("N1 reopen integrity", integrity, err)
	}
	foreign, err := e.db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if foreign.Next() {
		t.Fatal("N1 reopen foreign key violation")
	}
	if err := errors.Join(foreign.Err(), foreign.Close()); err != nil {
		t.Fatal(err)
	}
	if crashCommitted(witness.Case) {
		journal, err := checkN1DriverCommitted(t.Context(), e.db, *source)
		if err != nil {
			t.Fatal(err)
		}
		if source.Journal != nil {
			if err := compareInventoryCrashJournal(*source.Journal, journal); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	got, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverCheckpoint(source.Initial, got); err != nil {
		t.Fatal(err)
	}
	var receipts, children int64
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_receipts WHERE run_id=?`, source.Input.RunID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM scan_command_results WHERE run_id=?`, source.Input.RunID).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || children != 0 {
		t.Fatal("uncommitted N1 prefix survived restart", receipts, children)
	}
}

func closeN1DriverCrashFixture(t *testing.T, d *ScanDriver) {
	t.Helper()
	if d == nil {
		return
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The actual checkpoint fixture owns the keeper and opening. Its registered
	// cleanup admits/joins keeper shutdown before the final opening close;
	// asking the opening to close here must refuse while that keeper is live.
}

// TestCrashReplayN1DriverPublicPath executes the actual merged public N1
// command, not the private component body. It covers the five terminal
// boundaries, public API return and both lost-COMMIT replies, plus one native
// boundary per currently executed statement in this fixture.
func TestCrashReplayN1DriverPublicPath(t *testing.T) {
	t.Helper()
	if raceDetector {
		// This sequential child matrix exceeds one shard's race budget. The
		// same cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	d, preparation, command, _ := n1ComposedDriverPrepared(t)
	e, path := d.opening.executor, d.opening.binding.path
	clock := e.now()
	witness := crashWitness{Case: crashCase{Domain: "wc_n1_driver", Operation: "accept", Boundary: "trace"}}
	c, ok := command.(*inventoryAcceptCommand)
	if !ok || c == nil || !c.valid {
		t.Fatal("public N1 fixture lost its compiled original command")
	}
	source := n1DriverCrashSource{Input: n1DriverInputFrom(c)}
	source.Input.ClockMS = clock.UnixMilli()
	source.Baseline = n1DriverCaptureBaseline(t, t.Context(), e.db, source.Input)
	initial, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	source.Initial = initial
	ids := map[string]string{}
	for _, entry := range d.writer.effective.registry.Manifest() {
		ids[strings.TrimSpace(entry.SQL)] = string(entry.ID)
	}
	var phases []string
	seen := map[string]bool{}
	armed := false
	installCrashDriverAt(t, e, path, &witness, clock, func(query string) {
		if !armed {
			return
		}
		id, ok := ids[strings.TrimSpace(query)]
		if !ok || seen[id] {
			return
		}
		seen[id] = true
		phases = append(phases, "native_"+id)
	})
	armed = true
	out, err := preparation.Accept(t.Context(), command)
	armed = false
	if err != nil {
		t.Fatal(err)
	}
	journal, err := checkN1DriverCommitted(t.Context(), e.db, source)
	if err != nil {
		t.Fatal(err)
	}
	want, err := n1DriverExpectedResult(source, journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareN1DriverResult(want, out); err != nil {
		t.Fatal(err)
	}
	if e.db.Stats().InUse != 0 {
		t.Fatal("public N1 trace retained a physical connection")
	}
	if len(phases) == 0 {
		t.Fatal("public N1 trace observed no native statement")
	}
	closeN1DriverCrashFixture(t, d)
	boundaries := append([]string{"before_begin", "after_begin"}, phases...)
	boundaries = append(boundaries, "before_commit", "after_commit", "physical_return", "api_return")
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			got := runCrashChild(t, crashCase{"wc_n1_driver", "accept", boundary, ""})
			verifyN1DriverCrash(t, got)
		})
	}
	for _, fault := range []string{"before", "after"} {
		t.Run("uncertain_return/"+fault, func(t *testing.T) {
			got := runCrashChild(t, crashCase{"wc_n1_driver", "accept", "uncertain_return", fault})
			verifyN1DriverCrash(t, got)
		})
	}
}

// Three focused real corruption controls over independently committed raw rows.
// The oracle is exactly the same raw/input model used by the crash cells.
func TestCrashN1DriverOracleRejectsCorruption(t *testing.T) {
	d, preparation, command, original := n1ComposedDriverPrepared(t)
	e, path := d.opening.executor, d.opening.binding.path
	clock := e.now()
	c, ok := command.(*inventoryAcceptCommand)
	if !ok || c == nil || !c.valid {
		t.Fatal("public N1 control lost its compiled original command")
	}
	source := n1DriverCrashSource{Input: n1DriverInputFrom(c)}
	source.Input.ClockMS = clock.UnixMilli()
	source.Baseline = n1DriverCaptureBaseline(t, t.Context(), e.db, source.Input)
	initial, err := n1DriverCheckpoint(t.Context(), e.db)
	if err != nil {
		t.Fatal(err)
	}
	source.Initial = initial
	// Same frozen clock as the crash cells, so the independently expected
	// AppliedMS is the actual commit value rather than a later wall reading.
	witness := crashWitness{Case: crashCase{Domain: "wc_n1_driver", Operation: "accept", Boundary: "trace"}}
	installCrashDriverAt(t, e, path, &witness, clock, nil)
	if _, err := preparation.Accept(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := checkN1DriverCommitted(t.Context(), e.db, source); err != nil {
		t.Fatal(err)
	}
	journal, err := readN1DriverJournal(t.Context(), e.db, source.Input)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal.Children) != 1 {
		t.Fatal("control fixture did not commit one child")
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	receiptArgs := []any{original.RunID, source.Input.IdentityIncarnation, source.Input.IdentitySequence}
	for name, control := range map[string]struct {
		query string
		args  []any
	}{
		"receipt_source":   {`UPDATE scan_command_receipts SET source_revision='CORRUPTED-SOURCE' WHERE run_id=? AND command_incarnation=? AND command_sequence=?`, receiptArgs},
		"child_object":     {`UPDATE scan_command_results SET object_id='CORRUPTED-OBJECT' WHERE run_id=? AND command_incarnation=? AND command_sequence=?`, receiptArgs},
		"filename_visible": {`UPDATE catalog_entities SET title='CORRUPTED-TITLE' WHERE id=?`, []any{journal.Children[0].SubjectID}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), "SAVEPOINT crash_n1_oracle"); err != nil {
				t.Fatal(err)
			}
			result, err := conn.ExecContext(t.Context(), control.query, control.args...)
			if err != nil {
				t.Fatal(err)
			}
			if rows, err := result.RowsAffected(); err != nil || rows != 1 {
				t.Fatal("control did not mutate one raw row", rows, err)
			}
			if _, err := checkN1DriverCommitted(t.Context(), conn, source); err == nil {
				t.Fatal("N1 corruption escaped independent oracle", name)
			}
			if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO crash_n1_oracle"); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(t.Context(), "RELEASE crash_n1_oracle"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Three actual production overlays are explicit runner controls, rather than
// nested compiler jobs in ordinary unit CI. Only the killed child is mutated.
func TestCrashN1DriverProductionMutants(t *testing.T) {
	if os.Getenv("PORTICO_LANE_EVIDENCE_DIR") == "" {
		t.Skip("explicit hosted production-mutant proof")
	}
	for _, m := range []struct{ name, file, old, new, reason string }{
		{"public_return", "scan_inventory_preparation.go", "return out.inventory, nil", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" {out.inventory.SourceID=\"WRONG-N1-PUBLIC-PROJECTION\"}; return out.inventory, nil", "N1 public return changed"},
		{"saved_digest", "scan_fresh.go", "if err = execInventoryFreshOne(ctx, turn, scanReceiptInsert", "if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" {digest[0]++}; if err = execInventoryFreshOne(ctx, turn, scanReceiptInsert", "N1 header result_digest"},
		{"saved_child", "scan_fresh.go", "m.ObjectID, m.LibraryID, m.SubjectID", "func() string {if os.Getenv(\"PORTICO_CRASH_CASE\") != \"\" {return \"ffffffffffffffffffffffffffffffff\"};return m.ObjectID}(), m.LibraryID, m.SubjectID", "N1 header result_digest"},
	} {
		t.Run(m.name, func(t *testing.T) {
			data, err := os.ReadFile(m.file)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(data), m.old) != 1 {
				t.Fatal("N1 mutant source locator changed", m.file)
			}
			changed := strings.Replace(string(data), m.old, m.new, 1)
			changed = strings.Replace(changed, "import (", "import (\n\"os\"", 1)
			root := t.TempDir()
			replacement := filepath.Join(root, "mutant.go")
			if err = os.WriteFile(replacement, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			original, err := filepath.Abs(m.file)
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]any{"Replace": map[string]string{original: replacement}})
			if err != nil {
				t.Fatal(err)
			}
			overlay := filepath.Join(root, "overlay.json")
			if err = os.WriteFile(overlay, payload, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-overlay", overlay, "-p", "2", "-parallel", "2", "-count=1", "-timeout=2m", "-run", "^TestCrashReplayN1DriverPublicPath$/^api_return$", ".")
			cmd.Env = append(os.Environ(), "TMPDIR="+root, "GOTMPDIR="+root)
			output, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil || !strings.Contains(string(output), m.reason) || !strings.Contains(string(output), "TestCrashReplayChild") || strings.Contains(string(output), "[build failed]") {
				t.Fatalf("N1 mutant escaped intended child check: %v\n%s", err, output)
			}
			t.Log("rejected production mutation at child semantic check:", m.reason)
		})
	}
}

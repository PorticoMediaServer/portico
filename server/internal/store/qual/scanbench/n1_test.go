package scanbench

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/scanclock"
	"github.com/PorticoMediaServer/portico/server/internal/store/scan"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// These injected method/value controls have NO W authority, database or native
// lease. Their fabricated results test wiring, never accepting qualification.
type draftPreparation struct {
	identityRoot                                             []byte
	identity                                                 writer.CommandIdentity
	input                                                    writer.InventoryAcceptInput
	page                                                     nativeinventory.Page
	result                                                   writer.InventoryResult
	snapshotErr, observeErr, acceptErr, resolveErr, closeErr error
	events                                                   []string
	want, accepted, resolved                                 writer.ScanReceiptRequest
}

func (p *draftPreparation) Snapshot(context.Context) (writer.InventoryPreparationSnapshot, error) {
	p.events = append(p.events, "snapshot")
	return writer.InventoryPreparationSnapshot{Identity: p.identity, Input: p.input, IdentityRoot: bytes.Clone(p.identityRoot)}, p.snapshotErr
}
func (p *draftPreparation) ObserveInventory(context.Context) (nativeinventory.Page, error) {
	p.events = append(p.events, "observe")
	return p.page, p.observeErr
}
func (p *draftPreparation) Accept(_ context.Context, command writer.InventoryAcceptCommand) (writer.InventoryResult, error) {
	p.events = append(p.events, "accept")
	var err error
	p.accepted, err = writer.InventoryAcceptReference(command)
	if err != nil || p.accepted != p.want {
		return writer.InventoryResult{}, scan.ErrInvalid
	}
	return p.result, p.acceptErr
}
func (p *draftPreparation) ResolveScan(_ context.Context, original writer.ScanReceiptRequest) (writer.InventoryResult, error) {
	p.events = append(p.events, "resolve")
	p.resolved = original
	return p.result, p.resolveErr
}
func (p *draftPreparation) Close(context.Context) error {
	p.events = append(p.events, "close")
	return p.closeErr
}

func draftN1() (*draftPreparation, FilenameOptions, scanclock.Manifest) {
	p := &draftPreparation{identityRoot: []byte("/retained-library-root"), identity: writer.CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1},
		input: writer.InventoryAcceptInput{
			Scope:     writer.InventoryScopeInput{RunID: "fixture-run", LibraryID: 3, LibraryKey: "fixture-library"},
			Source:    writer.InventorySourceInput{ID: "source", Incarnation: "incarnation", RootIdentity: "root", ResolvedRoot: []byte("/media"), Generation: 7},
			Run:       writer.InventoryRunInput{Revision: 4, PolicyRevision: 5, LeaseOwner: 6, LeaseGeneration: 7, LeaseExpiresMS: 1234567890, Phase: "inventory", ScanMode: "incremental"},
			Directory: writer.InventoryDirectoryInput{Path: []byte("."), Identity: "directory", Revision: "revision", CursorKind: "local_getdents64_cookie_v1"}},
		page: nativeinventory.Page{Version: 1, RootIdentity: "root", DirectoryIdentity: "directory", DirectoryRevision: "revision", NextCursor: "17",
			Entries: []nativeinventory.Entry{{Path: []byte("/media/The Film.mkv"), Name: []byte("The Film.mkv"), ObjectIdentity: "dev:4:ino:9", ChangeToken: "change", Size: 123, ModifiedNS: -1}}}}
	return p, FilenameOptions{MetadataLanguage: "en"},
		scanclock.Manifest{Run: "fixture-run", Database: "fixture-database", ClockDomain: "process-monotonic", FixtureSHA256: strings.Repeat("a", 64),
			SourceSHA: strings.Repeat("b", 40), BinarySHA256: strings.Repeat("c", 64), HostEpoch: "fixture-epoch", MediaProfile: "throttled-hdd"}
}

func draftApplied(original writer.ScanReceiptRequest) writer.InventoryResult {
	return writer.InventoryResult{Outcome: writer.ReceiptOutcome{State: writer.Applied, Reference: original.Receipt}, RunID: original.RunID,
		AcceptedRunRevision: 5, SourceID: "source", SourceIncarnation: "incarnation", SourceRevision: "revision", NextCursor: "17", ResultCount: 1,
		Digest: writer.ResultDigest{1}, Members: []writer.InventoryMember{{ObjectID: "object", LibraryID: original.LibraryID, SubjectID: 31, AssetID: 41, AcceptedAssetRevision: 1}}}
}

func TestDraftN1AttemptSavesOriginalAndSeparatesAppliedFromVisibility(t *testing.T) {
	p, naming, manifest := draftN1()
	a, err := ObserveN1(t.Context(), p, naming, manifest)
	if err != nil || !reflect.DeepEqual(p.events, []string{"snapshot", "observe"}) {
		t.Fatal("observation did not stay with W", p.events, err)
	}
	p.want = a.Original() // saved BEFORE the first accept call
	p.result = draftApplied(p.want)
	result, err := a.Accept(t.Context())
	r := a.Report()
	if err != nil || !reflect.DeepEqual(result, p.result) || p.accepted != p.want || !r.AppliedObserved || r.AppliedObservedNS == nil || *r.AppliedObservedNS <= 0 {
		t.Fatal("actual call/ORIGINAL timing join lost", result, r, err)
	}
	if r.Label != "DIRECTIONAL" || r.Qualification || r.FilenameObserved || r.FirstFilenameVisibleNS != nil || r.AllFilenameVisibleNS != nil || r.FirstBasicNS != nil || r.AllBasicNS != nil || len(r.Missing) < 3 {
		t.Fatal("commit or declared HDD profile became visibility/Basic", r)
	}
	if _, err := a.Accept(t.Context()); !errors.Is(err, scan.ErrInvalid) || a.Original() != p.want || len(p.events) != 3 {
		t.Fatal("second submission or changed ORIGINAL", p.events, err)
	}
	p.closeErr = nativeinventory.ErrHelperRetirementPending
	if err := a.Close(t.Context()); !errors.Is(err, p.closeErr) || a.Original() != p.want {
		t.Fatal("pending close lost original custody handle", err)
	}
	if a.preparation == nil || a.prepared.Command() == nil {
		t.Fatal("pending close released prepared payload before physical retirement")
	}
	p.closeErr = nil
	if err := a.Close(t.Context()); err != nil || !reflect.DeepEqual(p.events, []string{"snapshot", "observe", "accept", "close", "close"}) {
		t.Fatal("did not rejoin SAME preparation", p.events, err)
	}
	if a.preparation != nil || a.prepared.Command() != nil || a.Original() != p.want {
		t.Fatal("retired consumer kept native payload or lost fixed recovery reference")
	}
}

func TestDraftN1AmbiguityResolvesSameOriginalWithoutFreshClockOrACK(t *testing.T) {
	p, naming, manifest := draftN1()
	a, err := ObserveN1(t.Context(), p, naming, manifest)
	if err != nil {
		t.Fatal(err)
	}
	p.want = a.Original()
	p.result, p.acceptErr = draftApplied(p.want), context.DeadlineExceeded
	result, err := a.Accept(t.Context())
	if !errors.Is(err, p.acceptErr) || !reflect.DeepEqual(result, writer.InventoryResult{}) || a.Report().AppliedObserved {
		t.Fatal("error output became Applied", result, err)
	}
	p.result.Outcome.Replayed, p.resolveErr = true, context.Canceled
	result, err = a.Resolve(t.Context(), p)
	if !errors.Is(err, p.resolveErr) || !reflect.DeepEqual(result, writer.InventoryResult{}) || p.resolved != p.want {
		t.Fatal("resolve did not retain SAME ORIGINAL/zero errors", result, err)
	}
	p.resolveErr = nil
	result, err = a.Resolve(t.Context(), p)
	if err != nil || !result.Outcome.Replayed || p.resolved != p.want || a.Report().AppliedObserved || a.Report().FirstFilenameVisibleNS != nil {
		t.Fatal("saved result manufactured an earlier acceptance/visibility clock", result, err)
	}
	if !reflect.DeepEqual(p.events, []string{"snapshot", "observe", "accept", "resolve", "resolve"}) {
		t.Fatal("recovery observed again, resubmitted, closed or ACKed", p.events)
	}
}

func TestDraftN1ZeroStateAndRawRefusalRemainUnmeasured(t *testing.T) {
	p, naming, manifest := draftN1()
	a, err := ObserveN1(t.Context(), p, naming, manifest)
	if err != nil {
		t.Fatal(err)
	}
	p.want = a.Original()
	if result, err := a.Accept(t.Context()); err != nil || !reflect.DeepEqual(result, writer.InventoryResult{}) || a.Report().AppliedObserved {
		t.Fatal("nil-error zero-state became a committed proof", result, err)
	}
	p, naming, manifest = draftN1()
	p.page.Entries[0].Name, p.page.Entries[0].Path = []byte("Film-\xff.mkv"), []byte("/media/Film-\xff.mkv")
	a, err = ObserveN1(t.Context(), p, naming, manifest)
	if a != nil || !errors.Is(err, writer.ErrFilenameUnrepresentable) || !reflect.DeepEqual(p.events, []string{"snapshot", "observe"}) {
		t.Fatal("raw policy refusal submitted or released preparation", a, p.events, err)
	}
	if string(p.page.Entries[0].Name) != "Film-\xff.mkv" {
		t.Fatal("raw filename repaired before SC's typed refusal")
	}
}

func TestDraftN1SnapshotOwnsIdentityRootAndPreservesPart(t *testing.T) {
	p, options, manifest := draftN1()
	p.page.Entries[0].Name, p.page.Entries[0].Path = []byte("!!!.mkv"), []byte("/media/!!!.mkv")
	snapshot, err := p.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.IdentityRoot[0] = 'X'
	if string(p.identityRoot) != "/retained-library-root" {
		t.Fatal("mutating detached snapshot changed W's retained root")
	}
	p.events = nil
	a, err := ObserveN1(t.Context(), p, options, manifest)
	if err != nil {
		t.Fatal(err)
	}
	want, err := writer.ProjectOrdinaryFilename(writer.InventoryOrdinaryFilenameInput{IdentityRoot: p.identityRoot,
		AssetPath: p.page.Entries[0].Path, RelativePath: p.page.Entries[0].Name, MetadataLanguage: options.MetadataLanguage})
	if err != nil || a.prepared.Filename() != want {
		t.Fatal("did not consume W snapshot identity root unchanged", a.prepared.Filename(), want, err)
	}
	p, options, manifest = draftN1()
	p.page.Entries[0].Name, p.page.Entries[0].Path = []byte("Film - pt2.mkv"), []byte("/media/Film - pt2.mkv")
	a, err = ObserveN1(t.Context(), p, options, manifest)
	if err != nil || a == nil || a.prepared.Filename().Part != 2 ||
		!reflect.DeepEqual(p.events, []string{"snapshot", "observe"}) {
		t.Fatal("multipart projection lost Part or submitted before saving ORIGINAL", a, p.events, err)
	}
}

//go:build !windows

package localmetadata

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// artworkWitnessFixture is a real local library tree, a real storage client and
// a counted helper boundary: every helper process records the request it was
// given, so a test can name the exact number of inventory stats and blob reads
// one candidate costs. Nothing here runs through a fake stat.
type artworkWitnessFixture struct {
	t        *testing.T
	root     string
	log      string
	store    *storage.Client
	service  *Service
	identity string
	observed int
	guarded  int
}

func newArtworkWitnessFixture(t *testing.T, images ...string) *artworkWitnessFixture {
	t.Helper()
	root := t.TempDir()
	for i, name := range images {
		writeCountedArtwork(t, filepath.Join(root, name), uint8(i+1))
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &artworkWitnessFixture{t: t, root: root, log: filepath.Join(t.TempDir(), "helper.log")}
	script := filepath.Join(t.TempDir(), "counted-helper")
	body := fmt.Sprintf("#!/bin/sh\npayload=$(cat)\nprintf '%%s\\n' \"$payload\" >> %q\nprintf '%%s' \"$payload\" | exec %q \"$@\"\n", f.log, binary)
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	f.store = storage.New(script)
	f.service, err = New(t.TempDir(), "", f.store)
	if err != nil {
		t.Fatal(err)
	}
	page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RelativePath: ".", Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	f.identity = page.RootIdentity
	return f
}

func writeCountedArtwork(t *testing.T, path string, n uint8) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 12))
	img.SetRGBA(0, 0, color.RGBA{R: n, G: 255, A: 255})
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

type helperCall struct {
	Operation string `json:"operation"`
	Inventory *struct {
		RelativePath string `json:"relativePath"`
	} `json:"inventory"`
}

// calls totals the helper requests recorded so far, by operation and path.
func (f *artworkWitnessFixture) calls() map[string]int {
	f.t.Helper()
	raw, err := os.ReadFile(f.log)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		f.t.Fatal(err)
	}
	calls := map[string]int{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var call helperCall
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			f.t.Fatalf("unrecorded helper request %q: %v", line, err)
		}
		if call.Inventory == nil {
			calls[call.Operation]++
			continue
		}
		calls[call.Operation+" "+call.Inventory.RelativePath]++
	}
	return calls
}

// observe is the same shape the ingestion guard installs: the policy check and
// the exact stat that admission already takes, returned instead of discarded.
func (f *artworkWitnessFixture) observe(ctx context.Context, path string, revoked *error) (storage.ScanReadWitness, error) {
	if revoked != nil && *revoked != nil {
		return storage.ScanReadWitness{}, *revoked
	}
	if err := ctx.Err(); err != nil {
		return storage.ScanReadWitness{}, err
	}
	relative, err := filepath.Rel(f.root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return storage.ScanReadWitness{}, errors.New("candidate escaped its root")
	}
	request := storage.InventoryRequest{Root: f.root, RootIdentity: f.identity, RelativePath: relative, FollowSymlinks: false}
	current, err := f.store.InventoryStat(ctx, "source", request)
	if err != nil {
		return storage.ScanReadWitness{}, err
	}
	return storage.ScanReadWitness{SourceID: "source", Root: request.Root, RootIdentity: request.RootIdentity,
		RelativePath: relative, FollowSymlinks: request.FollowSymlinks, Snapshot: current}, nil
}

// listing is the scan's own observation of one directory, taken here from the
// real inventory page.
func (f *artworkWitnessFixture) listing(dir string) func(string) (ArtworkDirectoryListing, bool) {
	f.t.Helper()
	page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: f.root, RootIdentity: f.identity, RelativePath: dir, Limit: 128})
	if err != nil {
		f.t.Fatal(err)
	}
	return func(string) (ArtworkDirectoryListing, bool) {
		listing := ArtworkDirectoryListing{SourceID: "source", RootIdentity: f.identity, FollowSymlinks: false}
		for _, entry := range page.Entries {
			listing.Entries = append(listing.Entries, ArtworkListingEntry{Name: entry.Name, Revision: storage.InventoryRevision(entry)})
		}
		return listing, true
	}
}

// context builds the exact read context ingestion builds: the counted
// observation, or only the guard when witness is false. Storage derives the
// admission guard from the witness, so a witnessed context has one authority for
// the candidate's pre-check, its byte admission and its post-check.
func (f *artworkWitnessFixture) context(dir string, witness bool, revoked *error) context.Context {
	f.t.Helper()
	ctx := storage.WithScanInput(context.Background(), f.store, "source", storage.InventoryRequest{Root: f.root, RootIdentity: f.identity})
	ctx = WithArtworkListing(ctx, f.listing(dir))
	if !witness {
		return storage.WithScanReadGuard(ctx, func(check context.Context, candidate string) error {
			f.guarded++
			_, err := f.observe(check, candidate, revoked)
			return err
		})
	}
	return storage.WithScanReadWitness(ctx, f.store, func(check context.Context, candidate string) (storage.ScanReadWitness, error) {
		f.observed++
		return f.observe(check, candidate, revoked)
	})
}

func (f *artworkWitnessFixture) read(ctx context.Context, media string) ([]assets.LocalArtworkCandidate, bool) {
	f.t.Helper()
	return f.service.ReadArtworkCandidates(ctx, "library", "movie", f.root, filepath.Join(f.root, media), -1)
}

func (f *artworkWitnessFixture) revision(relative string) string {
	f.t.Helper()
	current, err := storage.LocalInventoryStat(storage.InventoryRequest{Root: f.root, RootIdentity: f.identity, RelativePath: relative})
	if err != nil {
		f.t.Fatal(err)
	}
	return storage.InventoryRevision(current)
}

// The ingestion observation answers both sides of the read, so a candidate
// costs two inventory stats warm and three plus its blob cold — not four and
// five. The two sides remain two independent fresh observations.
func TestArtworkCandidateConsumesTheIngestionObservationOnBothSides(t *testing.T) {
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	ctx := f.context("Film", true, nil)
	cold, complete := f.read(ctx, "Film/Movie (2020).mkv")
	if !complete || len(cold) != 1 || cold[0].SourceName != "poster-1.png" {
		t.Fatal("the witnessed candidate was not read", cold, complete)
	}
	calls := f.calls()
	if calls["inventory-stat Film/poster-1.png"] != 3 || calls["inventory-blob Film/poster-1.png"] != 1 || f.observed != 3 || f.guarded != 0 {
		t.Fatal("cold candidate did not observe at its pre-check, byte admission and post-check", calls, f.observed, f.guarded)
	}
	warm, complete := f.read(ctx, "Film/Movie (2020).mkv")
	if !complete || len(warm) != 1 || warm[0] != cold[0] {
		t.Fatal("the warm candidate changed", warm, complete)
	}
	calls = f.calls()
	if calls["inventory-stat Film/poster-1.png"] != 5 || calls["inventory-blob Film/poster-1.png"] != 1 || f.observed != 5 || f.guarded != 0 {
		t.Fatal("warm candidate did not stay at two fresh observations", calls, f.observed, f.guarded)
	}
}

// Without an observation the caller keeps its own explicit proof, exactly as
// before: the guard and the stat are both still taken on each side.
func TestArtworkCandidateWithoutObservationKeepsItsExplicitProof(t *testing.T) {
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	ctx := f.context("Film", false, nil)
	cold, complete := f.read(ctx, "Film/Movie (2020).mkv")
	if !complete || len(cold) != 1 {
		t.Fatal("the guard-only candidate was not read", cold, complete)
	}
	calls := f.calls()
	if calls["inventory-stat Film/poster-1.png"] != 5 || calls["inventory-blob Film/poster-1.png"] != 1 {
		t.Fatal("guard-only cold candidate lost its explicit proof", calls)
	}
	if _, complete = f.read(ctx, "Film/Movie (2020).mkv"); !complete {
		t.Fatal("the warm guard-only candidate was refused")
	}
	calls = f.calls()
	if calls["inventory-stat Film/poster-1.png"] != 9 || calls["inventory-blob Film/poster-1.png"] != 1 {
		t.Fatal("guard-only warm candidate lost its explicit proof", calls)
	}
	if f.observed != 0 || f.guarded != 5 {
		t.Fatal("a guard-only context produced an observation", f.observed, f.guarded)
	}
}

// An observation that answered a different request or came from another storage
// namespace never decides: it takes the real observation, then returns an
// identity that is not this candidate, and the exact baseline proof runs instead.
func TestArtworkCandidateRejectsAnObservationForAnotherRequestOrProducer(t *testing.T) {
	for _, mismatch := range []string{"request", "producer"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newArtworkWitnessFixture(t, "Film/poster-1.png")
			ctx := storage.WithScanInput(context.Background(), f.store, "source", storage.InventoryRequest{Root: f.root, RootIdentity: f.identity})
			ctx = WithArtworkListing(ctx, f.listing("Film"))
			// The witness observes the candidate for real, then reports an
			// identity that answers another request (or another producer). If
			// that observation decided, its directory snapshot would refuse the
			// candidate.
			witness := func(check context.Context, candidate string) (storage.ScanReadWitness, error) {
				f.observed++
				observation, err := f.observe(check, candidate, nil)
				if err != nil {
					return observation, err
				}
				observation.Snapshot = storage.Snapshot{Path: "x", Name: "x", Size: 3, Directory: true}
				if mismatch == "request" {
					observation.RelativePath = "Film/other.png"
					observation.FollowSymlinks = true
				}
				return observation, nil
			}
			producer := f.store
			if mismatch == "producer" {
				producer = storage.New(f.store.Binary)
			}
			ctx = storage.WithScanReadWitness(ctx, producer, witness)
			values, complete := f.read(ctx, "Film/Movie (2020).mkv")
			if !complete || len(values) != 1 || values[0].SourceName != "poster-1.png" {
				t.Fatal("a foreign observation decided the candidate", values, complete)
			}
			// The witness observed on both candidate sides and at byte
			// admission; the mismatched identity then ran the exact guard and
			// stat pair on each side, never a cheaper proof.
			calls := f.calls()
			if calls["inventory-stat Film/poster-1.png"] != 7 || calls["inventory-blob Film/poster-1.png"] != 1 {
				t.Fatal("the exact baseline proof did not run after the mismatch", calls)
			}
			if f.observed != 5 {
				t.Fatal("the mismatched witness was not consulted as the one authority", f.observed)
			}
		})
	}
	// A matched observation is the proof that decides.
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	ctx := storage.WithScanInput(context.Background(), f.store, "source", storage.InventoryRequest{Root: f.root, RootIdentity: f.identity})
	ctx = WithArtworkListing(ctx, f.listing("Film"))
	ctx = storage.WithScanReadWitness(ctx, f.store, func(check context.Context, candidate string) (storage.ScanReadWitness, error) {
		f.observed++
		observation, err := f.observe(check, candidate, nil)
		if err == nil {
			observation.Snapshot.Directory = true
		}
		return observation, err
	})
	if values, complete := f.read(ctx, "Film/Movie (2020).mkv"); complete || len(values) != 0 {
		t.Fatal("a matched observation was not the proof that decided", values, complete)
	}
}

// A guard installed after the observation replaces it. Cached bytes do not make
// an inherited observation authoritative for the newer denial.
func TestArtworkCandidateNestedDenialReplacesTheInheritedObservation(t *testing.T) {
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	outer := f.context("Film", true, nil)
	if values, complete := f.read(outer, "Film/Movie (2020).mkv"); !complete || len(values) != 1 {
		t.Fatal("the witnessed candidate was not read", values, complete)
	}
	before := f.observed
	revoked := errors.New("current analysis authority revoked")
	nested := storage.WithScanReadGuard(outer, func(context.Context, string) error { return revoked })
	if values, complete := f.read(nested, "Film/Movie (2020).mkv"); complete || len(values) != 0 {
		t.Fatal("an inherited observation bypassed the newer denial", values, complete)
	}
	if f.observed != before {
		t.Fatal("the superseded observation was consulted under the newer denial", f.observed-before)
	}
}

// Both sides stay live observations: a candidate that changes while its bytes
// are being used is refused, and each side stats again.
func TestArtworkCandidateObservationStaysFreshBetweenTheTwoChecks(t *testing.T) {
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	ctx := f.context("Film", true, nil)
	poster := filepath.Join(f.root, "Film/poster-1.png")
	inner := f.service.readSmall
	f.service.readSmall = func(ctx context.Context, library, path string, limit int64) ([]byte, error) {
		raw, err := inner(ctx, library, path, limit)
		writeCountedArtwork(t, poster, 200)
		return raw, err
	}
	if values, complete := f.read(ctx, "Film/Movie (2020).mkv"); complete || len(values) != 0 {
		t.Fatal("a candidate changed between the two observations was published", values, complete)
	}
	if f.observed != 3 || f.guarded != 0 {
		t.Fatal("the candidate was not observed independently on both sides", f.observed, f.guarded)
	}
	if calls := f.calls(); calls["inventory-stat Film/poster-1.png"] != 3 {
		t.Fatal("the second side was not a fresh inventory stat", calls)
	}
}

// The same witness can be revoked between the two sides: the bytes already read
// do not buy publication, and the refused candidate records no cache key.
func TestArtworkCandidateRefusesASameWitnessRevocationBetweenTheTwoSides(t *testing.T) {
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	var revoked error
	ctx := f.context("Film", true, &revoked)
	poster := filepath.Join(f.root, "Film/poster-1.png")
	inner := f.service.readSmall
	f.service.readSmall = func(ctx context.Context, library, path string, limit int64) ([]byte, error) {
		raw, err := inner(ctx, library, path, limit)
		revoked = errors.New("analysis authority revoked")
		return raw, err
	}
	if values, complete := f.read(ctx, "Film/Movie (2020).mkv"); complete || len(values) != 0 {
		t.Fatal("a witness revoked after the byte read still published", values, complete)
	}
	// The pre-check and the byte admission were allowed; the post-check was the
	// first denial and was answered before its stat.
	if f.observed != 3 {
		t.Fatal("the post-check did not re-enter the same witness", f.observed)
	}
	if calls := f.calls(); calls["inventory-stat Film/poster-1.png"] != 2 || calls["inventory-blob Film/poster-1.png"] != 1 {
		t.Fatal("the denied side was not answered before its stat", calls)
	}
	f.service.mu.Lock()
	_, cached := f.service.artworkCandidateObservations[poster]
	f.service.mu.Unlock()
	if cached {
		t.Fatal("a refused candidate published a cache key")
	}
}

// A cached key does not skip the witness: after revocation the fresh pre-check
// refuses before the cached bytes are used, with no stat and no byte read.
func TestArtworkCandidateRevokesAFreshCheckForACachedKey(t *testing.T) {
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	var revoked error
	ctx := f.context("Film", true, &revoked)
	if values, complete := f.read(ctx, "Film/Movie (2020).mkv"); !complete || len(values) != 1 {
		t.Fatal("the candidate was not read before revocation", values, complete)
	}
	before, stats := f.observed, f.calls()["inventory-stat Film/poster-1.png"]
	revoked = errors.New("analysis authority revoked")
	if values, complete := f.read(ctx, "Film/Movie (2020).mkv"); complete || len(values) != 0 {
		t.Fatal("a cached candidate survived its witness revocation", values, complete)
	}
	if f.observed != before+1 {
		t.Fatal("the cached candidate did not re-check the witness exactly once", f.observed-before)
	}
	if calls := f.calls(); calls["inventory-stat Film/poster-1.png"] != stats || calls["inventory-blob Film/poster-1.png"] != 1 {
		t.Fatal("the revoked cached candidate read bytes or took a stat", calls)
	}
}

// A failed or cancelled observation is never usable, whatever is cached, and no
// observation is attempted after cancellation.
func TestArtworkCandidateRefusesAFailedOrCancelledObservation(t *testing.T) {
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	revoked := error(errors.New("policy revoked"))
	ctx := f.context("Film", true, &revoked)
	if values, complete := f.read(ctx, "Film/Movie (2020).mkv"); complete || len(values) != 0 {
		t.Fatal("a failed observation admitted a candidate", values, complete)
	}
	calls := f.calls()
	if calls["inventory-stat Film/poster-1.png"] != 0 || calls["inventory-blob Film/poster-1.png"] != 0 {
		t.Fatal("a failed observation fell back or read bytes", calls)
	}

	allowed := f.context("Film", true, nil)
	if values, complete := f.read(allowed, "Film/Movie (2020).mkv"); !complete || len(values) != 1 {
		t.Fatal("the candidate was not read before cancellation", values, complete)
	}
	// A fresh service keeps the byte read, so cancellation lands between the two
	// observations rather than on a cached key.
	cancelled := newArtworkWitnessFixture(t, "Film/poster-1.png")
	cancelledCtx, cancel := context.WithCancel(cancelled.context("Film", true, nil))
	inner := cancelled.service.readSmall
	cancelled.service.readSmall = func(ctx context.Context, library, path string, limit int64) ([]byte, error) {
		raw, err := inner(ctx, library, path, limit)
		cancel()
		return raw, err
	}
	if values, complete := cancelled.read(cancelledCtx, "Film/Movie (2020).mkv"); complete || len(values) != 0 {
		t.Fatal("a cancelled analysis published a candidate", values, complete)
	}
	// One observation answered the pre-check and the byte admission; the
	// cancellation between the sides answered before the second observation.
	if cancelled.observed != 2 {
		t.Fatal("cancellation was not answered before the second observation", cancelled.observed)
	}
}

// A root the listing does not identify refuses; the observation is not a way
// around the exact root identity.
func TestArtworkCandidateRefusesAChangedRootIdentity(t *testing.T) {
	f := newArtworkWitnessFixture(t, "Film/poster-1.png")
	ctx := storage.WithScanInput(context.Background(), f.store, "source", storage.InventoryRequest{Root: f.root, RootIdentity: f.identity})
	ctx = WithArtworkListing(ctx, func(string) (ArtworkDirectoryListing, bool) {
		return ArtworkDirectoryListing{SourceID: "source", RootIdentity: "another-root-identity",
			Entries: []ArtworkListingEntry{{Name: "poster-1.png", Revision: f.revision("Film/poster-1.png")}}}, true
	})
	ctx = storage.WithScanReadWitness(ctx, f.store, func(check context.Context, candidate string) (storage.ScanReadWitness, error) {
		return f.observe(check, candidate, nil)
	})
	if values, complete := f.read(ctx, "Film/Movie (2020).mkv"); complete || len(values) != 0 {
		t.Fatal("a listing under another root identity was admitted", values, complete)
	}
}

// The observation changes cost, not the observed result: the same slots, keys,
// order, source names and ranks come from both proofs.
func TestArtworkCandidateKeysOrderAndRanksMatchTheExplicitProof(t *testing.T) {
	images := []string{"Film/poster.png", "Film/poster-2.jpeg", "Film/fanart-1.webp", "Film/banner.jpg", "Film/poster-two.png", "Film/clearlogo-2.png"}
	witnessed := newArtworkWitnessFixture(t, images...)
	explicit := newArtworkWitnessFixture(t, images...)
	got, complete := witnessed.read(witnessed.context("Film", true, nil), "Film/Movie (2020).mkv")
	if !complete {
		t.Fatal("the witnessed enumeration was incomplete")
	}
	want, complete := explicit.read(explicit.context("Film", false, nil), "Film/Movie (2020).mkv")
	if !complete {
		t.Fatal("the explicit enumeration was incomplete")
	}
	if !reflect.DeepEqual(got, want) || len(got) != 4 {
		t.Fatal("the observation changed the admitted artwork", got, want)
	}
	slots := map[string]int{}
	names := map[string]bool{}
	for _, candidate := range got {
		if candidate.Key == "" || candidate.SourceRank == assets.LocalArtworkUnknownRank {
			t.Fatal("an admitted candidate lost its key or rank", candidate)
		}
		slots[candidate.Slot]++
		names[candidate.SourceName] = true
	}
	if !reflect.DeepEqual(slots, map[string]int{"item/poster": 2, "item/backdrop": 1, "item/logo": 1}) {
		t.Fatal("the admitted slots changed", slots)
	}
	for _, name := range []string{"poster.png", "poster-2.jpeg", "fanart-1.webp", "clearlogo-2.png"} {
		if !names[name] {
			t.Fatal("an admitted image name was lost", name, names)
		}
	}
	if names["banner.jpg"] || names["poster-two.png"] {
		t.Fatal("an unrecognized image was admitted", names)
	}
}

// Remote artwork bytes keep their rooted custody: the observation is not a read
// grant, and the media target stays the admission's target. One witness answers
// all four authority uses — the candidate's pre-check, the read's pre-admission,
// its post-read admission and the candidate's post-check — and a denial at any of
// them refuses. A post-read denial still clears the bytes it returned.
func TestArtworkCandidateRemoteBytesKeepTheirRootedCustody(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "remote", "library")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	remote := &artworkRemoteFixture{root: root, identity: "remote-root-identity"}
	store.Remote = remote
	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 12))
	img.SetRGBA(0, 0, color.RGBA{R: 4, G: 255, A: 255})
	if err = png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	remote.blob = raw.Bytes()
	revision := storage.InventoryRevision(remote.snapshot("Film/poster-1.png"))
	admission := storage.InventoryBlobAdmission{Purpose: storage.InventoryBlobAnalysisSidecar,
		JobID: "job", LibraryID: "library", LibrarySourceID: "source", RootIdentity: remote.identity,
		PolicyRevision: 1, ObjectID: "media-object", SourceRevision: "media-revision", Operation: "local_metadata"}
	media := filepath.Join(root, "Film/Movie (2020).mkv")
	// buildContext installs the one authority ingestion installs. Every call to
	// it is one authority use, whichever side of the read asked.
	buildContext := func(denyAt int) (context.Context, *int) {
		checks := 0
		ctx := storage.WithScanInput(context.Background(), store, "source", storage.InventoryRequest{Root: root, RootIdentity: remote.identity}, admission)
		ctx = WithArtworkListing(ctx, func(string) (ArtworkDirectoryListing, bool) {
			return ArtworkDirectoryListing{SourceID: "source", RootIdentity: remote.identity,
				Entries: []ArtworkListingEntry{{Name: "poster-1.png", Revision: revision}}}, true
		})
		ctx = storage.WithScanReadWitness(ctx, store, func(check context.Context, candidate string) (storage.ScanReadWitness, error) {
			checks++
			if checks == denyAt {
				return storage.ScanReadWitness{}, storage.ErrRemoteChanged
			}
			relative, err := filepath.Rel(root, candidate)
			if err != nil || !filepath.IsLocal(relative) {
				return storage.ScanReadWitness{}, errors.New("candidate escaped its root")
			}
			request := storage.InventoryRequest{Root: root, RootIdentity: remote.identity, RelativePath: relative}
			current, err := store.InventoryStat(check, "source", request)
			if err != nil {
				return storage.ScanReadWitness{}, err
			}
			return storage.ScanReadWitness{SourceID: "source", Root: root, RootIdentity: remote.identity,
				RelativePath: relative, Snapshot: current}, nil
		})
		return ctx, &checks
	}
	service, err := New(t.TempDir(), "", store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, checks := buildContext(0)
	values, complete := service.ReadArtworkCandidates(ctx, "library", "movie", root, media, -1)
	if !complete || len(values) != 1 || values[0].SourceName != "poster-1.png" {
		t.Fatal("the remote candidate was not read", values, complete)
	}
	if remote.reads != 1 || remote.request.RelativePath != "Film/poster-1.png" || remote.request.Root != root ||
		remote.admission != admission || *checks != 4 {
		t.Fatal("remote bytes left their rooted custody", remote.reads, remote.request, remote.admission, *checks)
	}
	// A denial at any one of the four uses refuses. The two candidate checks and
	// the read's pre-admission deny before any byte is read; the post-read
	// admission denies after it, and the returned bytes are cleared.
	for _, denyAt := range []int{1, 2, 3, 4} {
		remote.reads, remote.returned, remote.request, remote.admission = 0, nil, storage.InventoryRequest{}, storage.InventoryBlobAdmission{}
		fresh, err := New(t.TempDir(), "", store)
		if err != nil {
			t.Fatal(err)
		}
		ctx, checks := buildContext(denyAt)
		values, complete := fresh.ReadArtworkCandidates(ctx, "library", "movie", root, media, -1)
		if complete || len(values) != 0 {
			t.Fatalf("denial %d published remote bytes", denyAt)
		}
		if *checks != denyAt {
			t.Fatalf("denial %d did not land on the expected authority use: %d", denyAt, *checks)
		}
		wantReads := 0
		if denyAt >= 3 {
			wantReads = 1
		}
		if remote.reads != wantReads {
			t.Fatalf("denial %d reached the blob reader %d times", denyAt, remote.reads)
		}
		if denyAt == 3 {
			if len(remote.returned) == 0 {
				t.Fatal("the post-read denial did not exercise a returned blob")
			}
			for _, b := range remote.returned {
				if b != 0 {
					t.Fatal("revoked remote bytes were retained")
				}
			}
		}
	}
}

// artworkRemoteFixture is a virtual namespace with no backing folder: only the
// admitted inventory surface exists. It records the exact blob invocation.
type artworkRemoteFixture struct {
	root      string
	identity  string
	blob      []byte
	reads     int
	request   storage.InventoryRequest
	admission storage.InventoryBlobAdmission
	returned  []byte
}

func (r *artworkRemoteFixture) Handles(string) bool { return true }
func (r *artworkRemoteFixture) InspectRoot(context.Context, string) (storage.Snapshot, error) {
	return storage.Snapshot{Path: r.root, Name: filepath.Base(r.root), Directory: true, ObjectIdentity: r.identity}, nil
}
func (r *artworkRemoteFixture) Stat(_ context.Context, path string) (storage.Snapshot, error) {
	relative, err := filepath.Rel(r.root, path)
	if err != nil || !filepath.IsLocal(relative) {
		return storage.Snapshot{}, storage.ErrInventoryChanged
	}
	return r.snapshot(relative), nil
}
func (r *artworkRemoteFixture) snapshot(relative string) storage.Snapshot {
	return storage.Snapshot{Path: filepath.Join(r.root, relative), Name: filepath.Base(relative), Size: int64(len(r.blob)),
		ObjectIdentity: "object:" + relative, ChangeToken: "token:" + relative}
}
func (*artworkRemoteFixture) ListPage(context.Context, string, string, string) (storage.RemotePage, error) {
	return storage.RemotePage{}, storage.ErrRemoteCursor
}
func (*artworkRemoteFixture) CommitPage(context.Context, *sql.Tx, storage.RemotePage) error {
	return storage.ErrRemoteConfig
}
func (*artworkRemoteFixture) CheckScan(context.Context, string, string) error { return nil }
func (*artworkRemoteFixture) ValidateScan(context.Context, *sql.Tx, string, string) error {
	return nil
}
func (*artworkRemoteFixture) RecordReference(context.Context, *sql.Tx, string, string) error {
	return nil
}
func (*artworkRemoteFixture) Acquire(context.Context, string, string) (storage.RemoteObject, error) {
	return nil, storage.ErrRemoteConfig
}
func (r *artworkRemoteFixture) ReadInventoryBlob(_ context.Context, source string, request storage.InventoryRequest, admission storage.InventoryBlobAdmission, limit int64) ([]byte, error) {
	r.reads++
	r.request, r.admission = request, admission
	if source != "source" || int64(len(r.blob)) > limit {
		return nil, storage.ErrRemoteConfig
	}
	r.returned = append([]byte(nil), r.blob...)
	return r.returned, nil
}

func (r *artworkRemoteFixture) ListInventoryPage(ctx context.Context, job, directory, after string, limit int) (storage.RemotePage, error) {
	return r.ListPage(ctx, job, directory, after)
}

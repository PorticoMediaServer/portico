package downloads

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
)

// The fixture is one library, one movie and one real file on disk. Every path
// is built with filepath so the tests run identically on Windows, macOS and
// Linux; nothing here concatenates path separators.
type harness struct {
	db           *sql.DB
	service      *Service
	viewer       identity.Principal
	item         string
	itemID       int64
	asset        string
	assetID      int64
	assetPath    string
	show         string
	season       string
	itemIDs      map[string]string
	itemEntities map[string]int64
	root         string
	now          time.Time
}

const filmBody = "portico-download-fixture-bytes"

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "state", "portico.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	film := filepath.Join(root, "Film.mp4")
	if e = os.WriteFile(film, []byte(filmBody), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(film)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('lib','Movies','movie',?)`, root); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',x'00','profile',1)`); e != nil {
		t.Fatal(e)
	}
	h := &harness{db: db, root: root, assetPath: film, now: time.Unix(1_760_000_000, 0).UTC()}
	h.item, h.itemID = addCompactFixtureItem(t, db, "lib", compactcatalog.Movie, "item", "Film", 0, "", nil)
	h.asset, h.assetID = addCompactFixtureAsset(t, db, h.itemID, film, info, "mp4", "h264", "aac", 1920, 1080, 60)
	// The confined opener is not composed here: the package's own reader opens
	// the file and holds it to the size and modified time that were pinned.
	service, e := New(Options{DB: db, Now: func() time.Time { return h.now }, Plays: (&catalog.Service{}).AcceptPlaybackProgressAt})
	if e != nil {
		t.Fatal(e)
	}
	h.service = service
	h.viewer = identity.Principal{Viewer: identity.Viewer{AccountID: "account", ProfileID: "profile", ServerID: "server", Authority: "local", Role: "owner"}, Hash: "hash", Epoch: 1}
	settleDownloadsProjection(t, db)
	return h
}

func settleDownloadsProjection(t *testing.T, db *sql.DB) {
	t.Helper()
	worker := compactcatalog.NewWorker(db)
	for i := 0; i < 10000; i++ {
		_, err := worker.Step(context.Background(), 100)
		if err != nil {
			t.Fatal(err)
		}
		var pending int
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM catalog_dirty)+(SELECT count(*) FROM catalog_derivations WHERE rebuilding<>0)`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			return
		}
	}
	t.Fatal("download fixture projection did not settle")
}

// These fixtures seed compact catalogue facts through the production writer
// API so the tests exercise the same ids and derived work as live writes.
func addCompactFixtureItem(t testing.TB, db *sql.DB, library string, kind compactcatalog.Kind, key, title string, year int, added string, facts map[string]any) (string, int64) {
	return addCompactFixtureEntity(t, db, library, kind, key, title, year, added, 0, facts)
}

func addCompactFixtureEntity(t testing.TB, db *sql.DB, library string, kind compactcatalog.Kind, key, title string, year int, added string, parent int64, facts map[string]any) (string, int64) {
	t.Helper()
	var id int64
	var public string
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		lib, err := compactcatalog.LibraryTx(context.Background(), tx, library)
		if err != nil {
			return err
		}
		id, _, err = compactcatalog.UpsertEntityTx(context.Background(), tx, compactcatalog.Entity{Library: lib, Kind: kind, Parent: parent, Key: "fixture:" + key, Title: title, Year: year, Added: added})
		if err != nil {
			return err
		}
		if len(facts) != 0 {
			if err = compactcatalog.SetFactsTx(context.Background(), tx, id, facts); err != nil {
				return err
			}
		}
		return tx.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, id).Scan(&public)
	})
	if err != nil {
		t.Fatalf("seed compact entity %q: %v", key, err)
	}
	return public, id
}

func addCompactFixtureAsset(t testing.TB, db *sql.DB, item int64, path string, info os.FileInfo, container, video, audio string, width, height int, duration float64) (string, int64) {
	t.Helper()
	var id int64
	var token string
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		assetID, assetToken, err := compactcatalog.UpsertAssetTx(context.Background(), tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: container, VideoCodec: video, AudioCodec: audio, Width: width, Height: height, Duration: duration})
		if err != nil {
			return err
		}
		id, token = assetID, assetToken
		return compactcatalog.LinkAssetTx(context.Background(), tx, item, id, compactcatalog.Link{})
	})
	if err != nil {
		t.Fatalf("seed compact asset %q: %v", path, err)
	}
	return token, id
}

func addCompactFixtureLink(t testing.TB, db *sql.DB, item, asset int64) {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(context.Background(), tx, item, asset, compactcatalog.Link{})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func updateCompactFixtureAssetByToken(t testing.TB, db *sql.DB, token string, change func(*compactcatalog.Asset)) {
	t.Helper()
	var a compactcatalog.Asset
	err := db.QueryRow(`SELECT path,size,modified_ns,container,video_codec,audio_codec,width,height,duration FROM catalog_assets WHERE token=?`, token).Scan(&a.Path, &a.Size, &a.ModifiedNS, &a.Container, &a.VideoCodec, &a.AudioCodec, &a.Width, &a.Height, &a.Duration)
	if err != nil {
		t.Fatal(err)
	}
	change(&a)
	err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertAssetTx(context.Background(), tx, a)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func deleteCompactFixtureEntity(t testing.TB, db *sql.DB, id int64) {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error { return compactcatalog.DeleteEntityTx(context.Background(), tx, id) })
	if err != nil {
		t.Fatal(err)
	}
}

func (h *harness) advance(t *testing.T) {
	t.Helper()
	if e := h.service.Advance(context.Background()); e != nil {
		t.Fatal(e)
	}
}

// prepare admits one original-quality claim and drives it to ready.
func (h *harness) ready(t *testing.T, operation string) Preparation {
	t.Helper()
	batch, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: operation, MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(batch.Items) != 1 {
		t.Fatalf("expected one preparation, got %d rejected=%v", len(batch.Items), batch.Rejected)
	}
	h.advance(t)
	out, e := h.service.Get(context.Background(), h.viewer, batch.Items[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	if out.State != StateReady {
		t.Fatalf("state %q reason %q", out.State, out.Reason)
	}
	return out
}

func TestPreparationReachesReadyWithTheSourceDigest(t *testing.T) {
	h := newHarness(t)
	out := h.ready(t, "op-1")
	sum := sha256.Sum256([]byte(filmBody))
	if out.Artifact.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest %q", out.Artifact.SHA256)
	}
	if out.Artifact.Kind != "source" || out.Artifact.Estimated {
		t.Fatalf("artifact %+v", out.Artifact)
	}
	if out.Progress.BytesTotal != int64(len(filmBody)) || out.Progress.Percent != 100 {
		t.Fatalf("progress %+v", out.Progress)
	}
	if out.ExpiresAt == "" {
		t.Fatal("a ready claim must publish its retention deadline")
	}
	if len(out.Actions) != 1 || out.Actions[0] != ActionRemove {
		t.Fatalf("actions %v", out.Actions)
	}
}

func TestPreparationReplayReturnsTheOriginalBatch(t *testing.T) {
	h := newHarness(t)
	first, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-replay", MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	second, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-replay", MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !second.Duplicate || len(second.Items) != 1 || second.Items[0].ID != first.Items[0].ID {
		t.Fatalf("replay admitted new work: %+v", second)
	}
	var live int
	if e = h.db.QueryRow(`SELECT count(*) FROM download_preparations`).Scan(&live); e != nil {
		t.Fatal(e)
	}
	if live != 1 {
		t.Fatalf("%d preparations after a replay", live)
	}
	// A different operationId for the same item and quality returns the live
	// claim rather than duplicating it.
	third, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-again", MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if third.Accepted != 0 || len(third.Items) != 1 || third.Items[0].ID != first.Items[0].ID {
		t.Fatalf("duplicate claim admitted: %+v", third)
	}
}

func TestActionFencesAndLifecycle(t *testing.T) {
	h := newHarness(t)
	batch, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-life", MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	id := batch.Items[0].ID
	current := batch.Items[0]

	// A stale revision is refused and hands back the current row.
	if _, e = h.service.Act(context.Background(), h.viewer, id, Command{OperationID: "op-stale", Action: ActionPause, ExpectedRevision: current.Revision + 99}); e != ErrConflict {
		t.Fatalf("stale fence accepted: %v", e)
	}
	paused, e := h.service.Act(context.Background(), h.viewer, id, Command{OperationID: "op-pause", Action: ActionPause, ExpectedRevision: current.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if paused.State != StatePaused {
		t.Fatalf("state %q", paused.State)
	}
	// A paused claim is not advanced by the worker.
	h.advance(t)
	still, e := h.service.Get(context.Background(), h.viewer, id)
	if e != nil {
		t.Fatal(e)
	}
	if still.State != StatePaused {
		t.Fatalf("worker advanced a paused claim to %q", still.State)
	}
	// Pause is not offered from paused.
	if _, e = h.service.Act(context.Background(), h.viewer, id, Command{OperationID: "op-pause-2", Action: ActionPause, ExpectedRevision: still.Revision}); e != ErrConflict {
		t.Fatalf("illegal action accepted: %v", e)
	}
	resumed, e := h.service.Act(context.Background(), h.viewer, id, Command{OperationID: "op-resume", Action: ActionResume, ExpectedRevision: still.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if resumed.State != StateQueued {
		t.Fatalf("state %q", resumed.State)
	}
	h.advance(t)
	done, e := h.service.Get(context.Background(), h.viewer, id)
	if e != nil {
		t.Fatal(e)
	}
	if done.State != StateReady {
		t.Fatalf("state %q reason %q", done.State, done.Reason)
	}
	removed, e := h.service.Act(context.Background(), h.viewer, id, Command{OperationID: "op-remove", Action: ActionRemove, ExpectedRevision: done.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if removed.State != StateCancelled || removed.Reason != ReasonRemoved {
		t.Fatalf("removed %+v", removed)
	}
	// The slot is free again, so a fresh claim is a new row.
	again, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-after-remove", MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if again.Accepted != 1 || again.Items[0].ID == id {
		t.Fatalf("removal did not free the claim: %+v", again)
	}
}

func TestRetryReopensAFailedClaim(t *testing.T) {
	h := newHarness(t)
	batch, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-retry", MediaID: h.item, Quality: "720p-low"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	id := batch.Items[0].ID
	// Nothing has prepared a 1080p version and no viewer here may order one.
	h.advance(t)
	out, e := h.service.Get(context.Background(), h.viewer, id)
	if e != nil {
		t.Fatal(e)
	}
	if out.State != StateUnavailable || out.Reason != ReasonNotOptimized {
		t.Fatalf("state %q reason %q", out.State, out.Reason)
	}
	if !out.Artifact.Estimated || out.Artifact.Bytes <= 0 {
		t.Fatalf("a rung must publish an estimated size: %+v", out.Artifact)
	}
	retried, e := h.service.Act(context.Background(), h.viewer, id, Command{OperationID: "op-retry-1", Action: ActionRetry, ExpectedRevision: out.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if retried.State != StateQueued || retried.Reason != "" {
		t.Fatalf("retried %+v", retried)
	}
}

func TestStorageFullRefusesNewPreparations(t *testing.T) {
	h := newHarness(t)
	if _, e := h.service.ApplySettings(context.Background(), h.viewer, SettingsChange{OperationID: "op-settings", ExpectedRevision: 1, MaxPreparedBytes: 4, RetentionDays: 30}); e != nil {
		t.Fatal(e)
	}
	// The ceiling is on what the server stores: converted copies.
	_, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-full", MediaID: h.item, Quality: "720p-high"}, nil)
	if e != ErrStorageFull {
		t.Fatalf("expected storage_full, got %v", e)
	}
	// An original is the library file itself and takes no room, so it is never
	// refused for lack of it.
	if _, e = h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-full-2", MediaID: h.item, Quality: QualityOriginal}, nil); e != nil {
		t.Fatalf("an original was refused for storage it does not use: %v", e)
	}
	// Lifting the ceiling admits the converted request under a new operation id.
	if _, e = h.service.ApplySettings(context.Background(), h.viewer, SettingsChange{OperationID: "op-settings-2", ExpectedRevision: 2, MaxPreparedBytes: 0, RetentionDays: 30}); e != nil {
		t.Fatal(e)
	}
	if batch, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-full-3", MediaID: h.item, Quality: "720p-high"}, nil); e != nil || batch.Accepted != 1 {
		t.Fatalf("%+v %v", batch, e)
	}
	if _, e = h.db.Exec(`DELETE FROM download_preparations WHERE quality<>'original'`); e != nil {
		t.Fatal(e)
	}
	usage, e := h.service.Usage(context.Background(), h.viewer, true)
	if e != nil {
		t.Fatal(e)
	}
	if usage.ProfileCount != 1 || usage.ServerBytes != int64(len(filmBody)) {
		t.Fatalf("usage %+v", usage)
	}
	if usage.RemainingBytes != nil {
		t.Fatal("an unlimited ceiling must publish a null remaining")
	}
}

func TestSettingsFenceRefusesAStaleRevision(t *testing.T) {
	h := newHarness(t)
	if _, e := h.service.ApplySettings(context.Background(), h.viewer, SettingsChange{OperationID: "op-s1", ExpectedRevision: 99, MaxPreparedBytes: 0, RetentionDays: 7}); e != ErrConflict {
		t.Fatalf("stale settings write accepted: %v", e)
	}
}

func TestATransferAddressLivesWhileItIsUsedOrRenewed(t *testing.T) {
	h := newHarness(t)
	ready := h.ready(t, "op-grant")
	ctx := context.Background()
	grant, e := h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "op-grant-1"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(grant.URL, "/v1/downloads/artifacts/") || grant.Token == "" || grant.Renewed || grant.RenewAfterMs <= 0 {
		t.Fatalf("grant %+v", grant)
	}
	// A replayed operationId hands back the same address.
	same, e := h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "op-grant-1"})
	if e != nil || same.Token != grant.Token {
		t.Fatalf("a replayed grant request minted a second token: %v", e)
	}
	transfer, e := h.service.OpenGrant(ctx, grant.Token)
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(transfer.Reader)
	transfer.Reader.Close()
	if e != nil || string(body) != filmBody {
		t.Fatalf("body %q %v", body, e)
	}
	if transfer.SaveAs != "Film.mp4" {
		t.Fatalf("a saved file is named %q", transfer.SaveAs)
	}
	// A ranged read seeks against the same file; this is what a resumed
	// transfer does, hours later, with the same address.
	h.now = h.now.Add(GrantIdle - time.Hour)
	transfer, e = h.service.OpenGrant(ctx, grant.Token)
	if e != nil {
		t.Fatalf("the address ran out while it was within its idle time: %v", e)
	}
	if _, e = transfer.Reader.Seek(5, io.SeekStart); e != nil {
		t.Fatal(e)
	}
	part := make([]byte, 4)
	if _, e = io.ReadFull(transfer.Reader, part); e != nil {
		t.Fatal(e)
	}
	transfer.Reader.Close()
	if string(part) != filmBody[5:9] {
		t.Fatalf("range %q", part)
	}
	// That use slid the lifetime forward: another long pause is still covered.
	h.now = h.now.Add(GrantIdle - time.Hour)
	if transfer, e = h.service.OpenGrant(ctx, grant.Token); e != nil {
		t.Fatalf("using the address did not keep it alive: %v", e)
	}
	transfer.Reader.Close()
	// Left alone for longer than the idle time, it stops working.
	h.now = h.now.Add(GrantIdle + time.Minute)
	if _, e = h.service.OpenGrant(ctx, grant.Token); e != ErrGrant {
		t.Fatalf("an address nobody used or renewed still worked: %v", e)
	}
	// Renewal brings the same address back, so a transfer the phone's own
	// downloader is holding carries on where it stopped.
	renewed, e := h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "op-grant-2", Renew: grant.Token})
	if e != nil {
		t.Fatal(e)
	}
	if !renewed.Renewed || renewed.Token != grant.Token || renewed.URL != grant.URL {
		t.Fatalf("renewal changed the address: %+v", renewed)
	}
	if transfer, e = h.service.OpenGrant(ctx, grant.Token); e != nil {
		t.Fatalf("a renewed address did not work: %v", e)
	}
	transfer.Reader.Close()
	// Somebody else's token, or one for another download, is never extended.
	other, e := h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "op-grant-3", Renew: "not-a-grant"})
	if e != nil || other.Renewed || other.Token == grant.Token {
		t.Fatalf("an unknown token was renewed: %+v %v", other, e)
	}
	if _, e = h.service.OpenGrant(ctx, "not-a-grant"); e != ErrGrant {
		t.Fatalf("unknown grant accepted: %v", e)
	}
	// Giving up the claim ends its addresses.
	if _, e = h.service.Act(ctx, h.viewer, ready.ID, Command{OperationID: "op-grant-4", Action: ActionRemove, ExpectedRevision: ready.Revision}); e != nil {
		t.Fatal(e)
	}
	if _, e = h.service.OpenGrant(ctx, grant.Token); e != ErrGrant {
		t.Fatalf("an address outlived its claim: %v", e)
	}
}

func TestAnOriginalIsServedFromTheLibraryAndNeverCopied(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	ready := h.ready(t, "op-inplace")
	// Nothing was written beside the database for it.
	entries, e := os.ReadDir(filepath.Join(h.root, "state"))
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("an original download left a folder in the state directory: %s", entry.Name())
		}
	}
	// A second viewer asking for the same file waits for nothing: the digest
	// belongs to the file.
	second := h.viewer
	second.AccountID, second.ProfileID = "account-2", "profile-2"
	batch, e := h.service.Submit(ctx, second, Request{OperationID: "op-inplace-2", MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	var hashed int
	restore := hashChunk
	hashChunk = 1 // a second read of the file would now take one pass per byte
	t.Cleanup(func() { hashChunk = restore })
	h.advance(t)
	other, e := h.service.Get(ctx, second, batch.Items[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	if other.State != StateReady || other.Artifact.SHA256 != ready.Artifact.SHA256 {
		t.Fatalf("the second viewer did not share the digest: %q %+v", other.State, other.Artifact)
	}
	if e = h.db.QueryRow(`SELECT count(*) FROM download_source_digests`).Scan(&hashed); e != nil || hashed != 1 {
		t.Fatalf("digests remembered: %d %v", hashed, e)
	}
	// The file is replaced in the library. The address stops, the claim says
	// why, and asking again prepares the file that is there now.
	grant, e := h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "op-inplace-3"})
	if e != nil {
		t.Fatal(e)
	}
	replacement := "a different film of a different length"
	if e = os.WriteFile(h.assetPath, []byte(replacement), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(h.assetPath)
	if e != nil {
		t.Fatal(e)
	}
	// Until the library has looked at the file again, the pinned file simply
	// cannot be opened: nothing is ended, the transfer waits.
	if _, e = h.service.OpenGrant(ctx, grant.Token); e != ErrSourceOffline {
		t.Fatalf("a changed file was served, or the claim was ended early: %v", e)
	}
	updateCompactFixtureAssetByToken(t, h.db, h.asset, func(a *compactcatalog.Asset) { a.Size, a.ModifiedNS = info.Size(), info.ModTime().UnixNano() })
	settleDownloadsProjection(t, h.db)
	if _, e = h.service.OpenGrant(ctx, grant.Token); e != ErrUnavailable {
		t.Fatalf("a replaced file was still transferable: %v", e)
	}
	ended, e := h.service.Get(ctx, h.viewer, ready.ID)
	if e != nil || ended.State != StateUnavailable || ended.Reason != ReasonSourceChanged {
		t.Fatalf("claim after the file changed: %q %q %v", ended.State, ended.Reason, e)
	}
	hashChunk = restore
	again := h.ready(t, "op-inplace-4")
	sum := sha256.Sum256([]byte(replacement))
	if again.Artifact.SHA256 != hex.EncodeToString(sum[:]) || again.ID == ready.ID {
		t.Fatalf("asking again did not prepare the new file: %+v", again.Artifact)
	}
}

func TestReceiptSignVerifyRevalidateAndRevoke(t *testing.T) {
	h := newHarness(t)
	ready := h.ready(t, "op-receipt")
	results, e := h.service.IssueReceipts(context.Background(), h.viewer, "server", "op-r1", []string{ready.ID})
	if e != nil {
		t.Fatal(e)
	}
	if len(results) != 1 || results[0].Outcome != OutcomeIssued || results[0].Receipt == nil {
		t.Fatalf("results %+v", results)
	}
	envelope := *results[0].Receipt
	keys, e := h.service.ReceiptKeys(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	claims, e := VerifyReceipt(keys, envelope, h.now)
	if e != nil {
		t.Fatalf("verify: %v", e)
	}
	if claims.Artifact.SHA256 != ready.Artifact.SHA256 || claims.ItemID != h.item || claims.Viewer.ProfileID != "profile" {
		t.Fatalf("claims %+v", claims)
	}
	// A tampered payload fails: the signature covers the exact published bytes.
	tampered := envelope
	tampered.Payload = tampered.Payload[:len(tampered.Payload)-4] + "AAAA"
	if _, e = VerifyReceipt(keys, tampered, h.now); e != ErrReceipt {
		t.Fatalf("tampered receipt verified: %v", e)
	}
	// Past its expiry a receipt stops verifying even with a good signature.
	if _, e = VerifyReceipt(keys, envelope, h.now.Add(ReceiptTTL+time.Hour)); e != ErrReceipt {
		t.Fatal("an expired receipt verified")
	}
	// Revalidation renews it.
	h.now = h.now.Add(20 * 24 * time.Hour)
	renewed, e := h.service.Revalidate(context.Background(), h.viewer, "server", "op-r2", []string{envelope.ReceiptID}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(renewed) != 1 || renewed[0].Outcome != OutcomeRenewed || renewed[0].Receipt == nil {
		t.Fatalf("renewed %+v", renewed)
	}
	if _, e = VerifyReceipt(keys, *renewed[0].Receipt, h.now); e != nil {
		t.Fatalf("renewed receipt does not verify: %v", e)
	}
	// Revocation is immediate, appears in the list, and blocks revalidation.
	if _, e = h.service.Revoke(context.Background(), h.viewer, "op-r3", ReasonRemoved, []string{envelope.ReceiptID}, false); e != nil {
		t.Fatal(e)
	}
	page, e := h.service.Revocations(context.Background(), h.viewer, "", 0, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 || page.Items[0].ReceiptID != envelope.ReceiptID || page.Items[0].Reason != ReasonRemoved {
		t.Fatalf("revocations %+v", page)
	}
	after, e := h.service.Revalidate(context.Background(), h.viewer, "server", "op-r4", []string{envelope.ReceiptID}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if after[0].Outcome != OutcomeRefused || after[0].Code != ReasonRevoked {
		t.Fatalf("revoked receipt renewed: %+v", after)
	}
	// Reading from the sequence already seen returns nothing new.
	empty, e := h.service.Revocations(context.Background(), h.viewer, strconv.FormatInt(page.Items[0].Sequence, 10), 0, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("cursor returned seen entries: %+v", empty)
	}
}

func TestDownloadSwitchRevokesGrantAndReceiptRenewal(t *testing.T) {
	h := newHarness(t)
	ready := h.ready(t, "op-switch-ready")
	grant, err := h.service.IssueGrant(context.Background(), h.viewer, ready.ID, GrantRequest{OperationID: "op-switch-grant"})
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := h.service.IssueReceipts(context.Background(), h.viewer, "server", "op-switch-receipt", []string{ready.ID})
	if err != nil || len(receipts) != 1 || receipts[0].Receipt == nil {
		t.Fatalf("initial receipt: %+v %v", receipts, err)
	}
	if _, err = h.db.Exec(`INSERT INTO profile_restrictions(profile_id,allow_downloads) VALUES('profile',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err = h.service.IssueGrant(context.Background(), h.viewer, ready.ID, GrantRequest{OperationID: "op-switch-grant"}); err != ErrPolicy {
		t.Fatalf("grant replay survived switch: %v", err)
	}
	if _, err = h.service.OpenGrant(context.Background(), grant.Token); err != ErrPolicy {
		t.Fatalf("open grant survived switch: %v", err)
	}
	if _, err = h.service.IssueReceipts(context.Background(), h.viewer, "server", "op-switch-receipt", []string{ready.ID}); err != ErrPolicy {
		t.Fatalf("receipt replay survived switch: %v", err)
	}
	result, err := h.service.Revalidate(context.Background(), h.viewer, "server", "op-switch-revalidate", []string{receipts[0].Receipt.ReceiptID}, nil)
	if err != nil || len(result) != 1 || result[0].Outcome != OutcomeRefused || result[0].Code != ReasonNotAllowed {
		t.Fatalf("receipt renewed after switch: %+v %v", result, err)
	}
}

// A copy already on a device answers to what its viewer may do, never to what
// the server still keeps. Each of these is the server tidying up.
func TestACopyOnADeviceOutlivesServerTidying(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	held := func(t *testing.T, h *harness, operation string) (Preparation, string) {
		t.Helper()
		ready := h.ready(t, operation)
		results, e := h.service.IssueReceipts(ctx, h.viewer, "server", operation+"-receipt", []string{ready.ID})
		if e != nil || len(results) != 1 || results[0].Receipt == nil || results[0].TitleOnServer == nil || !*results[0].TitleOnServer {
			t.Fatalf("receipt %+v %v", results, e)
		}
		return ready, results[0].ReceiptID
	}
	renews := func(t *testing.T, h *harness, operation, receipt string, onServer bool) {
		t.Helper()
		out, e := h.service.Revalidate(ctx, h.viewer, "server", operation, []string{receipt}, nil)
		if e != nil || len(out) != 1 || out[0].Outcome != OutcomeRenewed || out[0].Receipt == nil {
			t.Fatalf("the copy was taken away: %+v %v", out, e)
		}
		if out[0].TitleOnServer == nil || *out[0].TitleOnServer != onServer {
			t.Fatalf("titleOnServer %v, want %v", out[0].TitleOnServer, onServer)
		}
		var revoked int
		if e = h.db.QueryRow(`SELECT count(*) FROM download_receipts WHERE revoked_ms>0`).Scan(&revoked); e != nil || revoked != 0 {
			t.Fatalf("receipts withdrawn: %d %v", revoked, e)
		}
	}
	t.Run("the claim ran out its retention", func(t *testing.T) {
		h := newHarness(t)
		ready, receipt := held(t, h, "op-kept-retention")
		if _, e := h.service.ApplySettings(ctx, h.viewer, SettingsChange{OperationID: "op-kept-settings", ExpectedRevision: 1, RetentionDays: 2}); e != nil {
			t.Fatal(e)
		}
		h.now = h.now.Add(3 * 24 * time.Hour)
		h.advance(t)
		out, e := h.service.Get(ctx, h.viewer, ready.ID)
		if e != nil || out.State != StateExpired || out.Reason != ReasonRetention {
			t.Fatalf("claim %q %q %v", out.State, out.Reason, e)
		}
		renews(t, h, "op-kept-retention-renew", receipt, true)
		// The server no longer hands the bytes over for the ended claim.
		if _, e = h.service.IssueGrant(ctx, h.viewer, ready.ID, GrantRequest{OperationID: "op-kept-retention-grant"}); e != ErrNotReady {
			t.Fatalf("an expired claim was still transferable: %v", e)
		}
	})
	t.Run("the viewer removed the claim from the server's list", func(t *testing.T) {
		h := newHarness(t)
		ready, receipt := held(t, h, "op-kept-removed")
		if _, e := h.service.Act(ctx, h.viewer, ready.ID, Command{OperationID: "op-kept-remove", Action: ActionRemove, ExpectedRevision: ready.Revision}); e != nil {
			t.Fatal(e)
		}
		renews(t, h, "op-kept-removed-renew", receipt, true)
	})
	t.Run("the file was replaced", func(t *testing.T) {
		h := newHarness(t)
		ready, receipt := held(t, h, "op-kept-replaced")
		updateCompactFixtureAssetByToken(t, h.db, h.asset, func(a *compactcatalog.Asset) { a.Size++ })
		settleDownloadsProjection(t, h.db)
		h.advance(t)
		out, e := h.service.Get(ctx, h.viewer, ready.ID)
		if e != nil || out.State != StateUnavailable || out.Reason != ReasonSourceChanged {
			t.Fatalf("claim %q %q %v", out.State, out.Reason, e)
		}
		renews(t, h, "op-kept-replaced-renew", receipt, true)
	})
	t.Run("the title was removed from the server", func(t *testing.T) {
		h := newHarness(t)
		ready, receipt := held(t, h, "op-kept-gone")
		deleteCompactFixtureEntity(t, h.db, h.itemID)
		h.advance(t)
		out, e := h.service.Get(ctx, h.viewer, ready.ID)
		if e != nil || out.State != StateUnavailable || out.Reason != ReasonItemDeleted {
			t.Fatalf("claim %q %q %v", out.State, out.Reason, e)
		}
		// The claim still reads as the title it was, and says it is gone.
		if out.ItemID != h.item || out.Title.Name != "Film" || out.Title.OnServer {
			t.Fatalf("a removed title lost its name: %+v", out)
		}
		usage, e := h.service.Usage(ctx, h.viewer, false)
		if e != nil || usage.ServerBytes != 0 || usage.ServerCount != 0 {
			t.Fatalf("an ended claim still counts: %+v %v", usage, e)
		}
		renews(t, h, "op-kept-gone-renew", receipt, false)
	})
}

// What does end a copy: the owner taking something away from the viewer.
func TestACopyOnADeviceEndsWhenAccessIsWithdrawn(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	ready := h.ready(t, "op-withdrawn")
	results, e := h.service.IssueReceipts(ctx, h.viewer, "server", "op-withdrawn-receipt", []string{ready.ID})
	if e != nil {
		t.Fatal(e)
	}
	receipt := results[0].ReceiptID
	// The viewer is no longer let into the title's library.
	refused := func(tx *sql.Tx, library string) error { return identity.ErrForbidden }
	out, e := h.service.Revalidate(ctx, h.viewer, "server", "op-withdrawn-renew", []string{receipt}, refused)
	if e != nil || out[0].Outcome != OutcomeRefused || out[0].Receipt != nil {
		t.Fatalf("a viewer shut out of the library kept the copy: %+v %v", out, e)
	}
	again, e := h.service.Revalidate(ctx, h.viewer, "server", "op-withdrawn-again", []string{receipt}, nil)
	if e != nil || again[0].Outcome != OutcomeRefused || again[0].Code != ReasonRevoked {
		t.Fatalf("a withdrawn receipt came back: %+v %v", again, e)
	}
	// A receipt this server has no record of says nothing about the copy.
	unknown, e := h.service.Revalidate(ctx, h.viewer, "server", "op-withdrawn-unknown", []string{"never-issued"}, nil)
	if e != nil || unknown[0].Code != ReasonUnknownReceipt {
		t.Fatalf("unknown receipt: %+v %v", unknown, e)
	}
	missing, e := h.service.IssueReceipts(ctx, h.viewer, "server", "op-withdrawn-missing", []string{"never-claimed"})
	if e != nil || missing[0].Code != ReasonUnknownPreparation {
		t.Fatalf("unknown claim: %+v %v", missing, e)
	}
	// Only a documented reason may be recorded against a receipt.
	if _, e = h.service.Revoke(ctx, h.viewer, "op-withdrawn-reason", "because", []string{receipt}, false); e != ErrInput {
		t.Fatalf("an undocumented revocation reason was accepted: %v", e)
	}
}

func TestOfflinePlaysFollowTheOrderingOfOnlinePlayback(t *testing.T) {
	h := newHarness(t)
	shareOfflineFixture(t, h)
	ctx := context.Background()
	updateCompactFixtureAssetByToken(t, h.db, h.asset, func(a *compactcatalog.Asset) { a.Duration = 3600 })
	settleDownloadsProjection(t, h.db)
	personal := identity.PersonalKey(h.viewer.Viewer)
	base := h.now
	at := func(d time.Duration) string { return base.Add(d).Format(time.RFC3339) }
	count := func(query string, args ...any) int {
		t.Helper()
		var n int
		if e := h.db.QueryRow(query, args...).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	out, e := h.service.SyncProgress(ctx, h.viewer, "op-p1", []ProgressEntry{{PlayID: "play-1", ItemID: h.item, PositionSeconds: 600, ObservedAt: at(-2 * time.Hour)}})
	if e != nil {
		t.Fatal(e)
	}
	if out.Applied != 1 || out.Entries[0].Outcome != ProgressApplied || out.Entries[0].PositionSeconds != 600 || out.Entries[0].PlayID != "play-1" {
		t.Fatalf("first play %+v", out)
	}
	// It is a play like any other: in the server's history and the viewer's.
	if count(`SELECT count(*) FROM play_history`) != 1 || count(`SELECT count(*) FROM personal_history WHERE profile_id=?`, personal) != 1 {
		t.Fatal("an offline play did not reach the histories")
	}
	// The same play reported again, further on, moves on; it is still one play.
	out, e = h.service.SyncProgress(ctx, h.viewer, "op-p2", []ProgressEntry{{PlayID: "play-1", ItemID: h.item, PositionSeconds: 900, ObservedAt: at(-90 * time.Minute)}})
	if e != nil || out.Applied != 1 || out.Entries[0].PositionSeconds != 900 {
		t.Fatalf("later report of the same play %+v %v", out, e)
	}
	if count(`SELECT count(*) FROM play_history`) != 1 {
		t.Fatal("one play became two")
	}
	// An older play from another device arrives late: it is recorded, and the
	// resume point stays where the newer play left it. This observation clears
	// the five-percent started threshold for the one-hour film.
	out, e = h.service.SyncProgress(ctx, h.viewer, "op-p3", []ProgressEntry{{PlayID: "play-0", ItemID: h.item, PositionSeconds: 240, ObservedAt: at(-5 * time.Hour)}})
	if e != nil || out.Conflicts != 1 || out.Entries[0].Outcome != ProgressStale || out.Entries[0].PositionSeconds != 900 {
		t.Fatalf("an older play moved the resume point %+v %v", out, e)
	}
	if count(`SELECT count(*) FROM play_history`) != 2 {
		t.Fatal("an older play was not recorded")
	}
	// The viewer marks the film watched on the web. An offline play made
	// before that cannot undo it.
	if _, e = h.db.Exec(`INSERT INTO personal_explicit_events(profile_id,item_id,operation_id,field,base_revision,base_value,value,device_id,device_mutation_id,sequence,authored_at,created_at) VALUES(?,?,'mark','watched',0,'false','true','web','m1',1,?,?)`, personal, h.itemID, at(-30*time.Minute), at(-30*time.Minute)); e != nil {
		t.Fatal(e)
	}
	out, e = h.service.SyncProgress(ctx, h.viewer, "op-p4", []ProgressEntry{{PlayID: "play-2", ItemID: h.item, PositionSeconds: 1200, ObservedAt: at(-time.Hour)}})
	if e != nil || out.Entries[0].Outcome != ProgressSuperseded || out.Entries[0].PositionSeconds != 900 {
		t.Fatalf("a play older than an explicit mark was applied %+v %v", out, e)
	}
	// One made after it is the newest thing known, and plays to the end: it
	// counts as watched, the resume point is cleared, and the play is counted.
	out, e = h.service.SyncProgress(ctx, h.viewer, "op-p5", []ProgressEntry{{PlayID: "play-3", ItemID: h.item, PositionSeconds: 3600, Ended: true, ObservedAt: at(-10 * time.Minute)}})
	if e != nil || out.Applied != 1 || !out.Entries[0].Watched || out.Entries[0].PositionSeconds != 0 {
		t.Fatalf("a finished play %+v %v", out, e)
	}
	if count(`SELECT plays FROM play_counts WHERE item_id=?`, h.itemID) != 1 {
		t.Fatal("a finished offline play was not counted")
	}
	// The intent is dated to the play, not to the moment it was reported.
	var authored string
	if e = h.db.QueryRow(`SELECT authored_at FROM personal_watched_intents WHERE profile_id=? AND item_id=?`, personal, h.itemID).Scan(&authored); e != nil || !strings.HasPrefix(authored, base.Add(-10*time.Minute).Format("2006-01-02T15:04:05")) {
		t.Fatalf("watched intent dated %q %v", authored, e)
	}
	// Online activity after an observation wins over it.
	if _, e = h.db.Exec(`UPDATE progress_activity SET updated_at=? WHERE profile_id=? AND item_id=?`, at(48*time.Hour), personal, h.itemID); e != nil {
		t.Fatal(e)
	}
	out, e = h.service.SyncProgress(ctx, h.viewer, "op-p6", []ProgressEntry{{PlayID: "play-4", ItemID: h.item, PositionSeconds: 42, ObservedAt: at(23 * time.Hour)}})
	if e != nil || out.Conflicts != 1 || out.Entries[0].Outcome != ProgressSuperseded {
		t.Fatalf("online activity lost to an older offline play: %+v %v", out, e)
	}
	// Unreadable and impossible entries are reported, never applied.
	out, e = h.service.SyncProgress(ctx, h.viewer, "op-p7", []ProgressEntry{
		{PlayID: "play-5", ItemID: h.item, PositionSeconds: 1, ObservedAt: "not-a-time"},
		{PlayID: "play-6", ItemID: "missing", PositionSeconds: 1, ObservedAt: at(0)},
		{PlayID: "play-7", ItemID: h.item, PositionSeconds: 1, ObservedAt: at(72 * time.Hour)},
		{ItemID: h.item, PositionSeconds: 1, ObservedAt: at(0)},
	})
	if e != nil {
		t.Fatal(e)
	}
	if out.Applied != 0 || out.Conflicts != 4 {
		t.Fatalf("invalid entries applied: %+v", out)
	}
	if out.Entries[0].Outcome != ProgressInvalid || out.Entries[1].Outcome != ProgressMissing || out.Entries[2].Outcome != ProgressFuture || out.Entries[3].Outcome != ProgressInvalid {
		t.Fatalf("outcomes %+v", out.Entries)
	}
}

// One viewer's play id can never name another viewer's play.
func TestOfflinePlayIdsBelongToTheViewer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	other := h.viewer
	other.AccountID, other.ProfileID = "account-2", "profile-2"
	for i, viewer := range []identity.Principal{h.viewer, other} {
		if _, e := h.service.SyncProgress(ctx, viewer, "op-own-"+strconv.Itoa(i), []ProgressEntry{{PlayID: "same-id", ItemID: h.item, PositionSeconds: 40, ObservedAt: h.now.Add(-time.Hour).Format(time.RFC3339)}}); e != nil {
			t.Fatal(e)
		}
	}
	var plays int
	if e := h.db.QueryRow(`SELECT count(DISTINCT playback_id) FROM play_history`).Scan(&plays); e != nil || plays != 2 {
		t.Fatalf("two viewers' plays shared a record: %d %v", plays, e)
	}
}

func TestProgressBatchIsIdempotent(t *testing.T) {
	h := newHarness(t)
	entries := []ProgressEntry{{PlayID: "play-idem", ItemID: h.item, PositionSeconds: 40, ObservedAt: h.now.Format(time.RFC3339)}}
	first, e := h.service.SyncProgress(context.Background(), h.viewer, "op-idem", entries)
	if e != nil {
		t.Fatal(e)
	}
	second, e := h.service.SyncProgress(context.Background(), h.viewer, "op-idem", entries)
	if e != nil {
		t.Fatal(e)
	}
	if second.Applied != first.Applied || second.Conflicts != first.Conflicts {
		t.Fatalf("replay differed: %+v vs %+v", first, second)
	}
}

func TestPolicyRefusesAViewerWithoutDownloads(t *testing.T) {
	h := newHarness(t)
	// The profile switch is server-local for every account.
	if _, e := h.db.Exec(`INSERT INTO profile_restrictions(profile_id,allow_downloads) VALUES('profile',0)`); e != nil {
		t.Fatal(e)
	}
	if _, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-policy", MediaID: h.item, Quality: QualityOriginal}, nil); e != ErrPolicy {
		t.Fatalf("a revoked profile was admitted: %v", e)
	}
	view, e := h.service.Options(context.Background(), h.viewer, h.item, true)
	if e != nil {
		t.Fatal(e)
	}
	if view.Policy.AllowDownloads || view.Policy.Reason != ReasonNotAllowed {
		t.Fatalf("policy %+v", view.Policy)
	}
	for _, option := range view.Options {
		if option.Available {
			t.Fatalf("option offered to a viewer who may not download: %+v", option)
		}
	}
}

func TestPolicyRevocationStopsAnAdmittedClaimAndItsReceipts(t *testing.T) {
	h := newHarness(t)
	// Disable the actual local membership after issuing its copy.
	ready := h.ready(t, "op-lost")
	if _, e := h.service.IssueReceipts(context.Background(), h.viewer, "server", "op-lost-1", []string{ready.ID}); e != nil {
		t.Fatal(e)
	}
	if _, e := h.db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='account'`); e != nil {
		t.Fatal(e)
	}
	out, e := h.service.Revalidate(context.Background(), h.viewer, "server", "op-lost-2", []string{}, nil)
	if e != ErrInput {
		t.Fatalf("empty revalidation accepted: %v %+v", e, out)
	}
	var receipt string
	if e = h.db.QueryRow(`SELECT id FROM download_receipts LIMIT 1`).Scan(&receipt); e != nil {
		t.Fatal(e)
	}
	out, e = h.service.Revalidate(context.Background(), h.viewer, "server", "op-lost-3", []string{receipt}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if out[0].Outcome != OutcomeRefused || out[0].Code != ReasonAccountDisabled {
		t.Fatalf("revalidation outcome %+v", out)
	}
	if _, e = h.service.OpenGrant(context.Background(), "irrelevant"); e != ErrGrant {
		t.Fatal("unknown grant accepted")
	}
}

func TestOptionsPublishTheLadderAndEstimates(t *testing.T) {
	h := newHarness(t)
	view, e := h.service.Options(context.Background(), h.viewer, h.item, true)
	if e != nil {
		t.Fatal(e)
	}
	if view.Options[0].Quality != QualityOriginal || view.Options[0].Estimated {
		t.Fatalf("first option %+v", view.Options[0])
	}
	seen := map[string]QualityOption{}
	for _, option := range view.Options {
		seen[option.Quality] = option
	}
	if len(view.Options) != 9 || view.Options[1].Quality != "automatic" {
		t.Fatalf("incomplete menu %+v", view.Options)
	}
	for _, option := range view.Options[2:] {
		if option.Available && !option.SameAsOriginal && !preparedmedia.Producible(option.PreparedProfileID) {
			t.Fatalf("unproducible available option %+v", option)
		}
	}
	if view.Options[2].MaxVideoBitrateBPS != 40_000_000 || view.Options[7].MaxVideoBitrateBPS != 1_500_000 {
		t.Fatalf("wrong canonical budgets %+v", view.Options)
	}
	if view.Source.Bytes != int64(len(filmBody)) || !view.Source.Available {
		t.Fatalf("source %+v", view.Source)
	}
}

func TestListPagesNewestFirst(t *testing.T) {
	h := newHarness(t)
	var hItemIDs [3]string
	for i, operation := range []string{"op-l1", "op-l2", "op-l3"} {
		h.now = h.now.Add(time.Duration(i+1) * time.Second)
		public, entity := addCompactFixtureItem(t, h.db, "lib", compactcatalog.Movie, operation, "Extra", 0, "", nil)
		addCompactFixtureLink(t, h.db, entity, h.assetID)
		settleDownloadsProjection(t, h.db)
		if _, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: operation, MediaID: public, Quality: QualityOriginal}, nil); e != nil {
			t.Fatal(e)
		}
		hItemIDs[i] = public
	}
	page, e := h.service.List(context.Background(), h.viewer, ListQuery{Limit: 2}, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("page %+v", page)
	}
	if page.Items[0].ItemID != hItemIDs[2] || page.Items[1].ItemID != hItemIDs[1] {
		t.Fatalf("order %v %v", page.Items[0].ItemID, page.Items[1].ItemID)
	}
	rest, e := h.service.List(context.Background(), h.viewer, ListQuery{Cursor: page.NextCursor, Limit: 2}, false)
	if e != nil {
		t.Fatal(e)
	}
	if len(rest.Items) != 1 || rest.Items[0].ItemID != hItemIDs[0] || rest.NextCursor != "" {
		t.Fatalf("rest %+v", rest)
	}
	if _, e = h.service.List(context.Background(), h.viewer, ListQuery{State: "nonsense", Limit: 2}, false); e != ErrInput {
		t.Fatal("an unknown state filter was accepted")
	}
	if _, e = h.service.List(context.Background(), h.viewer, ListQuery{Cursor: "bogus", Limit: 2}, false); e != ErrInput {
		t.Fatal("a fabricated cursor was accepted")
	}
}

// PERF-S23: a container is never a synchronous batch target (that path cut a
// season at 100 and knew no shows); containers are durable requests
// (requests_test.go). The batch still resolves the one next episode.
func TestNextEpisodeTargetAndOneFormOnly(t *testing.T) {
	h := newHarness(t)
	_, showID := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Show, "show", "Show", 0, "", 0, map[string]any{"local_key": "show"})
	seasonIDs := make([]int64, 2)
	for i := range seasonIDs {
		_, seasonIDs[i] = addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Season, fmt.Sprintf("season-%d", i+1), fmt.Sprintf("Season %d", i+1), 0, "", showID, map[string]any{"show_id": showID, "number": i + 1})
	}
	episodes := make([]string, 3)
	for i, spec := range []struct {
		key            string
		season, number int
	}{{"e1", 0, 1}, {"e2", 0, 2}, {"e3", 1, 1}} {
		public, id := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Episode, spec.key, spec.key, 0, "2020-01-01T00:00:00Z", seasonIDs[spec.season], map[string]any{"show_id": showID, "season_id": seasonIDs[spec.season], "numbering": "seasonal", "number": spec.number, "local_identity_status": "parsed"})
		episodes[i] = public
		addCompactFixtureLink(t, h.db, id, h.assetID)
	}
	settleDownloadsProjection(t, h.db)
	next, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-next", NextAfterMediaID: episodes[1], Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if next.Accepted != 1 || next.Items[0].ItemID != episodes[2] || next.Items[0].Origin != OriginNext {
		t.Fatalf("next %+v", next)
	}
	if _, e = h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-end", NextAfterMediaID: episodes[2], Quality: QualityOriginal}, nil); e != ErrNotFound {
		t.Fatalf("a last episode produced a next: %v", e)
	}
	if _, e = h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-two", MediaID: episodes[0], MediaIDs: []string{episodes[1]}, Quality: QualityOriginal}, nil); e != ErrInput {
		t.Fatal("two target forms were accepted")
	}
	if _, e = h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-bad", MediaID: episodes[0], Quality: "4320p"}, nil); e != ErrInput {
		t.Fatal("an unpublished quality was accepted")
	}
}

func TestMissingSourceIsRejectedPerTarget(t *testing.T) {
	h := newHarness(t)
	lonely, _ := addCompactFixtureItem(t, h.db, "lib", compactcatalog.Movie, "lonely", "No file", 0, "", nil)
	settleDownloadsProjection(t, h.db)
	batch, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-mixed", MediaIDs: []string{h.item, lonely, "ghost"}, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if batch.Accepted != 1 || len(batch.Rejected) != 2 {
		t.Fatalf("batch %+v", batch)
	}
	codes := map[string]string{}
	for _, rejection := range batch.Rejected {
		codes[rejection.ItemID] = rejection.Code
	}
	if codes[lonely] != ReasonItemDeleted || codes["ghost"] != ReasonItemDeleted {
		t.Fatalf("codes %v", codes)
	}
}

func TestResumeContinuesAPartialHash(t *testing.T) {
	// Shrinking the hashing window makes the checkpoint observable: the digest
	// must match a single-pass hash of the whole file even though it was folded
	// in four windows, across a pause and a resume.
	restore := hashChunk
	hashChunk = 8
	t.Cleanup(func() { hashChunk = restore })
	h := newHarness(t)
	big := filepath.Join(h.root, "Big.mp4")
	body := strings.Repeat("portico", 5) // 35 bytes: five windows, last one short
	if e := os.WriteFile(big, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(big)
	if e != nil {
		t.Fatal(e)
	}
	bigItem, bigID := addCompactFixtureItem(t, h.db, "lib", compactcatalog.Movie, "bigitem", "Big", 0, "", nil)
	_, _ = addCompactFixtureAsset(t, h.db, bigID, big, info, "mp4", "h264", "aac", 1920, 1080, 60)
	settleDownloadsProjection(t, h.db)
	batch, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-big", MediaID: bigItem, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	id := batch.Items[0].ID
	h.advance(t)
	partial, e := h.service.Get(context.Background(), h.viewer, id)
	if e != nil {
		t.Fatal(e)
	}
	if partial.State != StateRunning || partial.Progress.BytesDone != 8 {
		t.Fatalf("after one window: %q %+v", partial.State, partial.Progress)
	}
	var checkpoint []byte
	if e = h.db.QueryRow(`SELECT hash_state FROM download_preparations WHERE id=?`, id).Scan(&checkpoint); e != nil {
		t.Fatal(e)
	}
	if len(checkpoint) == 0 {
		t.Fatal("a partial hash left no checkpoint, so a pause would lose the work")
	}
	// Pausing mid-hash keeps the bytes already folded in.
	paused, e := h.service.Act(context.Background(), h.viewer, id, Command{OperationID: "op-big-pause", Action: ActionPause, ExpectedRevision: partial.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if paused.Progress.BytesDone != 8 {
		t.Fatalf("pause discarded work: %+v", paused.Progress)
	}
	h.advance(t)
	resumed, e := h.service.Act(context.Background(), h.viewer, id, Command{OperationID: "op-big-resume", Action: ActionResume, ExpectedRevision: paused.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if resumed.Progress.BytesDone != 8 {
		t.Fatalf("resume restarted the hash: %+v", resumed.Progress)
	}
	for i := 0; i < 8; i++ {
		h.advance(t)
	}
	out, e := h.service.Get(context.Background(), h.viewer, id)
	if e != nil {
		t.Fatal(e)
	}
	if out.State != StateReady {
		t.Fatalf("state %q reason %q progress %+v", out.State, out.Reason, out.Progress)
	}
	sum := sha256.Sum256([]byte(body))
	if out.Artifact.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("checkpointed digest %q does not match a single-pass hash", out.Artifact.SHA256)
	}
}

func TestGrantsAreRefusedBeforeAClaimIsReady(t *testing.T) {
	h := newHarness(t)
	batch, e := h.service.Submit(context.Background(), h.viewer, Request{OperationID: "op-early", MediaID: h.item, Quality: QualityOriginal}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = h.service.IssueGrant(context.Background(), h.viewer, batch.Items[0].ID, GrantRequest{OperationID: "op-early-1"}); e != ErrNotReady {
		t.Fatalf("a grant was minted before the artifact existed: %v", e)
	}
	if _, e = h.service.IssueReceipts(context.Background(), h.viewer, "server", "op-early-2", []string{batch.Items[0].ID}); e != nil {
		t.Fatal(e)
	}
	results, e := h.service.IssueReceipts(context.Background(), h.viewer, "server", "op-early-3", []string{batch.Items[0].ID})
	if e != nil {
		t.Fatal(e)
	}
	if results[0].Outcome != OutcomeRefused || results[0].Receipt != nil {
		t.Fatalf("a receipt vouched for bytes that do not exist: %+v", results)
	}
	// Another viewer cannot read, act on, grant or receipt this claim.
	stranger := identity.Principal{Viewer: identity.Viewer{AccountID: "other", ProfileID: "other", ServerID: "server", Authority: "local", Role: "viewer"}, Hash: "hash", Epoch: 1}
	if _, e = h.service.Get(context.Background(), stranger, batch.Items[0].ID); e != ErrNotFound {
		t.Fatalf("another viewer read the claim: %v", e)
	}
	if _, e = h.service.Act(context.Background(), stranger, batch.Items[0].ID, Command{OperationID: "op-steal", Action: ActionCancel, ExpectedRevision: 1}); e != ErrNotFound {
		t.Fatalf("another viewer acted on the claim: %v", e)
	}
	if _, e = h.service.IssueGrant(context.Background(), stranger, batch.Items[0].ID, GrantRequest{OperationID: "op-steal-2"}); e != ErrNotFound {
		t.Fatalf("another viewer took a grant: %v", e)
	}
}

func TestAudiobookItemsPublishOneOptimizedOption(t *testing.T) {
	h := newHarness(t)
	if _, err := h.db.Exec(`UPDATE libraries SET kind='audiobook' WHERE id='lib'`); err != nil {
		t.Fatal(err)
	}
	song := filepath.Join(h.root, "Part.flac")
	if e := os.WriteFile(song, []byte("flac"), 0600); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(song)
	if e != nil {
		t.Fatal(e)
	}
	_, bookID := addCompactFixtureItem(t, h.db, "lib", compactcatalog.Book, "book", "Book", 0, "", nil)
	track, trackID := addCompactFixtureEntity(t, h.db, "lib", compactcatalog.Part, "part", "Part", 0, "", bookID, nil)
	_, _ = addCompactFixtureAsset(t, h.db, trackID, song, info, "flac", "", "flac", 0, 0, 240)
	settleDownloadsProjection(t, h.db)
	view, e := h.service.Options(context.Background(), h.viewer, track, true)
	if e != nil {
		t.Fatal(e)
	}
	if len(view.Options) != 9 {
		t.Fatalf("audio menu %+v", view.Options)
	}
	optimized := view.Options[1]
	if optimized.Quality != "automatic" || optimized.TargetHeight != 0 || optimized.MaxVideoBitrateBPS != 0 || optimized.PreparedProfileID != "audio-aac-v3" {
		t.Fatalf("audio automatic invents picture %+v", optimized)
	}
	if optimized.Bytes < 5_500_000 || optimized.Bytes > 6_200_000 {
		t.Fatal(optimized.Bytes)
	}
	for _, option := range view.Options[2:] {
		if option.Available || option.Reason != "not_video" || option.PreparedProfileID != "" {
			t.Fatalf("video step applied to audio %+v", option)
		}
	}

}

func TestDownloadPinsRequestedVersionAndClaimsFollowMerge(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	first, err := h.service.Submit(ctx, h.viewer, Request{OperationID: "version-first", MediaID: h.item, VersionID: h.asset, Quality: QualityOriginal}, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.root, "Film-4K.mp4")
	if err = os.WriteFile(path, []byte("other version"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := addCompactFixtureAsset(t, h.db, h.itemID, path, info, "mp4", "h264", "aac", 3840, 2160, 60)
	second, err := h.service.Submit(ctx, h.viewer, Request{OperationID: "version-second", MediaID: h.item, VersionID: other, Quality: QualityOriginal}, nil)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatal(second, err)
	}
	h.advance(t)
	h.advance(t)
	ready, err := h.service.Get(ctx, h.viewer, first.Items[0].ID)
	if err != nil || ready.State != StateReady || ready.Artifact.Ref != h.asset {
		t.Fatal(ready, err)
	}
	into, intoID := addCompactFixtureItem(t, h.db, "lib", compactcatalog.Movie, "survivor", "Survivor", 0, "", nil)
	err = dbwork.WithWriteTx(ctx, h.db, dbwork.ClassInteractive, func(tx *sql.Tx) error { return catalog.MergeTitlesTx(ctx, tx, h.itemID, intoID) })
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range []Preparation{first.Items[0], second.Items[0]} {
		out, err := h.service.Get(ctx, h.viewer, claim.ID)
		if err != nil || out.ItemID != into {
			t.Fatal(out, err)
		}
	}
}

func TestDownloadUsageSeparatesStoredBytesAndRevokesAProfile(t *testing.T) {
	h := newHarness(t)
	p := h.ready(t, "usage-revoke-source")
	ctx := context.Background()
	usage, err := h.service.UsagePage(ctx, h.viewer, true, "", 1)
	if err != nil || len(usage.Profiles) != 1 || usage.DistinctBytes != 0 || usage.Profiles[0].StoredBytes != 0 || usage.Profiles[0].AccountID != h.viewer.AccountID {
		t.Fatal(usage, err)
	}
	out, err := h.service.RevokeProfile(ctx, h.viewer, usage.Profiles[0].ProfileKey, "revoke-one-profile", func(*sql.Tx) error { return nil })
	if err != nil || out.Claims != 1 || out.More {
		t.Fatal(out, err)
	}
	claim, err := h.service.Get(ctx, h.viewer, p.ID)
	if err != nil || claim.State != StateCancelled || claim.Reason != ReasonRevoked {
		t.Fatal(claim, err)
	}
}

package preparedmedia

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// These tests exercise real persistence/artifact ownership, not an encoder or
// sandbox qualification. Production construction still detects installed tools.
func preparedFixture(t *testing.T) (*Service, identity.Principal, *catalogtest.Catalog, catalogtest.Item) {
	t.Helper()
	catalog := catalogtest.Open(t)
	db := catalog.DB
	library := catalog.Library("library", "Movies", "movie", "/fixture")
	item := catalog.Movie(library, "/fixture/source.mp4", "Movie", 2024)
	catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"duration": float64(60)})
	})
	catalog.Drain()
	a, e := mediaartifact.New(filepath.Join(canonicalFixtureDir(t), "prepared"))
	if e != nil {
		t.Fatal(e)
	}
	locks, e := livechannels.NewPhysicalLocks(filepath.Join(canonicalFixtureDir(t), "custody"))
	if e != nil {
		t.Fatal(e)
	}
	auth := func(_ context.Context, _ *sql.Tx, p identity.Principal, _ string) (identity.Principal, error) {
		return p, nil
	}
	s := &Service{db: db, artifacts: a, locks: locks, ffmpeg: "/fixture/ffmpeg", ffprobe: "/fixture/ffprobe", sandboxCapable: true, authorize: auth, continuation: auth, readers: map[string]*artifactLease{}, running: map[string]context.CancelFunc{}, wake: worker.NewSignal(), maxBytes: 1 << 20}
	t.Cleanup(func() { s.Close() })
	return s, identity.Principal{Hash: "owner-session", Viewer: identity.Viewer{Authority: "local", Role: "owner", AccountID: "owner", ProfileID: "profile", ServerID: "server"}, Epoch: 1}, catalog, item
}
func settlePreparedProjection(t *testing.T, catalog *catalogtest.Catalog) {
	t.Helper()
	catalog.Drain()
}
func execPrepared(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, e := db.Exec(q, args...); e != nil {
		t.Fatal(e)
	}
}
func requestPrepared(t *testing.T, s *Service, p identity.Principal, item catalogtest.Item, key string) (Request, Job) {
	t.Helper()
	v, e := s.View(context.Background(), p, item.Public)
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Sources) != 1 || !v.Sources[0].Available {
		t.Fatalf("source: %+v", v)
	}
	r := Request{key, item.Public, item.Token, v.Sources[0].Revision, "portable-720p-high-v3", TargetID}
	j, e := s.Submit(context.Background(), p, r)
	if e != nil {
		t.Fatal(e)
	}
	return r, j
}
func runningPrepared(t *testing.T, s *Service, id string) work {
	t.Helper()
	execPrepared(t, s.db, `UPDATE prepared_media_jobs SET state='running',generation=1 WHERE id=?`, id)
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	w, e := readWork(context.Background(), tx, id)
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func testObject(t *testing.T, s *Service) mediaartifact.Object {
	t.Helper()
	w, e := s.artifacts.BeginRetained(strings.Repeat("d", 64)+".1", 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Abort()
	if _, e = w.Write([]byte("trusted unit fixture; not claimed to be playable media")); e != nil {
		t.Fatal(e)
	}
	o, e := w.Seal(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return o
}
func publishFixture(t *testing.T, s *Service, p identity.Principal, item catalogtest.Item) (Version, mediaartifact.Object) {
	t.Helper()
	_, j := requestPrepared(t, s, p, item, "publish")
	w := runningPrepared(t, s, j.ID)
	o := testObject(t, s)
	facts := Facts{Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1280, Height: 720, Duration: 60, AudioTracks: 1}
	if e := s.publish(context.Background(), w, o, facts, "observed", "recipe"); e != nil {
		t.Fatal(e)
	}
	v, e := s.View(context.Background(), p, item.Public)
	if e != nil || len(v.Versions) != 1 {
		t.Fatal(v, e)
	}
	return v.Versions[0], o
}
func scalarPrepared(t *testing.T, s *Service, q string, args ...any) string {
	t.Helper()
	var out string
	if e := s.db.QueryRow(q, args...).Scan(&out); e != nil {
		t.Fatal(e)
	}
	return out
}

func TestPreparedSubmitReceiptCurrentOwnerAndRetryCAS(t *testing.T) {
	s, p, _, item := preparedFixture(t)
	ctx := context.Background()
	r, j := requestPrepared(t, s, p, item, "submit")
	replay, e := s.Submit(ctx, p, r)
	if e != nil || replay.ID != j.ID {
		t.Fatal("duplicate", replay, e)
	}
	r.ProfileID = "portable-1080p-low-v3"
	if _, e = s.Submit(ctx, p, r); !errors.Is(e, operations.ErrConflict) {
		t.Fatal("key alias", e)
	}
	viewer := p
	viewer.Role = "viewer"
	r.ProfileID = "portable-720p-high-v3"
	if _, e = s.Submit(ctx, viewer, r); !errors.Is(e, ErrOwner) {
		t.Fatal("receipt disclosed without owner", e)
	}
	stopped, e := s.Command(ctx, p, j.ID, "cancel", Command{"cancel", j.Revision})
	if e != nil || stopped.State != "cancelled" {
		t.Fatal(stopped, e)
	}
	if _, e = s.Command(ctx, p, j.ID, "retry", Command{"stale", j.Revision}); !errors.Is(e, ErrConflict) {
		t.Fatal("stale CAS", e)
	}
	retried, e := s.Command(ctx, p, j.ID, "retry", Command{"retry", stopped.Revision})
	if e != nil || retried.State != "queued" {
		t.Fatal(retried, e)
	}
	if got := scalarPrepared(t, s, `SELECT count(*) FROM catalog_entities`); got != "1" {
		t.Fatal("invented library item", got)
	}
}
func TestPreparedSourceAvailabilityAndABAFence(t *testing.T) {
	s, p, catalog, item := preparedFixture(t)
	ctx := context.Background()
	v, _ := publishFixture(t, s, p, item)
	catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, item.Asset, false)
	})
	settlePreparedProjection(t, catalog)
	view, e := s.View(ctx, p, item.Public)
	if e != nil || !view.Versions[0].Selectable || view.Sources[0].Available {
		t.Fatal("outage invalidated derivative", view, e)
	}
	catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"modified_ns": int64(2)})
	})
	settlePreparedProjection(t, catalog)
	catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"modified_ns": int64(1), "available": true})
	})
	settlePreparedProjection(t, catalog)
	view, e = s.View(ctx, p, item.Public)
	if e != nil || view.Versions[0].Selectable || view.Versions[0].Reason != "source_changed" {
		t.Fatal("metadata ABA accepted", view, e)
	}
	tx, _ := s.db.Begin()
	defer tx.Rollback()
	if _, e = SelectTx(ctx, tx, item.Public, v.ID); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestPreparedPublicationFencesCancellationSourceAndCurrentAuthority(t *testing.T) {
	for _, why := range []string{"cancel", "source", "authority"} {
		t.Run(why, func(t *testing.T) {
			s, p, catalog, item := preparedFixture(t)
			_, j := requestPrepared(t, s, p, item, "request")
			w := runningPrepared(t, s, j.ID)
			switch why {
			case "cancel":
				execPrepared(t, s.db, `UPDATE prepared_media_jobs SET state='cancelling' WHERE id=?`, j.ID)
			case "source":
				catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
					return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"modified_ns": int64(2)})
				})
				settlePreparedProjection(t, catalog)
			case "authority":
				s.continuation = func(context.Context, *sql.Tx, identity.Principal, string) (identity.Principal, error) {
					return identity.Principal{}, identity.ErrUnauthorized
				}
			}
			if e := s.publish(context.Background(), w, mediaartifact.Object{Digest: strings.Repeat("a", 64), Size: 10}, Facts{Duration: 60}, "input", "transform"); e == nil {
				t.Fatal("invalid publication accepted")
			}
			if got := scalarPrepared(t, s, `SELECT count(*) FROM prepared_media_versions`); got != "0" {
				t.Fatal("partial publication", got)
			}
		})
	}
}
func TestPreparedConsoleObservationNeverImplicitlyRetriesTerminalDomain(t *testing.T) {
	for _, state := range []string{"failed", "cancelled", "succeeded"} {
		t.Run(state, func(t *testing.T) {
			s, p, _, item := preparedFixture(t)
			_, j := requestPrepared(t, s, p, item, "request")
			execPrepared(t, s.db, `UPDATE prepared_media_jobs SET state=? WHERE id=?`, state, j.ID)
			op := scalarPrepared(t, s, `SELECT id FROM console_operations WHERE resource=?`, j.ID)
			tx, e := s.db.Begin()
			if e != nil {
				t.Fatal(e)
			}
			id, e := s.Adapter().StartTx(context.Background(), tx, op, j.ID)
			if e != nil || id != j.ID {
				tx.Rollback()
				t.Fatal(id, e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			if got := scalarPrepared(t, s, `SELECT state FROM prepared_media_jobs WHERE id=?`, j.ID); got != state {
				t.Fatalf("console changed %s into %s", state, got)
			}
		})
	}
}
func TestPreparedDeletionWaitsForSessionAndPhysicalReader(t *testing.T) {
	s, p, _, item := preparedFixture(t)
	ctx := context.Background()
	v, o := publishFixture(t, s, p, item)
	execPrepared(t, s.db, `INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('session','owner-session','owner','profile',?,?,1,'playing','grant','grant',?,'play',60)`, item.ID, item.Token, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	tx, _ := s.db.Begin()
	if e := PinSessionTx(ctx, tx, "session", v); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	removing, e := s.Delete(ctx, p, v.ID, Command{"delete", v.Revision})
	if e != nil || removing.State != "deleting" {
		t.Fatal(removing, e)
	}
	if e = s.prune(ctx, removing); e != nil {
		t.Fatal(e)
	}
	if scalarPrepared(t, s, `SELECT state FROM prepared_media_versions WHERE id=?`, v.ID) != "deleting" {
		t.Fatal("pruned active session")
	}
	reader, handled, e := s.OpenSession(ctx, "session")
	if e != nil || !handled {
		t.Fatal(handled, e)
	}
	defer reader.Close()
	execPrepared(t, s.db, `UPDATE playback_sessions SET state='stopped' WHERE id='session'`)
	if e = s.prune(ctx, removing); !errors.Is(e, livechannels.ErrPhysicalBusy) && !errors.Is(e, mediaartifact.ErrLeased) {
		t.Fatal("pruned active physical reader", e)
	}
	reader.Close()
	if e = s.prune(ctx, removing); e != nil {
		t.Fatal(e)
	}
	if scalarPrepared(t, s, `SELECT state FROM prepared_media_versions WHERE id=?`, v.ID) != "deleted" {
		t.Fatal("not pruned after retirement")
	}
	if r, e := s.artifacts.Open(ctx, o); e == nil {
		r.Close()
		t.Fatal("bytes survived unreferenced deletion")
	}
	if _, handled, e = s.OpenSession(ctx, "session"); !handled || e == nil {
		t.Fatal("silently fell back to original", handled, e)
	}
}
func TestPreparedProfilesAndCompleteOutputValidation(t *testing.T) {
	p, _ := profile("portable-720p-high-v3")
	raw := []byte(`{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"60"},"streams":[{"codec_type":"video","codec_name":"h264","width":1280,"height":720,"pix_fmt":"yuv420p","profile":"High","level":40,"avg_frame_rate":"30/1"},{"codec_type":"audio","codec_name":"aac","channels":2}]}`)
	f, e := parseProbe(raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = acceptSource(f, p, "movie"); e != nil {
		t.Fatal(e)
	}
	if e = acceptOutput(f, f, p); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*probed){func(o *probed) { o.Duration = 20 }, func(o *probed) { o.AudioTracks = 0 }, func(o *probed) { o.HDR = true }, func(o *probed) { o.Width = 1281 }, func(o *probed) { o.VideoCodec = "hevc" }, func(o *probed) { o.AudioChannels = []int{6} }, func(o *probed) { o.OtherStreams = 1 }, func(o *probed) { o.VideoLevel = 51 }, func(o *probed) { o.VideoProfile = "High 10" }, func(o *probed) { o.FrameRate = 61 }, func(o *probed) { o.FrameRate = 0 }} {
		bad := f
		change(&bad)
		if e = acceptOutput(f, bad, p); !errors.Is(e, ErrOutput) {
			t.Fatal("unsafe output", bad, e)
		}
	}
	for _, raw := range []string{`{}`, `{"format":{"duration":"NaN"}}`, `{"format":{"duration":"Inf"}}`} {
		if _, e = parseProbe([]byte(raw)); e == nil {
			t.Fatal("invalid probe accepted")
		}
	}
	if _, e = profile("arbitrary-flags"); !errors.Is(e, ErrInput) {
		t.Fatal(e)
	}
	ps := Profiles()
	ps[0].ID = "mutated"
	if Profiles()[0].ID == "mutated" {
		t.Fatal("mutable recipe catalog")
	}
}

func TestPreparedRetrySignalsWithoutInterruptingFreshAttempt(t *testing.T) {
	s, p, _, item := preparedFixture(t)
	_, j := requestPrepared(t, s, p, item, "retry-fence")
	stopped, e := s.Command(context.Background(), p, j.ID, "cancel", Command{"cancel-fence", j.Revision})
	if e != nil {
		t.Fatal(e)
	}
	interrupted := false
	s.running[j.ID] = func() { interrupted = true }
	if _, e = s.Command(context.Background(), p, j.ID, "retry", Command{"fresh-retry", stopped.Revision}); e != nil {
		t.Fatal(e)
	}
	if interrupted {
		t.Fatal("retry interrupted the newly admitted generation")
	}
}

func TestPreparedWholeSealedRecordingEndAndMultipartAdmission(t *testing.T) {
	s, p, catalog, item := preparedFixture(t)
	end := 60.0
	catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, item.Asset, compactcatalog.Link{Part: 0, End: &end})
	})
	view, e := s.View(context.Background(), p, item.Public)
	if e != nil || !view.Sources[0].Available {
		t.Fatal("synchronous catalogue association was not immediately readable", view, e)
	}
	settlePreparedProjection(t, catalog)
	v, e := s.View(context.Background(), p, item.Public)
	if e != nil || !v.Sources[0].Available {
		t.Fatal("exact full sealed-media end rejected", v, e)
	}
	end = 59
	catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, item.Asset, compactcatalog.Link{Part: 0, End: &end})
	})
	settlePreparedProjection(t, catalog)
	v, e = s.View(context.Background(), p, item.Public)
	if e != nil || v.Sources[0].Available {
		t.Fatal("clipped end accepted", v, e)
	}
	catalog.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.LinkAssetTx(ctx, tx, item.ID, item.Asset, compactcatalog.Link{Part: 0}); err != nil {
			return err
		}
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/fixture/part2.mp4", Size: 1000, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{Part: 1})
	})
	settlePreparedProjection(t, catalog)
	v, e = s.View(context.Background(), p, item.Public)
	if e != nil || len(v.Sources) != 2 || v.Sources[0].Available || v.Sources[1].Available {
		t.Fatal("unselectable multipart admitted", v, e)
	}
}

func TestPreparedRecipesUseCanonicalVersionedIdentity(t *testing.T) {
	for _, current := range Profiles() {
		r := recipeFor(current)
		if strings.HasPrefix(current.ID, "music-") {
			quality, codec, ok := MusicProfile(current.ID)
			if !ok || !strings.HasSuffix(current.ID, "-v1") || !r.AudioOnly || r.MusicQuality != quality || r.MusicCodec != codec {
				t.Fatal("invalid immutable music recipe", current.ID)
			}
			continue
		}
		if !strings.HasSuffix(current.ID, "-v3") && !strings.HasSuffix(current.ID, "-v4") {
			t.Fatal(current.ID)
		}
		if !r.DropChapters || !r.BoundedFragments {
			t.Fatal("unbounded or unremapped current recipe", current.ID)
		}
		if r.KeepSeventhOne != strings.HasSuffix(current.ID, "-v4") {
			t.Fatal("recipe version does not match its bytes", current.ID)
		}
	}
	for _, gone := range []string{"portable-720-v1", "portable-1080-v2", "portable-1440-v3"} {
		if _, err := profile(gone); err == nil {
			t.Fatal("removed recipe alias", gone)
		}
	}
}

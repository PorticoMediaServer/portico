package playback

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback/vod"
	"strings"
	"sync"
	"testing"
	"time"
)

// Uses real ffmpeg, ffprobe, and the production isolated descriptor helper.
func finiteFixture(t *testing.T) (context.Context, *sql.DB, *Service, *HLS, identity.Principal, Session, string) {
	t.Helper()
	binary := decodertest.QualifiedFFmpeg(t)
	decodertest.QualifiedFFprobe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	root := t.TempDir()
	input := filepath.Join(root, "fixture.mkv")
	out, err := exec.CommandContext(ctx, binary, "-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-t", "49", "-c:v", "libx264", "-preset", "ultrafast", "-g", "48", "-pix_fmt", "yuv420p", "-c:a", "aac", input).CombinedOutput()
	if err != nil {
		cancel()
		t.Fatalf("fixture: %v %s", err, out)
	}
	facts, err := (assets.Probe{}).Inspect(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	mustAudioExec(t, db, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',X'00','profile',1)`)
	insertFixtureSession(t, db, "login", "owner", "profile", "owner", "2099-01-01T00:00:00Z")
	_, item, _ := catalogFixture(t, db, "lib", root, compactcatalog.Movie, "item", "Movie", compactcatalog.Asset{Path: input, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
	h, err := NewHLS(ctx, db, filepath.Join(root, "hls"), binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.EnableFinite(decodertest.QualifiedFFprobe(t)); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	s.ConfigureHLS(h)
	p := identity.Principal{Hash: "login", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}, Epoch: 1}
	session, err := s.Create(p, item, "automatic", "finite")
	if err != nil {
		t.Fatal(err)
	}
	grant := strings.TrimSuffix(strings.TrimPrefix(session.StreamURL, "/v1/media/"), "/master.m3u8")
	t.Cleanup(func() {
		cancel()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			h.mu.Lock()
			n := len(h.active)
			h.mu.Unlock()
			if n == 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		db.Close()
	})
	if err = s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	return ctx, db, s, h, p, session, grant
}

func TestFiniteRealDemandFarBackCoalescingRetentionAndStop(t *testing.T) {
	ctx, db, s, h, p, session, grant := finiteFixture(t)
	manifest, err := s.HLSFileContext(ctx, grant, "master.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "#EXT-X-ENDLIST") || strings.Contains(string(before), "PLAYLIST-TYPE:EVENT") {
		t.Fatalf("not finite: %s", before)
	}
	var committed int
	if err = db.QueryRow(`SELECT count(*) FROM playback_vod_intervals WHERE session_id=? AND status!='absent'`, session.ID).Scan(&committed); err != nil || committed != 0 {
		t.Fatal("speculative work", committed, err)
	}
	// Index 7 requires a real far demand with six intervening absent intervals.
	var wg sync.WaitGroup
	paths := make([]string, 6)
	errs := make([]error, 6)
	for i := range paths {
		wg.Add(1)
		go func(i int) { defer wg.Done(); paths[i], errs[i] = s.HLSFileContext(ctx, grant, "segment-000007.ts") }(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}
	var status, hash string
	var attempts int
	var bytes, order int64
	var wall, cpu, probe float64
	if err = db.QueryRow(`SELECT status,attempts,bytes,sha256,requested_order,encode_wall,encode_cpu,probe_wall FROM playback_vod_intervals WHERE session_id=? AND ordinal=7`, session.ID).Scan(&status, &attempts, &bytes, &hash, &order, &wall, &cpu, &probe); err != nil {
		t.Fatal(err)
	}
	if status != "committed" || attempts != 1 || bytes <= 0 || len(hash) != 64 || order <= 0 || wall <= 0 || cpu <= 0 || probe <= 0 {
		t.Fatalf("production evidence: %s attempts=%d bytes=%d hash=%s order=%d work=%v/%v/%v", status, attempts, bytes, hash, order, wall, cpu, probe)
	}
	if err = db.QueryRow(`SELECT count(*) FROM playback_vod_intervals WHERE session_id=? AND status='committed'`, session.ID).Scan(&committed); err != nil || committed != 1 {
		t.Fatal("unexpected production", committed, err)
	}
	farHash, err := finiteHash(paths[0])
	if err != nil || farHash != hash {
		t.Fatal(farHash, hash, err)
	}
	if _, err = s.HLSFileContext(ctx, grant, "segment-000000.ts"); err != nil {
		t.Fatal(err)
	}
	again, err := s.HLSFileContext(ctx, grant, "segment-000007.ts")
	if err != nil {
		t.Fatal(err)
	}
	afterHash, err := finiteHash(again)
	if err != nil || afterHash != farHash {
		t.Fatal("retained bytes changed", err)
	}
	after, err := os.ReadFile(manifest)
	if err != nil || string(before) != string(after) {
		t.Fatal("manifest changed", err)
	}
	var backOrder int64
	if err = db.QueryRow(`SELECT requested_order FROM playback_vod_intervals WHERE session_id=? AND ordinal=0`, session.ID).Scan(&backOrder); err != nil || backOrder <= order {
		t.Fatal("order not persisted", backOrder, order, err)
	}
	for _, name := range []string{"../fixture.mkv", "segment-999999.ts", "work/1/segment-000007.ts"} {
		if _, err = s.HLSFileContext(ctx, grant, name); err == nil {
			t.Fatal("scope accepted", name)
		}
	}
	if err = s.Stop(p, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.HLSFileContext(ctx, grant, "segment-000007.ts"); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("stopped retained GET", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.active)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stopped actor retained")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFiniteRealCorruptCommittedResourceFailsClosed(t *testing.T) {
	ctx, db, s, _, _, session, grant := finiteFixture(t)
	path, err := s.HLSFileContext(ctx, grant, "segment-000000.ts")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.HLSFileContext(ctx, grant, "segment-000000.ts"); !errors.Is(err, vod.ErrChanged) {
		t.Fatal("corrupt resource accepted", err)
	}
	var attempts int
	if err = db.QueryRow(`SELECT attempts FROM playback_vod_intervals WHERE session_id=? AND ordinal=0`, session.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("corrupt resource regenerated", attempts, err)
	}
}

func TestFiniteRealCanceledWaitAndRevocation(t *testing.T) {
	ctx, db, s, h, _, session, grant := finiteFixture(t)
	h.finite.mu.Lock()
	a := h.finite.actors[session.ID]
	h.finite.mu.Unlock()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.HLSFileContext(canceled, grant, "segment-000004.ts"); err == nil {
		t.Fatal("canceled request accepted")
	}
	result := make(chan error, 1)
	go func() { _, err := s.HLSFileContext(ctx, grant, "segment-000006.ts"); result <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.finite.mu.Lock()
		n := a.waiters
		h.finite.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiter not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	mustAudioExec(t, db, `UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash='login')`)
	select {
	case err := <-result:
		if !errors.Is(err, identity.ErrUnauthorized) {
			t.Fatal("post-wait denial", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revoked wait stuck")
	}
	for _, name := range []string{"master.m3u8", "segment-000006.ts"} {
		if _, err := s.HLSFileContext(ctx, grant, name); !errors.Is(err, identity.ErrUnauthorized) {
			t.Fatal("revoked GET", name, err)
		}
	}
}

func TestFiniteRealRestartReplaysPersistedQueueInOrder(t *testing.T) {
	ctx, db, s, h, _, session, grant := finiteFixture(t)
	path, err := s.HLSFileContext(ctx, grant, "segment-000000.ts")
	if err != nil {
		t.Fatal(err)
	}
	original, err := finiteHash(path)
	if err != nil {
		t.Fatal(err)
	}
	h.finite.mu.Lock()
	h.finite.actors[session.ID].cancel()
	h.finite.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.active)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actor did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Explicit crash-state injection: a persisted in-flight interval and an older
	// queued demand survive without any worker. Reconnection uses real encoding.
	mustAudioExec(t, db, `UPDATE playback_vod_intervals SET status='working',generation=99,requested_order=20 WHERE session_id=? AND ordinal=5`, session.ID)
	mustAudioExec(t, db, `UPDATE playback_vod_intervals SET status='queued',requested_order=10 WHERE session_id=? AND ordinal=3`, session.ID)
	if err = h.EnableFinite(decodertest.QualifiedFFprobe(t)); err != nil {
		t.Fatal(err)
	}
	var state string
	var generation int
	if err = db.QueryRow(`SELECT status,generation FROM playback_vod_intervals WHERE session_id=? AND ordinal=5`, session.ID).Scan(&state, &generation); err != nil || state != "queued" || generation != 0 {
		t.Fatal("restart ownership not reset", state, generation, err)
	}
	if _, err = s.HLSFileContext(ctx, grant, "segment-000005.ts"); err != nil {
		t.Fatal(err)
	}
	var third, fifth int
	if err = db.QueryRow(`SELECT generation FROM playback_vod_intervals WHERE session_id=? AND ordinal=3 AND status='committed'`, session.ID).Scan(&third); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT generation FROM playback_vod_intervals WHERE session_id=? AND ordinal=5 AND status='committed'`, session.ID).Scan(&fifth); err != nil || third >= fifth {
		t.Fatal("persisted FIFO not replayed", third, fifth, err)
	}
	again, err := s.HLSFileContext(ctx, grant, "segment-000000.ts")
	if err != nil {
		t.Fatal(err)
	}
	retained, err := finiteHash(again)
	if err != nil || retained != original {
		t.Fatal("restart changed retained output", retained, original, err)
	}
}

func TestFiniteAdmissionBoundsWithoutWorker(t *testing.T) {
	ctx, db, s, h, _, session, grant := finiteFixture(t)
	h.finite.mu.Lock()
	prior := h.finite.actors[session.ID]
	prior.cancel()
	h.finite.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.mu.Lock()
		n := len(h.active)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actor did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Narrow admission-only stub: pause processing to make queue/waiter limits
	// deterministic. The real-encoder tests above cover production behavior.
	ready := make(chan struct{})
	close(ready)
	owner, err := s.RetainPreparation(ctx, session.ID, prior.generation)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	paused := &finiteActor{ctx: owner.Context(), cancel: owner.Close, ready: ready, wake: make(chan struct{}, 1), source: prior.source,
		generation: owner.run.generation, entry: owner.entry, run: owner.run, waiters: 32}
	h.finite.mu.Lock()
	h.finite.actors[session.ID] = paused
	h.finite.mu.Unlock()
	if _, err := s.HLSFileContext(ctx, grant, "segment-000008.ts"); !errors.Is(err, ErrConversionCapacity) {
		t.Fatal("waiter cap", err)
	}
	h.finite.mu.Lock()
	paused.waiters = 0
	h.finite.mu.Unlock()
	mustAudioExec(t, db, `UPDATE playback_vod_intervals SET status='queued',requested_order=ordinal+1 WHERE session_id=? AND ordinal<8`, session.ID)
	if _, err := s.HLSFileContext(ctx, grant, "segment-000008.ts"); !errors.Is(err, ErrConversionCapacity) {
		t.Fatal("queue cap", err)
	}
	var state string
	if err := db.QueryRow(`SELECT status FROM playback_vod_intervals WHERE session_id=? AND ordinal=8`, session.ID).Scan(&state); err != nil || state != "absent" {
		t.Fatal("rejected demand persisted", state, err)
	}
	h.finite.mu.Lock()
	delete(h.finite.actors, session.ID)
	h.finite.mu.Unlock()
	h.mu.Lock()
	h.active["admission-stub-one"] = func() {}
	h.active["admission-stub-two"] = func() {}
	h.mu.Unlock()
	_, admissionErr := s.HLSFileContext(ctx, grant, "master.m3u8")
	h.mu.Lock()
	delete(h.active, "admission-stub-one")
	delete(h.active, "admission-stub-two")
	h.mu.Unlock()
	if errors.Is(admissionErr, ErrConversionCapacity) {
		t.Fatal("unrelated producers imposed a hidden actor cap", admissionErr)
	}
}

func TestFiniteManifestMergedTail(t *testing.T) {
	manifest := string(finiteManifest(&vod.Source{Duration: 96.023}))
	if strings.Count(manifest, "#EXTINF:") != 16 || !strings.Contains(manifest, "#EXT-X-TARGETDURATION:7\n") || !strings.Contains(manifest, "#EXTINF:6.023000000,\nsegment-000015.ts") || strings.Contains(manifest, "segment-000016.ts") {
		t.Fatalf("finite index must preserve terminal extent in preceding interval: %s", manifest)
	}
}

package playback

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback/vod"
)

// These invoke prepare's actual post-Analyze acceptance port. The writer is
// deliberately occupied after publication admission, not before producer entry.
func TestFinitePostAnalyzeWriterCannotPublishAfterStopOrSuccessor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mutation := range []string{"state='stopped'", "generation=generation+1", "request_id='successor-request'"} {
		t.Run(mutation, func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			insertFixtureAccount(t, db, "owner", "profile")
			insertFixtureSession(t, db, "session", "owner", "profile", "owner", "2999-01-01T00:00:00Z")
			service := New(db)
			service.ConfigureHLS(h)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			owner, err := service.RetainPreparation(ctx, id, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			actor := &finiteActor{generation: 3, entry: owner.entry, run: owner.run}
			f := &FiniteHLS{h: h}
			dir := filepath.Join(h.root, id)
			if err = os.MkdirAll(filepath.Join(dir, "work"), 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, "work", "successor-owned")
			if err = os.WriteFile(marker, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			held, err := dbwork.Begin(ctx, db, dbwork.ClassSecurityFence)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Rollback()
			result := make(chan error, 1)
			go func() {
				result <- f.publishPreparation(owner.Context(), id, actor, &vod.Source{Size: 1, ModifiedNS: 1, Duration: 6, FPS: 24})
			}()
			for {
				h.occurrences.mu.Lock()
				admitted := owner.entry.publications > 0
				h.occurrences.mu.Unlock()
				if admitted && dbwork.WriteGate().Waiting()[dbwork.ClassPlaybackStart.String()] > 0 {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("post-Analyze returned before accepting writer: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			if _, err = held.Tx().ExecContext(held.Context(), `UPDATE playback_sessions SET `+mutation+` WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			if err = held.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-result:
				if err == nil {
					t.Fatal("old post-Analyze published")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			var published int
			if err = db.QueryRow(`SELECT (SELECT count(*) FROM playback_vod_plans WHERE session_id=?)+(SELECT count(*) FROM playback_vod_intervals WHERE session_id=?)+(SELECT count(*) FROM playback_artifacts WHERE session_id=?)`, id, id, id).Scan(&published); err != nil {
				t.Fatal(err)
			}
			if published != 0 || actor.source != nil {
				t.Fatal("old plan/artifact/source published")
			}
			if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
				t.Fatal("old preparation removed successor work", err)
			}
			if _, err = os.Stat(filepath.Join(dir, "master.m3u8")); !os.IsNotExist(err) {
				t.Fatal("old manifest published", err)
			}
		})
	}
}

func TestRenditionRelocationAwaitKeepsOriginalGenerationAndPlan(t *testing.T) {
	for _, change := range []string{"generation", "plan"} {
		t.Run(change, func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			h.ctx = context.Background()
			var asset string
			if err := db.QueryRow(`SELECT asset_id FROM playback_sessions WHERE id=?`, id).Scan(&asset); err != nil {
				t.Fatal(err)
			}
			r := AudioRenditionPlan{Ordinal: 0, StreamIndex: 1, Action: "copy", Codec: "aac", Channels: 2, Packaging: "mpegts_hls"}
			plan := DeliveryPlan{Profile: DeliveryProfileLegacy, SourceID: asset, SourceSize: 1, SourceModifiedNS: 1, Mode: "hls", Strategy: DeliveryCopyRemux, OutputContainer: "mpegts_hls", VideoAction: "copy", AudioAction: "copy", Duration: 100, Renditions: []AudioRenditionPlan{r}}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err = persistDeliveryPlan(tx, id, &plan); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			reached := make(chan struct{})
			done := make(chan struct{})
			key := renditionKey(id, 0)
			old := &hlsWindow{generation: 3, start: 0, done: done, cancel: func() { close(reached) }}
			h.windows[key] = old
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- h.startRendition(ctx, id, r, 30) }()
			select {
			case <-reached:
			case err := <-result:
				t.Fatalf("not at relocation wait: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			query := `UPDATE playback_sessions SET generation=generation+1 WHERE id=?`
			if change == "plan" {
				query = `UPDATE playback_delivery_plans SET reason='successor-plan' WHERE session_id=?`
			}
			if _, err = db.Exec(query, id); err != nil {
				t.Fatal(err)
			}
			successor := &hlsWindow{generation: 4, start: 30, done: make(chan struct{})}
			var cancelled atomic.Int32
			nextRun := &occurrenceRun{generation: 4, requestID: "successor", assetID: asset, cancel: func() { cancelled.Add(1) }}
			h.mu.Lock()
			h.windows[key] = successor
			h.installOccurrenceLocked(key, nextRun, nextRun.cancel)
			h.mu.Unlock()
			close(done)
			select {
			case err = <-result:
				if err == nil {
					t.Fatal("relocation adopted successor generation/plan")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			h.mu.Lock()
			preserved := h.windows[key] == successor && h.activeRuns[key] == nextRun
			h.mu.Unlock()
			if !preserved || cancelled.Load() != 0 {
				t.Fatal("old relocation changed successor")
			}
		})
	}
}

func TestLegacyPrefetchForgetCannotRetireCurrentOwnedRecord(t *testing.T) {
	s := New(nil)
	next := &prefetchRecord{service: s, id: "same", generation: 2, used: 37, readers: 1}
	s.prefetchOwned = map[string]*prefetchRecord{"same": next}
	s.ForgetPrefetch("same") // actual id-only bridge called by the old stop path
	if s.prefetchOwned["same"] != next || next.retired || next.used != 37 || next.readers != 1 {
		t.Fatal("id-only forget retired successor owned budget")
	}
}

func TestOldPrimaryCancellationCannotSelectSuccessorRenditions(t *testing.T) {
	h := &HLS{active: map[string]context.CancelFunc{}}
	old := &occurrenceRun{generation: 3, requestID: "old", itemID: 1, assetID: "asset"}
	next := &occurrenceRun{generation: 4, requestID: "next", itemID: 1, assetID: "asset"}
	child := &occurrenceRun{generation: 4, requestID: "next", itemID: 1, assetID: "asset"}
	var stopped atomic.Int32
	h.installOccurrenceLocked("same", next, func() {})
	h.installOccurrenceLocked(renditionKey("same", 0), child, func() { stopped.Add(1) })
	h.cancelOccurrenceRenditionsLocked("same", old)
	if stopped.Load() != 0 {
		t.Fatal("old primary cancelled successor child")
	}
	h.cancelOccurrenceRenditionsLocked("same", next)
	if stopped.Load() != 1 {
		t.Fatal("exact current primary did not cancel its own child")
	}
	// Even a currently installed primary cannot select a child of another tuple.
	foreign := &occurrenceRun{generation: 4, requestID: "different", itemID: 1, assetID: "asset"}
	h.installOccurrenceLocked(renditionKey("same", 1), foreign, func() { t.Error("tuple mismatch child cancelled") })
	h.cancelOccurrenceRenditionsLocked("same", next)
}

func TestActualOldWindowThrottleCannotCancelSuccessorChild(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	service := New(db)
	service.ConfigureHLS(h)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner, err := service.RetainPreparation(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	dir := filepath.Join(h.root, id)
	for i := 0; i < 12; i++ {
		if err = os.WriteFile(filepath.Join(dir, hlsSegmentFile(i)), []byte("owned segment"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var childStopped, primaryStopped atomic.Int32
	old := &hlsWindow{generation: 3, run: owner.run, start: 0, served: -1, produced: -1}
	h.windows[id] = old
	next := &occurrenceRun{generation: 4, requestID: "successor", itemID: owner.run.itemID, assetID: owner.run.assetID}
	h.installOccurrenceLocked(id, next, func() {})
	h.installOccurrenceLocked(renditionKey(id, 0), next, func() { childStopped.Add(1) })
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.watchWindow(owner.Context(), id, dir, "", 0, -1, func() { primaryStopped.Add(1) }, nil, nil, nil, old)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("old throttle did not reach its actual cancellation boundary")
	}
	if primaryStopped.Load() != 1 || !old.throttled {
		t.Fatal("fixture missed actual old-window throttle")
	}
	if childStopped.Load() != 0 || h.activeRuns[renditionKey(id, 0)] != next {
		t.Fatal("old throttle cancelled successor child")
	}
}

func TestFinitePrivateCleanupDoesNotHoldUnrelatedWriter(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	insertFixtureAccount(t, db, "owner", "profile")
	insertFixtureSession(t, db, "session", "owner", "profile", "owner", "2999-01-01T00:00:00Z")
	service := New(db)
	service.ConfigureHLS(h)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner, err := service.RetainPreparation(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	blocked, release := make(chan string, 1), make(chan struct{})
	f := &FiniteHLS{h: h, cleanupWork: func(path string) error { blocked <- path; <-release; return os.RemoveAll(path) }}
	actor := &finiteActor{generation: 3, entry: owner.entry, run: owner.run}
	result := make(chan error, 1)
	// Different size rejects at the actual same-writer source fence, after its
	// private staging has been allocated but before any public artifact changes.
	go func() {
		result <- f.publishPreparation(owner.Context(), id, actor, &vod.Source{Size: 2, ModifiedNS: 1, Duration: 6, FPS: 24})
	}()
	var path string
	select {
	case path = <-blocked:
	case <-ctx.Done():
		close(release)
		t.Fatal(ctx.Err())
	}
	if filepath.Dir(path) != filepath.Join(h.root, id) || !strings.HasPrefix(filepath.Base(path), ".finite-") {
		close(release)
		t.Fatal("cleanup selected shared/foreign work", path)
	}
	writer := make(chan error, 1)
	go func() {
		_, err := dbwork.ExecWrite(ctx, db, dbwork.ClassSecurityFence, `UPDATE accounts SET username=username WHERE id='owner'`)
		writer <- err
	}()
	select {
	case err = <-writer:
		if err != nil {
			close(release)
			t.Fatal(err)
		}
	case <-ctx.Done():
		close(release)
		t.Fatal("blocked private cleanup retained global writer")
	}
	close(release)
	if err = <-result; err == nil {
		t.Fatal("changed source accepted")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("owned rejected staging not removed", err)
	}
}

func TestHeldAcceptingDriftPreservesActualOwnedAudioBudgetAndReaders(t *testing.T) {
	for _, mutation := range []string{"generation=generation+1", "grant_hash='successor-grant'"} {
		t.Run(mutation, func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			insertFixtureAccount(t, db, "owner", "profile")
			insertFixtureSession(t, db, "session", "owner", "profile", "owner", "2999-01-01T00:00:00Z")
			if _, err := db.Exec(`UPDATE playback_sessions SET grant_hash=? WHERE id=?`, identity.Digest("token"), id); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO playback_private_presentations(session_id,owner_session_id,expires_ms) VALUES(?,'original-parent',?)`, id, time.Now().Add(time.Minute).UnixMilli()); err != nil {
				t.Fatal(err)
			}
			service := New(db)
			service.ConfigureHLS(h)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			first, err := service.RetainAudioOccurrence(ctx, "token", id, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := service.RetainAudioOccurrence(ctx, "token", id, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			if !first.Charge(37, 100) {
				t.Fatal("actual owned charge refused")
			}
			record := first.record
			if record != second.record || service.prefetchOwned[id] != record || record.used != 37 || record.readers != 2 {
				t.Fatal("fixture does not share original owned token")
			}
			claim, err := service.ClaimStopOccurrence(ctx, id, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer claim.Abort()
			writer, err := dbwork.Begin(ctx, db, dbwork.ClassSecurityFence)
			if err != nil {
				t.Fatal(err)
			}
			err = claim.FenceTx(writer.Context(), writer.Tx(), func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE playback_sessions SET `+mutation+` WHERE id=?`, id)
				return err
			})
			if !errors.Is(err, ErrOccurrenceChanged) {
				writer.Rollback()
				t.Fatalf("accepting drift: %v", err)
			}
			writer.Rollback()
			claim.Abort()
			var generation int
			var grant string
			if err = db.QueryRow(`SELECT generation,grant_hash FROM playback_sessions WHERE id=?`, id).Scan(&generation, &grant); err != nil {
				t.Fatal(err)
			}
			if generation != 3 || grant != identity.Digest("token") {
				t.Fatal("refusal changed durable successor graph")
			}
			if service.prefetchOwned[id] != record || record.used != 37 || record.readers != 2 || record.retired || first.Context().Err() != nil || second.Context().Err() != nil {
				t.Fatal("refusal changed captured owned budget/readers")
			}
			h.occurrences.mu.Lock()
			_, budget := first.entry.budgets[record]
			_, one := first.entry.runs[first.run]
			_, two := first.entry.runs[second.run]
			h.occurrences.mu.Unlock()
			if !budget || !one || !two {
				t.Fatal("refusal replaced original registry tokens")
			}
			if first.Take(1, 100) != 1 || record.used != 38 {
				t.Fatal("Abort did not preserve exact original owned admission")
			}
		})
	}
}

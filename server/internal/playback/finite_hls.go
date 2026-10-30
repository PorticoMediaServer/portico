package playback

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback/vod"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrSegmentPreparing = errors.New("Media is still being prepared. Retry playback.")
var ErrFiniteUnsupported = errors.New("This source needs a playback path that is not available in the selected conversion policy.")

// FiniteHLS is the conversion-policy adapter. The session remains the authority;
// an internal generation owns only unpublished work. One actor per admitted HLS
// session serializes its bounded FIFO and shares the existing global admission.
// It never turns a late segment GET into a replacement playback intent.
type FiniteHLS struct {
	h           *HLS
	encoder     *vod.Encoder
	mu          sync.Mutex
	actors      map[string]*finiteActor
	cleanupWork func(string) error // exact private directory cleanup, always outside the writer
}
type finiteActor struct {
	ctx        context.Context
	cancel     context.CancelFunc
	ready      chan struct{}
	wake       chan struct{}
	err        error // written before ready closes
	source     *vod.Source
	workDir    string // unique to this installed actor; never selected by session id
	waiters    int    // under FiniteHLS.mu
	generation int
	done       chan struct{}
	entry      *occurrenceEntry
	run        *occurrenceRun
}

func (h *HLS) EnableFinite(ffprobe string) error {
	e, err := vod.NewEncoder(h.binary, ffprobe, vod.Limits{})
	if err != nil {
		return err
	}
	h.finite = &FiniteHLS{h: h, encoder: e, actors: map[string]*finiteActor{}}
	// A process restart cannot adopt a worker generation that no longer exists.
	_, err = dbwork.ExecWrite(context.Background(), h.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_vod_intervals SET status='queued',generation=0 WHERE status='working'`)
	return err
}
func (f *FiniteHLS) input(ctx context.Context, id string) (vod.Input, error) {
	release, err := childprocess.AcquirePublication(ctx)
	if err != nil {
		return vod.Input{}, err
	}
	defer release()
	pin, err := assets.ResolvePlaybackSource(ctx, f.h.db, id, "")
	if err != nil {
		return vod.Input{}, err
	}
	source, err := assets.OpenPlaybackInput(ctx, f.h.db, f.h.SourceStorage, id)
	if err != nil {
		return vod.Input{}, err
	}
	check, err := childprocess.AcquirePublication(ctx)
	if err != nil {
		source.Close()
		return vod.Input{}, err
	}
	check()
	return vod.Input{Release: source.Close, File: source.File, Argument: source.Argument, Inherit: source.Inherit,
		Identity: vod.Identity{Size: pin.Size, ModifiedNS: pin.ModifiedNS},
		Validate: func(ctx context.Context, file *os.File, p vod.Identity) error {
			owned, err := childprocess.OwnedIOContext(ctx)
			if err != nil {
				return err
			}
			return f.h.SourceStorage.ValidatePlaybackDescriptor(owned, file, p.Size, p.ModifiedNS)
		}}, nil
}
func (f *FiniteHLS) live(id string) bool {
	return occurrenceLive(f.h.db, id, 0)
}
func (f *FiniteHLS) liveGeneration(id string, generation int) bool {
	return occurrenceLive(f.h.db, id, generation)
}

// occurrenceLive reports whether an admitted occurrence may keep producing.
// Authentication is checked through the session family's current token, not
// the token that started playback: a routine refresh mid-film must not cancel
// the transcode (R-03). generation 0 accepts any generation.
func occurrenceLive(db *sql.DB, id string, generation int) bool {
	var live bool
	now := time.Now().UTC().Format(time.RFC3339)
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM playback_sessions p JOIN authorization_access s ON s.hash=`+identity.CurrentFamilyTokenSQL("p.session_hash")+` LEFT JOIN accounts a ON a.id=s.account_id WHERE p.id=? AND (?=0 OR p.generation=?) AND p.state NOT IN ('stopped','ended','failed') AND p.expires_at>? AND s.expires_at>? AND s.revoked=0 AND (s.authority!='local' OR s.epoch=a.epoch))`, id, generation, generation, now, now).Scan(&live)
	return err == nil && live
}
func finiteManifest(s *vod.Source) []byte {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:7\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n")
	for i := 0; i < vod.IntervalCount(s.Duration); i++ {
		// Video and AAC packet clocks share one source-relative timeline.
		start, end, _ := vod.IntervalBounds(s.Duration, i)
		fmt.Fprintf(&b, "#EXTINF:%.9f,\nsegment-%06d.ts\n", end-start, i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}
func (f *FiniteHLS) ensure(ctx context.Context, id string) (*finiteActor, error) {
	if dbwork.Snapshot(ctx) != nil {
		return nil, errOccurrenceClaimed
	}
	intent, err := f.h.occurrences.intent(id)
	if err != nil {
		return nil, err
	}
	defer intent.release()
	var generation int
	var requestID, assetID string
	var itemID int64
	if err = dbwork.QueryRow(ctx, f.h.db, `SELECT generation,request_id,item_id,asset_id FROM playback_sessions WHERE id=? AND state NOT IN ('stopped','ended','failed') AND expires_at>?`, id, time.Now().UTC().Format(time.RFC3339)).Scan(&generation, &requestID, &itemID, &assetID); errors.Is(err, sql.ErrNoRows) {
		// A stopped, ended, failed or expired occurrence is a grant that no
		// longer authorizes anything, not a missing row.
		return nil, identity.ErrUnauthorized
	} else if err != nil {
		return nil, err
	}
	if !f.liveGeneration(id, generation) || !intent.current() {
		return nil, identity.ErrUnauthorized
	}
	f.mu.Lock()
	a := f.actors[id]
	if a != nil && (a.generation != generation || a.run == nil || a.run.requestID != requestID || a.run.itemID != itemID || a.run.assetID != assetID) {
		f.mu.Unlock()
		return nil, identity.ErrUnauthorized
	}
	if a == nil {
		f.h.mu.Lock()
		if f.h.cleaning[id] {
			f.h.mu.Unlock()
			f.mu.Unlock()
			return nil, identity.ErrUnauthorized
		}
		worker, cancel := context.WithCancel(f.h.ctx)
		a = &finiteActor{ctx: worker, cancel: cancel, ready: make(chan struct{}), wake: make(chan struct{}, 1), done: make(chan struct{}), generation: generation, entry: intent.entry}
		a.run = &occurrenceRun{generation: generation, requestID: requestID, itemID: itemID, assetID: assetID, cancel: cancel, done: a.done}
		a.ctx = f.h.occurrenceContext(a.ctx, id, a.entry, a.run)
		err = intent.install(a.run, func() { f.actors[id] = a; f.h.installOccurrenceLocked(id, a.run, cancel) })
		if err != nil {
			cancel()
			f.h.mu.Unlock()
			f.mu.Unlock()
			return nil, err
		}
		f.h.mu.Unlock()
		supervise.Go("playback.finite-hls.run", func() { f.run(id, a) })
	}
	f.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.ready:
		if a.err != nil {
			return a, a.err
		}
		if a.ctx.Err() != nil {
			return nil, ErrSegmentPreparing
		}
		return a, nil
	}
}

func (f *FiniteHLS) prepare(id string, a *finiteActor) error {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	input, err := f.input(ctx, id)
	if err != nil {
		return err
	}
	defer input.Close()
	source, err := f.encoder.Analyze(ctx, input)
	if err != nil {
		if errors.Is(err, vod.ErrUnsupported) {
			return ErrFiniteUnsupported
		}
		return err
	}
	return f.publishPreparation(ctx, id, a, source)
}

// publishPreparation is the accepting post-Analyze boundary. The original
// registration stays published until both its writer and manifest have settled.
func (f *FiniteHLS) publishPreparation(ctx context.Context, id string, a *finiteActor, source *vod.Source) error {
	owner, _ := ctx.Value(occurrenceOwnerKey{}).(*occurrenceOwner)
	if owner == nil || owner.id != id || owner.entry != a.entry || owner.run != a.run || a.run == nil || a.generation != a.run.generation {
		return errOccurrenceClaimed
	}
	release, err := childprocess.AcquirePublication(ctx)
	if err != nil {
		return err
	}
	defer release()
	manifest := finiteManifest(source)
	hash := fmt.Sprintf("%x", sha256.Sum256(manifest))
	dir, err := f.h.createOutput(id, false)
	if err != nil {
		return err
	}
	// All file preparation and recursive cleanup own a newly allocated private
	// tree, never shared work/<generation> or a successor chosen by id.
	workDir, err := os.MkdirTemp(dir, ".finite-")
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			release()
			_ = f.cleanupOwnedWork(workDir)
		}
	}()
	staged := filepath.Join(workDir, "master.m3u8")
	if err = os.WriteFile(staged, manifest, 0600); err != nil {
		return err
	}
	path := filepath.Join(dir, "master.m3u8")
	var existing os.FileInfo
	file, openErr := os.Open(path)
	if openErr == nil {
		existing, err = file.Stat()
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(file, int64(len(manifest))+1))
		}
		file.Close()
		if err != nil {
			return err
		}
		if string(data) != string(manifest) {
			return vod.ErrChanged
		}
	} else if !errors.Is(openErr, os.ErrNotExist) {
		return openErr
	}
	gated, err := dbwork.Begin(ctx, f.h.db, dbwork.ClassPlaybackStart)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	ctx = gated.Context()
	var live bool
	run := a.run
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions p JOIN authorization_access s ON s.hash=`+identity.CurrentFamilyTokenSQL("p.session_hash")+` LEFT JOIN accounts account ON account.id=s.account_id JOIN playback_source_pins pin ON pin.session_id=p.id WHERE p.id=? AND p.generation=? AND p.request_id=? AND p.item_id=? AND p.asset_id=? AND p.state NOT IN('stopped','ended','failed') AND p.expires_at>? AND s.expires_at>? AND s.revoked=0 AND (s.authority!='local' OR s.epoch=account.epoch) AND pin.asset_id=p.asset_id AND pin.size=? AND pin.modified_ns=?)`, id, run.generation, run.requestID, run.itemID, run.assetID, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339), source.Size, source.ModifiedNS).Scan(&live)
	if err != nil {
		return err
	}
	if !live || !owner.registry.ownsRun(id, owner.entry, run) {
		return errOccurrenceClaimed
	}
	var previous, previousPolicy string
	var size, modified int64
	var fps float64
	var vi, ai, width, height int
	err = tx.QueryRowContext(ctx, `SELECT manifest_hash,policy,source_size,source_modified_ns,fps,video_index,audio_index,width,height FROM playback_vod_plans WHERE session_id=?`, id).Scan(&previous, &previousPolicy, &size, &modified, &fps, &vi, &ai, &width, &height)
	if err == nil && (previousPolicy != vod.Policy || size != source.Size || modified != source.ModifiedNS || fps != source.FPS || vi != source.VideoIndex || ai != source.AudioIndex || width != source.Width || height != source.Height) {
		return vod.ErrChanged
	}
	if err == nil && previous != hash {
		return vod.ErrChanged
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO playback_vod_plans(session_id,policy,source_size,source_modified_ns,duration,fps,video_index,audio_index,width,height,interval_count,manifest_hash,status,analysis_wall,analysis_cpu,probe_output_bytes) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'ready',?,?,?) ON CONFLICT(session_id) DO NOTHING`, id, vod.Policy, source.Size, source.ModifiedNS, source.Duration, source.FPS, source.VideoIndex, source.AudioIndex, source.Width, source.Height, vod.IntervalCount(source.Duration), hash, source.AnalysisWork.WallSeconds, source.AnalysisWork.CPUSeconds, source.AnalysisWork.ProbeOutputBytes); err != nil {
		return err
	}
	for i := 0; i < vod.IntervalCount(source.Duration); i++ {
		start, end, _ := vod.IntervalBounds(source.Duration, i)
		if _, err = tx.Exec(`INSERT INTO playback_vod_intervals(session_id,ordinal,status,start,end) VALUES(?,?,'absent',?,?) ON CONFLICT(session_id,ordinal) DO NOTHING`, id, i, start, end); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO playback_artifacts(session_id,directory,status) VALUES(?,?,'building') ON CONFLICT(session_id) DO UPDATE SET directory=excluded.directory,status='building',error=''`, id, dir); err != nil {
		return err
	}
	// Only bounded metadata/no-clobber publication occurs inside this same
	// accepting writer. No recursive removal or manifest file IO holds it.
	if existing != nil {
		current, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !os.SameFile(existing, current) || existing.Size() != current.Size() || !existing.ModTime().Equal(current.ModTime()) {
			return ErrOccurrenceChanged
		}
	} else if err = os.Link(staged, path); err != nil {
		return err
	}
	if err = gated.Commit(); err != nil {
		return err
	}
	a.source, a.workDir = source, workDir
	installed = true
	return nil
}
func (f *FiniteHLS) cleanupOwnedWork(path string) error {
	if path == "" {
		return nil
	}
	if f.cleanupWork != nil {
		return f.cleanupWork(path)
	}
	return os.RemoveAll(path)
}

func (f *FiniteHLS) run(id string, a *finiteActor) {
	// Keep authority monitoring independent of a potentially stalled subprocess.
	// Cancellation does not release the admission until the process actually exits.
	supervise.Go("playback.finite-hls.watch", func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-tick.C:
				if !f.liveGeneration(id, a.generation) {
					a.cancel()
					return
				}
			}
		}
	})
	defer func() {
		a.cancel()
		f.mu.Lock()
		f.h.mu.Lock()
		if f.actors[id] == a {
			delete(f.actors, id)
			f.h.removeOccurrenceLocked(id, a.run)
		}
		f.h.mu.Unlock()
		f.mu.Unlock()
		_ = f.cleanupOwnedWork(a.workDir)
		close(a.done)
		f.h.occurrences.finished(id, a.entry, a.run)
	}()
	// The producer's failures are fenced to the generation it started for.
	fail := withProducerGeneration(a.ctx, a.generation)
	for {
		a.err = f.prepare(id, a)
		if !errors.Is(a.err, errOccurrenceClaimed) || a.ctx.Err() != nil {
			break
		}
		// No initial IO was admitted. Hold the installed owner through Abort or
		// its committed cancellation rather than fabricating a source failure.
		select {
		case <-a.ctx.Done():
		case <-time.After(25 * time.Millisecond):
		}
	}
	close(a.ready)
	if a.err != nil {
		f.h.failed(fail, id)
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if !f.liveGeneration(id, a.generation) {
			return
		}
		var index int
		err := f.h.db.QueryRow(`SELECT ordinal FROM playback_vod_intervals WHERE session_id=? AND status='queued' ORDER BY requested_order,ordinal LIMIT 1`, id).Scan(&index)
		if err == nil {
			if err = f.produce(id, a, index); err != nil {
				if errors.Is(err, errOccurrenceClaimed) && a.ctx.Err() == nil {
					select {
					case <-a.ctx.Done():
					case <-tick.C:
					}
					continue
				}
				if a.ctx.Err() != nil {
					return
				}
				var attempts int
				readErr := f.h.db.QueryRow(`SELECT attempts FROM playback_vod_intervals WHERE session_id=? AND ordinal=?`, id, index).Scan(&attempts)
				if readErr == nil && attempts < 3 && !errors.Is(err, vod.ErrUnsupported) && !errors.Is(err, vod.ErrChanged) && !errors.Is(err, vod.ErrBudget) && !errors.Is(err, identity.ErrUnauthorized) {
					_, updateErr := dbwork.ExecWrite(context.Background(), f.h.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_vod_intervals SET status='absent',error_code='production_retryable' WHERE session_id=? AND ordinal=? AND status='working' AND EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND request_id=? AND item_id=? AND asset_id=? AND state NOT IN ('stopped','ended','failed'))`, id, index, id, a.generation, a.run.requestID, a.run.itemID, a.run.assetID)
					if updateErr == nil {
						continue
					}
				}
				_, _ = dbwork.ExecWrite(context.Background(), f.h.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_vod_intervals SET status='failed',error_code='production_failed' WHERE session_id=? AND ordinal=? AND status!='committed' AND EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND request_id=? AND item_id=? AND asset_id=? AND state NOT IN ('stopped','ended','failed'))`, id, index, id, a.generation, a.run.requestID, a.run.itemID, a.run.assetID)
				f.h.failed(fail, id)
				return
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			f.h.failed(fail, id)
			return
		}
		select {
		case <-a.ctx.Done():
			return
		case <-a.wake:
		case <-tick.C:
		}
	}
}
func (f *FiniteHLS) produce(id string, a *finiteActor, index int) (resultErr error) {
	if a.workDir == "" {
		return errOccurrenceClaimed
	}
	var workGeneration int64
	defer func() {
		if !errors.Is(resultErr, errOccurrenceClaimed) || workGeneration == 0 {
			return
		}
		cleanup, cancel := persist(a.ctx)
		defer cancel()
		// Only this not-started work token is returned to its original queue. A
		// committed stop or successor generation cannot be reopened by this defer.
		_, _ = f.h.producerWrite(cleanup, id, dbwork.ClassEstablishedPlayback, `UPDATE playback_vod_intervals SET status='queued',generation=0,attempts=max(0,attempts-1) WHERE session_id=? AND ordinal=? AND status='working' AND generation=?`, id, index, workGeneration)
	}()

	// Retain this run's publication ticket across the writer. A stop claim
	// closes admission and waits outside the registry mutex for this writer.
	releasePublication, err := f.h.occurrences.publication(id, a.entry, a.run)
	if err != nil {
		return err
	}
	defer releasePublication()
	gated2, err := dbwork.Begin(a.ctx, f.h.db, dbwork.ClassPlaybackStart)
	if err != nil {
		return err
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	var current bool
	if err = tx.QueryRowContext(a.ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND request_id=? AND item_id=? AND asset_id=? AND state NOT IN ('stopped','ended','failed'))`, id, a.generation, a.run.requestID, a.run.itemID, a.run.assetID).Scan(&current); err != nil {
		return err
	}
	if !current {
		return identity.ErrUnauthorized
	}
	var generation int64
	if err = tx.QueryRow(`UPDATE playback_vod_plans SET generation=generation+1 WHERE session_id=? RETURNING generation`, id).Scan(&generation); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE playback_vod_intervals SET status='working',generation=?,attempts=attempts+1 WHERE session_id=? AND ordinal=? AND status='queued'`, generation, id, index)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("work ownership changed")
	}
	var retained int64
	if err = tx.QueryRow(`SELECT COALESCE(sum(bytes),0) FROM playback_vod_intervals WHERE session_id=?`, id).Scan(&retained); err != nil {
		return err
	}
	if retained+(32<<20) > audioArtifactBytes {
		return vod.ErrBudget
	}
	if err = gated2.Commit(); err != nil {
		return err
	}
	workGeneration = generation
	releasePublication()
	input, err := f.input(a.ctx, id)
	if err != nil {
		return err
	}
	defer input.Close()
	dir := filepath.Join(a.workDir, strconv.FormatInt(generation, 10))
	observe, finish := f.h.observeConversion(a.ctx, id, a.generation)
	defer finish()
	window, err := f.encoder.Encode(a.ctx, a.source, input, vod.Request{Index: index, GenerationDir: dir, ObserveConversion: observe})
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if !f.liveGeneration(id, a.generation) {
		return identity.ErrUnauthorized
	}
	if err := childprocess.CheckOwnedIO(a.ctx); err != nil {
		return err
	}
	digest, err := finiteHash(window.Path)
	if err != nil {
		return err
	}
	path := filepath.Join(f.h.outputDir(id), fmt.Sprintf("segment-%06d.ts", index))
	// Link is no-clobber, unlike Rename. A crashed pre-commit publication may be
	// adopted only if its bytes match this deterministic generation's output.
	if err = os.Link(window.Path, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		old, e := finiteHash(path)
		if e != nil {
			return e
		}
		if old != digest {
			return vod.ErrChanged
		}
	}
	result, err = dbwork.ExecWrite(context.Background(), f.h.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_vod_intervals SET status='committed',bytes=?,sha256=?,decode_from=?,encode_wall=?,encode_cpu=?,probe_wall=? WHERE session_id=? AND ordinal=? AND status='working' AND generation=? AND EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND request_id=? AND item_id=? AND asset_id=? AND state NOT IN ('stopped','ended','failed'))`, window.Bytes, digest, window.DecodeFrom, window.EncodeWork.WallSeconds, window.EncodeWork.CPUSeconds, window.ProbeWork.WallSeconds, id, index, generation, id, a.generation, a.run.requestID, a.run.itemID, a.run.assetID)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return identity.ErrUnauthorized
	}
	return nil
}
func (f *FiniteHLS) file(ctx context.Context, id, name string) (string, error) {
	a, err := f.ensure(ctx, id)
	if err != nil {
		return "", err
	}
	path := filepath.Join(f.h.outputDir(id), name)
	if name == "master.m3u8" {
		return path, nil
	}
	if !artifactName.MatchString(name) {
		return "", errors.New("unknown finite resource")
	}
	var index int
	if _, err = fmt.Sscanf(name, "segment-%06d.ts", &index); err != nil {
		return "", err
	}
	if index < 0 || index >= vod.IntervalCount(a.source.Duration) {
		return "", errors.New("unknown finite interval")
	}
	f.mu.Lock()
	if a.waiters >= 32 {
		f.mu.Unlock()
		return "", ErrConversionCapacity
	}
	a.waiters++
	f.mu.Unlock()
	defer func() { f.mu.Lock(); a.waiters--; f.mu.Unlock() }()
	releasePublication, err := f.h.occurrences.publication(id, a.entry, a.run)
	if err != nil {
		return "", err
	}
	defer releasePublication()
	gated3, err := dbwork.Begin(ctx, f.h.db, dbwork.ClassPlaybackStart)
	if err != nil {
		return "", err
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	var state string
	if err = tx.QueryRow(`SELECT status FROM playback_vod_intervals WHERE session_id=? AND ordinal=?`, id, index).Scan(&state); err != nil {
		return "", err
	}
	if state == "absent" {
		var queued int
		if err = tx.QueryRow(`SELECT count(*) FROM playback_vod_intervals WHERE session_id=? AND status IN ('queued','working')`, id).Scan(&queued); err != nil {
			return "", err
		}
		if queued >= 8 {
			return "", ErrConversionCapacity
		}
		if _, err = tx.Exec(`UPDATE playback_vod_intervals SET status='queued',requested_order=? WHERE session_id=? AND ordinal=? AND status='absent'`, time.Now().UnixNano(), id, index); err != nil {
			return "", err
		}
	}
	if err = gated3.Commit(); err != nil {
		return "", err
	}
	releasePublication()
	select {
	case a.wake <- struct{}{}:
	default:
	}
	wait, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if !f.liveGeneration(id, a.generation) {
			return "", identity.ErrUnauthorized
		}
		var hash string
		if err = f.h.db.QueryRowContext(wait, `SELECT status,sha256 FROM playback_vod_intervals WHERE session_id=? AND ordinal=?`, id, index).Scan(&state, &hash); err != nil {
			return "", err
		}
		if state == "committed" {
			// Committed bytes never regenerate under an existing URI. Missing/corrupt
			// retained output fails this presentation rather than serving changed media.
			digest, e := finiteHash(path)
			if e != nil {
				f.h.failed(a.ctx, id)
				return "", e
			}
			if digest != hash {
				f.h.failed(a.ctx, id)
				return "", vod.ErrChanged
			}
			return path, nil
		}
		if state == "failed" {
			return "", ErrFiniteUnsupported
		}
		if state == "absent" {
			return "", ErrSegmentPreparing
		}
		select {
		case <-wait.Done():
			return "", ErrSegmentPreparing
		case <-tick.C:
		}
	}
}

func finiteHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err = io.CopyBuffer(digest, file, make([]byte, 32<<10)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

package preparedmedia

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"strconv"
	"strings"
	"sync"
	"time"

	"log"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/subtitlevideo"
	"sync/atomic"
)

// Start joins both domain loops on Close. The supervisor remains the owner of
// every physical child; a cancelled API request never abandons its decoder.
func (s *Service) Start(parent context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done != nil {
		return
	}
	ctx, cancel := context.WithCancel(dbwork.WithClass(parent, dbwork.ClassBackgroundMedia))
	s.cancel = cancel
	s.done = make(chan struct{})
	supervise.Go("prepared.worker", func() {
		var wg sync.WaitGroup
		wg.Add(2)
		supervise.Go("prepared.worker.run", func() { defer wg.Done(); s.run(ctx) })
		supervise.Go("prepared.worker.collect", func() {
			defer wg.Done()
			// Collection has something to reclaim only after a job or a lease
			// changed; five seconds of asking an idle database
			// bought nothing that a wake does not.
			collect := worker.NewSignal()
			unregister := watchPreparedCollection(collect)
			defer unregister()
			worker.Run(ctx, "prepared.worker.collect", collect, func(ctx context.Context) time.Duration {
				if !dbwork.Yield(ctx) {
					return 0
				}
				_ = s.collect(ctx)
				return 0
			})
		})
		wg.Wait()
		close(s.done)
	})
}
func (s *Service) Close() error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if s.inventory != nil {
		s.inventory.Close()
	}
	return s.artifacts.Close()
}
func (s *Service) run(ctx context.Context) {
	shared := worker.NewSignal()
	unregister := watchPreparedWork(shared)
	defer unregister()
	worker.RunWith(ctx, "prepared.worker.run", s.wake, shared, func(ctx context.Context) time.Duration {
		// A pass that found work says so by asking to run again immediately;
		// there is usually more behind it. A pass that found none waits for the
		// next enqueue rather than for a three-second tick.
		if s.workNext(ctx) {
			return time.Millisecond
		}
		return 0
	})
}

// spaceWarned keeps the "no room" message to one line per minute: the loop is
// woken by relevant input changes, and a busy server has plenty of those.
var spaceWarned atomic.Int64

// roomToEncode reports whether the artifact volume has room for another
// prepared encode. This is the same floor conversions use, for the same reason:
// the volume it writes to is usually the volume holding the database, and a
// producer that fills it turns "this download is not ready" into "every write
// fails".
func (s *Service) roomToEncode() bool {
	if s.artifacts == nil {
		return true
	}
	if diskspace.Room(s.artifacts.Root(), diskspace.ProducerFloor) {
		return true
	}
	now := time.Now().Unix()
	if last := spaceWarned.Load(); now-last >= 60 && spaceWarned.CompareAndSwap(last, now) {
		log.Printf("prepared media is waiting for disk space: %s has less than %d MiB free", s.artifacts.Root(), diskspace.ProducerFloor>>20)
	}
	return false
}

func (s *Service) workNext(ctx context.Context) bool {
	// Checked before the queue is read, so a full volume costs one syscall
	// rather than a claim, a probe and a failed encode. The job stays queued and
	// starts as soon as there is room: nothing is failed for a condition the
	// owner can fix by deleting something.
	if !s.roomToEncode() {
		return false
	}
	rows, e := s.db.QueryContext(ctx, `SELECT id FROM prepared_media_jobs WHERE state IN('queued','running','cancelling') ORDER BY created_ms,id LIMIT 64`)
	if e != nil {
		return false
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false
	}
	for _, id := range ids {
		// Constant custody generation across retries. An inherited old child keeps
		// the job quarantined even when a restarted parent has a new generation.
		lock, err := s.acquireCustody(id)
		if err != nil {
			continue
		}
		worked := s.execute(ctx, id, lock)
		lock.Close()
		if worked {
			return true
		}
	}
	return false
}

type work struct {
	Job
	Source    selection
	Principal identity.Principal
}

func readWork(ctx context.Context, tx *sql.Tx, id string) (work, error) {
	var w work
	var rawSource, rawPrincipal string
	j, e := readJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM `+jobSource+` WHERE j.id=?`, id))
	w.Job = j
	if e != nil {
		return w, e
	}
	e = tx.QueryRowContext(ctx, `SELECT selection_json,principal_json FROM prepared_media_jobs WHERE id=?`, id).Scan(&rawSource, &rawPrincipal)
	if e == nil {
		e = json.Unmarshal([]byte(rawSource), &w.Source)
	}
	if e == nil {
		e = json.Unmarshal([]byte(rawPrincipal), &w.Principal)
	}
	return w, e
}
func (s *Service) fence(ctx context.Context, w work) error {
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var state string
	var gen int64
	e = tx.QueryRowContext(ctx, `SELECT state,generation FROM prepared_media_jobs WHERE id=?`, w.ID).Scan(&state, &gen)
	if e != nil {
		return e
	}
	if gen != w.Generation || state != "running" {
		return ErrCancelled
	}
	if _, e = s.check(ctx, tx, w.Principal, w.ItemID, true); e != nil {
		return e
	}
	if e = conversionAllowed(ctx, tx); e != nil {
		return e
	}
	if e = validateSelection(ctx, tx, w.Source, true); e != nil {
		return e
	}
	return gated.Commit()
}
func (s *Service) execute(parent context.Context, id string, custody *attemptCustody) bool {
	gated2, e := dbwork.BeginSnapshot(parent, s.db)
	if e != nil {
		return false
	}
	tx := gated2.Tx()
	w, e := readWork(parent, tx, id)
	gated2.Rollback()
	if e != nil {
		return false
	}
	if w.State != "queued" && w.State != "running" && w.State != "cancelling" {
		return false
	}
	if w.Generation > 0 {
		if e = s.removeAttempt(id, w.Generation); e != nil {
			return false
		}
	}
	if w.State == "cancelling" {
		_, e = dbwork.ExecWrite(parent, s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_jobs SET state='cancelled',phase='cancelled',error_code='cancelled',cleaned_generation=generation,revision=revision+1,updated_ms=? WHERE id=? AND generation=? AND state='cancelling'`, time.Now().UnixMilli(), id, w.Generation)
		return e == nil
	}
	// Restart recovery starts a fresh private file, never appends a second MP4
	// producer to a crashed encoder's possibly incomplete fragmented stream.
	res, e := dbwork.ExecWrite(parent, s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_jobs SET state='running',phase='probing source',cleaned_generation=generation,generation=generation+1,bytes=0,error_code='',revision=revision+1,updated_ms=? WHERE id=? AND revision=? AND state IN('queued','running')`, time.Now().UnixMilli(), id, w.Revision)
	if e != nil {
		return false
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return false
	}
	w.Generation++
	ctx, cancel := context.WithCancelCause(parent)
	s.mu.Lock()
	s.running[id] = func() { cancel(ErrCancelled) }
	s.mu.Unlock()
	defer func() { cancel(context.Canceled); s.mu.Lock(); delete(s.running, id); s.mu.Unlock() }()
	watchDone := make(chan struct{})
	stopWatch := make(chan struct{})
	supervise.Go("prepared.worker.fence", func() {
		defer close(watchDone)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopWatch:
				return
			case <-tick.C:
				if e := s.fence(ctx, w); e != nil {
					cancel(e)
					return
				}
			}
		}
	})
	e = s.produce(ctx, w, custody)
	close(stopWatch)
	<-watchDone
	if e != nil {
		if cause := context.Cause(ctx); cause != nil {
			e = cause
		}
		state, phase, code := "failed", "failed", errorCode(e)
		if errors.Is(e, ErrCancelled) {
			state, phase, code = "cancelled", "cancelled", "cancelled"
		}
		if parent.Err() != nil {
			state, phase, code = "queued", "interrupted; waiting to retry", ""
		}
		// Cancellation wins over failure/shutdown, but can never undo a committed
		// successful publication. No input/process handles survive this point.
		_, _ = dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_jobs SET state=CASE WHEN state='cancelling' THEN 'cancelled' ELSE ? END,phase=CASE WHEN state='cancelling' THEN 'cancelled' ELSE ? END,error_code=CASE WHEN state='cancelling' THEN 'cancelled' ELSE ? END,revision=revision+1,updated_ms=? WHERE id=? AND generation=? AND state IN('running','cancelling')`, state, phase, code, time.Now().UnixMilli(), id, w.Generation)
	}
	if cleanup := s.removeAttempt(id, w.Generation); cleanup == nil {
		_, _ = dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_jobs SET cleaned_generation=? WHERE id=? AND generation=?`, w.Generation, id, w.Generation)
	}
	return true
}
func (s *Service) phase(ctx context.Context, w work, phase string) error {
	res, e := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_jobs SET phase=?,revision=revision+1,updated_ms=? WHERE id=? AND generation=? AND state='running'`, phase, time.Now().UnixMilli(), w.ID, w.Generation)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrCancelled
	}
	return nil
}

type progressWriter struct {
	s     *Service
	w     work
	out   *mediaartifact.Writer
	ctx   context.Context
	bytes int64
	last  time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	if e := p.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := p.out.Write(b)
	p.bytes += int64(n)
	if e != nil {
		return n, e
	}
	if time.Since(p.last) >= 2*time.Second {
		// Bytes are progress, not a resumable checkpoint or a proof of publication.
		res, err := dbwork.ExecWrite(p.ctx, p.s.db, dbwork.ClassBackgroundMedia, `UPDATE prepared_media_jobs SET bytes=?,updated_ms=? WHERE id=? AND generation=? AND state='running'`, p.bytes, time.Now().UnixMilli(), p.w.ID, p.w.Generation)
		if err != nil {
			return n, err
		}
		count, _ := res.RowsAffected()
		if count != 1 {
			return n, ErrCancelled
		}
		p.last = time.Now()
	}
	return n, nil
}
func binaryDigest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (s *Service) tools() (string, error) {
	paths := append([]string{s.ffmpeg, s.ffprobe}, s.libraries...)
	if s.sandbox != "" {
		paths = append(paths, s.sandbox)
	}
	digests := make([]string, 0, len(paths))
	for _, p := range paths {
		var d string
		var e error
		if s.fileSandbox() && (p == s.ffmpeg || p == s.ffprobe) {
			d, e = decoder.PreparedToolsDigest(p, s.libraries...)
		} else {
			d, e = binaryDigest(p)
		}
		if e != nil {
			return "", e
		}
		digests = append(digests, d)
	}
	return hash(digests), nil
}
func (s *Service) produce(ctx context.Context, w work, custody *attemptCustody) error {
	if ok, _ := s.configured(); !ok {
		return ErrConfiguration
	}
	if e := s.fence(ctx, w); e != nil {
		return e
	}
	p, e := profile(w.ProfileID)
	if e != nil {
		return e
	}
	tools, e := s.tools()
	if e != nil {
		return ErrConfiguration
	}
	input, e := s.input(ctx, w.ItemID, w.SourceID, w.ID)
	if e != nil {
		return e
	}
	defer input.Close()

	if s.fileSandbox() {
		capture, object, err := s.spool(ctx, w, input)
		if err != nil {
			return err
		}
		defer capture.Abort()
		return capture.WithClosedBytes(ctx, func(reader io.ReaderAt, size int64) error {
			source, err := s.outputInput(reader, size)
			if err != nil {
				return err
			}
			source.evidence = hash([]any{"complete-prepared-source-observation-v1", input.Evidence(), object.Digest, object.Size})
			return s.encodeAndPublish(ctx, w, custody, input, source, p, tools)
		})
	}
	return s.encodeAndPublish(ctx, w, custody, input, producerInput{bridge: subtitlevideo.NewMediaBridge(input)}, p, tools)
}
func (s *Service) encodeAndPublish(ctx context.Context, w work, custody *attemptCustody, input subtitles.RenderInput, source producerInput, p Profile, tools string) error {
	original, e := s.probe(ctx, w, custody, source)
	if e != nil {
		return e
	}
	if e = acceptSource(original, p, w.Source.Kind); e != nil {
		return e
	}
	if e = input.Validate(ctx); e != nil {
		return e
	}
	output, e := s.artifacts.BeginRetained(fmt.Sprintf("%s.%d", w.ID, w.Generation), s.maxBytes)
	if e != nil {
		return e
	}
	defer output.Abort()
	if e = s.phase(ctx, w, "encoding"); e != nil {
		return e
	}
	progress := &progressWriter{s: s, w: w, out: output, ctx: ctx}
	var retainedTags audiotags.Tags
	recipe := recipeFor(p)
	recipe.Source = original.recipeSource()
	if recipe.MusicQuality != "" {
		if w.Source.Cue {
			recipe.MusicStart = w.Source.Start
			recipe.MusicEnd = *w.Source.End
		}
		tags, err := s.musicTags(ctx, w, input, source)
		if err != nil {
			return err
		}
		original = musicSourceFacts(original, tags, input.Size())
		recipe.Source = original.recipeSource()
		retainedTags = tags
		e = s.encodeMusic(ctx, w, custody, source, recipe, progress, tags)
	} else {
		e = s.encode(ctx, w, custody, source, recipe, progress)
	}
	if e != nil {
		return e
	}
	if e = s.phase(ctx, w, "validating completed media"); e != nil {
		return e
	}
	var facts Facts
	e = output.WithClosedBytes(ctx, func(reader io.ReaderAt, size int64) error {
		closed, err := s.outputInput(reader, size)
		if err != nil {
			return err
		}
		observed, err := s.probe(ctx, w, custody, closed)
		if err != nil {
			return err
		}
		expected := original
		if w.Source.Cue {
			expected.Duration = *w.Source.End - w.Source.Start
		}
		if err = acceptOutput(expected, observed, p); err != nil {
			return err
		}
		if recipe.MusicQuality != "" {
			if err = verifyMusicMetadata(reader, size, retainedTags); err != nil {
				return err
			}
		}
		facts = observed.Facts
		facts.Container = "mp4"
		if r, ok := musicRecipe(p); ok {
			r.Source = original.recipeSource()
			o, err := decoder.PreparedMusicFormat(r)
			if err != nil {
				return err
			}
			facts.Container = o.Container
		}
		// Probe success alone is not validation: decode every retained output packet
		// with fatal decoder errors before a byte can be published to viewers.
		return s.validateOutput(ctx, w, custody, closed)
	})
	if e != nil {
		return errors.Join(ErrOutput, e)
	}
	if e = input.Validate(ctx); e != nil {
		return e
	}
	after, e := s.tools()
	if e != nil || after != tools {
		return ErrConfiguration
	}
	if e = s.fence(ctx, w); e != nil {
		return e
	}
	object, e := output.Checkpoint()
	if e != nil {
		return e
	}
	// Serialize the seal/DB-reference gap with reader-aware deletion even when a
	// second server process shares this state directory.
	var gate *os.File
	// One timer, reset per attempt. time.After allocates a timer that lives
	// until it fires whether or not anybody is still listening, which on a
	// contended object is a timer per second per waiter, held for a second each.
	retry := time.NewTimer(time.Second)
	if !retry.Stop() {
		<-retry.C
	}
	defer retry.Stop()
	for {
		gate, e = s.objectGate(object.Digest)
		if e == nil {
			break
		}
		if !errors.Is(e, livechannels.ErrPhysicalBusy) {
			return e
		}
		retry.Reset(time.Second)
		select {
		case <-ctx.Done():
			if !retry.Stop() {
				<-retry.C
			}
			return ctx.Err()
		case <-retry.C:
		}
	}
	defer gate.Close()
	object, e = output.Seal(ctx)
	if e != nil {
		return e
	}
	return s.publish(ctx, w, object, facts, source.Evidence(), hash(struct {
		Profile Profile
		Tools   string
	}{p, tools}))
}
func (s *Service) publish(ctx context.Context, w work, object mediaartifact.Object, facts Facts, evidence, transformation string) error {
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	var valid bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM prepared_media_jobs WHERE id=? AND state='running' AND generation=?)`, w.ID, w.Generation).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return ErrCancelled
	}
	if _, e = s.check(ctx, tx, w.Principal, w.ItemID, true); e != nil {
		return e
	}
	if e = conversionAllowed(ctx, tx); e != nil {
		return e
	}
	if e = validateSelection(ctx, tx, w.Source, true); e != nil {
		return e
	}
	id := hash([]any{w.ID, w.Generation, object.Digest})
	sel, _ := json.Marshal(w.Source)
	raw := facts.EncodeStored()
	now := time.Now().UnixMilli()
	entity, e := entityid.Resolve(ctx, tx, w.ItemID)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,edition_id,input_evidence,transformation_digest,digest,size,facts_json,state,created_ms,job_id) VALUES(?,?,?,?,?,?,?,?,?,NULL,?,?,?,?,?,'published',?,?)`, id, entity, w.SourceID, w.Source.Library, w.ProfileID, w.TargetID, hash(w.Source), string(sel), w.Source.Part, evidence, transformation, object.Digest, object.Size, string(raw), now, w.ID)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE prepared_media_jobs SET state='succeeded',phase='published',bytes=?,version_id=?,error_code='',revision=revision+1,updated_ms=? WHERE id=? AND generation=? AND state='running'`, object.Size, id, now, w.ID, w.Generation)
	if e != nil {
		return e
	}
	return gated3.Commit()
}

// closedInput borrows the exact closed staging descriptor. No filesystem path
// or source URL is exposed to the output validator.
type closedInput struct {
	reader io.ReaderAt
	size   int64
}

func (c *closedInput) Evidence() string                   { return "closed-prepared-output" }
func (c *closedInput) Size() int64                        { return c.size }
func (c *closedInput) Validate(ctx context.Context) error { return ctx.Err() }
func (c *closedInput) Close() error                       { return nil }
func (c *closedInput) ReadExtent(ctx context.Context, off, n int64) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if off < 0 || n <= 0 || n > 1<<20 || off > c.size-n {
		return nil, ErrInput
	}
	b := make([]byte, n)
	_, e := c.reader.ReadAt(b, off)
	return b, e
}

type probed struct {
	Facts
	VideoStreams, OtherStreams int
	HDR, AttachedVideo         bool
	PixFmt, VideoProfile       string
	VideoLevel                 int
	FrameRate                  float64
	AudioChannels              []int
	AudioBitrateBPS            int
	AudioLossless              *bool
	// The first picture's range as a recipe reads it (sdr, hdr10, hlg; a
	// Dolby Vision picture's base layer), its depth, and HDR10's metadata.
	// Range is "" for a Dolby Vision picture with no base layer to show.
	Range                    string
	BitDepth                 int
	DolbyVision              bool
	MasterDisplay, MaxCLL    string
	ColorTransfer, Primaries string
}

// recipeSource is what an encode is told about the source it reads.
func (f probed) recipeSource() decoder.PreparedSource {
	return decoder.PreparedSource{AudioLossless: f.AudioLossless, DynamicRange: f.Range, BitDepth: f.BitDepth, DolbyVision: f.DolbyVision, MasterDisplay: f.MasterDisplay, MaxCLL: f.MaxCLL, AudioChannels: append([]int(nil), f.AudioChannels...), AudioSampleRate: f.AudioSampleRate, AudioBitDepth: f.AudioBitDepth, AudioCodec: f.AudioCodec, AudioBitrateBPS: f.AudioBitrateBPS}
}

func parseProbe(raw []byte) (probed, error) {
	return parseProbeMode(raw, false)
}

func parseProbeMode(raw []byte, measuredAudio bool) (probed, error) {
	var doc struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		}
		Streams []struct {
			CodecType               string `json:"codec_type"`
			CodecName               string `json:"codec_name"`
			Width, Height, Channels int
			PixFmt                  string `json:"pix_fmt"`
			Profile                 string `json:"profile"`
			Level                   int    `json:"level"`
			AverageFrameRate        string `json:"avg_frame_rate"`
			ColorTransfer           string `json:"color_transfer"`
			ColorPrimaries          string `json:"color_primaries"`
			BitsRaw                 string `json:"bits_per_raw_sample"`
			BitsPerSample           int    `json:"bits_per_sample"`
			SampleRate              string `json:"sample_rate"`
			Bitrate                 string `json:"bit_rate"`
			Disposition             struct {
				AttachedPic int `json:"attached_pic"`
			}
			SideData []json.RawMessage `json:"side_data_list"`
		}
	}
	var p probed
	if len(raw) == 0 || len(raw) > decoder.MaxProbeOutputBytes || json.Unmarshal(raw, &doc) != nil || len(doc.Streams) > 64 {
		return p, ErrOutput
	}
	p.Duration, _ = strconv.ParseFloat(doc.Format.Duration, 64)
	if math.IsNaN(p.Duration) || math.IsInf(p.Duration, 0) || p.Duration < 0 || p.Duration == 0 && !measuredAudio {
		return p, ErrOutput
	}
	p.Container = doc.Format.FormatName
	for _, s := range doc.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition.AttachedPic != 0 {
				p.AttachedVideo = true
				continue
			}
			p.VideoStreams++
			if p.VideoStreams == 1 {
				p.VideoCodec = s.CodecName
				p.Width = s.Width
				p.Height = s.Height
				p.PixFmt = s.PixFmt
				p.VideoProfile, p.VideoLevel = s.Profile, s.Level
				p.FrameRate = probeRate(s.AverageFrameRate)
				p.ColorTransfer, p.Primaries = s.ColorTransfer, s.ColorPrimaries
				p.BitDepth = pixelDepth(s.PixFmt, s.BitsRaw)
				p.MasterDisplay, p.MaxCLL = assets.HDRStaticMetadata(s.SideData)
			}
			p.HDR = p.HDR || s.ColorTransfer == "smpte2084" || s.ColorTransfer == "arib-std-b67" || s.ColorPrimaries == "bt2020"
			for _, raw := range s.SideData {
				var d struct {
					Type          string `json:"side_data_type"`
					Profile       int    `json:"dv_profile"`
					Compatibility int    `json:"dv_bl_signal_compatibility_id"`
				}
				if json.Unmarshal(raw, &d) != nil {
					continue
				}
				if strings.Contains(strings.ToLower(d.Type), "dovi") {
					p.HDR = true
					if p.VideoStreams == 1 && d.Profile > 0 {
						p.DolbyVision = true
						p.Range = decoder.DolbyVisionBaseRange(d.Profile, d.Compatibility)
					}
				}
			}
			if p.VideoStreams == 1 && !p.DolbyVision {
				p.Range = assets.DynamicRangeOf(s.ColorTransfer, 0)
			}
		case "audio":
			switch s.CodecName {
			case "musepack7":
				s.CodecName = "mpc7"
			case "musepack8":
				s.CodecName = "mpc8"
			}
			p.AudioTracks++
			if p.AudioTracks == 1 {
				p.AudioSampleRate, _ = strconv.Atoi(s.SampleRate)
				p.AudioBitDepth, _ = strconv.Atoi(s.BitsRaw)
				if p.AudioBitDepth == 0 {
					p.AudioBitDepth = s.BitsPerSample
				}
				p.AudioBitrateBPS, _ = strconv.Atoi(s.Bitrate)
				p.AudioChannelCount = s.Channels
			}
			p.AudioChannels = append(p.AudioChannels, s.Channels)
			if p.AudioCodec == "" {
				p.AudioCodec = s.CodecName
			} else if p.AudioCodec != s.CodecName {
				p.AudioCodec = "mixed"
			}
		default:
			p.OtherStreams++
		}
	}
	measurable := p.Container == "flac" && p.AudioCodec == "flac" || p.Container == "mpc" && p.AudioCodec == "mpc7" || p.Container == "mpc8" && p.AudioCodec == "mpc8"
	if p.Duration == 0 && (!measurable || p.AudioTracks != 1 || p.VideoStreams != 0 || p.OtherStreams != 0) {
		return p, ErrOutput
	}
	return p, nil
}
func acceptSource(f probed, p Profile, kind string) error {
	audio := kind == "song" || kind == "audiobook_file"
	if (p.Kind == "audio") != audio || f.AudioTracks > 16 {
		return ErrUnsupported
	}
	if audio {
		if f.AudioTracks < 1 || f.VideoStreams != 0 {
			return ErrUnsupported
		}
	} else if f.VideoStreams != 1 || f.AttachedVideo || f.Width < 2 || f.Height < 2 {
		return ErrUnsupported
	} else if f.HDR {
		// An HDR picture is kept by an HEVC recipe and tone-mapped by an H.264
		// one. A picture neither can show correctly (Dolby Vision without a base
		// layer, wide-gamut pictures of no known range) is refused rather than
		// written in the wrong colours.
		switch {
		case f.Range == "" || f.Range == assets.RangeSDR && !f.DolbyVision:
			return ErrUnsupported
		case f.DolbyVision && Rich(p.ID) && !decoder.PreparedDolbyVisionBaseAvailable():
			return ErrUnsupported
		case !Rich(p.ID) && f.Range != assets.RangeSDR && !decoder.PreparedToneMapAvailable():
			return ErrUnsupported
		}
	}
	return nil
}

// pixelDepth is a picture's depth from its pixel format, or from the raw
// sample size ffprobe reports.
func pixelDepth(format, raw string) int {
	if depth, err := strconv.Atoi(raw); err == nil && depth > 0 && depth <= 16 {
		return depth
	}
	format = strings.ToLower(format)
	for _, depth := range []string{"16", "14", "12", "10"} {
		if strings.HasSuffix(format, depth+"le") || strings.HasSuffix(format, depth+"be") || strings.HasPrefix(format, "p0"+depth) {
			n, _ := strconv.Atoi(depth)
			return n
		}
	}
	if format == "" {
		return 0
	}
	return 8
}
func probeRate(raw string) float64 {
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return 0
	}
	n, ne := strconv.ParseFloat(parts[0], 64)
	d, de := strconv.ParseFloat(parts[1], 64)
	if ne != nil || de != nil || n <= 0 || d <= 0 || math.IsNaN(n) || math.IsNaN(d) || math.IsInf(n, 0) || math.IsInf(d, 0) {
		return 0
	}
	return n / d
}

func acceptOutput(original, output probed, p Profile) error {
	if _, _, ok := MusicProfile(p.ID); ok {
		return acceptMusicOutput(original, output, p)
	}
	if math.IsNaN(output.Duration) || math.IsInf(output.Duration, 0) || output.Duration <= 0 || output.OtherStreams != 0 {
		return ErrOutput
	}
	if !strings.Contains(","+output.Container+",", ",mp4,") && !strings.Contains(","+output.Container+",", ",mov,") {
		return ErrOutput
	}
	rich := Rich(p.ID)
	// An HEVC recipe keeps an HDR source's range; everything else is SDR.
	keptHDR := rich && original.Range != "" && original.Range != assets.RangeSDR
	if math.Abs(original.Duration-output.Duration) > math.Max(1, original.Duration*0.001) || output.AudioTracks != original.AudioTracks || output.HDR != keptHDR || output.AttachedVideo {
		return ErrOutput
	}
	if keptHDR && (output.Range != original.Range || output.DolbyVision) {
		return ErrOutput
	}
	seventhOne := rich && recipeFor(p).KeepSeventhOne
	expectedAudio := "aac"
	if seventhOne && decoder.PreparedAudioCodec(true, original.AudioChannels) == "flac" {
		expectedAudio = "flac"
	}
	if output.AudioTracks > 0 && output.AudioCodec != expectedAudio {
		return ErrOutput
	}
	for i, c := range output.AudioChannels {
		if i >= len(original.AudioChannels) {
			return ErrOutput
		}
		want := decoder.PreparedChannels(rich, original.AudioChannels[i])
		if seventhOne {
			want = decoder.PreparedChannelsRich(true, original.AudioChannels[i])
		}
		if c != want {
			return ErrOutput
		}
	}
	if rich {
		depth := 8
		if keptHDR || original.BitDepth >= 10 {
			depth = 10
		}
		if output.VideoStreams != 1 || output.VideoCodec != "hevc" || output.BitDepth != depth || output.Width < 2 || output.Height < 2 || output.Width > p.Width || output.Height > p.Height || output.Width%2 != 0 || output.Height%2 != 0 || output.FrameRate <= 0 || output.FrameRate > 60.1 {
			return ErrOutput
		}
		return nil
	}
	if p.Kind == "audio" {
		if output.VideoStreams != 0 {
			return ErrOutput
		}
	} else if output.VideoStreams != 1 || output.VideoCodec != "h264" || output.PixFmt != "yuv420p" || output.Width < 2 || output.Height < 2 || output.Width > p.Width || output.Height > p.Height || output.Width%2 != 0 || output.Height%2 != 0 || (output.VideoProfile != "High" && output.VideoProfile != "Main" && output.VideoProfile != "Constrained Baseline" && output.VideoProfile != "Baseline") || output.VideoLevel < 1 || output.VideoLevel > decoder.H264OutputLevel(p.Width, p.Height, 60) || output.FrameRate <= 0 || output.FrameRate > 60.1 {
		return ErrOutput
	}
	return nil
}

// watchPreparedCollection follows this runtime's durable inputs; clock-only expiry
// still runs through worker.SafetyTick. Unrelated telemetry is not work.
func watchPreparedCollection(signal *worker.Signal) func() {
	return dbwork.WakeOnTables(signal,
		"prepared_media_*", "playback_sessions", "catalog_asset_links", "catalog_assets",
		"catalog_entities",
	)
}

// watchPreparedWork follows this runtime's durable inputs; clock-only expiry
// still runs through worker.SafetyTick. Unrelated telemetry is not work.
func watchPreparedWork(signal *worker.Signal) func() {
	return dbwork.WakeOnTables(signal,
		"prepared_media_*", "catalog_assets", "catalog_asset_links", "catalog_entities",
		"catalog_item_availability", "catalog_libraries", "asset_stream*", "episode_asset_boundaries",
		"music_virtual_tracks", "inventory_objects", "library_sources", "library_network_policy",
		"source_*", "remote_source_*", "remote_sources", "remote_scan_observations", "playback_origin_*",
		"playback_owner_policy", "playback_sessions", "server_settings", "console_settings_revision",
		"authorization_*", "accounts", "direct_profiles", "direct_memberships",
		"restrictions", "hosted_*", "identity_devices",
		"profile_*", "media_process_*",
	)
}

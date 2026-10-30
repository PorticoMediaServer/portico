// Package recordingmedia adapts source allocations to the shared confined
// decoder and immutable artifact store. It contains no player or identity model.
package recordingmedia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/linearinput"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels/dvr"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/probefacts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

var recordingID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Driver struct {
	root, ffmpeg, ffprobe string
	libraries             []string
	mu                    sync.Mutex
	stores                map[string]*mediaartifact.Store
	supervisor            storage.Supervisor
	available             bool
	writeMu               sync.Mutex
	measureMu             sync.Mutex
	writtenTotal          int64
	used                  int64
	reserved              map[string]int64
	policySource          func() dvr.StoragePolicy
	rootIdentity          os.FileInfo
	writeHealthy          bool
}

func New(root, ffmpeg, ffprobe string, libraries []string) (*Driver, error) {
	if root == "" {
		capabilityreport.Report(capabilityreport.Recording, false, "recording_folder_not_selected", nil)
		return &Driver{stores: map[string]*mediaartifact.Store{}, reserved: map[string]int64{}}, nil
	}

	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, dvr.ErrInvalid
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	canonical, e := filepath.EvalSymlinks(root)
	if e != nil || canonical != root {
		return nil, mediaartifact.ErrPrivate
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() {
		return nil, mediaartifact.ErrPrivate
	}
	d := &Driver{root: root, libraries: append([]string{}, libraries...), stores: map[string]*mediaartifact.Store{}, reserved: map[string]int64{}, rootIdentity: info, writeHealthy: true}
	if _, e = d.Measure(context.Background()); e != nil {
		return nil, e
	}
	// Whatever decides capture availability below is logged once and shown in
	// the owner's diagnostics; it never fails silently.
	reason, cause := "", error(nil)
	defer func() { capabilityreport.Report(capabilityreport.Recording, d.available, reason, cause) }()
	// Capture runs on every platform: sandboxed where the platform can, with the
	// baseline restrictions elsewhere, as the owner's diagnostics report
	// (D-MEDIA-6, mediaexec). Only an owner who requires the sandbox on a host
	// without one gets decoder_confinement_unavailable, below.
	for _, p := range libraries {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, dvr.ErrInvalid
		}
	}
	d.ffmpeg, e = executable(ffmpeg)
	if e != nil {
		reason, cause = "ffmpeg_not_configured", fmt.Errorf("ffmpeg %q is not an executable file: %w", ffmpeg, e)
		return d, nil
	}
	d.ffprobe, e = executable(ffprobe)
	if e != nil {
		reason, cause = "ffprobe_not_configured", fmt.Errorf("ffprobe %q is not an executable file: %w", ffprobe, e)
		return d, nil
	}
	d.libraries, e = decoder.ResolveLibraries(d.ffmpeg, d.ffprobe, libraries)
	if e != nil {
		reason, cause = "decoder_dependencies_unavailable", fmt.Errorf("FFmpeg's libraries can't be resolved (PORTICO_LINEAR_DECODER_LIBRARIES): %w", e)
		return d, nil
	}
	if cause = decoder.RequiredConfinement(d.ffmpeg); cause != nil {
		reason = "decoder_confinement_unavailable"
		return d, nil
	}
	d.available = true
	return d, nil
}
func executable(name string) (string, error) {
	p, e := exec.LookPath(name)
	if e != nil {
		return "", e
	}
	p, e = filepath.Abs(p)
	if e != nil {
		return "", e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	i, e := os.Stat(p)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0111 == 0 {
		return "", dvr.ErrCaptureUnavailable
	}
	return p, nil
}
func (d *Driver) Available() bool { return d.available }
func (d *Driver) RetirementAvailable() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "darwin"
}
func (d *Driver) store(o livechannels.Owner, artifact string) (*mediaartifact.Store, string, error) {
	if !o.Valid() {
		return nil, "", dvr.ErrDenied
	}
	parts := strings.Split(artifact, ".")
	if len(parts) != 2 || !recordingID.MatchString(parts[0]) {
		return nil, "", dvr.ErrInvalid
	}
	if e := d.checkRoot(); e != nil {
		return nil, "", e
	}
	key := o.Key() + ":" + parts[0]
	d.mu.Lock()
	defer d.mu.Unlock()
	root := filepath.Join(d.root, o.Key())
	objectRoot := filepath.Join(root, "captures", parts[0])
	if s := d.stores[key]; s != nil {
		return s, root, nil
	}
	s, e := mediaartifact.New(objectRoot)
	if e != nil {
		return nil, "", e
	}
	d.stores[key] = s
	return s, root, nil
}

// The absolute file bound supplements, never replaces, measured floor/cap enforcement.
const maxCaptureBytes = int64(1) << 50

func (d *Driver) Capture(ctx context.Context, in dvr.CaptureRequest, checkpoint func(dvr.CaptureCheckpoint) error) (dvr.CaptureResult, error) {
	var out dvr.CaptureResult
	if !d.available {
		return out, dvr.ErrCaptureUnavailable
	}
	if e := d.reserve(in); e != nil {
		return out, e
	}
	defer d.releaseReservation(in.Recording.ID)
	store, root, e := d.store(in.Owner, in.ArtifactKey)
	if e != nil {
		return out, e
	}
	writer, e := store.BeginRetained(in.ArtifactKey, maxCaptureBytes)
	if e != nil {
		return out, e
	}
	defer writer.Retain()
	end, e := time.Parse(time.RFC3339Nano, in.Recording.End)
	if e != nil {
		return out, e
	}
	cp := dvr.CaptureCheckpoint{}
	captureErr, truncated := d.continueCapture(ctx, in, writer, end, &cp, checkpoint)
	if in.OnRetired != nil {
		in.OnRetired()
	}
	cp.WriterRetired = true
	cp.ProducerSucceeded = captureErr == nil && !truncated

	object, e := writer.Checkpoint()
	if e != nil {
		return out, e
	}
	if object.Size == 0 && captureErr != nil {
		return out, captureErr
	}
	if captureErr != nil {
		if e = writer.Retain(); e != nil {
			return out, e
		}
		prefix := object.Size - object.Size%188
		writer, e = store.ResumeRetained(ctx, in.ArtifactKey, prefix, maxCaptureBytes)
		if e != nil {
			return out, e
		}
		defer writer.Retain()
		object, e = writer.Checkpoint()
		if e != nil {
			return out, e
		}
	}
	cp.Object = object
	if checkpoint != nil {
		if e = checkpoint(cp); e != nil {
			return out, e
		}
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	out, e = d.validate(ctx, store, writer, root, in, cp)
	if e != nil {
		return out, e
	}
	if captureErr != nil || truncated {
		out.Complete = false
		out.Reason = "capture-interrupted"
		if errors.Is(captureErr, dvr.ErrStorageFloor) {
			out.Reason = "storage-floor"
		}
		if errors.Is(captureErr, dvr.ErrStorageCap) {
			out.Reason = "storage-cap"
		}
		if truncated {
			out.Reason = "capture-truncated"
		}
	}
	return out, nil
}
func (d *Driver) Recover(ctx context.Context, in dvr.CaptureRequest, cp dvr.CaptureCheckpoint) (dvr.CaptureResult, error) {
	var out dvr.CaptureResult
	store, root, e := d.store(in.Owner, in.ArtifactKey)
	if e != nil {
		return out, e
	}
	// Seal-before-publication crashes may already have a verified immutable object.
	if cp.Object.Size > 0 && cp.Object.Digest != "" {
		reader, e := store.Open(ctx, cp.Object)
		if e == nil {
			defer reader.Close()
			facts, e := d.probe(ctx, in.Recording.ID, reader, cp.Object.Size)
			if e != nil {
				return out, e
			}
			path, e := store.ObjectPath(cp.Object)
			if e != nil {
				return out, e
			}
			return d.result(in, cp, root, path, facts)
		}
		if !errors.Is(e, os.ErrNotExist) {
			return out, e
		}
	}
	writer, e := store.ResumeRetained(ctx, in.ArtifactKey, cp.Object.Size, maxCaptureBytes)
	if e != nil {
		return out, e
	}
	defer writer.Retain()
	end, _ := time.Parse(time.RFC3339Nano, in.Recording.End)
	if time.Now().Before(end) && in.Input.Locator != "" {
		if e = d.reserve(in); e != nil {
			return out, e
		}
		defer d.releaseReservation(in.Recording.ID)
		cp.WriterRetired, cp.ProducerSucceeded, cp.BoundaryReached = false, false, false
		cp.Gaps++
		_, _ = d.continueCapture(ctx, in, writer, end, &cp, in.Checkpoint)
		cp.WriterRetired = true
		cp.ProducerSucceeded = false
		cp.Object, e = writer.Checkpoint()
		if e != nil {
			return out, e
		}
	}
	if in.OnRetired != nil {
		in.OnRetired()
	}
	// Match Capture's seal/publication protocol: a crash after sealing must
	// find the exact immutable object in the durable checkpoint, not an old prefix.
	cp.WriterRetired = true
	cp.Object, e = writer.Checkpoint()
	if e != nil {
		return out, e
	}
	if in.Checkpoint != nil {
		if e = in.Checkpoint(cp); e != nil {
			return out, e
		}
	}
	out, e = d.validate(ctx, store, writer, root, in, cp)
	if e == nil {
		out.Complete = false
		out.Reason = "recovered-partial"
	}
	return out, e
}
func (d *Driver) validate(ctx context.Context, store *mediaartifact.Store, writer *mediaartifact.Writer, root string, in dvr.CaptureRequest, cp dvr.CaptureCheckpoint) (dvr.CaptureResult, error) {
	var out dvr.CaptureResult
	var facts probefacts.ProbeFacts
	e := writer.WithClosedBytes(ctx, func(reader io.ReaderAt, size int64) error {
		var e error
		facts, e = d.probe(ctx, in.Recording.ID, reader, size)
		return e
	})
	if e != nil {
		return out, e
	}
	object, e := writer.Seal(ctx)
	if e != nil {
		return out, e
	}
	if object != cp.Object {
		return out, mediaartifact.ErrIdentity
	}
	path, e := store.ObjectPath(object)
	if e != nil {
		return out, e
	}
	return d.result(in, cp, root, path, facts)
}
func (d *Driver) probe(ctx context.Context, key string, reader io.ReaderAt, size int64) (probefacts.ProbeFacts, error) {
	var empty probefacts.ProbeFacts
	if e := validateTransport(ctx, reader, size); e != nil {
		return empty, e
	}
	bridge, e := linearinput.OpenClosed(reader, size)
	if e != nil {
		return empty, e
	}
	defer bridge.Close()
	reservation, e := bridge.Reserve()
	if e != nil {
		return empty, e
	}
	defer reservation.Close()
	check, cancel := context.WithCancel(ctx)
	defer cancel()
	raw, e := decoder.RunAllocatedProbe(check, &d.supervisor, key, d.ffprobe, bridge.URL(), reservation, d.libraries...)
	if e != nil {
		return empty, e
	}
	facts, e := probefacts.Parse(raw)
	if e != nil {
		return empty, e
	}
	return facts, nil
}
func (d *Driver) result(in dvr.CaptureRequest, cp dvr.CaptureCheckpoint, root, path string, facts probefacts.ProbeFacts) (dvr.CaptureResult, error) {
	out := dvr.CaptureResult{CaptureCheckpoint: cp, Root: root, Path: path, Container: "mpegts", Reason: "coverage-incomplete"}
	if facts.Format.DurationSeconds == nil || facts.Format.DurationSeconds.Denominator <= 0 {
		return out, dvr.ErrUnavailable
	}
	out.Duration = float64(facts.Format.DurationSeconds.Numerator) / float64(facts.Format.DurationSeconds.Denominator)
	if out.Duration <= 0 || out.Duration > 8*24*3600 {
		return out, dvr.ErrUnavailable
	}
	for _, stream := range facts.Streams {
		if stream.Kind == "video" && out.VideoCodec == "" && stream.Video != nil {
			out.VideoCodec = stream.Codec
			out.Width = int(stream.Video.Width)
			out.Height = int(stream.Video.Height)
		}
		if stream.Kind == "audio" && out.AudioCodec == "" {
			out.AudioCodec = stream.Codec
		}
	}
	if out.VideoCodec == "" && out.AudioCodec == "" {
		return out, dvr.ErrUnavailable
	}
	st, e := os.Stat(path)
	if e != nil {
		return out, e
	}
	if !st.Mode().IsRegular() || st.Size() != cp.Object.Size || st.Mode().Perm()&0222 != 0 {
		return out, mediaartifact.ErrIdentity
	}
	out.ModifiedNS = st.ModTime().UnixNano()
	start, e := time.Parse(time.RFC3339Nano, in.Recording.Programme.Start)
	if e != nil {
		start, e = time.Parse(time.RFC3339Nano, in.Recording.Start)
	}
	if e != nil {
		return out, e
	}
	end, e := time.Parse(time.RFC3339Nano, in.Recording.Programme.End)
	if e != nil {
		end, e = time.Parse(time.RFC3339Nano, in.Recording.End)
	}
	if e != nil {
		return out, e
	}
	// Observed coverage plus finite container duration must agree. The declared
	// four-second tolerance accommodates keyframe/TS probe endpoint precision,
	// never missing segments, reconnects, revocation or a nonzero producer failure.
	tolerance := 4 * time.Second
	expected := end.Sub(start).Seconds()
	out.Complete = cp.ProducerSucceeded && cp.BoundaryReached && cp.Gaps == 0 && !cp.First.IsZero() && !cp.Last.IsZero() && !cp.First.After(start.Add(tolerance)) && !cp.Last.Before(end.Add(-tolerance)) && out.Duration >= expected-tolerance.Seconds() && out.Duration <= expected+float64(in.Recording.Options.BeforeMs+in.Recording.Options.AfterMs)/1000+2*tolerance.Seconds()
	if out.Complete {
		out.Reason = ""
	}
	return out, nil
}
func (d *Driver) Remove(ctx context.Context, o livechannels.Owner, key string, object mediaartifact.Object) error {
	if key == "" {
		if object.Digest != "" || object.Size != 0 {
			return mediaartifact.ErrIdentity
		}
		return nil
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	s, _, e := d.store(o, key)
	if e != nil {
		return e
	}
	if object.Digest != "" {
		if e = s.Remove(object); e != nil {
			return e
		}
	}
	if key != "" {
		if e = s.RemoveRetained(key); e != nil {
			return e
		}
	}
	_, e = d.Measure(ctx)
	return e
}
func (d *Driver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var e error
	for _, s := range d.stores {
		e = errors.Join(e, s.Close())
	}
	return e
}

// validateTransport scans all output packet framing, including the tail. A
// nonempty/truncated file or a successful FFmpeg exit is not integrity evidence.
func validateTransport(ctx context.Context, r io.ReaderAt, size int64) error {
	if size < 188*3 || size%188 != 0 {
		return mediaartifact.ErrIdentity
	}
	b := make([]byte, 188*1024)
	offset := int64(0)
	for offset < size {
		if e := ctx.Err(); e != nil {
			return e
		}
		n := min(int64(len(b)), size-offset)
		got, e := r.ReadAt(b[:n], offset)
		if e != nil && e != io.EOF {
			return e
		}
		if int64(got) != n {
			return mediaartifact.ErrIdentity
		}
		for i := 0; i < got; i += 188 {
			p := b[i : i+188]
			if p[0] != 0x47 || p[1]&0x80 != 0 {
				return mediaartifact.ErrIdentity
			}
			control := (p[3] >> 4) & 3
			if control == 0 || control >= 2 && int(p[4]) > 183 {
				return mediaartifact.ErrIdentity
			}
		}
		offset += int64(got)
	}
	// Complete-byte traversal is intentional; object sealing separately verifies
	// the capture writer's SHA-256 rather than trusting decoder-reported filenames.
	return nil
}

// Each restarted producer appends a separately muxed transport-stream interval.
// The checkpoint preserves all gaps and cancellation never claims missing bytes.
func (d *Driver) continueCapture(ctx context.Context, in dvr.CaptureRequest, writer *mediaartifact.Writer, end time.Time, cp *dvr.CaptureCheckpoint, checkpoint func(dvr.CaptureCheckpoint) error) (error, bool) {
	var last error
	truncated := false
	for ctx.Err() == nil && time.Now().Before(end) {
		span, e, cut, boundary := d.captureAttempt(ctx, in, writer, end, *cp, checkpoint)
		if cp.First.IsZero() {
			cp.First = span.First
		}
		if !span.Last.IsZero() {
			cp.Last = span.Last
		}
		cp.Gaps += span.Gaps
		cp.BoundaryReached = boundary
		truncated = truncated || cut
		last = e
		if boundary || errors.Is(e, dvr.ErrStorageFloor) || errors.Is(e, dvr.ErrStorageCap) || errors.Is(e, dvr.ErrStorageUnavailable) {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err(), truncated
		}
		cp.Gaps++
		timer := time.NewTimer(min(2*time.Second, max(time.Duration(0), time.Until(end))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err(), truncated
		case <-timer.C:
		}
	}
	if ctx.Err() != nil {
		return ctx.Err(), truncated
	}
	return last, truncated
}

func boundaryReached(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
func (d *Driver) captureAttempt(ctx context.Context, in dvr.CaptureRequest, writer *mediaartifact.Writer, end time.Time, prior dvr.CaptureCheckpoint, checkpoint func(dvr.CaptureCheckpoint) error) (linearinput.Span, error, bool, bool) {
	gateway, e := linearinput.Open(ctx, in.Input)
	if e != nil {
		return linearinput.Span{}, e, false, false
	}
	defer gateway.Close()
	reservation, e := gateway.Reserve()
	if e != nil {
		return linearinput.Span{}, e, false, false
	}
	defer reservation.Close()
	// Local scheduled stop closes input normally. Only an unretired child after
	// the grace interval is killed. A never-ending live source need not reach EOF.
	process, cancel := context.WithCancel(ctx)
	defer cancel()
	timerDone := make(chan struct{})
	boundary := make(chan struct{})
	supervise.Go("recording.driver.boundary", func() {
		defer close(timerDone)
		timer := time.NewTimer(max(time.Duration(0), time.Until(end)))
		defer timer.Stop()
		select {
		case <-process.Done():
			return
		case <-timer.C:
			close(boundary)
			gateway.StopInput()
		}
		grace := time.NewTimer(10 * time.Second)
		defer grace.Stop()
		select {
		case <-process.Done():
		case <-grace.C:
			cancel()
		}
	})
	lastCheckpoint := time.Time{}
	truncated := false
	captureErr := decoder.RunLinearCopy(process, &d.supervisor, in.Recording.ID, d.ffmpeg, gateway.URL(), gateway.HLS(), reservation, in.PhysicalLock, func(reader io.Reader) error {
		buf := make([]byte, 188*1024)
		for {
			n, e := io.ReadFull(reader, buf)
			if n%188 != 0 {
				truncated = true
				n -= n % 188
			}
			if n > 0 {
				if _, we := d.writeCapture(in.Recording.ID, writer, buf[:n]); we != nil {
					return we
				}
			}
			if n > 0 && time.Since(lastCheckpoint) >= time.Second {
				object, we := writer.Checkpoint()
				if we != nil {
					return we
				}
				span := gateway.Snapshot()
				cp := prior
				if cp.First.IsZero() {
					cp.First = span.First
				}
				cp.Last = span.Last
				cp.Gaps += span.Gaps
				cp.Object = object
				select {
				case <-boundary:
					cp.BoundaryReached = true
				default:
				}
				if checkpoint != nil {
					if we = checkpoint(cp); we != nil {
						return we
					}
				}
				lastCheckpoint = time.Now()
			}
			if e == io.EOF || e == io.ErrUnexpectedEOF {
				return nil
			}
			if e != nil {
				return e
			}
		}
	}, d.libraries...)
	cancel()
	<-timerDone
	// Gateway reads and physical decoder ownership must both end before validation.
	span := gateway.Snapshot()
	_ = gateway.Close()
	return span, captureErr, truncated, boundaryReached(boundary)

}

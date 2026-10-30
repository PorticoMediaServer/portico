package subtitlevideo

// This package retains observed source inputs and bounded extraction. Playback
// delivery, encoding, windows and manifests are owned by ordinary playback HLS.
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/sourceaccess"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Options struct {
	DB                         *sql.DB
	Storage                    *storage.Client
	Mounts                     *mounts.Service
	Directory, FFmpeg, FFprobe string
	Libraries                  []string
}
type Runtime struct {
	DiscInputs                 func(context.Context, string, string, string) (subtitles.DiscInput, error)
	DiscAvailable              func() bool
	custody                    *livechannels.PhysicalLocks
	db                         *sql.DB
	storage                    *storage.Client
	roots                      *sourceaccess.Registry
	directory, ffmpeg, ffprobe string
	libraries                  []string
	life                       context.Context
	cancel                     context.CancelFunc
	RemoteStorage              RemoteStorageInput
	activeAudioConversions     atomic.Int64
}
type audioTrack struct {
	Index           int
	Name, Language  string
	Group, Playlist string
	Default         bool
}
type facts struct {
	videoCodec, audioCodec, container string
	width, height                     int
	origin, duration                  int64
	video                             int
	audios                            []audioTrack
	// probe is the confined ffprobe document these facts were read from, kept
	// so a remote source's planner can read every stream from the same bytes.
	probe []byte
}

func New(o Options) (*Runtime, error) {
	if o.DB == nil || o.Storage == nil || o.Directory == "" {
		return nil, subtitles.ErrInput
	}
	dir, e := filepath.Abs(o.Directory)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	ffmpeg, _ := exec.LookPath(o.FFmpeg)
	ffprobe, _ := exec.LookPath(o.FFprobe)
	if ffmpeg != "" {
		ffmpeg, _ = filepath.Abs(ffmpeg)
	}
	if ffprobe != "" {
		ffprobe, _ = filepath.Abs(ffprobe)
	}
	life, cancel := context.WithCancel(context.Background())
	// A nil *mounts.Service must stay a nil owner: wrapped in the interface it
	// would be called (a server without managed mounts, and tests).
	roots := sourceaccess.New(o.Storage, nil)
	if o.Mounts != nil {
		roots = sourceaccess.New(o.Storage, o.Mounts)
	}
	r := &Runtime{db: o.DB, storage: o.Storage, roots: roots, directory: dir, ffmpeg: ffmpeg, ffprobe: ffprobe, libraries: append([]string(nil), o.Libraries...), life: life, cancel: cancel}
	// Restart cannot adopt a dead observed acquisition. Persisted selection remains
	// visible, but explicit retry must prepare a fresh rendition before new access.
	if r.custody, e = livechannels.NewPhysicalLocks(filepath.Join(dir, "custody")); e != nil {
		cancel()
		return nil, e
	}
	if e = r.reconcileOrphans(); e != nil {
		cancel()
		return nil, e
	}
	supervise.Go("subtitle-inputs.orphan-sweep", func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-r.life.Done():
				return
			case <-ticker.C:
				_ = r.reconcileOrphans()
			}
		}
	})
	return r, nil
}
func (r *Runtime) Close() error { r.cancel(); return r.roots.Close() }

func (r *Runtime) probe(ctx context.Context, input subtitles.RenderInput, ledger *extentLedger) (facts, []probeStream, error) {
	var f facts
	f.video = -1
	if r.ffprobe == "" {
		return f, nil, subtitles.ErrRendererConfiguration
	}
	bridge, e := openBridge(ctx, input, ledger)
	if e != nil {
		return f, nil, e
	}
	defer bridge.Close()
	data, e := decoder.RunProbe(ctx, r.storage.Supervisor, identity.Token(), r.ffprobe, bridge.url, bridge.reservation, r.libraries...)
	if e != nil {
		return f, nil, e
	}
	var document struct {
		Format struct {
			Name     string `json:"format_name"`
			Duration string `json:"duration"`
			Start    string `json:"start_time"`
		}
		Streams []probeStream `json:"streams"`
	}
	if json.Unmarshal(data, &document) != nil {
		return f, nil, subtitles.ErrInput
	}
	duration, e := strconv.ParseFloat(document.Format.Duration, 64)
	if e != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 86400 {
		return f, nil, subtitles.ErrTiming
	}
	f.duration = int64(math.Round(duration * 1e6))
	f.container = document.Format.Name
	f.probe = data
	origin, e := strconv.ParseFloat(document.Format.Start, 64)
	if e != nil || math.IsNaN(origin) || math.IsInf(origin, 0) || math.Abs(origin) > 86400 {
		return f, nil, subtitles.ErrTiming
	}
	f.origin = int64(math.Round(origin * 1e6))
	for _, s := range document.Streams {
		if s.Index < 0 || s.Index > 65535 {
			return f, nil, subtitles.ErrInput
		}
		switch s.Type {
		case "video":
			if s.Disposition.Attached == 0 && f.video < 0 {
				f.video = s.Index
				f.videoCodec = s.Codec
				f.width = s.Width
				f.height = s.Height
			}
		case "audio":
			if f.audioCodec == "" {
				f.audioCodec = s.Codec
			}
			if len(f.audios) >= 4096 {
				return f, nil, subtitles.ErrCapacity
			}
			name := s.Tags["title"]
			if name == "" {
				name = fmt.Sprintf("Audio %d", len(f.audios)+1)
			}
			f.audios = append(f.audios, audioTrack{Index: s.Index, Name: manifestText(name), Language: manifestText(s.Tags["language"]), Default: s.Disposition.Default != 0})
		}
	}
	if f.video < 0 || f.width < 1 || f.height < 1 || f.width > 8192 || f.height > 4320 {
		return f, nil, subtitles.ErrUnsupported
	}
	return f, document.Streams, input.Validate(ctx)
}

type probeStream struct {
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	Index         int               `json:"index"`
	Type          string            `json:"codec_type"`
	Codec         string            `json:"codec_name"`
	ColorTransfer string            `json:"color_transfer"`
	Tags          map[string]string `json:"tags"`
	Disposition   struct {
		Default         int `json:"default"`
		Forced          int `json:"forced"`
		Attached        int `json:"attached_pic"`
		HearingImpaired int `json:"hearing_impaired"`
	} `json:"disposition"`
}

func manifestText(v string) string {
	return strings.Map(func(c rune) rune {
		if c < 32 || c == 127 || c == '"' || c == '\\' {
			return -1
		}
		return c
	}, v)
}
func (r *Runtime) Extract(ctx context.Context, item, source string, index int, format string) ([]byte, string, error) {
	if r.ffmpeg == "" {
		return nil, "", subtitles.ErrRendererConfiguration
	}
	input, e := r.OpenSubtitleInput(ctx, item, source, "")
	if e != nil {
		return nil, "", e
	}
	defer input.Close()
	ctx, progress, finish := extractionProgress(ctx, input)
	defer finish()
	ledger := &extentLedger{}
	bridge, e := openBridge(ctx, progress, ledger)
	if e != nil {
		return nil, "", e
	}
	defer bridge.Close()
	data, e := decoder.RunSubtitleExtract(ctx, r.storage.Supervisor, identity.Token(), r.ffmpeg, bridge.url, bridge.reservation, index, format, r.libraries...)
	if e != nil {
		return nil, "", e
	}
	if e = input.Validate(ctx); e != nil {
		return nil, "", e
	}
	if format == "vobsub" {
		idx, sub, err := subtitles.VobSubOriginal(data)
		if err != nil {
			return nil, "", err
		}
		data, e = subtitles.CanonicalAsset(idx, sub, "vobsub", 0)
	} else if format == "dvb" {
		data, e = json.Marshal(subtitles.RenderAsset{Version: 2, TimeDomain: "source-relative", Renderer: "burn_in", Format: "mks", Data: data})
	} else {
		data, e = subtitles.Canonical(data, format, 0)
	}
	if e == nil && (format == "ass" || format == "ssa") {
		var asset subtitles.RenderAsset
		if json.Unmarshal(data, &asset) == nil && asset.Version == 2 {
			asset.Fonts, asset.SkippedFonts, e = r.extractFonts(ctx, progress)
			if e != nil {
				return nil, "", e
			}
			data, e = json.Marshal(asset)
		}
	}
	return data, persistentEvidence(input), e
}
func (r *Runtime) Probe(ctx context.Context, item, source string) (*subtitles.Inventory, string, error) {
	input, e := r.OpenSubtitleInput(ctx, item, source, "")
	if e != nil {
		return nil, "", e
	}
	defer input.Close()
	facts, streams, e := r.probe(ctx, input, &extentLedger{})
	if e != nil {
		return nil, "", e
	}
	inventory := &subtitles.Inventory{OriginUS: facts.origin, TimingKnown: true, Status: "known", Tracks: []subtitles.Track{}}
	for _, s := range streams {
		if s.Type != "subtitle" {
			continue
		}
		format := subtitles.CodecFormat(s.Codec)
		reason := ""
		if format == "" {
			format = s.Codec
			reason = "unsupported_format"
		}
		inventory.Tracks = append(inventory.Tracks, subtitles.Track{Origin: "embedded", Format: format, StreamIndex: s.Index, Title: s.Tags["title"], Language: s.Tags["language"], Default: s.Disposition.Default != 0, Forced: s.Disposition.Forced != 0, HearingImpaired: s.Disposition.HearingImpaired != 0, Reason: reason})
	}
	return inventory, persistentEvidence(input), nil
}

func (r *Runtime) InspectSubtitleSource(ctx context.Context, item, source string) (subtitles.SourceMediaInfo, error) {
	input, e := r.OpenSubtitleInput(ctx, item, source, "")
	if e != nil {
		return subtitles.SourceMediaInfo{}, e
	}
	defer input.Close()
	f, _, e := r.probe(ctx, input, &extentLedger{})
	if e != nil {
		return subtitles.SourceMediaInfo{}, e
	}
	remote, ok := input.(*remoteInput)
	if !ok {
		return subtitles.SourceMediaInfo{}, subtitles.ErrUnsupported
	}
	return subtitles.SourceMediaInfo{Evidence: input.Evidence(), DescriptorDigest: remote.digest, VideoCodec: f.videoCodec, AudioCodec: f.audioCodec, DurationUS: f.duration, OriginUS: f.origin, Width: f.width, Height: f.height, Container: f.container, NetworkPolicyRevision: remote.source.networkRevision, Probe: f.probe}, nil
}

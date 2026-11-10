package mediafacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

type Chapter struct {
	Title string  `json:"title"`
	Start float64 `json:"startSeconds"`
	End   float64 `json:"endSeconds"`
}

// AudioFileMetadata is the file-owned baseline before local sidecars are applied.
// It is retained separately so removing a sidecar restores embedded evidence.
type AudioFileMetadata struct {
	MusicCueTracks                                 []Chapter
	Tags                                           map[string]string
	Chapters                                       []Chapter
	ChapterStatus, ArtworkKey, Issue, ArtworkIssue string
}

type Facts struct {
	AudioFileMetadata   *AudioFileMetadata   `json:"-"`
	DiscAlternate       bool                 `json:"-"`
	MusicCueTracks      []Chapter            `json:"-"`
	ArtworkObserved     bool                 `json:"-"`
	AnalysisAlgorithm   string               `json:"-"`
	SubtitleTextPending bool                 `json:"-"`
	VideoMetadataStatus string               `json:"-"`
	AnalysisOperations  []string             `json:"-"`
	ObservedRevision    string               `json:"-"`
	InventoryOnly       bool                 `json:"-"`
	SubtitleInventory   *subtitles.Inventory `json:"-"`
	OriginUS            int64                `json:"-"`
	TimingKnown         bool                 `json:"-"`
	ChapterStatus       string               `json:"-"`
	Streams             []Stream             `json:"-"`
	TagSources          map[string]string    `json:"-"`
	Tags                map[string]string    `json:"-"`
	Chapters            []Chapter            `json:"-"`
	AttachedPicture     bool                 `json:"-"`
	ArtworkKey          string               `json:"-"`
	// ArtworkExtra carries sidecar cache keys (localmetadata.ReadSidecars:
	// "show/poster", "season/2/poster", "artist/portrait", "item/logo",
	// "album/cover") for the catalog commit to write as local_artwork rows.
	ArtworkExtra              map[string]string `json:"-"`
	ArtworkCandidatesObserved bool              `json:"-"`
	LocalMetadataIssue        string            `json:"-"`
	ObservedSize              int64             `json:"-"`
	ObservedModifiedNS        int64             `json:"-"`
	Container                 string            `json:"container"`
	VideoCodec                string            `json:"videoCodec"`
	AudioCodec                string            `json:"audioCodec"`
	Width                     int               `json:"width"`
	Height                    int               `json:"height"`
	Duration                  float64           `json:"duration"`
	BitRate                   int64             `json:"-"`
	AnalysisSourceEvidence    string            `json:"-"`
}

type Source struct {
	ID string `json:"id"`
	Facts
}
type Probe struct {
	ReadGuard  func(context.Context, string) error
	Binary     string
	Supervisor *processpool.Supervisor
	// ScanCommand and ReadPrefix let the caller serve the probe through its own
	// admitted-file reader. Both are optional; see access.go.
	ScanCommand func(ctx context.Context, binary string, args []string, path string, mode ScanMode, out io.Writer) (handled bool, err error)
	ReadPrefix  func(ctx context.Context, path string, limit int64) ([]byte, error)
}
type boundedBuffer struct {
	data  []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > b.limit {
		return 0, errors.New("probe output exceeds limit")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func (p Probe) Inspect(ctx context.Context, path string) (Facts, error) {
	return p.inspect(ctx, path, []string{"-protocol_whitelist", "file,pipe", "-format_whitelist", mediaexec.LocalInputFormats})
}

// InspectScan applies bounded local-media probing and rejects network protocols.
// File List Only never calls this method; ReadGuard is checked again at launch.
func (p Probe) InspectScan(ctx context.Context, path string) (Facts, error) {
	return p.inspect(ctx, path, []string{"-protocol_whitelist", "file,pipe", "-format_whitelist", mediaexec.LocalInputFormats, "-probesize", "8388608", "-analyzeduration", "5000000"})
}

// InspectRemote reads a file that is not on a disk here through the private loopback
// address that serves it (package inputbridge), the way a scan reads a local one.
func (p Probe) InspectRemote(ctx context.Context, address string) (Facts, error) {
	return p.inspect(ctx, address, []string{"-protocol_whitelist", "http,tcp", "-format_whitelist", mediaexec.LocalInputFormats, "-probesize", "8388608",
		"-analyzeduration", "5000000"})
}

func (p Probe) InspectRemoteMP4(ctx context.Context, path string) (Facts, error) {
	facts, e := p.inspect(ctx, path, []string{"-f", "mov", "-enable_drefs", "0", "-use_absolute_path", "0", "-protocol_whitelist", "http,tcp", "-probesize", "8388608", "-analyzeduration", "5000000"})
	facts.Container = "mp4"
	return facts, e
}
func (p Probe) probeOutput(ctx context.Context, path, input string, options []string) ([]byte, error) {
	return p.probeOutputMode(ctx, path, input, options, false)
}

// probeOutputMode reads a disc folder through its descriptor-bound directory
// input; every other probe keeps the file descriptor or bridge it always had.
func (p Probe) probeOutputMode(ctx context.Context, path, input string, options []string, directory bool) ([]byte, error) {
	bin := p.Binary
	if bin == "" {
		bin = mediatools.Resolve("ffprobe")
	}
	args := append(
		options,
		[]string{
			"-v",
			"error",
			"-show_format",
			"-show_streams",
			"-show_chapters",
			"-show_entries",
			"format:stream:chapter:format_tags=title,show,tv_show,year,date,season_number,episode_sort,edition,imdb,imdbid,tmdb,tmdbid,tvdb,tvdbid,anilist,anilistid:stream_tags=language,title:chapt" +
				"er_tags=title",
			"-of",
			"json",
			input,
		}...)
	out := &boundedBuffer{limit: 1 << 20}
	var runErr error
	if p.ReadGuard != nil {
		if e := p.ReadGuard(ctx, path); e != nil {
			return nil, e
		}
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	mode := ScanFile
	if directory {
		// A disc folder is one admitted directory descriptor; the sandbox
		// mounts it at its own root and the tool never sees a host path.
		mode = ScanDirectory
	} else if input != path {
		// Only the validated disc reader uses a prefixed input: exactly
		// bluray:path, and the caller's reader keeps that prefix.
		mode = ScanBluRay
	}
	if handled, err := p.runScan(ctx, bin, args, path, mode, out); handled {
		runErr = err
	} else if directory {
		// A disc folder has no path-based fallback: without the admitted
		// descriptor mount it stays unreadable.
		return nil, fmt.Errorf("media probe failed: %w", errDirectoryReader)
	} else if p.Supervisor != nil {
		cmd, err := mediaexec.Command(probeJob(bin, args, path))
		if err != nil {
			return nil, fmt.Errorf("media probe failed: %w", err)
		}
		key := path
		if len(options) > 0 && options[0] == "-f" {
			key = "playback:probe:" + path
		}
		runErr = p.Supervisor.Run(ctx, key, cmd, func(r io.Reader) error { _, e := io.Copy(out, r); return e })
	} else {
		cmd, err := mediaexec.CommandContext(ctx, probeJob(bin, args, path))
		if err != nil {
			return nil, fmt.Errorf("media probe failed: %w", err)
		}
		cmd.Stdout = out
		cmd.Stderr = io.Discard
		cmd.WaitDelay = time.Second
		runErr = cmd.Run()
	}
	if runErr != nil {
		return nil, fmt.Errorf("media probe failed: %w", runErr)
	}
	return out.data, nil
}

// DecodeProbe reads an ffprobe JSON document (format and streams) the way a
// scan does. A remote source's planner reads its measured streams through it.
func DecodeProbe(data []byte) (Facts, error) { return decodeMediaProbe(data) }

func decodeMediaProbe(data []byte) (Facts, error) {
	var raw struct {
		Format struct {
			Duration string            `json:"duration"`
			Start    string            `json:"start_time"`
			Name     string            `json:"format_name"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
		Streams []struct {
			Index          *int              `json:"index"`
			Channels       int               `json:"channels"`
			ChannelLayout  string            `json:"channel_layout"`
			Type           string            `json:"codec_type"`
			Codec          string            `json:"codec_name"`
			Width          int               `json:"width"`
			Height         int               `json:"height"`
			ColorTransfer  string            `json:"color_transfer"`
			ColorPrimaries string            `json:"color_primaries"`
			Tags           map[string]string `json:"tags"`
			Disposition    struct {
				AttachedPic     int `json:"attached_pic"`
				Default         int `json:"default"`
				Forced          int `json:"forced"`
				HearingImpaired int `json:"hearing_impaired"`
				Comment         int `json:"comment"`
				VisualImpaired  int `json:"visual_impaired"`
			} `json:"disposition"`
		} `json:"streams"`
		Chapters []struct {
			Start string            `json:"start_time"`
			End   string            `json:"end_time"`
			Tags  map[string]string `json:"tags"`
		} `json:"chapters"`
	}
	if e := json.Unmarshal(data, &raw); e != nil {
		return Facts{}, e
	}
	// Detail is read in a second pass over the same bytes so the two shapes stay
	// independent. A detail that fails to decode costs the detail, never the probe.
	var detail struct {
		Format struct {
			BitRate string `json:"bit_rate"`
		} `json:"format"`
		Streams []probeStreamDetail `json:"streams"`
	}
	if json.Unmarshal(data, &detail) != nil || len(detail.Streams) != len(raw.Streams) {
		detail.Streams = make([]probeStreamDetail, len(raw.Streams))
	}
	f := Facts{Container: demuxerIdentity(raw.Format.Name)}
	f.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	if rate, e := strconv.ParseInt(detail.Format.BitRate, 10, 64); e == nil && rate > 0 && rate < 1<<40 {
		f.BitRate = rate
	}
	if start, e := strconv.ParseFloat(raw.Format.Start, 64); e == nil && !math.IsNaN(start) && !math.IsInf(start, 0) && math.Abs(start) <= 86400 {
		f.OriginUS = int64(math.Round(start * 1e6))
		f.TimingKnown = true
	}
	f.Tags = map[string]string{}
	f.TagSources = map[string]string{}

	if len(raw.Streams) > 128 {
		return Facts{}, errors.New("source stream count exceeds 128")
	}
	f.Streams = []Stream{}
	knownStreams := true
	seenStreams := map[int]bool{}
	for position, s := range raw.Streams {
		s.Codec = canonicalMusicCodec(s.Codec)
		if s.Disposition.AttachedPic != 0 {
			f.AttachedPicture = true
			continue
		}
		if s.Type == "video" || s.Type == "audio" || s.Type == "subtitle" {
			if s.Index == nil {
				knownStreams = false
			} else {
				if *s.Index < 0 || *s.Index > 65535 || s.Channels < 0 || s.Channels > 128 || seenStreams[*s.Index] {
					return Facts{}, errors.New("invalid probe stream index")
				}
				seenStreams[*s.Index] = true
				factDetail := streamDetail(s.Type, s.Codec, s.Width, s.Height, detail.Streams[position])
				factDetail.AudioDescription = s.Disposition.VisualImpaired != 0
				f.Streams = append(f.Streams, Stream{Index: *s.Index, Type: s.Type, Codec: streamText(s.Codec, 64), Language: streamText(s.Tags["language"], 32), Title: streamText(s.Tags["title"], 200),
					Channels: s.Channels, ChannelLayout: streamText(s.ChannelLayout, 64), Default: s.Disposition.Default != 0, Forced: s.Disposition.Forced != 0, ColorTransfer: streamText(s.ColorTransfer,
						32), ColorPrimaries: streamText(s.ColorPrimaries, 32), HearingImpaired: s.Disposition.HearingImpaired != 0, Commentary: s.Disposition.Comment != 0, Detail: factDetail})
			}
		}
		if s.Type == "video" && f.VideoCodec == "" {
			f.VideoCodec = s.Codec
			f.Width = s.Width
			f.Height = s.Height
		}
		if s.Type == "audio" && f.AudioCodec == "" {
			f.AudioCodec = s.Codec

		}
	}
	if !knownStreams {
		f.Streams = nil
	}
	f.ChapterStatus = "known"
	for _, c := range raw.Chapters {
		start, e1 := strconv.ParseFloat(c.Start, 64)
		end, e2 := strconv.ParseFloat(c.End, 64)
		if e1 != nil || e2 != nil {
			f.LocalMetadataIssue = "invalid_chapter_timeline"
			continue
		}
		f.Chapters = append(f.Chapters, Chapter{streamText(c.Tags["title"], 500), start, end})
	}
	return f, nil
}

// ErrNoTimedStream says the tool read the file and found no audio or video with a length.
var ErrNoTimedStream = errors.New("file has no timed media stream")

func (p Probe) inspect(ctx context.Context, path string, options []string) (Facts, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	data, err := p.probeOutput(ctx, path, path, options)
	if err != nil {
		return Facts{}, err
	}
	f, err := decodeMediaProbe(data)
	if err != nil {
		return Facts{}, err
	}
	if err := p.completeContainer(ctx, path, &f); err != nil {
		return Facts{}, err
	}
	if err := p.completeMusepackTimeline(ctx, path, &f); err != nil {
		return Facts{}, err
	}
	f.Chapters, f.ChapterStatus = CleanChapters(f.Chapters, f.Duration)
	if f.ChapterStatus == "partial" {
		f.LocalMetadataIssue = "invalid_chapter_timeline"
	}
	if (f.VideoCodec == "" && f.AudioCodec == "") || (f.Duration <= 0 || math.IsNaN(f.Duration) || math.IsInf(f.Duration, 0)) {
		return Facts{}, ErrNoTimedStream
	}
	return f, nil
}

// FFmpeg reports aliases separated by commas. Preserve the demuxer's canonical
// name; filename extensions are never media identity.
func demuxerIdentity(raw string) string {
	name, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(raw)), ",")
	return streamText(name, 64)
}

// hasMotionVideo reports a video stream that is not a still image: cover art
// is set aside before this (AttachedPicture), and a chapter-image track uses an
// image codec.
func hasMotionVideo(f Facts) bool {
	for _, st := range f.Streams {
		if st.Type != "video" {
			continue
		}
		switch st.Codec {
		case "mjpeg", "png", "bmp", "gif", "webp", "tiff", "jpegls":
			continue
		}
		return true
	}
	return false
}

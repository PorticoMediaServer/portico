package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"strconv"
	"strings"
	"time"
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
	DiscSelection       *DiscMediaSelection  `json:"-"`
	MusicCueTracks      []Chapter            `json:"-"`
	VideoName           naming.Embedded      `json:"-"`
	VideoNameSkipped    bool                 `json:"-"`
	ArtworkObserved     bool                 `json:"-"`
	AnalysisAlgorithm   string               `json:"-"`
	SubtitleTextPending bool                 `json:"-"`
	VideoMetadata       []VideoNFO           `json:"-"`
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
	EmbeddedAudio       *audiotags.Tags      `json:"-"`
	AttachedPicture     bool                 `json:"-"`
	ArtworkKey          string               `json:"-"`
	// ArtworkExtra carries sidecar cache keys (localmetadata.ReadSidecars:
	// "show/poster", "season/2/poster", "artist/portrait", "item/logo",
	// "album/cover") for the catalog commit to write as local_artwork rows.
	ArtworkExtra              map[string]string       `json:"-"`
	ArtworkCandidates         []LocalArtworkCandidate `json:"-"`
	ArtworkCandidatesObserved bool                    `json:"-"`
	LocalMetadataIssue        string                  `json:"-"`
	ObservedSize              int64                   `json:"-"`
	ObservedModifiedNS        int64                   `json:"-"`
	Container                 string                  `json:"container"`
	VideoCodec                string                  `json:"videoCodec"`
	AudioCodec                string                  `json:"audioCodec"`
	Width                     int                     `json:"width"`
	Height                    int                     `json:"height"`
	Duration                  float64                 `json:"duration"`
	BitRate                   int64                   `json:"-"`
	AudioEvidence             []AudioTagEvidence      `json:"-"`
	AnalysisSourceEvidence    string                  `json:"-"`
}

// DiscMediaSelection identifies the exact title input whose timed media was
// read. Both values are private scan evidence, never client display/selection IDs.
type DiscMediaSelection struct {
	TitleKey, StructureFingerprint string
	Members                        []storage.DiscMember
}
type Source struct {
	ID string `json:"id"`
	Facts
}
type Probe struct {
	ReadGuard  func(context.Context, string) error
	Binary     string
	Supervisor *storage.Supervisor
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
	args := append(options, []string{"-v", "error", "-show_format", "-show_streams", "-show_chapters", "-show_entries", "format:stream:chapter:format_tags=title,show,tv_show,year,date,season_number,episode_sort,edition,imdb,imdbid,tmdb,tmdbid,tvdb,tvdbid,anilist,anilistid:stream_tags=language,title:chapter_tags=title", "-of", "json", input}...)
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
	runScan := storage.RunScanCommand
	if directory {
		// A disc folder is one admitted directory descriptor; the sandbox
		// mounts it at its own root and the tool never sees a host path.
		runScan = func(ctx context.Context, binary string, args []string, path string, out io.Writer) (bool, error) {
			return storage.RunDiscDirectoryCommand(ctx, binary, args, path, out)
		}
	} else if input != path {
		// Only the validated disc reader uses a prefixed input. The storage
		// bridge accepts exactly bluray:path and retains that prefix on FD3.
		runScan = func(ctx context.Context, binary string, args []string, path string, out io.Writer) (bool, error) {
			return storage.RunDiscScanCommand(ctx, binary, args, path, true, out)
		}
	}
	if handled, err := runScan(ctx, bin, args, path, out); handled {
		runErr = err
	} else if directory {
		// A disc folder has no path-based fallback: without the admitted
		// descriptor mount it stays unreadable.
		return nil, fmt.Errorf("media probe failed: %w", storage.ErrInventoryChanged)
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
				f.Streams = append(f.Streams, Stream{Index: *s.Index, Type: s.Type, Codec: streamText(s.Codec, 64), Language: streamText(s.Tags["language"], 32), Title: streamText(s.Tags["title"], 200), Channels: s.Channels, ChannelLayout: streamText(s.ChannelLayout, 64), Default: s.Disposition.Default != 0, Forced: s.Disposition.Forced != 0, ColorTransfer: streamText(s.ColorTransfer, 32), ColorPrimaries: streamText(s.ColorPrimaries, 32), HearingImpaired: s.Disposition.HearingImpaired != 0, Commentary: s.Disposition.Comment != 0, Detail: factDetail})
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
	if f.VideoCodec != "" {
		f.VideoName, f.VideoNameSkipped = EmbeddedVideoName(raw.Format.Tags)
	}
	return f, nil
}

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
	if !strings.Contains(path, "://") {
		tags, err := storage.ReadAudioTags(ctx, path)
		if err == nil {
			f.EmbeddedAudio = &tags
			CopyEmbeddedAudioTags(&f, tags.JSONValues())
			if len(tags.Chapters) > 0 {
				f.Chapters = nil
				for _, c := range tags.Chapters {
					f.Chapters = append(f.Chapters, Chapter{streamText(c.Title, 500), c.Start, c.End})
				}
			}
			if len(tags.Warnings) > 0 {
				f.LocalMetadataIssue = "invalid_audio_tags"
			}
		} else {
			f.LocalMetadataIssue = "unreadable_audio_tags"
		}
	}
	f.Chapters, f.ChapterStatus = CleanChapters(f.Chapters, f.Duration)
	if f.ChapterStatus == "partial" {
		f.LocalMetadataIssue = "invalid_chapter_timeline"
	}
	if (f.VideoCodec == "" && f.AudioCodec == "") || (f.Duration <= 0 || math.IsNaN(f.Duration) || math.IsInf(f.Duration, 0)) {
		return Facts{}, errors.New("file has no timed media stream")
	}
	return f, nil
}
func SupportedPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wmv", ".asf", ".flv", ".f4v", ".3gp", ".3g2", ".ogv", ".ogm", ".mxf", ".dv", ".rm", ".rmvb", ".m2v", ".m1v", ".y4m", ".nut", ".nsv", ".fli", ".flc", ".bik", ".smk", ".mjpeg", ".mjpg", ".264", ".h264", ".265", ".hevc", ".h266", ".vvc", ".mp4", ".m4v", ".mov", ".mkv", ".webm", ".avi", ".ts", ".m2ts", ".mts", ".vob", ".mpg", ".mpeg", ".strm", ".mp3", ".m4a", ".m4b", ".flac", ".ogg", ".opus", ".wav", ".aac", ".aiff", ".aif", ".oga", ".mka", ".wma", ".ape", ".wv", ".tta", ".caf", ".w64", ".amr", ".dsf", ".dff", ".mpc":
		return true
	}
	return false
}
func Open(db *sql.DB, id string) (*os.File, error) {
	v, e := ResolveCatalogPlaybackSource(context.Background(), db, id)
	if e != nil {
		return nil, e
	}
	return storage.OpenLocalPlaybackFile(v.Path, v.Size, v.ModifiedNS, v.Binding)
}

// DiscImage reports whether this path is a disc image the bounded disc reader
// can open as one title (VIDEO §3 VS-4a). The catalogue classifies it as video
// media and the analysis probes its main title, never its ISO file structure.
func DiscImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".iso", ".img":
		return true
	}
	return false
}

func SupportedForKind(path, kind string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	if kind == "book" {
		return extension == ".epub" || extension == ".pdf" || extension == ".mobi" || extension == ".azw3"
	}
	if kind == "comic" {
		if extension == ".cbz" || extension == ".pdf" {
			return true
		}
		return extension == ".cb7" && readingcap.CB7() || extension == ".cbr" && readingcap.CBR()
	}
	audio := false
	switch extension {
	case ".mp3", ".m4a", ".m4b", ".flac", ".ogg", ".opus", ".wav", ".aac", ".aiff", ".aif", ".oga", ".mka", ".wma", ".ape", ".wv", ".tta", ".caf", ".w64", ".amr", ".dsf", ".dff", ".mpc":
		audio = true
	}
	if kind == "music" || kind == "audiobook" {
		return audio || extension == ".mp4"
	}
	return SupportedPath(path) && !audio
}

// OpenPlayback acquires the exact accepted local object and checks pinned size/mtime.
func OpenPlayback(db *sql.DB, session, grantHash string) (*os.File, error) {
	v, e := ResolvePlaybackSource(context.Background(), db, session, grantHash)
	if e != nil {
		return nil, e
	}
	return storage.OpenLocalPlaybackFile(v.Path, v.Size, v.ModifiedNS, v.Binding)
}

// FFmpeg reports aliases separated by commas. Preserve the demuxer's canonical
// name; filename extensions are never media identity.
func demuxerIdentity(raw string) string {
	name, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(raw)), ",")
	return streamText(name, 64)
}

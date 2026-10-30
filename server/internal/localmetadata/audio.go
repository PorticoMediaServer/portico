package localmetadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
)

type Service struct {
	artworkCandidateObservations map[string]observedArtworkImage
	ArtworkLimit                 func(context.Context) int64
	artworkBytes                 int64
	artworkAccounted             bool
	lastArtworkIssue             string
	root, binary                 string
	storage                      *storage.Client
	mu                           sync.Mutex
	// sidecars caches sidecar-file reads by full path (positive hits only,
	// bounded): one show's hundred episodes share a single poster read.
	sidecars map[string]string
	// readSmall reads one small file; storage in production, a fake in tests.
	readSmall func(ctx context.Context, library, path string, limit int64) ([]byte, error)
}

func New(root, binary string, store *storage.Client) (*Service, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	s := &Service{root: root, binary: binary, storage: store}
	if store != nil {
		s.readSmall = store.ReadSmall
	}
	return s, nil
}

func (s *Service) Prepare(ctx context.Context, library, path string, f assets.Facts) assets.Facts {
	return s.PrepareKind(ctx, library, "music", "", path, f)
}

// PrepareKind adds a music or audiobook file's local evidence: sidecars, cover
// and, for an audiobook, cue/sidecar chapters and what its folders under the
// library root say (root may be empty when unknown).
func (s *Service) PrepareKind(ctx context.Context, library, kind, root, path string, f assets.Facts) assets.Facts {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Prepare the file's fallback once; a refresh never has to reopen its media.
	// Its extraction issue matters only when a sidecar does not supply the cover.
	fileIssue := f.LocalMetadataIssue
	f = s.prepareEmbeddedAudioArtwork(ctx, library, path, f)
	artworkIssue := ""
	if f.LocalMetadataIssue != fileIssue {
		artworkIssue = f.LocalMetadataIssue
	}
	f.LocalMetadataIssue = fileIssue
	var musicCueTracks []assets.Chapter
	if kind == "music" && (strings.EqualFold(filepath.Ext(path), ".flac") || strings.EqualFold(filepath.Ext(path), ".wav")) {
		// Retain the original cue observations. The overlay validates the chosen
		// timeline after sidecar precedence, just as a full analysis does.
		musicCueTracks = embeddedMusicCue(path, f)
	}
	tags := make(map[string]string, len(f.Tags))
	for k, v := range f.Tags {
		tags[k] = v
	}
	f.AudioFileMetadata = &assets.AudioFileMetadata{Tags: tags, MusicCueTracks: musicCueTracks, Chapters: append([]assets.Chapter(nil), f.Chapters...), ChapterStatus: f.ChapterStatus, ArtworkKey: f.ArtworkKey, Issue: fileIssue, ArtworkIssue: artworkIssue}
	result := s.PrepareAudioSidecars(ctx, library, kind, root, path, f)
	// An unavailable embedded cover is not a complete baseline. Preserve the
	// ordinary full-probe retry path rather than promise a fallback we lack.
	if f.ArtworkKey == "" && (f.AttachedPicture || f.EmbeddedAudio != nil && len(f.EmbeddedAudio.Pictures) > 0) {
		result.AudioFileMetadata = nil
	}
	return result
}

// PrepareAudioSidecars overlays only local sidecars on the file-owned baseline.
func (s *Service) PrepareAudioSidecars(ctx context.Context, library, kind, root, path string, f assets.Facts) assets.Facts {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	fallback := f.ArtworkKey
	f.ArtworkKey = ""
	f = s.prepareAudioEvidence(ctx, library, kind, root, path, f)
	if s.storage != nil {
		if raw, e := s.storage.ReadAudioCover(ctx, library, path); e == nil {
			if key, e := s.store(ctx, raw); e == nil {
				f.ArtworkKey = key
				return f
			} else {
				f.LocalMetadataIssue = "invalid_local_cover"
			}
		}
	}
	coverNames := []string{"cover.jpg", "folder.jpg", "front.jpg", "album.jpg", "cover.jpeg", "folder.jpeg", "cover.webp", "folder.webp", "front.webp", "cover.png", "folder.png", "front.png", "Cover.JPG", "Folder.jpg"}
	if kind == "music" {
		coverNames = musicCoverNames()
	}
	for _, name := range coverNames {
		if unlisted(ctx, filepath.Join(filepath.Dir(path), name)) {
			continue
		}
		raw, err := s.read(ctx, library, filepath.Join(filepath.Dir(path), name), 4<<20)
		if err != nil {
			continue
		}
		if key, err := s.store(ctx, raw); err == nil {
			f.ArtworkKey = key
			return f
		}
		f.LocalMetadataIssue = "invalid_local_cover"
	}
	f.ArtworkKey = fallback
	if f.AudioFileMetadata != nil && f.AudioFileMetadata.ArtworkIssue != "" {
		f.LocalMetadataIssue = f.AudioFileMetadata.ArtworkIssue
	}
	return f
}

func (s *Service) prepareEmbeddedAudioArtwork(ctx context.Context, library, path string, f assets.Facts) assets.Facts {
	if f.EmbeddedAudio != nil {
		pictures := f.EmbeddedAudio.Pictures
		for _, kind := range []int{3, -1} {
			for _, pic := range pictures {
				if kind != -1 && pic.Type != kind {
					continue
				}
				if key, e := s.store(ctx, pic.Data); e == nil {
					f.ArtworkKey = key
					return f
				}
			}
		}
	}
	if f.AttachedPicture && s.binary != "" {
		if s.storage.IsRemote(path) {
			f.LocalMetadataIssue = "remote_embedded_artwork_not_prepared"
			return f
		}
		if err := storage.CheckScanRead(ctx, path); err != nil {
			return f
		}
		// A malformed file that makes ffmpeg hang used to block this goroutine
		// forever: no context, no WaitDelay. Both now, matching the probe path.
		extract, cancelExtract := context.WithTimeout(ctx, 2*time.Minute)
		defer cancelExtract()
		args := []string{"-nostdin", "-v", "error", "-protocol_whitelist", "file,pipe", "-format_whitelist", "mov,matroska,mp3,flac,ogg,wav,aac,asf,aiff,ape,wv", "-i", path, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale=1024:1024:force_original_aspect_ratio=decrease", "-c:v", "mjpeg", "-f", "image2pipe", "pipe:1"}
		var raw []byte
		var buffer bytes.Buffer
		handled, err := storage.RunScanCommand(extract, s.binary, args, path, &buffer)
		if handled {
			raw = buffer.Bytes()
		} else {
			// Outside a scan: the one library file, read-only, through the media
			// executor (mediaexec), at background priority.
			var cmd *exec.Cmd
			if cmd, err = mediaexec.Command(mediaexec.Job{Executable: s.binary, Args: args, ReadPaths: []string{path}, Background: true}); err == nil {
				cmd.WaitDelay = 2 * time.Second
				err = s.storage.Supervisor.Run(extract, library+":art", cmd, func(r io.Reader) error {
					var err error
					raw, err = io.ReadAll(io.LimitReader(r, (4<<20)+1))
					if len(raw) > 4<<20 {
						return errors.New("artwork exceeds limit")
					}
					return err
				})
			}
		}
		if err == nil {
			if key, err := s.store(ctx, raw); err == nil {
				f.ArtworkKey = key
			} else {
				f.LocalMetadataIssue = "invalid_embedded_cover"
			}
		}
	}
	return f
}
func (s *Service) store(ctx context.Context, raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 4<<20 {
		return "", errors.New("artwork exceeds limit")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") || config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return "", errors.New("invalid local artwork")
	}
	digest := sha256.Sum256(raw)
	key := hex.EncodeToString(digest[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	target := filepath.Join(s.root, key+".img")
	if _, err = os.Stat(target); err == nil {
		return key, nil
	}
	if err = s.accountArtworkLocked(ctx); err != nil {
		return "", err
	}
	limit := int64(2 << 30)
	if s.ArtworkLimit != nil {
		limit = s.ArtworkLimit(context.Background())
	}
	if s.artworkBytes+int64(len(raw)) > limit {
		if s.lastArtworkIssue != "capacity" {
			log.Printf("Local artwork has reached its storage limit. Raise the local artwork limit in metadata settings to accept more images.")
			s.lastArtworkIssue = "capacity"
		}
		return "", errors.New("local artwork cache full")
	}
	file, err := os.CreateTemp(s.root, "art-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), target)
	}
	if err == nil {
		s.artworkBytes += int64(len(raw))
		s.lastArtworkIssue = ""
	}
	return key, err
}

var keyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Service) Open(key string) (*os.File, string, error) {
	if !keyPattern.MatchString(key) {
		return nil, "", errors.New("invalid artwork key")
	}
	file, err := os.Open(filepath.Join(s.root, key+".img"))
	if err != nil {
		return nil, "", err
	}
	var header [512]byte
	n, err := file.Read(header[:])
	if err != nil && err != io.EOF {
		file.Close()
		return nil, "", err
	}
	_, _ = file.Seek(0, 0)
	return file, http.DetectContentType(header[:n]), nil
}

func (s *Service) MovieArtwork(ctx context.Context, library, path string, f assets.Facts) assets.Facts {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for _, name := range videoArtworkNames(stem+"-poster", stem, "poster", "folder", "cover", "default", "movie") {
		if key, ok := s.sidecarKey(ctx, library, filepath.Join(filepath.Dir(path), name)); ok {
			f.ArtworkKey = key
			return f
		}
	}
	return f
}

// ArtworkUsage returns the accounting already used by ingestion. A console
// request never waits behind the first directory measurement or image write.
// Unknown means ingestion has not measured it yet (or is currently updating it).
func (s *Service) ArtworkUsage() (int64, bool) {
	if s == nil || !s.mu.TryLock() {
		return 0, false
	}
	defer s.mu.Unlock()
	return s.artworkBytes, s.artworkAccounted
}

// Package prepare makes the smaller copies people download: one file of a version at a
// quality, made once and shared by everyone who asks for it. Downloads is its first caller;
// nothing else makes copies of its own.
//
// A copy is a prepared_media row and a file in the prepared folder named by the row's public
// id. Asking makes the row, waiting; the copies wait their turn and the owner says how many are
// made at once. Each one is converted by the sandboxed encoder at background priority, so it
// runs behind people who are watching. A copy cut short by a restart is made again from the
// start. What is running, and how far it has got, lives in memory.
package prepare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/processpool"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

var (
	// ErrNoConverter says this server cannot make copies: it has no working ffmpeg.
	ErrNoConverter = errors.New("this server can't make smaller copies")
	// ErrSpaceFull says the owner's download space has no room for another copy.
	ErrSpaceFull = errors.New("the server's download space is full")
	// ErrCannot says this version cannot be made at that quality.
	ErrCannot = errors.New("this title can't be prepared at that quality")
)

// Options say where the service works and what it reads.
type Options struct {
	DB *db.DB
	// Folder holds the copies. Settings holds the owner's downloads document.
	Folder   string
	Settings *settings.Settings
	// FFmpeg is the encoder; empty finds the bundled or installed one.
	FFmpeg string
	// Remote opens a remote source by id, for a version kept on remote storage.
	Remote func(ctx context.Context, sourceID int64) (remote.Storage, time.Duration, error)
	Log    *slog.Logger
	Now    func() time.Time
}

// Service makes and keeps the copies.
type Service struct {
	Options
	processes *processpool.Supervisor
	wake      chan struct{}

	mu sync.Mutex
	// running is every copy being made now, by row id.
	running map[int64]*making
	// usable says the encoder was found; found once.
	usable *bool
	// warnedFull is when the log last said copies wait for disk room.
	warnedFull time.Time
}

// making is one copy being made.
type making struct {
	done, total int64
	stop        context.CancelFunc
}

// New makes the service. Run must be called for copies to be made.
func New(o Options) (*Service, error) {
	if o.FFmpeg == "" {
		o.FFmpeg = mediatools.Resolve("ffmpeg")
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(o.Folder, 0o700); err != nil {
		return nil, err
	}
	s := &Service{Options: o, processes: &processpool.Supervisor{}, wake: make(chan struct{}, 1), running: map[int64]*making{}}
	Document.Watch(o.Settings, func(Settings) { s.kick() })
	return s, nil
}

// Wake looks for copies to make now: a request was resumed or retried.
func (s *Service) Wake() { s.kick() }

func (s *Service) kick() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Converts says whether this server can make copies at all.
func (s *Service) Converts() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.usable == nil {
		path, err := exec.LookPath(s.FFmpeg)
		usable := err == nil
		if usable {
			s.FFmpeg = path
		}
		s.usable = &usable
	}
	return *s.usable
}

// Want returns the copy of a version at a recipe, asking for it to be made if nobody has.
// Asking again for one that exists keeps it longer. A new copy is refused when this server
// cannot convert or the owner's download space has no room for it.
func (s *Service) Want(ctx context.Context, v db.Version, kind db.ItemKind, recipe string) (db.Prepared, error) {
	mediaID := v.MediaID()
	if !slices.Contains(Recipes(v, kind, Document.Get(s.Settings).HighestQuality), recipe) || len(v.Parts) == 0 {
		return db.Prepared{}, ErrCannot
	}
	if p, err := s.DB.FindPrepared(ctx, mediaID, recipe); err == nil {
		return p, s.DB.UsePrepared(ctx, p.ID, s.Now())
	} else if !errors.Is(err, db.ErrNotFound) {
		return db.Prepared{}, err
	}
	if !s.Converts() {
		return db.Prepared{}, ErrNoConverter
	}
	if room, limited, err := s.Room(ctx); err != nil {
		return db.Prepared{}, err
	} else if limited && EstimatedSize(v, recipe) > room {
		return db.Prepared{}, ErrSpaceFull
	}
	p, err := s.DB.WantPrepared(ctx, mediaID, recipe, EstimatedSize(v, recipe), s.Now())
	s.kick()
	return p, err
}

// Room is how much of the owner's download space is left; limited is false when there is no
// limit.
func (s *Service) Room(ctx context.Context) (room int64, limited bool, err error) {
	limit := Document.Get(s.Settings).SpaceGB * 1_000_000_000
	if limit == 0 {
		return 0, false, nil
	}
	used, err := s.DB.PreparedSpace(ctx)
	return max(limit-used, 0), true, err
}

// Progress says how far a copy being made has got, in bytes written of about how many.
func (s *Service) Progress(id int64) (done, total int64, running bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.running[id]
	if m == nil {
		return 0, 0, false
	}
	return m.done, m.total, true
}

// Stop ends the making of a copy nobody wants made now. It waits for its turn again.
func (s *Service) Stop(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.running[id]; m != nil {
		m.stop()
	}
}

// Files are the files of a ready copy, one for each part of its version, in order.
func (s *Service) Files(p db.Prepared) []string {
	hashes := strings.Fields(p.ContentHash)
	if len(hashes) <= 1 {
		return []string{filepath.Join(s.Folder, p.PublicID+"."+p.Container)}
	}
	out := make([]string, len(hashes))
	for n := range hashes {
		out[n] = filepath.Join(s.Folder, fmt.Sprintf("%s-%d.%s", p.PublicID, n+1, p.Container))
	}
	return out
}

// Run makes the copies that wait, as many at once as the owner allows, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		s.fill(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tick.C:
		}
	}
}

func (s *Service) fill(ctx context.Context) {
	limit := Document.Get(s.Settings).ConversionsAtOnce
	for ctx.Err() == nil {
		s.mu.Lock()
		busy := slices.Collect(maps.Keys(s.running))
		s.mu.Unlock()
		if len(busy) >= limit {
			return
		}
		p, err := s.DB.NextPrepared(ctx, busy)
		if err != nil {
			if !errors.Is(err, db.ErrNotFound) && ctx.Err() == nil {
				s.Log.Error("reading the copies waiting to be made failed", "error", err)
			}
			return
		}
		if !s.diskRoom(ctx, p) {
			return // tried again at the next tick
		}
		work, stop := context.WithCancel(ctx)
		s.mu.Lock()
		s.running[p.ID] = &making{stop: stop}
		s.mu.Unlock()
		go func() {
			defer s.kick()
			defer stop()
			s.make(work, p)
			s.mu.Lock()
			delete(s.running, p.ID)
			s.mu.Unlock()
		}()
	}
}

// freeSpace answers how much room is left where the copies are written; tests replace it.
var freeSpace = diskspace.Free

// diskRoom says whether the disk holding the copies (and the database beside them) has room
// for this copy and diskspace.ProducerFloor more. Whatever the owner's download space, even
// none, a copy never fills that disk: without room it waits, and the log says so at most
// once an hour. A disk that cannot be measured is not taken for a full one.
func (s *Service) diskRoom(ctx context.Context, p db.Prepared) bool {
	free, err := freeSpace(s.Folder)
	if err != nil || free >= diskspace.ProducerFloor+p.Size {
		return true
	}
	s.mu.Lock()
	warn := s.Now().Sub(s.warnedFull) > time.Hour
	if warn {
		s.warnedFull = s.Now()
	}
	s.mu.Unlock()
	if warn {
		s.Log.Warn("copies for downloads wait: the disk holding them is nearly full", "folder", s.Folder, "freeBytes", free,
			"copyBytes", p.Size)
	}
	return false
}

// make makes one copy and records how it went. A copy stopped because the server is stopping,
// or because nobody wants it now, is left to be made again later.
func (s *Service) make(ctx context.Context, p db.Prepared) {
	if err := s.DB.SetPreparedState(ctx, p.ID, db.PreparedPreparing, ""); err != nil {
		return
	}
	container, size, hash, err := s.convert(ctx, p)
	write := context.WithoutCancel(ctx)
	switch {
	case ctx.Err() != nil:
		s.DB.SetPreparedState(write, p.ID, db.PreparedWaiting, "")
	case err != nil:
		s.Log.Warn("a copy for downloading could not be made", "copy", p.PublicID, "error", err)
		s.DB.SetPreparedState(write, p.ID, db.PreparedFailed, reason(err))
	default:
		if err := s.DB.FinishPrepared(write, p.ID, container, size, hash, s.Now()); err != nil {
			s.Log.Error("a finished copy could not be recorded", "copy", p.PublicID, "error", err)
		}
	}
}

// reason is why a copy failed, as a person reads it.
func reason(err error) string {
	switch {
	case errors.Is(err, ErrCannot):
		return "This title can't be made smaller at this quality."
	case errors.Is(err, errSource):
		return "The file for this title can't be reached right now."
	}
	return "The server couldn't convert this title."
}

// ErrSource says a library file cannot be opened now: gone, moved or unreachable.
var ErrSource = errors.New("the file for this title can't be reached right now")

var errSource = ErrSource

// convert makes the file of each part of the version, then puts them in place.
func (s *Service) convert(ctx context.Context, p db.Prepared) (container string, size int64, hash string, err error) {
	v, kind, err := s.DB.PreparedVersion(ctx, p.MediaID)
	if err != nil {
		return "", 0, "", errSource
	}
	total := EstimatedSize(v, p.Recipe)
	s.mu.Lock()
	if m := s.running[p.ID]; m != nil {
		m.total = total
	}
	s.mu.Unlock()
	var hashes, temps []string
	defer func() {
		for _, t := range temps {
			os.Remove(t)
		}
	}()
	var written int64
	for n, part := range v.Parts {
		recipe, ext, ok := recipeFor(p.Recipe, kind, v, part)
		if !ok {
			return "", 0, "", ErrCannot
		}
		container = ext
		temp := filepath.Join(s.Folder, fmt.Sprintf("%s-%d.part", p.PublicID, n+1))
		temps = append(temps, temp)
		sum, n, err := s.convertPart(ctx, p, part, recipe, temp, written)
		if err != nil {
			return "", 0, "", err
		}
		written += n
		hashes = append(hashes, sum)
	}
	ready := db.Prepared{PublicID: p.PublicID, Container: container, ContentHash: strings.Join(hashes, " ")}
	for n, final := range s.Files(ready) {
		if err := os.Rename(temps[n], final); err != nil {
			return "", 0, "", err
		}
	}
	if !Music(kind) {
		for n, part := range v.Parts {
			s.writeSubtitles(ctx, p, part, n+1)
		}
	}
	return container, written, ready.ContentHash, nil
}

// convertPart converts one part into a file and returns its SHA-256 and size. before is what
// earlier parts wrote, for the progress.
func (s *Service) convertPart(ctx context.Context, p db.Prepared, part db.Part, recipe decoder.PreparedRecipe, temp string,
	before int64) (string, int64, error) {
	source, size, _, err := s.OpenFile(ctx, part.FileID)
	if err != nil {
		return "", 0, err
	}
	defer source.Close()
	bridge, err := inputbridge.Open(ctx, source, size)
	if err != nil {
		return "", 0, errSource
	}
	defer bridge.Close()
	reservation, err := bridge.Reserve()
	if err != nil {
		return "", 0, err
	}
	out, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()
	// The encoder's sandbox holds this while it runs, so it is known to have ended.
	held, err := os.Open(temp)
	if err != nil {
		return "", 0, err
	}
	defer held.Close()
	sum := sha256.New()
	counter := &counting{w: io.MultiWriter(out, sum), add: func(n int64) {
		s.mu.Lock()
		if m := s.running[p.ID]; m != nil {
			m.done = before + n
		}
		s.mu.Unlock()
	}}
	err = decoder.RunPreparedEncode(ctx, s.processes, p.PublicID+":"+strconv.FormatInt(part.FileID, 10), s.FFmpeg, bridge.URL(), reservation,
		held, recipe, counter)
	if err == nil {
		err = out.Sync()
	}
	if err == nil && counter.n == 0 {
		err = errors.New("the encoder wrote nothing")
	}
	return hex.EncodeToString(sum.Sum(nil)), counter.n, err
}

// Source is a library file opened for reading anywhere in it.
type Source interface {
	io.ReaderAt
	io.Closer
}

// OpenFile opens a library file by the scanner's record of it, never by anything a request
// said: on a disk here through libraryfile, or on remote storage through its source. A file
// that changed size since the scan is refused. It returns the size and the modified time the
// scanner recorded.
func (s *Service) OpenFile(ctx context.Context, fileID int64) (Source, int64, time.Time, error) {
	place, err := s.DB.FileOnDisk(ctx, fileID)
	if err == nil {
		folders, err := s.DB.LibraryFolders(ctx)
		if err != nil {
			return nil, 0, time.Time{}, err
		}
		file, err := libraryfile.Open(place.Path, folders)
		if err != nil {
			return nil, 0, time.Time{}, errSource
		}
		if info, err := file.Stat(); err != nil || info.Size() != place.Size {
			file.Close()
			return nil, 0, time.Time{}, errSource
		}
		return file, place.Size, time.Unix(0, place.ModifiedNS), nil
	}
	if !errors.Is(err, db.ErrNotFound) || s.Remote == nil {
		return nil, 0, time.Time{}, errSource
	}
	sourceID, place, err := s.DB.RemoteFileOf(ctx, fileID)
	if err != nil {
		return nil, 0, time.Time{}, errSource
	}
	storage, _, err := s.Remote(ctx, sourceID)
	if err != nil {
		return nil, 0, time.Time{}, errSource
	}
	file, err := storage.Open(ctx, place.Path)
	if err != nil || file.Size() != place.Size {
		if file != nil {
			file.Close()
		}
		return nil, 0, time.Time{}, errSource
	}
	return file, place.Size, time.Unix(0, place.ModifiedNS), nil
}

type counting struct {
	w   io.Writer
	n   int64
	add func(int64)
}

func (c *counting) Write(b []byte) (int, error) {
	n, err := c.w.Write(b)
	c.n += int64(n)
	c.add(c.n)
	return n, err
}

// Tidy removes copies nobody has asked for or fetched within the owner's keeping time, and
// any file in the prepared folder that no copy names. A download that waited for a removed
// copy reads as expired, with Try again. Copies already on people's devices are not touched.
func (s *Service) Tidy(ctx context.Context) error {
	keep := time.Duration(Document.Get(s.Settings).KeepDays) * 24 * time.Hour
	for ctx.Err() == nil {
		unused, err := s.DB.UnusedPrepared(ctx, s.Now().Add(-keep), 100)
		if err != nil || len(unused) == 0 {
			if err != nil {
				return err
			}
			break
		}
		ids := make([]int64, len(unused))
		for n, p := range unused {
			ids[n] = p.ID
		}
		// The rows go first: a file left behind is swept below, a row left without its file
		// would be handed out.
		if err := s.DB.RemovePrepared(ctx, ids); err != nil {
			return err
		}
		for _, p := range unused {
			// The copy's files and the subtitles written beside it.
			matches, _ := filepath.Glob(filepath.Join(s.Folder, p.PublicID+"[.-]*"))
			for _, file := range matches {
				os.Remove(file)
			}
		}
	}
	names, err := s.DB.PreparedNames(ctx)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(s.Folder)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// A file made in the last hour may belong to a copy asked for since the names were read.
		if info, err := entry.Info(); err != nil || s.Now().Sub(info.ModTime()) < time.Hour {
			continue
		}
		id, _, _ := strings.Cut(entry.Name(), ".")
		id, _, _ = strings.Cut(id, "-")
		if !names[id] {
			os.Remove(filepath.Join(s.Folder, entry.Name()))
		}
	}
	return nil
}

// Package backup makes, lists, checks and removes backups of the server: its database, the key
// files that open what the database keeps sealed, the pictures people uploaded and downloaded subtitles. Never
// media, recordings, the cache, logs or conversions. A backup is one .tar.gz in the backup
// folder with a small .json beside it describing it.
//
// Backups run by themselves at the owner's hour on the owner's days (3 am every night unless
// changed), and the newest scheduled ones are kept (7 unless changed); one made with Back up
// now stays until the owner deletes it. A backup is made while the server is in use: the
// database is copied a few pages at a time behind people's writes (db.BackupTo).
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Settings is the owner's `backups` settings document.
type Settings struct {
	// Days are the weekdays scheduled backups run on, 0 for Sunday; none is Never.
	Days []int `json:"days"`
	// Hour is the hour of the day they run at, in the server's time zone.
	Hour int `json:"hour"`
	// Keep is how many scheduled backups are kept, 1 to 365. Backups made with Back up now are
	// not counted and stay until deleted.
	Keep int `json:"keep"`
	// Folder is where backups are kept; empty is the backups folder in the data folder.
	Folder string `json:"folder"`
	// IncludeArtwork adds the pictures Portico could download again.
	IncludeArtwork bool `json:"includeArtwork"`
}

// Document is the backups settings document.
var Document = settings.Define("backups", Settings{Days: []int{0, 1, 2, 3, 4, 5, 6}, Hour: 3, Keep: 7}, func(s *Settings) error {
	slices.Sort(s.Days)
	s.Days = slices.Compact(s.Days)
	for _, d := range s.Days {
		if d < 0 || d > 6 {
			return &settings.Invalid{Field: "days", Message: "Days are 0 (Sunday) to 6 (Saturday)."}
		}
	}
	if s.Hour < 0 || s.Hour > 23 {
		return &settings.Invalid{Field: "hour", Message: "Choose an hour from 0 to 23."}
	}
	if s.Keep < 1 || s.Keep > 365 {
		return &settings.Invalid{Field: "keep", Message: "Keep 1 to 365 backups."}
	}
	s.Folder = strings.TrimSpace(s.Folder)
	if s.Folder != "" {
		if !filepath.IsAbs(s.Folder) {
			return &settings.Invalid{Field: "folder", Message: "Choose a folder by its full path."}
		}
		s.Folder = filepath.Clean(s.Folder)
	}
	// A check also runs when the server starts, so it looks at nothing outside the value: a
	// backup drive not mounted yet keeps the owner's settings. Whether the folder can be
	// written is answered by the backups page and found by each backup.
	return nil
})

// writable checks a file can be written in an existing folder.
func writable(folder string) error {
	f, err := os.CreateTemp(folder, ".portico-check-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

// folderLimit bounds how long the backup folder may take to answer: a check of it, and
// writing one backup into it. A drive that has gone away fails the backup, never the server.
var (
	folderCheckLimit = 5 * time.Second
	folderWriteLimit = 2 * time.Hour
)

// folderHook, set by tests, stands for a backup drive that does not answer.
var folderHook func(folder string)

// folderWork is the work on a backup folder still under way, which can outlast its limit: a
// drive that answers late lets it carry on and fail. Tests wait for it.
var folderWork sync.WaitGroup

// ErrFolderSlow: the backup folder did not answer in time.
var ErrFolderSlow = errors.New("the backup folder did not answer in time")

// within runs fn in its own goroutine and gives up waiting after limit: a call into a hung
// network drive can block for ever, and only that goroutine waits on it.
func within(ctx context.Context, limit time.Duration, fn func() error) error {
	done := make(chan error, 1)
	folderWork.Go(func() { done <- fn() })
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return ErrFolderSlow
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FolderWritable says whether a backup could be written to the backup folder now.
func (s *Service) FolderWritable() bool { return s.FolderProblem() == nil }

// FolderProblem is why a backup could not be written to the backup folder now, or nil.
func (s *Service) FolderProblem() error {
	return within(context.Background(), folderCheckLimit, func() error { return s.prepareFolder(s.Folder()) })
}

// prepareFolder makes the default folder inside the data folder if it is missing, and checks
// a chosen one exists and can be written. A chosen folder is never made: a drive that is not
// mounted must not be replaced by a folder on the disk beneath it.
func (s *Service) prepareFolder(folder string) error {
	if Document.Get(s.Settings).Folder == "" {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			return err
		}
	} else if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return fmt.Errorf("the backup folder %s is not there: is its drive connected?", folder)
	}
	if err := writable(folder); err != nil {
		return fmt.Errorf("Portico cannot write to the backup folder %s", folder)
	}
	return nil
}

// Credentials name the working credentials a backup holds, for the owner's page to say so.
const (
	// CredentialServerKeys: the keys the server signs sign-ins with and proves who it is by.
	CredentialServerKeys = "server_keys"
	// CredentialSourceSignIns: remote storage sources' passwords and keys, sealed, with the
	// key file that opens them.
	CredentialSourceSignIns = "source_sign_ins"
	// CredentialKeyFile: a key file that opens what the database keeps sealed.
	CredentialKeyFile = "key_file"
)

// keyFiles are the key files beside the database that open what it keeps sealed.
var keyFiles = []string{"remote-sources.key", "secrets.key"}

// Backup is one backup as its description says.
type Backup struct {
	ID          string    `json:"id"`
	CreatedAt   time.Time `json:"createdAt"`
	Scheduled   bool      `json:"scheduled"`
	Version     string    `json:"version"`
	Size        int64     `json:"size"`
	Credentials []string  `json:"credentials"`
	// Pictures is how many pictures it holds.
	Pictures int  `json:"pictures"`
	Artwork  bool `json:"artwork"`
	// Mark is made over the archive with this server's key; see mark.go.
	Mark string `json:"mark,omitempty"`
}

// Service makes and keeps backups.
type Service struct {
	DB       *db.DB
	Settings *settings.Settings
	DataDir  string
	Version  string
	Log      *slog.Logger
	Now      func() time.Time
	// Stopping ends when the server begins to stop: a backup being made is then cut off, and
	// Wait returns once it has let go of the database. Without it a backup is never cut off.
	Stopping context.Context

	making  sync.WaitGroup // the backups being made
	mu      sync.Mutex
	running *Progress
	last    error // the last scheduled backup's failure, until one succeeds
	failed  error // that failure once its retries are used up: what the owner is told
	// newest is the newest backup in the backup folder, read at start and kept as backups are
	// made and deleted, so the Dashboard never reads a backup folder that may be slow.
	newest *Backup
	known  bool // newest has been read
}

// Progress is a backup being made.
type Progress struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"startedAt"`
	// PagesLeft is how much of the database is still to copy.
	PagesLeft int `json:"pagesLeft"`
	scheduled bool
}

// ErrRunning: a backup is already being made.
var ErrRunning = errors.New("a backup is being made")

// ErrPaused says the running scheduled backup is waiting for background work to resume.
var ErrPaused = errors.New("a scheduled backup is paused")

// ErrNotFound: no backup has that id.
var ErrNotFound = errors.New("no such backup")

// Folder is where backups are kept now.
func (s *Service) Folder() string {
	if folder := Document.Get(s.Settings).Folder; folder != "" {
		return folder
	}
	return filepath.Join(s.DataDir, "backups")
}

// Running is the backup being made, or nil.
func (s *Service) Running() *Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		return nil
	}
	p := *s.running
	return &p
}

// Newest is the newest backup in the backup folder. known is false until the folder has been
// read since the server started (a backup drive that does not answer stays unknown).
func (s *Service) Newest() (b *Backup, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.newest != nil {
		copied := *s.newest
		b = &copied
	}
	return b, s.known
}

// readNewest reads the newest backup from the backup folder, within the folder's time limit.
func (s *Service) readNewest() {
	_ = within(context.Background(), folderCheckLimit, func() error {
		list, err := s.List()
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.newest, s.known = nil, true
		if len(list) > 0 {
			s.newest = &list[0]
		}
		return nil
	})
}

// LastError is why the last scheduled backup failed, while none has succeeded since.
func (s *Service) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// Failed is why scheduled backups are failing, once one has failed and been tried again for
// 4 hours without success, until a backup succeeds, scheduled or made by hand. It is what the owner is alerted to: a first
// failure is tried again quietly.
func (s *Service) Failed() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// failingSinceKey is the setting that holds when scheduled backups began to fail, in
// milliseconds, empty while they work. It is kept in the database so that the 4 hours before
// the owner is told are not begun again by every restart: a computer switched off each
// evening would otherwise never tell its owner.
const failingSinceKey = "backup.failing_since"

// failingSince is when scheduled backups began to fail, zero while they work.
func (s *Service) failingSince() time.Time {
	value, _, _ := s.DB.Setting(context.Background(), failingSinceKey)
	ms, _ := strconv.ParseInt(value, 10, 64)
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// noteFailing records the first of a run of failed scheduled backups, and forgets it when
// a backup succeeds.
func (s *Service) noteFailing(failed bool) {
	since, value := s.failingSince(), ""
	if failed == !since.IsZero() {
		return
	}
	if failed {
		value = strconv.FormatInt(s.Now().UnixMilli(), 10)
	}
	if err := s.DB.SaveSetting(context.Background(), failingSinceKey, value, s.Now()); err != nil {
		s.Log.Warn("when scheduled backups began to fail could not be recorded", "error", err)
	}
}

// forgetFailure ends the alert and what led to it: the owner has chosen Never, so no
// scheduled backup can succeed to end it.
func (s *Service) forgetFailure() {
	// The database first: what the page shows is never ahead of what survives a restart.
	s.noteFailing(false)
	s.mu.Lock()
	s.last, s.failed = nil, nil
	s.mu.Unlock()
}

// GiveUp is called when a scheduled backup has failed and its 4 hours of retries are used
// up: from now the owner is told, until a scheduled backup succeeds.
func (s *Service) GiveUp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = s.last
}

// Start begins a backup in the background and returns its id at once. scheduled backups are
// counted against the number kept.
func (s *Service) Start(scheduled bool) (string, error) {
	s.mu.Lock()
	if s.running != nil {
		paused := s.running.scheduled && !s.Settings.BackgroundPausedUntil(s.Now()).IsZero()
		s.mu.Unlock()
		if paused {
			return "", ErrPaused
		}
		return "", ErrRunning
	}
	now := s.Now()
	id := "portico-" + now.Format("20060102-150405")
	s.running = &Progress{ID: id, StartedAt: now, scheduled: scheduled}
	s.mu.Unlock()
	stopping := s.Stopping
	if stopping == nil {
		stopping = context.Background()
	}
	s.making.Go(func() {
		err := s.make(stopping, id, scheduled)
		// When backups began to fail is recorded before the backup reads as finished, so a
		// stop in between cannot leave a success with the old failure time kept (or the
		// reverse) for the next start to count from. A backup cut off by a stop records nothing.
		cutOff := stopping.Err() != nil && err != nil
		if !cutOff && (err == nil || scheduled) {
			s.noteFailing(err != nil)
		}
		s.mu.Lock()
		s.running = nil
		if cutOff {
			// Not a failure to tell the owner of: a scheduled backup cut off by a stop is
			// made again soon after the next start (see missed).
			s.mu.Unlock()
			s.Log.Info("a backup was cut off because the server is stopping", "backup", id)
			return
		}
		// Any backup that succeeds, by hand or scheduled, ends the alert: the owner who fixed
		// the folder and backed up by hand has a backup. Only a scheduled one begins it.
		if err == nil {
			s.last, s.failed = nil, nil
		} else if scheduled {
			s.last = err
		}
		s.mu.Unlock()
		if err != nil {
			s.Log.Error("a backup failed", "backup", id, "error", err)
			return
		}
		if scheduled {
			s.removeOld()
		}
	})
	return id, nil
}

// Wait returns when no backup is being made. A stopping server calls it after Stopping has
// ended, before it closes the database a backup copies from.
func (s *Service) Wait() { s.making.Wait() }

// make writes one backup: the database copied while in use and checked whole, the key files,
// and the pictures people uploaded; then its description. Nothing is in the backup folder
// under its name until all of it has been written.
func (s *Service) make(ctx context.Context, id string, scheduled bool) error {
	// The database is copied on its own disk, inside the data folder: the copy runs a step at
	// a time on the connection that writes, and a slow backup drive must never hold that.
	if free, err := diskspace.Free(s.DataDir); err == nil {
		if size := databaseSize(s.DataDir); size > 0 && free < size {
			return fmt.Errorf("the data folder's disk needs %d MB free to copy the database for a backup", size>>20)
		}
	}
	work, err := os.MkdirTemp(s.DataDir, workPrefix)
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	copyPath := filepath.Join(work, "main.db")
	if err := s.DB.BackupTo(ctx, copyPath, func(left int) {
		if scheduled {
			_ = s.Settings.WaitForBackground(ctx, s.Now)
		}
		s.mu.Lock()
		if s.running != nil {
			s.running.PagesLeft = left
		}
		s.mu.Unlock()
	}); err != nil {
		return fmt.Errorf("copying the database: %w", err)
	}
	if err := db.CheckCopy(ctx, copyPath); err != nil {
		return fmt.Errorf("the copy failed its check: %w", err)
	}
	desc := Backup{ID: id, CreatedAt: s.Now().UTC(), Scheduled: scheduled, Version: s.Version, Credentials: []string{CredentialServerKeys},
		Artwork: Document.Get(s.Settings).IncludeArtwork}
	if has, err := s.DB.HasRemoteSources(ctx); err == nil && has {
		desc.Credentials = append(desc.Credentials, CredentialSourceSignIns)
	}
	files := []archived{{name: "main.db", path: copyPath}}
	for _, name := range keyFiles {
		if _, err := os.Stat(filepath.Join(s.DataDir, name)); err == nil {
			files = append(files, archived{name: name, path: filepath.Join(s.DataDir, name)})
			if !slices.Contains(desc.Credentials, CredentialKeyFile) {
				desc.Credentials = append(desc.Credentials, CredentialKeyFile)
			}
		}
	}
	pictures, err := s.pictures(ctx, desc.Artwork)
	if err != nil {
		return err
	}
	files = append(files, pictures...)
	subtitles, err := db.BackupSubtitles(ctx, copyPath)
	if err != nil {
		return err
	}
	for _, hash := range subtitles {
		if len(hash) != 64 {
			return fmt.Errorf("a downloaded subtitle has an invalid file name")
		}
		name := "subtitles/" + hash[:2] + "/" + hash
		if entryKind(name) != "subtitle" {
			return fmt.Errorf("a downloaded subtitle has an invalid file name")
		}
		path := filepath.Join(s.DataDir, filepath.FromSlash(name))
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		files = append(files, archived{name: name, path: path})
	}
	desc.Pictures = len(pictures)

	// Only now is the backup folder touched, by this goroutine alone and within a limit.
	folder := s.Folder()
	return within(ctx, folderWriteLimit, func() error {
		if folderHook != nil {
			folderHook(folder)
		}
		if err := s.prepareFolder(folder); err != nil {
			return err
		}
		partial := filepath.Join(folder, makingPrefix+id+".tar.gz")
		defer os.Remove(partial)
		if err := s.writeArchive(ctx, partial, files, scheduled); err != nil {
			return err
		}
		info, err := os.Stat(partial)
		if err != nil {
			return err
		}
		desc.Size = info.Size()
		// A key that cannot be read does not stop the backup: it is made without a mark, and
		// restoring it asks the owner to confirm.
		if key, err := markKey(s.DataDir); err != nil {
			s.Log.Warn("a backup is made without its mark", "error", err)
		} else if desc.Mark, err = markOf(key, partial); err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(desc, "", "  ")
		description := filepath.Join(folder, makingPrefix+id+".json")
		defer os.Remove(description)
		if err := os.WriteFile(description, raw, 0o600); err != nil {
			return err
		}
		// The archive goes in first: a description is only ever beside a whole backup.
		if err := os.Rename(partial, filepath.Join(folder, id+".tar.gz")); err != nil {
			return err
		}
		if err := os.Rename(description, filepath.Join(folder, id+".json")); err != nil {
			return err
		}
		s.mu.Lock()
		if s.newest == nil || !desc.CreatedAt.Before(s.newest.CreatedAt) {
			s.newest, s.known = &desc, true // the one just made is the newest
		}
		s.mu.Unlock()
		return nil
	})
}

const (
	// workPrefix names a backup's work folder in the data folder; makingPrefix a backup not
	// yet whole in the backup folder. Both are removed at start if a stop left them.
	workPrefix   = ".backup-"
	makingPrefix = ".making-"
)

// databaseSize is the size of the database and its log.
func databaseSize(dataDir string) int64 {
	var size int64
	for _, name := range []string{"main.db", "main.db-wal"} {
		if info, err := os.Stat(filepath.Join(dataDir, name)); err == nil {
			size += info.Size()
		}
	}
	return size
}

// removeLeftovers removes what a stop in the middle of a backup, a check or a restore left:
// each can hold a whole copy of the database.
func (s *Service) removeLeftovers() {
	for _, pattern := range []string{workPrefix + "*", ".checking-*", ".staging-*"} {
		names, _ := filepath.Glob(filepath.Join(s.DataDir, pattern))
		for _, name := range names {
			os.RemoveAll(name)
		}
	}
	within(context.Background(), folderCheckLimit, func() error {
		names, _ := filepath.Glob(filepath.Join(s.Folder(), makingPrefix+"*"))
		for _, name := range names {
			os.RemoveAll(name)
		}
		return nil
	})
}

type archived struct{ name, path string }

// pictures are the picture files a backup holds: those people uploaded, or with artwork,
// the whole picture store.
func (s *Service) pictures(ctx context.Context, all bool) ([]archived, error) {
	store := filepath.Join(s.DataDir, "artwork")
	var out []archived
	if all {
		err := filepath.WalkDir(store, func(path string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(s.DataDir, path)
			out = append(out, archived{name: filepath.ToSlash(rel), path: path})
			return nil
		})
		return out, err
	}
	hashes, err := s.DB.UploadedPictures(ctx)
	if err != nil {
		return nil, err
	}
	for _, hash := range hashes {
		if len(hash) < 2 {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(store, hash[:2], hash+".*"))
		for _, path := range matches {
			rel, _ := filepath.Rel(s.DataDir, path)
			out = append(out, archived{name: filepath.ToSlash(rel), path: path})
		}
	}
	return out, nil
}

func (s *Service) writeArchive(ctx context.Context, path string, files []archived, scheduled bool) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	tw := tar.NewWriter(zw)
	write := func(a archived) error {
		src, err := os.Open(a.path)
		if err != nil {
			return err
		}
		defer src.Close()
		info, err := src.Stat()
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: a.name, Mode: 0o600, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		for left := info.Size(); left > 0; {
			if scheduled {
				if err := s.Settings.WaitForBackground(ctx, s.Now); err != nil {
					return err
				}
			}
			n, err := io.CopyN(tw, src, min(left, 1<<20))
			if err != nil {
				return err
			}
			left -= n
		}
		return nil
	}
	for _, a := range files {
		if err := write(a); err != nil {
			f.Close()
			return fmt.Errorf("adding %s: %w", a.name, err)
		}
	}
	err = errors.Join(tw.Close(), zw.Close(), f.Sync(), f.Close())
	return err
}

// List lists the backups in the backup folder, newest first.
func (s *Service) List() ([]Backup, error) {
	names, err := filepath.Glob(filepath.Join(s.Folder(), "portico-*.json"))
	if err != nil {
		return nil, err
	}
	var out []Backup
	for _, name := range names {
		var b Backup
		raw, err := os.ReadFile(name)
		if err != nil || json.Unmarshal(raw, &b) != nil || b.ID+".json" != filepath.Base(name) {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Folder(), b.ID+".tar.gz")); err != nil {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// validID says an id can name a backup: nothing else is ever joined to the backup folder.
func validID(id string) bool {
	if !strings.HasPrefix(id, "portico-") || len(id) > 64 {
		return false
	}
	for _, c := range id[len("portico-"):] {
		if (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// Delete removes a backup. Deleting one that is not there is fine.
func (s *Service) Delete(id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	folder := s.Folder()
	err := errors.Join(ignoreMissing(os.Remove(filepath.Join(folder, id+".tar.gz"))), ignoreMissing(os.Remove(filepath.Join(folder, id+".json"))))
	if newest, _ := s.Newest(); newest != nil && newest.ID == id {
		s.readNewest()
	}
	return err
}

func ignoreMissing(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// removeOld removes the scheduled backups past the number the owner keeps.
func (s *Service) removeOld() {
	list, err := s.List()
	if err != nil {
		return
	}
	keep := Document.Get(s.Settings).Keep
	for _, b := range list {
		if !b.Scheduled {
			continue
		}
		if keep > 0 {
			keep--
			continue
		}
		if err := s.Delete(b.ID); err != nil {
			s.Log.Warn("an old backup could not be removed", "backup", b.ID, "error", err)
		}
	}
}

// Path is the archive of a listed backup.
func (s *Service) Path(id string) (string, error) {
	if !validID(id) {
		return "", ErrNotFound
	}
	path := filepath.Join(s.Folder(), id+".tar.gz")
	if _, err := os.Stat(path); err != nil {
		return "", ErrNotFound
	}
	return path, nil
}

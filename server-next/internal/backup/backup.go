// Package backup makes, lists, checks and removes backups of the server: its database, the key
// files that open what the database keeps sealed, and the pictures people uploaded. Never
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
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
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
		if err := writable(s.Folder); err != nil {
			return &settings.Invalid{Field: "folder", Message: "Portico cannot write to this folder."}
		}
	}
	return nil
})

// writable makes the folder if it is missing and checks a file can be written in it.
func writable(folder string) error {
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(folder, ".portico-check-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
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
}

// Service makes and keeps backups.
type Service struct {
	DB       *db.DB
	Settings *settings.Settings
	DataDir  string
	Version  string
	Log      *slog.Logger
	Now      func() time.Time

	mu      sync.Mutex
	running *Progress
	last    error // the last scheduled backup's failure, until one succeeds
}

// Progress is a backup being made.
type Progress struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"startedAt"`
	// PagesLeft is how much of the database is still to copy.
	PagesLeft int `json:"pagesLeft"`
}

// ErrRunning: a backup is already being made.
var ErrRunning = errors.New("a backup is being made")

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

// LastError is why the last scheduled backup failed, while none has succeeded since.
func (s *Service) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

// Start begins a backup in the background and returns its id at once. scheduled backups are
// counted against the number kept.
func (s *Service) Start(scheduled bool) (string, error) {
	s.mu.Lock()
	if s.running != nil {
		s.mu.Unlock()
		return "", ErrRunning
	}
	now := s.Now()
	id := "portico-" + now.Format("20060102-150405")
	s.running = &Progress{ID: id, StartedAt: now}
	s.mu.Unlock()
	go func() {
		err := s.make(context.Background(), id, scheduled)
		s.mu.Lock()
		s.running = nil
		if scheduled {
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
	}()
	return id, nil
}

// make writes one backup: the database copied while in use and checked whole, the key files,
// and the pictures people uploaded; then its description. Nothing is in the backup folder
// under its name until all of it has been written.
func (s *Service) make(ctx context.Context, id string, scheduled bool) error {
	folder := s.Folder()
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	work, err := os.MkdirTemp(folder, ".making-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	copyPath := filepath.Join(work, "main.db")
	if err := s.DB.BackupTo(ctx, copyPath, func(left int) {
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
	desc.Pictures = len(pictures)

	archive := filepath.Join(work, id+".tar.gz")
	if err := writeArchive(archive, files); err != nil {
		return err
	}
	info, err := os.Stat(archive)
	if err != nil {
		return err
	}
	desc.Size = info.Size()
	raw, _ := json.MarshalIndent(desc, "", "  ")
	if err := os.WriteFile(filepath.Join(work, id+".json"), raw, 0o600); err != nil {
		return err
	}
	// The archive goes in first: a description is only ever beside a whole backup.
	if err := os.Rename(archive, filepath.Join(folder, id+".tar.gz")); err != nil {
		return err
	}
	return os.Rename(filepath.Join(work, id+".json"), filepath.Join(folder, id+".json"))
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

func writeArchive(path string, files []archived) error {
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
		_, err = io.Copy(tw, src)
		return err
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
	return errors.Join(ignoreMissing(os.Remove(filepath.Join(folder, id+".tar.gz"))), ignoreMissing(os.Remove(filepath.Join(folder, id+".json"))))
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

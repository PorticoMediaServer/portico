// Package storagepaths defines the on-disk server layout. It never migrates or
// deletes a previous layout: pre-release owners can inspect it and choose a new
// data folder. Callers pass the data root, not a working directory.
package storagepaths

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Layout struct {
	Data, Database, Metadata, Cache, Logs, Keys, Backups, Temp, Transcode, Config string
	Container, TranscodeFallback                                                  bool
}
type Options struct {
	Platform, Home, LocalAppData, ProgramData, DataDirectory string
	Service, Container                                       bool
	Mounts                                                   []Mount
}

// AllowsTranscodeDirectory keeps container output within its two documented
// writable conversion roots. The disk folder remains configurable within them.
func (l Layout) AllowsTranscodeDirectory(path string) bool {
	if path == "" {
		return true
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	if !l.Container {
		return true
	}
	for _, root := range []string{"/transcode", filepath.Join(l.Data, "Transcode")} {
		rel, err := filepath.Rel(root, path)
		if err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return true
		}
	}
	return false
}

// Resolve is pure; the caller obtains service/container identity and mountinfo.
func Resolve(o Options) (Layout, error) {
	platform := o.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	var data, transcode string
	switch {
	case o.Container:
		data, transcode = "/config", "/transcode"
	case platform == "windows":
		base := o.LocalAppData
		if o.Service {
			base = o.ProgramData
			if base == "" {
				base = `C:\ProgramData`
			}
		}
		if base == "" {
			return Layout{}, errors.New("LOCALAPPDATA is unavailable")
		}
		// filepath follows the host OS. Windows defaults remain testable on Linux.
		sep := string(os.PathSeparator)
		if strings.Contains(base, `\`) {
			sep = `\`
		}
		data = strings.TrimRight(base, `/\`) + sep + "Portico"
	case platform == "darwin":
		data = filepath.Join(o.Home, "Library", "Application Support", "Portico")
	case o.Service:
		data = "/var/lib/portico"
	default:
		if o.Home == "" {
			return Layout{}, errors.New("user home is unavailable")
		}
		data = filepath.Join(o.Home, ".local", "share", "portico")
	}
	if o.DataDirectory != "" {
		data = o.DataDirectory
	}
	if platform != "windows" || runtime.GOOS == "windows" {
		if !filepath.IsAbs(data) || filesystemRoot(data) {
			return Layout{}, errors.New("PORTICO_DATA_DIR must name an absolute data directory, not the filesystem root")
		}
		data = filepath.Clean(data)
	}
	if !o.Container {
		if platform == "windows" && runtime.GOOS != "windows" && strings.Contains(data, `\`) {
			transcode = strings.TrimRight(data, `\/`) + `\Transcode`
		} else {
			transcode = filepath.Join(data, "Transcode")
		}
	}
	l := At(data)
	l.Container = o.Container
	if o.Container && !IsMount("/transcode", o.Mounts) {
		transcode = filepath.Join(data, "Transcode")
		l.TranscodeFallback = true
	}
	l.Transcode = transcode
	return l, nil
}
func At(data string) Layout {
	return Layout{Data: data, Database: filepath.Join(data, "Database"), Metadata: filepath.Join(data, "Metadata"), Cache: filepath.Join(data, "Cache"), Logs: filepath.Join(data, "Logs"), Keys: filepath.Join(data, "Keys"), Backups: filepath.Join(data, "Backups"), Temp: filepath.Join(data, "Temp"), Config: filepath.Join(data, "portico.toml")}
}

// Mapping is the complete former flat layout. Values are relative to Data.
// Configurations/keys and rebuildable managed-storage contents are separate.
var Mapping = map[string]string{
	"server.sqlite": "Database/server.sqlite", "server.sqlite-wal": "Database/server.sqlite-wal", "server.sqlite-shm": "Database/server.sqlite-shm", "server.lock": "Database/server.lock",
	"networking-keys": "Keys/networking-keys", "networking-tls.pem": "Keys/networking-tls.pem", "networking-claim-credentials.key": "Keys/networking-claim-credentials.key", "networking-tls.key": "Keys/networking-tls.key", "networking-tls-seal.key": "Keys/networking-tls-seal.key", "authorization-replay.key": "Keys/authorization-replay.key", "setup-token": "Keys/setup-token", "invitation-link.key": "Keys/invitation-link.key", "keys": "Keys", "supervisor": "Keys/supervisor", "managed-storage": "Keys/managed-storage",
	"artwork": "Metadata/artwork", "local-artwork": "Metadata/local-artwork", "subtitles": "Metadata/subtitles", "channel-logos": "Metadata/channel-logos", "guide-images": "Metadata/guide-images", "analysis-artifacts": "Metadata/analysis-artifacts",
	"title-dataset": "Cache/title-dataset", "remote-sources": "Cache/remote-sources", "mounts": "Temp/mounts", "prepared-media": "Cache/prepared-media", "subtitle-video": "Cache/subtitle-video", "linear-media": "Cache/linear-media", "run": "Database/run", "live-source-locks": "Temp/live-source-locks", "logs": "Logs", "backups": "Backups", "restore-staged": "Backups/restore-staged", "restore-result.json": "Backups/restore-result.json",
}

func Path(data string, parts ...string) string {
	if len(parts) == 0 {
		return data
	}
	parts = append([]string(nil), parts...)
	first, tail, _ := strings.Cut(filepath.ToSlash(parts[0]), "/")
	if mapped, ok := Mapping[first]; ok {
		parts[0] = filepath.FromSlash(mapped)
		if tail != "" {
			parts[0] = filepath.Join(parts[0], filepath.FromSlash(tail))
		}
	}
	return filepath.Join(append([]string{data}, parts...)...)
}

// Prepare refuses an old installation without copying, rewriting, or removing it.
func (l Layout) Prepare() error {
	if !filepath.IsAbs(l.Data) || filesystemRoot(l.Data) {
		return errors.New("invalid data directory")
	}
	if err := canonicalParents(l.Data); err != nil {
		return err
	}
	entries, err := os.ReadDir(l.Data)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		_, old := Mapping[entry.Name()]
		if old || entry.Name() == "hls" || entry.Name() == "recordings" {
			return fmt.Errorf("previous data layout at %s: choose a fresh data folder; automatic migration is unavailable", l.Data)
		}
	}
	// Never adopt an existing disposable scope. A directory is ours only if
	// this layout created it and recorded that fact before putting work in it.
	for _, name := range []string{"Cache", "Temp"} {
		if _, err := os.Lstat(filepath.Join(l.Data, name)); err == nil {
			if err := l.verifyOwned(name); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := l.prepareIdentity(); err != nil {
		return err
	}
	for _, dir := range []string{l.Data, l.Database, l.Metadata, l.Cache, l.Logs, l.Keys, l.Backups, l.Temp, l.Transcode} {
		if dir == "" {
			continue
		}
		if err := canonicalParents(dir); err != nil {
			return err
		}
		if dir == l.Cache || dir == l.Temp {
			if err := l.prepareOwned(filepath.Base(dir)); err != nil {
				return err
			}
		} else if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	// Startup-only: layout preparation completes before backup admission.
	f, err := os.OpenFile(l.Config, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}
func filesystemRoot(path string) bool {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	return clean == volume+string(os.PathSeparator) || volume != "" && clean == volume
}

func canonicalParents(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("directory must not contain symbolic links: %s", p)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}

// EmptyOwned empties only the exact fixed Cache or Temp child, via a pinned
// directory handle. RemoveAll does not follow links. Root forbids escapes even
// if another process swaps a directory during the walk.
func (l Layout) EmptyOwned(name string) error {
	if name != "Temp" && name != "Cache" {
		return errors.New("only the owned Temp or Cache may be emptied")
	}
	if !filepath.IsAbs(l.Data) || filesystemRoot(l.Data) {
		return errors.New("invalid data directory")
	}
	if err := canonicalParents(l.Data); err != nil {
		return err
	}
	validatedData, err := os.Lstat(l.Data)
	if err != nil {
		return err
	}
	if err := refuseMountedChildren(filepath.Join(l.Data, name)); err != nil {
		return err
	}
	parent, err := os.OpenRoot(l.Data)
	if err != nil {
		return err
	}
	defer parent.Close()
	openedData, err := parent.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(validatedData, openedData) {
		return errors.New("data directory changed while acquiring its handle")
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("owned directory must not be a symbolic link")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer root.Close()
	current, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, current) {
		return errors.New("owned directory changed")
	}
	expected, err := ownedScopeIdentity(parent, name)
	if err != nil {
		return err
	}
	if err := verifyOwnedRoot(root, expected); err != nil {
		return err
	}
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	for {
		entries, err := file.ReadDir(128)
		for _, entry := range entries {
			if entry.Name() == ownershipMarker {
				continue
			}
			if e := root.RemoveAll(entry.Name()); e != nil {
				return e
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
func (l Layout) ResetTemp() error { return l.EmptyOwned("Temp") }
func (l Layout) ActivateTemp() error {
	if !filepath.IsAbs(l.Data) || filesystemRoot(l.Data) || l.Temp != filepath.Join(l.Data, "Temp") {
		return errors.New("invalid owned Temp directory")
	}
	if err := canonicalParents(l.Temp); err != nil {
		return err
	}
	if err := l.prepareIdentity(); err != nil {
		return err
	}
	if err := l.prepareOwned("Temp"); err != nil {
		return err
	}

	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if err := os.Setenv(key, l.Temp); err != nil {
			return err
		}
	}
	return nil
}
func (l Layout) ValidateRecordings(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("choose an absolute recordings folder outside the data folder")
	}
	if err := canonicalParents(l.Data); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		resolved = filepath.Clean(path)
		if err = canonicalParents(filepath.Dir(path)); err != nil {
			return err
		}
	}
	relative, err := filepath.Rel(l.Data, resolved)
	if err != nil {
		// Windows Rel cannot compare different volumes. Establish actual local
		// volume identity rather than accepting different drive/share spellings;
		// mapped letters and UNC aliases must not bypass containment.
		if distinct, volumeErr := distinctNativeVolumes(l.Data, resolved); volumeErr == nil && distinct {
			return nil
		}
		return err
	}
	if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return errors.New("recordings must be outside the data folder")
	}
	return nil
}

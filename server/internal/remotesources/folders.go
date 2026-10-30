package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// The folder explorer inside a saved WebDAV source lists one folder at a time,
// as the owner opens it, and downloads nothing. Its paths are the absolute
// paths the catalog stores for this source's folders, so a folder chosen here
// can be handed straight to the library that will use it.
type SourceFolder struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Relative string `json:"relative"`
	Hidden   bool   `json:"hidden"`
}

type SourceFolderPage struct {
	SourceID   string         `json:"sourceId"`
	Generation string         `json:"generation"`
	Path       string         `json:"path"`
	Relative   string         `json:"relative"`
	Entries    []SourceFolder `json:"entries"`
	NextCursor string         `json:"nextCursor"`
	// Truncated says this folder holds more folders than one bounded listing
	// examines. Paging still walks the names that listing did examine, and the
	// page stops when those run out instead of guessing beyond them.
	Truncated bool `json:"truncated"`
}

const (
	// The drawn explorer shows 500 folders at a time and a "Show more folders"
	// action; the local picker's page is 200. Both are bounded here.
	folderPageMax     = 500
	folderPageDefault = 200
	folderNameMax     = 255
	// One listing examines at most this many names. A folder holding more is
	// reported as truncated rather than read without bound.
	folderScanCap = 4096
)

func validFolderCursor(after string) bool {
	if after == "" {
		return true
	}
	return utf8.ValidString(after) && len(after) <= folderNameMax && !strings.ContainsAny(after, "/\\\x00") && strings.IndexFunc(after, unicode.IsControl) < 0
}

// folderRelative validates the path the owner typed or opened, inside the
// source. The empty string is the source's root; everything else must be a
// clean relative path, never an absolute one.
func folderRelative(directory string) (string, error) {
	if directory == "" {
		return "", nil
	}
	if len(directory) > 8192 || !utf8.ValidString(directory) || strings.ContainsAny(directory, "\\\x00%") || strings.IndexFunc(directory, unicode.IsControl) >= 0 ||
		strings.HasPrefix(directory, "/") || path.Clean(directory) != directory || directory == "." || directory == ".." || strings.HasPrefix(directory, "../") {
		return "", storage.ErrRemoteConfig
	}
	return directory, nil
}

// ListSourceFolders reads one folder of a saved source. Only folders are
// returned: the explorer chooses libraries' folders, and listing files is what
// a scan does, not what opening a folder does. One request examines a bounded
// number of names, and a folder that holds more is reported as truncated.
func (s *Service) ListSourceFolders(ctx context.Context, identities *identity.Service, owner identity.Principal, source, directory string, showHidden bool, after string, limit int) (SourceFolderPage, error) {
	empty := SourceFolderPage{Entries: []SourceFolder{}}
	if s == nil || s.db == nil || s.mounts == nil || source == "" {
		return empty, storage.ErrRemoteConfig
	}
	if limit == 0 {
		limit = folderPageDefault
	}
	if limit < 1 || limit > folderPageMax || !validFolderCursor(after) {
		return empty, storage.ErrRemoteConfig
	}
	row, err := s.admitFolderRead(ctx, identities, owner, source)
	if err != nil {
		return empty, err
	}
	relative, err := folderRelative(directory)
	if err != nil {
		return empty, err
	}
	b := binding{ID: row.ID, Kind: string(row.Kind), Root: row.RootPath, MountID: row.MountID}
	generation := strconv.FormatInt(row.Generation, 10)
	bindingRow := row
	var listing folderListing
	switch row.Kind {
	case SourceWebDAV:
		// The saved-source folder list is the same direct WebDAV provider path
		// as a read, so it takes the source's shared operation permit; the
		// listing also drains or queues behind an existing-source connection
		// test instead of running beside it.
		ops, opsErr := s.sourceOperations(ctx, row.ID, generation)
		if opsErr != nil {
			return empty, opsErr
		}
		listing, err = s.listDAVFolders(ctx, row, b, generation, relative, showHidden, ops)
	default:
		// A mounted remote is a normalized mount on this server; its folders are
		// read through a root that cannot leave the source or follow a symlink.
		listing, err = mountedFolders(ctx, row.RootPath, relative, showHidden)
	}
	if err != nil {
		return empty, err
	}
	// The provider read is the slow part. A source removed, regranted or changed
	// while it ran must not publish what that read found.
	if err = s.reconfirmOwnerSource(ctx, identities, owner, bindingRow); err != nil {
		return empty, err
	}
	sort.Slice(listing.entries, func(i, j int) bool { return listing.entries[i].Name < listing.entries[j].Name })
	kept := listing.entries
	if after != "" {
		kept = make([]SourceFolder, 0, len(listing.entries))
		for _, entry := range listing.entries {
			if entry.Name > after {
				kept = append(kept, entry)
			}
		}
	}
	out := SourceFolderPage{SourceID: row.ID, Generation: generation, Path: filepath.Join(row.RootPath, filepath.FromSlash(relative)), Relative: relative, Entries: kept, NextCursor: "", Truncated: listing.truncated}
	total := len(kept)
	if total > limit {
		out.Entries = kept[:limit]
		// More filtered folders really remain after the last name, so the next
		// page is real. That holds after a truncated bounded scan too: the next
		// request lists this folder again and continues after this name, so the
		// examined names stay reachable and the last of them ends the paging.
		out.NextCursor = out.Entries[len(out.Entries)-1].Name
	}
	if out.Entries == nil {
		out.Entries = []SourceFolder{}
	}
	return out, nil
}

// listDAVFolders reads one folder through the saved source's DAV client. The
// provider response itself is bounded by the DAV parser (4 MiB of XML); this
// loop additionally stops after a bounded number of qualifying folders, and
// reports truncation when it does.
func (s *Service) listDAVFolders(ctx context.Context, row Source, b binding, generation, relative string, showHidden bool, ops *sourceOpsGate) (folderListing, error) {
	d, current, err := s.dav(ctx, row.ID)
	if err != nil {
		// No provider permit existed for this failure.
		s.health(false, b, generation, err)
		return folderListing{}, mapError(err)
	}
	if current != generation {
		return folderListing{}, storage.ErrRemoteChanged
	}
	deadline, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	gated, grelease, gateErr := ops.budget.Enter(deadline)
	if gateErr != nil {
		return folderListing{}, sourceOpsError(ctx, gateErr)
	}
	if waitErr := ops.pacer.waitRequest(gated); waitErr != nil {
		grelease()
		return folderListing{}, sourceOpsError(ctx, waitErr)
	}
	page, err := d.List(gated, relative)
	// Capture the original permit's retirement before releasing it; the outer
	// request context is not the provider operation's custody.
	live := gated.Err() == nil
	grelease()
	if err != nil {
		s.health(live, b, generation, err)
		return folderListing{}, mapError(err)
	}
	s.health(live, b, generation, nil)
	out := folderListing{entries: make([]SourceFolder, 0, 32)}
	for _, entry := range page.Entries {
		if !entry.Directory || entry.Relative == relative || entry.Relative == "" && relative == "" {
			continue
		}
		// Depth:1 should only answer direct children; a deeper href is not one.
		if path.Dir(entry.Relative) != relative && !(relative == "" && !strings.Contains(entry.Relative, "/")) {
			continue
		}
		name := path.Base(entry.Relative)
		if !validFolderName(name) || !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if len(out.entries) >= folderScanCap {
			out.truncated = true
			break
		}
		out.entries = append(out.entries, SourceFolder{
			Name:     name,
			Path:     filepath.Join(row.RootPath, filepath.FromSlash(entry.Relative)),
			Relative: entry.Relative,
			Hidden:   strings.HasPrefix(name, "."),
		})
	}
	return out, nil
}

// admitFolderRead is the short admission read. Its snapshot is released before
// any provider or mount I/O, so a slow listing never holds a database reader.
func (s *Service) admitFolderRead(ctx context.Context, identities *identity.Service, owner identity.Principal, source string) (Source, error) {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return Source{}, err
	}
	defer snapshot.Rollback()
	if err = currentLocalOwnerTx(ctx, snapshot.Tx(), identities, owner); err != nil {
		return Source{}, err
	}
	row, err := sourceRowTx(ctx, snapshot.Tx(), source)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Source{}, storage.ErrRemoteOffline
		}
		return Source{}, err
	}
	return row, nil
}

// reconfirmOwnerSource is the post-read fence: in a new transaction, the same
// owner, source, generation, root and mount must still hold. Anything that
// drifted while the read ran fails closed.
func (s *Service) reconfirmOwnerSource(ctx context.Context, identities *identity.Service, owner identity.Principal, admitted Source) error {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	defer snapshot.Rollback()
	if err = currentLocalOwnerTx(ctx, snapshot.Tx(), identities, owner); err != nil {
		return err
	}
	row, err := sourceRowTx(ctx, snapshot.Tx(), admitted.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrRemoteOffline
		}
		return err
	}
	if row.Generation != admitted.Generation || row.RootPath != admitted.RootPath || row.MountID != admitted.MountID || row.Kind != admitted.Kind {
		return storage.ErrRemoteChanged
	}
	return nil
}

func sourceRowTx(ctx context.Context, tx *sql.Tx, id string) (Source, error) {
	var row Source
	err := tx.QueryRowContext(ctx, `SELECT id,kind,name,root,generation,state,error_code,range_support,origin,insecure,credential_present,mount_id FROM remote_sources WHERE id=? AND removed=0`, id).Scan(&row.ID, &row.Kind, &row.Name, &row.RootPath, &row.Generation, &row.State, &row.ErrorCode, &row.RangeSupport, &row.Origin, &row.InsecureLocal, &row.CredentialPresent, &row.MountID)
	return row, err
}

func validFolderName(name string) bool {
	return name != "" && name != "." && name != "/" && len(name) <= folderNameMax && utf8.ValidString(name) && strings.IndexFunc(name, unicode.IsControl) < 0
}

type folderListing struct {
	entries   []SourceFolder
	truncated bool
}

// mountedFolders lists the folders of a mounted remote through a directory
// pinned by descriptor: every step is opened relative to the previous one with
// no symlink following, and one read examines a bounded number of names. The
// caller's context is checked between those synchronous mount calls, so a
// cancelled or expired request stops at the next step; a mount call that is
// already blocked is not interrupted by these checks.
func mountedFolders(ctx context.Context, root, relative string, showHidden bool) (folderListing, error) {
	if err := ctx.Err(); err != nil {
		return folderListing{}, err
	}
	handle, err := storage.OpenRootedDir(root)
	if err != nil {
		return folderListing{}, mountedError(err)
	}
	defer handle.Close()
	directory := handle
	if relative != "" {
		for _, part := range strings.Split(relative, "/") {
			if e := ctx.Err(); e != nil {
				if directory != handle {
					directory.Close()
				}
				return folderListing{}, e
			}
			next, err := directory.Child(part)
			if directory != handle {
				directory.Close()
			}
			if err != nil {
				return folderListing{}, mountedError(err)
			}
			directory = next
		}
	}
	if directory != handle {
		defer directory.Close()
	}
	names, more, err := directory.Names(folderScanCap)
	if err != nil {
		return folderListing{}, mountedError(err)
	}
	out := folderListing{entries: make([]SourceFolder, 0, 32), truncated: more}
	for _, name := range names {
		// Every name costs one open on the mount, so cancellation is checked
		// between names rather than once per page.
		if e := ctx.Err(); e != nil {
			return folderListing{}, e
		}
		if !validFolderName(name) || !showHidden && strings.HasPrefix(name, ".") || !directory.IsDirectoryEntry(name) {
			continue
		}
		inner := name
		if relative != "" {
			inner = relative + "/" + name
		}
		out.entries = append(out.entries, SourceFolder{
			Name:     name,
			Path:     filepath.Join(root, filepath.FromSlash(inner)),
			Relative: inner,
			Hidden:   strings.HasPrefix(name, "."),
		})
	}
	return out, nil
}

func mountedError(err error) error {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) {
		return storage.ErrRemoteOffline
	}
	return storage.ErrRemoteOffline
}

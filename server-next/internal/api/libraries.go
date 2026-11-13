package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// The owner's libraries: making, changing and removing them, scanning, what a scan skipped,
// missing titles, and deleting files. Only the owner sees or changes any of it. A key made
// for a tool needs the library scope.

// LibraryOptions are the owner's choices for one library.
type LibraryOptions struct {
	SegmentDetection string `json:"segmentDetection,omitempty"`
	// Depth is how much of each file a scan reads: files, basic (the default) or complete.
	Depth string `json:"depth"`
	// Trash says when missing titles are removed.
	Trash TrashRule `json:"trash"`
	// AllowDelete lets the owner delete files of this library from disk. It starts off.
	AllowDelete bool `json:"allowDelete"`
	// Watch is whether the library's folders are watched for changes, which are then picked
	// up within about ten seconds. It starts on; leaving it out of an edit keeps it on.
	Watch *bool `json:"watch,omitempty"`
	// RescanMinutes is how often the library's folders are scanned by themselves, from 15
	// minutes to 30 days; 0 is no periodic scan. With watch off and 0 here the library is
	// scanned only when asked. Where watch is on and the folders cannot be watched, they are
	// scanned every hour instead, or every six on a network share.
	RescanMinutes int `json:"rescanMinutes"`
	// GroupSeries is "Group films from the same series": a collection appears once two films
	// of a series are in the library. On unless turned off; left out of an edit, it stays.
	GroupSeries *bool `json:"groupSeries,omitempty"`
	// Lyrics is a music library's lyrics source: "" for its files only (the default), or
	// "lrclib" for its files, then LRCLIB, fetched after each scan for songs without any.
	Lyrics string `json:"lyrics,omitempty"`
	// SoundAnalysis is a music library's "Sonic analysis": its songs are read by the sound
	// model for Mixes and radio by sound. On unless turned off, like watch; off keeps what was
	// read. Its status is GET /v1/admin/libraries/{id}/sound-analysis.
	SoundAnalysis *bool `json:"soundAnalysis,omitempty"`
}

// TrashRule is days (the default, with Days from 1 to 3,650, starting at 30), scan (after
// every scan) or manual (only when asked).
type TrashRule struct {
	Mode string `json:"mode"`
	Days int    `json:"days,omitempty"`
}

// LibraryFolder is one folder of a library and how the last scan found it.
type LibraryFolder struct {
	Path string `json:"path"`
	// Source is the remote source the folder is on, with Path the folder within it; absent
	// for a folder on a disk the server reads.
	Source    string `json:"source,omitempty"`
	Available bool   `json:"available"`
	// Problem says why a folder that is not available could not be scanned.
	Problem    string     `json:"problem,omitempty"`
	LastScanAt *time.Time `json:"lastScanAt,omitempty"`
	// Watched says the folder is being watched for changes.
	Watched bool `json:"watched"`
	// ScanEveryMinutes is how often the folder is scanned by itself; 0 is never.
	ScanEveryMinutes int `json:"scanEveryMinutes"`
	// Notice is a sentence for the library's settings when the folder is not kept up to date
	// the way the owner chose: watching was asked for and is not available for it.
	Notice string `json:"notice,omitempty"`
}

// AdminLibrary is a library as its owner manages it.
type AdminLibrary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Language string `json:"language,omitempty"`
	// MetadataProvider is the chosen provider, resolved to the kind's default when none was saved.
	MetadataProvider string `json:"metadataProvider"`
	// MetadataProviders lists the kind's choices, with runtime availability and why a choice cannot be used.
	MetadataProviders []metadata.LibraryMetadataProvider `json:"metadataProviders"`
	// Region is the saved video metadata region; empty uses the provider's default.
	Region  *string         `json:"region,omitempty"`
	Folders []LibraryFolder `json:"folders"`
	Options LibraryOptions  `json:"options"`
	// WatchNote is one sentence for where the watching switch is shown, while watching is
	// on: what watching does not cover.
	WatchNote          string     `json:"watchNote,omitempty"`
	Scanning           bool       `json:"scanning"`
	LastScanStartedAt  *time.Time `json:"lastScanStartedAt,omitempty"`
	LastScanFinishedAt *time.Time `json:"lastScanFinishedAt,omitempty"`
	// Revision changes with every edit of the library's name, options or folders. It is also
	// the ETag of a read of one library; an edit may send it back in If-Match, and one made
	// on an out-of-date copy is then refused with 412.
	Revision string `json:"revision"`
}

func (l AdminLibrary) reply(h http.Header) int {
	if l.Revision != "" {
		etag(h, l.Revision)
	}
	return 0
}

// LibraryCreate makes a library and starts its first scan. Kind is movies, shows, anime,
// music, audiobooks, books or comics. Send an Idempotency-Key so a repeat does not make a second.
type LibraryCreate struct {
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Language         string   `json:"language,omitempty"`
	MetadataProvider string   `json:"metadataProvider,omitempty"`
	Region           string   `json:"region,omitempty"`
	Folders          []string `json:"folders"`
	// RemoteFolders are folders on the owner's remote sources. A library needs at least one
	// folder of either kind.
	RemoteFolders []RemoteFolderRef `json:"remoteFolders,omitempty"`
	Options       *LibraryOptions   `json:"options,omitempty"`
}

// RemoteFolderRef is a folder on a remote source: Source is the source's id, Path the folder
// within it, as GET /v1/admin/remote-sources/{id}/folders gives it ("" is its top).
type RemoteFolderRef struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

// LibraryEdit changes a library. Fields left out stay as they are. Folders is the whole
// list: a folder no longer in it leaves the library with its titles, and files on disk are
// not touched; an added folder is scanned.
type LibraryEdit struct {
	Name             *string   `json:"name,omitempty"`
	Language         *string   `json:"language,omitempty"`
	MetadataProvider *string   `json:"metadataProvider,omitempty"`
	Region           *string   `json:"region,omitempty"`
	Folders          *[]string `json:"folders,omitempty"`
	// RemoteFolders is the whole list of the library's folders on remote sources, as Folders
	// is for folders on disks here.
	RemoteFolders *[]RemoteFolderRef `json:"remoteFolders,omitempty"`
	Options       *LibraryOptions    `json:"options,omitempty"`
}

// ScanRequest starts a scan. With AllFiles it reads every file again, changed or not; a scan
// already waiting or running is joined as it is.
type ScanRequest struct {
	AllFiles bool `json:"allFiles,omitempty"`
}

// Job is slow work to watch at /v1/jobs/{id}. State is waiting, running, done, failed or cancelled.
type Job struct {
	ID       string       `json:"id"`
	Kind     string       `json:"kind"`
	State    string       `json:"state"`
	Progress *JobProgress `json:"progress,omitempty"`
	Error    string       `json:"error,omitempty"`
}

// JobProgress is how far a job has got. Total is absent when the size of the work is not known.
type JobProgress struct {
	Done  int64  `json:"done"`
	Total *int64 `json:"total,omitempty"`
}

// ScanStatus is a library's scan: the one waiting or running, or the last one.
type ScanStatus struct {
	// State is idle, waiting or running.
	State              string          `json:"state"`
	Job                *Job            `json:"job,omitempty"`
	LastScanStartedAt  *time.Time      `json:"lastScanStartedAt,omitempty"`
	LastScanFinishedAt *time.Time      `json:"lastScanFinishedAt,omitempty"`
	Folders            []LibraryFolder `json:"folders"`
	// Counts are of the scan running, or of the last one since the server started.
	Counts *ScanCounts `json:"counts,omitempty"`
}

// ScanCounts is what a scan has done so far.
type ScanCounts struct {
	Files        int `json:"files"`
	FilesNew     int `json:"filesNew"`
	FilesChanged int `json:"filesChanged"`
	FilesMoved   int `json:"filesMoved"`
	FilesMissing int `json:"filesMissing"`
	TitlesNew    int `json:"titlesNew"`
	FilesRead    int `json:"filesRead"`
	// FilesToRead is how many files still have their details (length, picture and sound
	// tracks) to be read. Titles are in the library, and can be played, as soon as their
	// folders have been walked; this counts down after that, so a large first scan with
	// titles listed and this above 0 is not stuck, it is reading.
	FilesToRead int `json:"filesToRead"`
}

// SkippedFile is a file the last scan could not use. Reason is damaged, not_supported,
// not_media, unreadable_name, encrypted_disc or no_disc_reader; Message says it in a sentence.
type SkippedFile struct {
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// MissingTitle is a title in a library's trash. RemovedAt is when the library's rule will
// remove it; it is absent when the trash is only emptied by hand.
type MissingTitle struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Title          string     `json:"title"`
	Year           int        `json:"year,omitempty"`
	MissingSinceAt time.Time  `json:"missingSinceAt"`
	RemovedAt      *time.Time `json:"removedAt,omitempty"`
}

// MissingTitles is a library's trash: the longest missing first, and how many there are.
type MissingTitles struct {
	Items []MissingTitle `json:"items"`
	Total int            `json:"total"`
}

// TrashEmptied says how many missing titles were removed.
type TrashEmptied struct {
	Removed int `json:"removed"`
}

// DeletionPreview is what Delete would take, shown before it is confirmed.
type DeletionPreview struct {
	Title       string `json:"title"`
	Titles      int    `json:"titles"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	ListEntries int    `json:"listEntries"`
	// OtherPeople is how many saved places and marks of other profiles go with the titles.
	OtherPeople int `json:"otherPeople"`
	// AlsoRemoves are other titles played from the same files (the second episode of a file
	// holding two, the other tracks of an album in one file): deleting the files removes them
	// too. Titles counts them.
	AlsoRemoves []TitleRef `json:"alsoRemoves"`
}

// TitleRef names a title.
type TitleRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// DeletionResult says what Delete did. A file that changed since Portico last read it, or
// that could not be deleted, is kept, and then so is its title.
type DeletionResult struct {
	Removed      bool       `json:"removed"`
	FilesDeleted int        `json:"filesDeleted"`
	Bytes        int64      `json:"bytes"`
	Kept         []KeptFile `json:"kept"`
	// AlsoRemoved are the other titles that were played from the deleted files and went with them.
	AlsoRemoved []TitleRef `json:"alsoRemoved"`
}

// KeptFile is a file Delete left on disk, and why.
type KeptFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

var skippedMessages = map[db.FileProblem]string{
	db.ProblemDamaged:        "This file is damaged.",
	db.ProblemUnsupported:    "This file is not supported.",
	db.ProblemEncryptedDisc:  "This disc is copy-protected. Portico can't read copy-protected discs.",
	db.ProblemNoDiscReader:   "This server's media tools can't read discs.",
	db.ProblemNotMedia:       "This is not a media file.",
	db.ProblemUnreadableName: "Portico can't read this file's name. Rename it, then scan again.",
}

func timePtr(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func libraryOptionsOut(o db.LibraryOptions) LibraryOptions {
	watch := !o.NoWatch
	group, sound := o.GroupSeries == nil || *o.GroupSeries, !o.NoSoundAnalysis
	out := LibraryOptions{Depth: o.Depth, Trash: TrashRule{Mode: o.Trash.Mode, Days: o.Trash.Days}, AllowDelete: o.AllowDelete,
		Watch: &watch, RescanMinutes: o.RescanMinutes, GroupSeries: &group, Lyrics: o.Lyrics, SoundAnalysis: &sound, SegmentDetection: o.SegmentDetection}
	if out.SegmentDetection == "" {
		out.SegmentDetection = "server"
	}
	if out.Depth == "" {
		out.Depth = db.DepthBasic
	}
	if out.Trash.Mode == "" {
		out.Trash.Mode = db.TrashAfterDays
	}
	if out.Trash.Mode == db.TrashAfterDays && out.Trash.Days == 0 {
		out.Trash.Days = 30
	}
	return out
}

func optionsIn(o LibraryOptions) db.LibraryOptions {
	return db.LibraryOptions{Depth: o.Depth, Trash: db.TrashRule{Mode: o.Trash.Mode, Days: o.Trash.Days}, AllowDelete: o.AllowDelete,
		NoWatch: o.Watch != nil && !*o.Watch, RescanMinutes: o.RescanMinutes, GroupSeries: o.GroupSeries, Lyrics: o.Lyrics,
		NoSoundAnalysis: o.SoundAnalysis != nil && !*o.SoundAnalysis, SegmentDetection: o.SegmentDetection}
}

func (s *Server) foldersOut(lib db.Library, folders []db.LibraryFolder) []LibraryFolder {
	out := []LibraryFolder{}
	for _, f := range folders {
		folder := LibraryFolder{Path: f.Path, Source: f.Source, Available: f.State == "ok", Problem: f.Error, LastScanAt: timePtr(f.LastScanAt)}
		for _, p := range lib.Paths {
			if p.ID != f.ID {
				continue
			}
			if watched, every, ok := s.scans.FolderSchedule(lib, p); ok {
				folder.Watched, folder.ScanEveryMinutes = watched, int(every/time.Minute)
				if !watched && !lib.Options.NoWatch {
					folder.Notice = "Watching is not available for this folder; scanning " + everyText(every) + " instead."
				}
			}
		}
		out = append(out, folder)
	}
	return out
}

// everyText is an interval in words: "every hour", "every 6 hours", "every 30 minutes".
func everyText(every time.Duration) string {
	switch hours := int(every / time.Hour); {
	case every%time.Hour != 0:
		return fmt.Sprintf("every %d minutes", int(every/time.Minute))
	case hours == 1:
		return "every hour"
	case hours%24 == 0 && hours > 24:
		return fmt.Sprintf("every %d days", hours/24)
	case hours == 24:
		return "every day"
	default:
		return fmt.Sprintf("every %d hours", hours)
	}
}

// jobOut is a job as one caller sees it. A failed job's reason may name titles, so all of it
// goes to the owner alone.
func jobOut(r *http.Request, j db.Job) *Job {
	out := &Job{ID: j.PublicID, Kind: j.Kind, State: j.State, Error: jobs.ErrorText(j, CallerFrom(r.Context()).Role == "owner")}
	if j.State == db.JobRunning || j.Done > 0 {
		out.Progress = &JobProgress{Done: j.Done}
		if j.Total > 0 {
			out.Progress.Total = &j.Total
		}
	}
	return out
}

// watchNote is shown with the watching switch. Starting the server scans nothing by itself
// (the owner's ruling), so what changed while it was off waits for a scan.
const watchNote = "Changes made while the server was off are picked up at the next scheduled scan or Scan now, not when the server starts."

func (s *Server) libraryOut(r *http.Request, l db.AdminLibrary) (AdminLibrary, error) {
	out := AdminLibrary{ID: l.PublicID, Name: l.Name, Kind: l.Kind, Language: l.Language, Folders: s.foldersOut(l.Library, l.Folders),
		Options: libraryOptionsOut(l.Options), LastScanStartedAt: timePtr(l.ScanStartedAt), LastScanFinishedAt: timePtr(l.ScanFinishedAt),
		Revision: strconv.FormatInt(l.Revision, 10)}
	out.MetadataProvider = (db.LibrarySource{Kind: l.Kind, Provider: l.MetadataProvider}).ProviderName()
	out.MetadataProviders = s.metadata.LibraryProviders(l.Kind)
	if l.Kind == db.LibraryMovies || l.Kind == db.LibraryShows || l.Kind == db.LibraryAnime {
		out.Region = &l.Region
	}
	if !l.Options.NoWatch {
		out.WatchNote = watchNote
	}
	job, err := s.db.JobFor(r.Context(), db.JobScan, l.PublicID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return out, err
	}
	out.Scanning = err == nil && (job.State == db.JobWaiting || job.State == db.JobRunning)
	return out, nil
}

// library finds the library a route names, or answers not found.
func (s *Server) library(r *http.Request) (db.AdminLibrary, error) {
	found, err := s.db.AdminLibraries(r.Context(), r.PathValue("id"))
	if err != nil {
		return db.AdminLibrary{}, err
	}
	if len(found) == 0 {
		return db.AdminLibrary{}, ErrNotFound()
	}
	return found[0], nil
}

// The folders the owner may never point a library at: they are not places files are kept.
var refusedFolders = []string{"/proc", "/sys", "/dev", "/run"}

// real is a folder's path with links resolved, or the path itself where that cannot be done.
func real(folder string) string {
	if abs, err := filepath.Abs(folder); err == nil {
		folder = abs
	}
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		return resolved
	}
	return folder
}

// within reports whether a folder is another folder or inside it.
func within(folder, parent string) bool {
	rel, err := filepath.Rel(parent, folder)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// notForFiles reports whether a path is one of the places that are not for files, or inside
// one: as it is written, and where it leads once links are followed (/var/run is /run on
// most systems, and a link kept in a media folder can point anywhere).
func notForFiles(folder string) bool {
	for _, path := range []string{folder, real(folder)} {
		for _, refused := range refusedFolders {
			if path == refused || strings.HasPrefix(path, refused+"/") {
				return true
			}
		}
	}
	return false
}

// checkFolders refuses a folder the server cannot use, saying which and why.
func (s *Server) checkFolders(folders []string, field string) error {
	if len(folders) == 0 {
		return ErrField(field, "A library needs at least one folder.")
	}
	for n, folder := range folders {
		at := field + "[" + strconv.Itoa(n) + "]"
		if !filepath.IsAbs(folder) || filepath.Clean(folder) != folder {
			return ErrField(at, "Give the folder's full path.")
		}
		if notForFiles(folder) {
			return ErrField(at, "This folder cannot be used for a library.")
		}
		info, err := os.Stat(folder)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return ErrField(at, "This folder was not found.")
		case errors.Is(err, fs.ErrPermission):
			return ErrField(at, "Portico has no permission to read this folder.")
		case err != nil:
			return ErrField(at, "This folder cannot be reached.")
		case !info.IsDir():
			return ErrField(at, "This is a file, not a folder.")
		}
		// The whole disk, and the server's own folder with its database: a title there would
		// list and never play.
		here := real(folder)
		if here == string(filepath.Separator) {
			return ErrField(at, "The whole disk cannot be a library. Choose the folder your media is in.")
		}
		if data := real(s.dataDir); s.dataDir != "" && (within(here, data) || within(data, here)) {
			return ErrField(at, "This folder holds Portico's own data, or is inside it. Choose another folder.")
		}
	}
	return nil
}

func libraryError(err error) error {
	switch {
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound()
	case errors.Is(err, db.ErrInvalid):
		return ErrInvalid(strings.TrimPrefix(err.Error(), "invalid: "))
	case errors.Is(err, db.ErrKeyReused):
		return errKeyReused()
	case errors.Is(err, db.ErrStale):
		return refusal(http.StatusPreconditionFailed, "library_changed", "This library changed. Reload and try again.", false)
	}
	return err
}

// RunSchedules starts scans by themselves until ctx ends: when watched folders change and on
// rescan intervals. The program calls it once, beside serving; Wait waits for it too.
func (s *Server) RunSchedules(ctx context.Context) {
	s.background.Add(1)
	defer s.background.Done()
	s.scans.Run(ctx)
}

func (s *Server) libraryRoutes() {
	s.explorerRoutes()
	s.selectionRoutes()
	Handle(s, "GET /v1/admin/libraries", Owner, s.listAdminLibraries)
	Handle(s, "POST /v1/admin/libraries", Owner, s.createLibrary)
	Handle(s, "GET /v1/admin/libraries/{id}", Owner, func(r *http.Request, _ None) (AdminLibrary, error) {
		lib, err := s.library(r)
		if err != nil {
			return AdminLibrary{}, err
		}
		return s.libraryOut(r, lib)
	})
	Handle(s, "PATCH /v1/admin/libraries/{id}", Owner, s.editLibrary)
	Handle(s, "DELETE /v1/admin/libraries/{id}", Owner, s.deleteLibrary).Status = http.StatusAccepted
	Handle(s, "POST /v1/admin/libraries/{id}/scan", Owner, s.startScan).with(ScopeLibrary).Status = http.StatusAccepted
	Handle(s, "DELETE /v1/admin/libraries/{id}/scan", Owner, s.cancelScan).with(ScopeLibrary)
	Handle(s, "GET /v1/admin/libraries/{id}/scan", Owner, s.scanStatus).with(ScopeLibrary)
	Handle(s, "GET /v1/admin/libraries/{id}/skipped", Owner, s.skippedFiles)
	s.skippedRoutes()
	Handle(s, "GET /v1/admin/libraries/{id}/missing", Owner, s.missingTitles)
	Handle(s, "DELETE /v1/admin/libraries/{id}/missing", Owner, s.emptyTrash)
	Handle(s, "GET /v1/admin/items/{id}/deletion", Owner, s.previewItemDeletion)
	Handle(s, "GET /v1/admin/versions/{id}/deletion", Owner, s.previewVersionDeletion)
	Handle(s, "DELETE /v1/admin/items/{id}", Owner, s.deleteItem).with(ScopeLibrary)
	Handle(s, "DELETE /v1/admin/versions/{id}", Owner, s.deleteVersion).with(ScopeLibrary)
}

// with gives a route a scope other than its default.
func (r *Route) with(scope Scope) *Route {
	r.Scope = scope
	return r
}

func (s *Server) listAdminLibraries(r *http.Request, _ None) (AdminLibraryPage, error) {
	out := AdminLibraryPage{Items: []AdminLibrary{}, MetadataProviders: s.libraryMetadataChoices()}
	libraries, err := s.db.AdminLibraries(r.Context(), "")
	if err != nil {
		return out, err
	}
	for _, l := range libraries {
		lib, err := s.libraryOut(r, l)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, lib)
	}
	return out, nil
}

// checkRemoteFolders turns folders on remote sources into the library's own, refusing an
// unknown source, a path that is not plainly inside the source, and a folder the source
// cannot list now. A folder in have is already the library's and is not listed again.
func (s *Server) checkRemoteFolders(ctx context.Context, refs []RemoteFolderRef, have map[db.RemoteFolder]bool) ([]db.RemoteFolder, error) {
	out := []db.RemoteFolder{}
	for n, ref := range refs {
		at := "remoteFolders[" + strconv.Itoa(n) + "]"
		row, err := s.db.RemoteSource(ctx, ref.Source)
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrField(at+".source", "There is no such remote source.")
		}
		if err != nil {
			return nil, err
		}
		p := strings.Trim(ref.Path, "/")
		if p != "" && (path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") || strings.ContainsAny(p, "\\\x00")) {
			return nil, ErrField(at+".path", "Give the folder's path within the source, as the source's folder list shows it.")
		}
		folder := db.RemoteFolder{SourceID: row.ID, Path: p}
		if !have[folder] {
			if _, err := s.listRemote(ctx, row.ID, p); err != nil {
				return nil, err
			}
		}
		out = append(out, folder)
	}
	return out, nil
}

func (s *Server) createLibrary(r *http.Request, req LibraryCreate) (AdminLibrary, error) {
	if req.MetadataProvider != "" {
		if err := s.checkLibraryProvider(req.Kind, req.MetadataProvider); err != nil {
			return AdminLibrary{}, err
		}
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return AdminLibrary{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	if len(req.Folders) > 0 || len(req.RemoteFolders) == 0 {
		if err := s.checkFolders(req.Folders, "folders"); err != nil {
			return AdminLibrary{}, err
		}
	}
	remoteFolders, err := s.checkRemoteFolders(r.Context(), req.RemoteFolders, nil)
	if err != nil {
		return AdminLibrary{}, err
	}
	caller := CallerFrom(r.Context())
	n := db.NewLibrary{Name: req.Name, Kind: req.Kind, Language: req.Language, Folders: req.Folders, RemoteFolders: remoteFolders,
		UserID: caller.UserID, IdempotencyKey: key, RequestHash: requestHash(req)}
	n.MetadataProvider, n.Region = req.MetadataProvider, req.Region
	if req.Options != nil {
		n.Options = optionsIn(*req.Options)
	}
	lib, err := s.db.NewLibrary(r.Context(), n)
	if err != nil {
		return AdminLibrary{}, libraryError(err)
	}
	// A new library is scanned at once.
	if _, err := s.scans.Scan(r.Context(), lib.PublicID, caller.UserID, false); err != nil {
		return AdminLibrary{}, err
	}
	found, err := s.db.AdminLibraries(r.Context(), lib.PublicID)
	if err != nil || len(found) == 0 {
		return AdminLibrary{}, errors.Join(err, ErrNotFound())
	}
	return s.libraryOut(r, found[0])
}

func (s *Server) editLibrary(r *http.Request, req LibraryEdit) (AdminLibrary, error) {
	lib, err := s.library(r)
	if err != nil {
		return AdminLibrary{}, err
	}
	edit := db.LibraryEdit{Name: req.Name, Language: req.Language, Folders: req.Folders}
	edit.MetadataProvider, edit.Region = req.MetadataProvider, req.Region
	if req.MetadataProvider != nil {
		if err := s.checkLibraryProvider(lib.Kind, *req.MetadataProvider); err != nil {
			return AdminLibrary{}, err
		}
	}
	// An edit made on a copy of the library that has since changed is refused, when the app
	// says which copy it was made on.
	if header := strings.Trim(strings.TrimPrefix(r.Header.Get("If-Match"), "W/"), `"`); header != "" && header != "*" {
		if edit.Revision, err = strconv.ParseInt(header, 10, 64); err != nil || edit.Revision <= 0 {
			return AdminLibrary{}, libraryError(db.ErrStale)
		}
	}
	if req.RemoteFolders != nil {
		// Only folders being added are listed: one already in the library may be away just now.
		have := map[db.RemoteFolder]bool{}
		for _, f := range lib.Paths {
			if f.RemoteID != 0 {
				have[db.RemoteFolder{SourceID: f.RemoteID, Path: f.Path}] = true
			}
		}
		folders, err := s.checkRemoteFolders(r.Context(), *req.RemoteFolders, have)
		if err != nil {
			return AdminLibrary{}, err
		}
		edit.RemoteFolders = &folders
	}
	if req.Options != nil {
		options := optionsIn(*req.Options)
		if req.Options.SegmentDetection == "" {
			options.SegmentDetection = lib.Options.SegmentDetection
		}
		edit.Options = &options
	}
	if req.Folders != nil {
		// Only folders being added are looked at: one already in the library may be away just now.
		have := map[string]bool{}
		for _, folder := range lib.Folders {
			if folder.Source == "" {
				have[folder.Path] = true
			}
		}
		var added []string
		for _, folder := range *req.Folders {
			if !have[folder] {
				added = append(added, folder)
			}
		}
		if len(added) > 0 {
			if err := s.checkFolders(added, "folders"); err != nil {
				return AdminLibrary{}, err
			}
		}
	}
	added, removed, err := s.db.EditLibrary(r.Context(), lib.PublicID, edit)
	if err != nil {
		return AdminLibrary{}, libraryError(err)
	}
	for _, pathID := range removed {
		if err := s.db.RemoveFolder(r.Context(), lib.ID, pathID); err != nil {
			return AdminLibrary{}, err
		}
	}
	if added {
		if _, err := s.scans.Scan(r.Context(), lib.PublicID, CallerFrom(r.Context()).UserID, false); err != nil {
			return AdminLibrary{}, err
		}
	}
	if lib, err = s.library(r); err != nil {
		return AdminLibrary{}, err
	}
	return s.libraryOut(r, lib)
}

// confirmed refuses a destructive request that does not carry what the owner was asked to
// type: the name for one thing, the count for several.
func confirmed(r *http.Request, want string) error {
	if got := r.URL.Query().Get("confirm"); !strings.EqualFold(strings.TrimSpace(got), strings.TrimSpace(want)) || got == "" {
		return refusal(http.StatusBadRequest, "confirmation_required", "Type the name or the count shown to confirm.", false)
	}
	return nil
}

func (s *Server) deleteLibrary(r *http.Request, _ None) (Job, error) {
	lib, err := s.library(r)
	if err != nil {
		return Job{}, err
	}
	if err := confirmed(r, lib.Name); err != nil {
		return Job{}, err
	}
	job, err := s.scans.DeleteLibrary(r.Context(), lib.PublicID, CallerFrom(r.Context()).UserID)
	if err != nil {
		return Job{}, err
	}
	return *jobOut(r, job), nil
}

func (s *Server) startScan(r *http.Request, req ScanRequest) (Job, error) {
	lib, err := s.library(r)
	if err != nil {
		return Job{}, err
	}
	job, err := s.scans.Scan(r.Context(), lib.PublicID, CallerFrom(r.Context()).UserID, req.AllFiles)
	if err != nil {
		return Job{}, err
	}
	return *jobOut(r, job), nil
}

func (s *Server) cancelScan(r *http.Request, _ None) (None, error) {
	lib, err := s.library(r)
	if err != nil {
		return None{}, err
	}
	job, err := s.db.JobFor(r.Context(), db.JobScan, lib.PublicID)
	if errors.Is(err, db.ErrNotFound) {
		return None{}, nil
	}
	if err != nil {
		return None{}, err
	}
	return None{}, s.scans.Cancel(r.Context(), job)
}

func (s *Server) scanStatus(r *http.Request, _ None) (ScanStatus, error) {
	lib, err := s.library(r)
	if err != nil {
		return ScanStatus{}, err
	}
	out := ScanStatus{State: "idle", Folders: s.foldersOut(lib.Library, lib.Folders), LastScanStartedAt: timePtr(lib.ScanStartedAt),
		LastScanFinishedAt: timePtr(lib.ScanFinishedAt)}
	job, err := s.db.JobFor(r.Context(), db.JobScan, lib.PublicID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return out, err
	}
	if err == nil {
		out.Job = jobOut(r, job)
		if job.State == db.JobWaiting || job.State == db.JobRunning {
			out.State = job.State
		}
	}
	if stats, ok := s.scans.Stats(lib.PublicID); ok {
		out.Counts = &ScanCounts{Files: stats.Files, FilesNew: stats.FilesNew, FilesChanged: stats.FilesChanged, FilesMoved: stats.FilesMoved,
			FilesMissing: stats.FilesGone, TitlesNew: stats.ItemsNew, FilesRead: stats.Probed, FilesToRead: stats.Unread}
	}
	return out, nil
}

// skippedPage is how many skipped files one page holds.
const skippedPage = 40

func (s *Server) skippedFiles(r *http.Request, _ None) (Page[SkippedFile], error) {
	out := Page[SkippedFile]{Items: []SkippedFile{}}
	lib, err := s.library(r)
	if err != nil {
		return out, err
	}
	offset := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		if offset, err = strconv.Atoi(cursor); err != nil || offset < 0 {
			return out, refusal(http.StatusBadRequest, "cursor_expired", "Start the list again.", false)
		}
	}
	files, total, err := s.db.SkippedFiles(r.Context(), lib.ID, offset, skippedPage)
	if err != nil {
		return out, err
	}
	for _, f := range files {
		out.Items = append(out.Items, SkippedFile{Path: f.Path, Reason: f.Problem.String(), Message: skippedMessages[f.Problem]})
	}
	if offset+len(files) < total {
		out.NextCursor = strconv.Itoa(offset + len(files))
	}
	return out, nil
}

// missingPage is the most missing titles one answer lists.
const missingPage = 200

func (s *Server) missingTitles(r *http.Request, _ None) (MissingTitles, error) {
	out := MissingTitles{Items: []MissingTitle{}}
	lib, err := s.library(r)
	if err != nil {
		return out, err
	}
	items, total, err := s.db.MissingItems(r.Context(), lib.ID, missingPage)
	if err != nil {
		return out, err
	}
	out.Total = total
	for _, m := range items {
		title := MissingTitle{ID: m.PublicID, Kind: m.Kind.String(), Title: m.Title, Year: m.Year, MissingSinceAt: time.UnixMilli(m.MissingSince).UTC()}
		switch rule := libraryOptionsOut(lib.Options).Trash; rule.Mode {
		case db.TrashAfterDays:
			title.RemovedAt = timePtr(time.UnixMilli(m.MissingSince).AddDate(0, 0, rule.Days).UnixMilli())
		case db.TrashAfterScan:
			title.RemovedAt = timePtr(m.MissingSince)
		}
		out.Items = append(out.Items, title)
	}
	return out, nil
}

func (s *Server) emptyTrash(r *http.Request, _ None) (TrashEmptied, error) {
	lib, err := s.library(r)
	if err != nil {
		return TrashEmptied{}, err
	}
	_, total, err := s.db.MissingItems(r.Context(), lib.ID, 1)
	if err != nil {
		return TrashEmptied{}, err
	}
	if total == 0 {
		return TrashEmptied{}, nil
	}
	if err := confirmed(r, strconv.Itoa(total)); err != nil {
		return TrashEmptied{}, err
	}
	removed, err := s.db.EmptyTrash(r.Context(), lib.ID, s.now())
	return TrashEmptied{Removed: removed}, err
}

func (s *Server) viewer(r *http.Request) db.Viewer {
	c := CallerFrom(r.Context())
	return db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}
}

func (s *Server) previewItemDeletion(r *http.Request, _ None) (DeletionPreview, error) {
	del, err := s.db.ItemDeletionPreview(r.Context(), s.viewer(r), r.PathValue("id"))
	if err != nil {
		return DeletionPreview{}, libraryError(err)
	}
	return settledDeletionPreview(del), nil
}

func (s *Server) previewVersionDeletion(r *http.Request, _ None) (DeletionPreview, error) {
	del, err := s.db.VersionDeletionPreview(r.Context(), s.viewer(r), r.PathValue("id"))
	if err != nil {
		return DeletionPreview{}, libraryError(err)
	}
	return settledDeletionPreview(del), nil
}

func settledDeletionPreview(del db.DeletionImpact) DeletionPreview {
	return DeletionPreview{Title: del.Title, Titles: del.Titles, Files: len(del.Files), Bytes: del.Bytes,
		ListEntries: del.ListEntries, OtherPeople: del.OtherStates, AlsoRemoves: titleRefs(del.AlsoRemoves)}
}

func deletionPreview(del db.Deletion) DeletionPreview {
	others := del.Others
	if del.MediaID != 0 {
		others = slices.DeleteFunc(slices.Clone(others), func(o db.SharedTitle) bool { return !o.RemovesTitle })
	}
	also := titleRefs(others)
	return DeletionPreview{Title: del.Title, Titles: del.Titles + len(also), Files: len(del.Files), Bytes: del.Bytes, ListEntries: del.ListEntries,
		OtherPeople: del.OtherStates, AlsoRemoves: also}
}

func titleRefs(others []db.SharedTitle) []TitleRef {
	out := []TitleRef{}
	for _, o := range others {
		if !slices.ContainsFunc(out, func(t TitleRef) bool { return t.ID == o.PublicID }) {
			out = append(out, TitleRef{ID: o.PublicID, Title: o.Title})
		}
	}
	return out
}

func (s *Server) deleteItem(r *http.Request, _ None) (DeletionResult, error) {
	del, err := s.db.ItemDeletion(r.Context(), s.viewer(r), r.PathValue("id"))
	if err != nil {
		return DeletionResult{}, libraryError(err)
	}
	if err := confirmed(r, del.Title); err != nil {
		return DeletionResult{}, err
	}
	return s.deleteFiles(r, del)
}

func (s *Server) deleteVersion(r *http.Request, _ None) (DeletionResult, error) {
	del, err := s.db.VersionDeletion(r.Context(), s.viewer(r), r.PathValue("id"))
	if err != nil {
		return DeletionResult{}, libraryError(err)
	}
	return s.deleteFiles(r, del)
}

// hasEntry reports whether a folder can be listed and holds anything.
func hasEntry(folder string) bool {
	dir, err := os.Open(folder)
	if err != nil {
		return false
	}
	defer dir.Close()
	names, _ := dir.Readdirnames(1)
	return len(names) > 0
}

// deleteFiles deletes a title's or a version's files from disk, then takes what is gone out
// of the library. A file is only deleted while it is still the file Portico last read.
// foldersThere refuses a delete when a library folder holding one of the files is not there,
// or is empty where it has files: a disk that is not connected. Nothing is deleted then and
// no title is removed. checked remembers the folders already looked at, so that a selection
// looks at each of its folders once, before any of its titles is deleted. A folder is looked
// at only for a first entry: a show has thousands of files under one folder that may hold
// thousands of others.
func foldersThere(checked map[string]bool, files []db.DiskFile) error {
	for _, f := range files {
		if checked[f.Root] {
			continue
		}
		checked[f.Root] = true
		if !hasEntry(f.Root) {
			return ErrConflict("folder_unavailable", "The library folder "+f.Root+" can't be reached: its disk may not be connected. Nothing was deleted.")
		}
	}
	return nil
}

func (s *Server) deleteFiles(r *http.Request, del db.Deletion) (DeletionResult, error) {
	out := DeletionResult{Kept: []KeptFile{}, AlsoRemoved: []TitleRef{}}
	if !del.AllowDelete {
		return out, refusal(http.StatusForbidden, "deleting_not_allowed", "Deleting files is turned off for this library.", false)
	}
	for _, f := range del.Files {
		if f.Remote {
			return out, ErrConflict("remote_storage", "Portico can't delete files on remote storage")
		}
	}
	if err := foldersThere(map[string]bool{}, del.Files); err != nil {
		return out, err
	}
	var deleted []int64
	for _, f := range del.Files {
		info, err := os.Stat(f.Path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Not where it was, and so not deleted by this: the next scan says what became of it.
			out.Kept = append(out.Kept, KeptFile{Path: f.Path, Reason: "The file was not found. Scan the library, then delete again."})
			continue
		case err != nil:
			out.Kept = append(out.Kept, KeptFile{Path: f.Path, Reason: "The file could not be reached."})
			continue
		case info.Size() != f.Size || info.ModTime().UnixNano() != f.ModifiedNS:
			out.Kept = append(out.Kept, KeptFile{Path: f.Path, Reason: "The file changed since Portico last read it. Scan the library, then delete again."})
			continue
		default:
			if err := os.Remove(f.Path); err != nil {
				out.Kept = append(out.Kept, KeptFile{Path: f.Path, Reason: "The file could not be deleted."})
				continue
			}
			out.FilesDeleted++
			out.Bytes += f.Size
		}
		deleted = append(deleted, f.ID)
	}
	// The files are gone from disk whatever becomes of the request: the record of it is
	// written even if the browser, or a proxy on the way, gave up waiting meanwhile. A title
	// would otherwise stay listed with no files until the next scan.
	if err := s.db.Deleted(context.WithoutCancel(r.Context()), del, deleted); err != nil {
		return out, err
	}
	if out.Removed = len(out.Kept) == 0; out.Removed {
		out.AlsoRemoved = deletionPreview(del).AlsoRemoves
	}
	return out, nil
}

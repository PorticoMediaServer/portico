package api

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The owner's libraries: making, changing and removing them, scanning, what a scan skipped,
// missing titles, and deleting files. Only the owner sees or changes any of it. A key made
// for a tool needs the library scope.

// LibraryOptions are the owner's choices for one library.
type LibraryOptions struct {
	// Depth is how much of each file a scan reads: files, basic (the default) or complete.
	Depth string `json:"depth"`
	// Trash says when missing titles are removed.
	Trash TrashRule `json:"trash"`
	// AllowDelete lets the owner delete files of this library from disk. It starts off.
	AllowDelete bool `json:"allowDelete"`
	// GroupSeries is "Group films from the same series": a collection appears once two films
	// of a series are in the library. On unless turned off; left out of an edit, it stays.
	GroupSeries *bool `json:"groupSeries,omitempty"`
}

// TrashRule is days (the default, with Days from 1 to 3,650, starting at 30), scan (after
// every scan) or manual (only when asked).
type TrashRule struct {
	Mode string `json:"mode"`
	Days int    `json:"days,omitempty"`
}

// LibraryFolder is one folder of a library and how the last scan found it.
type LibraryFolder struct {
	Path      string `json:"path"`
	Available bool   `json:"available"`
	// Problem says why a folder that is not available could not be scanned.
	Problem    string     `json:"problem,omitempty"`
	LastScanAt *time.Time `json:"lastScanAt,omitempty"`
}

// AdminLibrary is a library as its owner manages it.
type AdminLibrary struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Kind               string          `json:"kind"`
	Language           string          `json:"language,omitempty"`
	Folders            []LibraryFolder `json:"folders"`
	Options            LibraryOptions  `json:"options"`
	Scanning           bool            `json:"scanning"`
	LastScanStartedAt  *time.Time      `json:"lastScanStartedAt,omitempty"`
	LastScanFinishedAt *time.Time      `json:"lastScanFinishedAt,omitempty"`
}

// LibraryCreate makes a library and starts its first scan. Kind is movies, shows, anime,
// music, audiobooks, books or comics. Send an Idempotency-Key so a repeat does not make a second.
type LibraryCreate struct {
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Language string          `json:"language,omitempty"`
	Folders  []string        `json:"folders"`
	Options  *LibraryOptions `json:"options,omitempty"`
}

// LibraryEdit changes a library. Fields left out stay as they are. Folders is the whole
// list: a folder no longer in it leaves the library with its titles, and files on disk are
// not touched; an added folder is scanned.
type LibraryEdit struct {
	Name     *string         `json:"name,omitempty"`
	Language *string         `json:"language,omitempty"`
	Folders  *[]string       `json:"folders,omitempty"`
	Options  *LibraryOptions `json:"options,omitempty"`
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
}

// SkippedFile is a file the last scan could not use. Reason is damaged, not_supported,
// not_media or unreadable_name; Message says it in a sentence.
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
	group := o.GroupSeries == nil || *o.GroupSeries
	out := LibraryOptions{Depth: o.Depth, Trash: TrashRule{Mode: o.Trash.Mode, Days: o.Trash.Days}, AllowDelete: o.AllowDelete, GroupSeries: &group}
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
	return db.LibraryOptions{Depth: o.Depth, Trash: db.TrashRule{Mode: o.Trash.Mode, Days: o.Trash.Days}, AllowDelete: o.AllowDelete, GroupSeries: o.GroupSeries}
}

func foldersOut(folders []db.LibraryFolder) []LibraryFolder {
	out := []LibraryFolder{}
	for _, f := range folders {
		out = append(out, LibraryFolder{Path: f.Path, Available: f.State == "ok", Problem: f.Error, LastScanAt: timePtr(f.LastScanAt)})
	}
	return out
}

func jobOut(j db.Job) *Job {
	out := &Job{ID: j.PublicID, Kind: j.Kind, State: j.State, Error: j.Error}
	if j.State == db.JobRunning || j.Done > 0 {
		out.Progress = &JobProgress{Done: j.Done}
		if j.Total > 0 {
			out.Progress.Total = &j.Total
		}
	}
	return out
}

func (s *Server) libraryOut(r *http.Request, l db.AdminLibrary) (AdminLibrary, error) {
	out := AdminLibrary{ID: l.PublicID, Name: l.Name, Kind: l.Kind, Language: l.Language, Folders: foldersOut(l.Folders),
		Options: libraryOptionsOut(l.Options), LastScanStartedAt: timePtr(l.ScanStartedAt), LastScanFinishedAt: timePtr(l.ScanFinishedAt)}
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
		for _, refused := range refusedFolders {
			if folder == refused || strings.HasPrefix(folder, refused+"/") {
				return ErrField(at, "This folder cannot be used for a library.")
			}
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
	}
	return err
}

func (s *Server) libraryRoutes() {
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
	Handle(s, "GET /v1/admin/libraries/{id}/missing", Owner, s.missingTitles)
	Handle(s, "DELETE /v1/admin/libraries/{id}/missing", Owner, s.emptyTrash)
	Handle(s, "GET /v1/admin/items/{id}/deletion", Owner, s.previewItemDeletion)
	Handle(s, "DELETE /v1/admin/items/{id}", Owner, s.deleteItem).with(ScopeLibrary)
	Handle(s, "DELETE /v1/admin/versions/{id}", Owner, s.deleteVersion).with(ScopeLibrary)
}

// with gives a route a scope other than its default.
func (r *Route) with(scope Scope) *Route {
	r.Scope = scope
	return r
}

func (s *Server) listAdminLibraries(r *http.Request, _ None) (Page[AdminLibrary], error) {
	out := Page[AdminLibrary]{Items: []AdminLibrary{}}
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

func (s *Server) createLibrary(r *http.Request, req LibraryCreate) (AdminLibrary, error) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return AdminLibrary{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	if err := s.checkFolders(req.Folders, "folders"); err != nil {
		return AdminLibrary{}, err
	}
	caller := CallerFrom(r.Context())
	n := db.NewLibrary{Name: req.Name, Kind: req.Kind, Language: req.Language, Folders: req.Folders, UserID: caller.UserID,
		IdempotencyKey: key, RequestHash: requestHash(req)}
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
	if req.Options != nil {
		options := optionsIn(*req.Options)
		edit.Options = &options
	}
	if req.Folders != nil {
		// Only folders being added are looked at: one already in the library may be away just now.
		have := map[string]bool{}
		for _, folder := range lib.Folders {
			have[folder.Path] = true
		}
		var added []string
		for _, folder := range *req.Folders {
			if !have[folder] {
				added = append(added, folder)
			}
		}
		if len(*req.Folders) == 0 {
			return AdminLibrary{}, ErrField("folders", "A library needs at least one folder.")
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
	return *jobOut(job), nil
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
	return *jobOut(job), nil
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
	out := ScanStatus{State: "idle", Folders: foldersOut(lib.Folders), LastScanStartedAt: timePtr(lib.ScanStartedAt),
		LastScanFinishedAt: timePtr(lib.ScanFinishedAt)}
	job, err := s.db.JobFor(r.Context(), db.JobScan, lib.PublicID)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return out, err
	}
	if err == nil {
		out.Job = jobOut(job)
		if job.State == db.JobWaiting || job.State == db.JobRunning {
			out.State = job.State
		}
	}
	if stats, ok := s.scans.Stats(lib.PublicID); ok {
		out.Counts = &ScanCounts{Files: stats.Files, FilesNew: stats.FilesNew, FilesChanged: stats.FilesChanged, FilesMoved: stats.FilesMoved,
			FilesMissing: stats.FilesGone, TitlesNew: stats.ItemsNew, FilesRead: stats.Probed}
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
	del, err := s.db.ItemDeletion(r.Context(), s.viewer(r), r.PathValue("id"))
	if err != nil {
		return DeletionPreview{}, libraryError(err)
	}
	return DeletionPreview{Title: del.Title, Titles: del.Titles + len(del.Others), Files: len(del.Files), Bytes: del.Bytes, ListEntries: del.ListEntries,
		OtherPeople: del.OtherStates, AlsoRemoves: titleRefs(del.Others)}, nil
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
	// A library folder that is not there, or is empty where it has files, is a disk that is
	// not connected: nothing is deleted and no title is removed.
	// Each folder is looked at once and only for a first entry: a show has thousands of files
	// under one folder that may hold thousands of others.
	checked := map[string]bool{}
	for _, f := range del.Files {
		if checked[f.Root] {
			continue
		}
		checked[f.Root] = true
		if !hasEntry(f.Root) {
			return out, ErrConflict("folder_unavailable", "The library folder "+f.Root+" can't be reached: its disk may not be connected. Nothing was deleted.")
		}
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
	if err := s.db.Deleted(r.Context(), del, deleted); err != nil {
		return out, err
	}
	if out.Removed = len(out.Kept) == 0; out.Removed {
		out.AlsoRemoved = titleRefs(del.Others)
	}
	return out, nil
}

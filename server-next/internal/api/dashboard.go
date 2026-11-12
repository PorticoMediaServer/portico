package api

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/diskspace"
	"github.com/PorticoMediaServer/portico/server-next/internal/musicanalysis"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// The owner's Dashboard: one read of what is happening now, gathered from what the server
// already knows (in memory, or stored by the parts it reads) and nothing kept for it alone.
// The charts and the top lists have their own reads: statistics and Play history.

// Dashboard is the owner's Dashboard: alerts, what is running, and the totals of now.
type Dashboard struct {
	ObservedAt time.Time `json:"observedAt"`
	// Alerts are present while their cause lasts and go by themselves, most urgent first.
	Alerts []DashboardAlert `json:"alerts"`
	// Scans are the library scans waiting or running.
	Scans []DashboardScan `json:"scans"`
	// Tasks are the other jobs waiting or running, a backup being made and an upgrade still
	// finishing: the first few, running first, and how many there are.
	Tasks      DashboardTasks   `json:"tasks"`
	NowPlaying StreamTotals     `json:"nowPlaying"`
	Backups    DashboardBackups `json:"backups"`
	// OpenReports is how many problem and crash reports nobody has answered yet.
	OpenReports int `json:"openReports"`
}

// DashboardAlert is one line with one action. Code is storage_nearly_full, backup_failed,
// data_folder_readable, library_unreachable, remote_unreachable or update_available; Severity
// is warning or major.
type DashboardAlert struct {
	Code     string            `json:"code"`
	Severity string            `json:"severity"`
	Major    bool              `json:"major"`
	Params   map[string]string `json:"params"`
	Action   AlertAction       `json:"action"`
	// Count is how many of the thing the alert is about, where that is more than one.
	Count *int `json:"count,omitempty"`
}

// AlertAction is where the alert's one action goes: storage, backups, fix-permissions (POST
// /v1/admin/data-folder/permissions/fix, once the owner confirms), library-source (Target is
// the library), remote-source (Target is the source) or update; none is an alert nothing in
// the app can cure, whose params say why.
type AlertAction struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// DashboardScan is one library's scan. Phase is waiting, walking (finding the files) or
// reading (reading their details); Total is absent until the files to read are known.
type DashboardScan struct {
	Kind        string `json:"kind"`
	LibraryID   string `json:"libraryId"`
	LibraryName string `json:"libraryName"`
	Phase       string `json:"phase"`
	Discovered  int    `json:"discovered"`
	Analyzed    int    `json:"analyzed"`
	Total       *int   `json:"total,omitempty"`
	// Current is the file the scan is on, as a path within the library's folder, while running.
	Current string `json:"current,omitempty"`
}

// DashboardTasks is the first tasks and how many there are.
type DashboardTasks struct {
	Count int             `json:"count"`
	Items []DashboardTask `json:"items"`
}

// DashboardTask is a job, a backup being made, or an upgrade still finishing after the server
// started (kind upgrade, its phase saying how far it has got; waiting when it stopped on an
// error and is tried again at the next start). Cancel a job with
// DELETE /v1/jobs/{id}; a backup and an upgrade cannot be cancelled.
type DashboardTask struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	LibraryID   string `json:"libraryId,omitempty"`
	LibraryName string `json:"libraryName,omitempty"`
	Phase       string `json:"phase"`
	State       string `json:"state"`
	Processed   *int64 `json:"processed,omitempty"`
	Total       *int64 `json:"total,omitempty"`
	// Unit is what Processed and Total count, for a kind of job that counts: files, songs or
	// titles ("38 of 96 titles").
	Unit string `json:"unit,omitempty"`
}

// taskUnits is what each kind of job's own progress counts.
var taskUnits = map[string]string{db.JobScan: "files", musicanalysis.JobKind: "songs", jobRefreshLibrary: "titles",
	jobRefreshItems: "titles", jobEditItems: "titles", stateJobKind: "titles"}

// StreamTotals counts every stream, paused ones included.
type StreamTotals struct {
	Streams    int `json:"streams"`
	Away       int `json:"away"`
	Converting int `json:"converting"`
	// Problems are the streams whose player has waited ProblemAfter or more for its stream.
	Problems int `json:"problems"`
}

// DashboardBackups says when the newest backup was made. Read is false while the backup
// folder has not answered since the server started; Last is then not known.
type DashboardBackups struct {
	Read bool             `json:"read"`
	Last *DashboardBackup `json:"last,omitempty"`
}

// DashboardBackup is one backup in short.
type DashboardBackup struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Scheduled bool      `json:"scheduled"`
}

const (
	// dashboardTasks is how many tasks the Dashboard lists; the rest are counted.
	dashboardTasks = 3
	// dashboardAlerts is how many alerts the Dashboard shows at most.
	dashboardAlerts = 24
)

func (s *Server) dashboardRoutes() {
	Handle(s, "GET /v1/admin/dashboard", Owner, s.dashboard)
}

func (s *Server) dashboard(r *http.Request, _ None) (Dashboard, error) {
	ctx := r.Context()
	out := Dashboard{ObservedAt: s.now().UTC(), Alerts: []DashboardAlert{}, Scans: []DashboardScan{}, Tasks: DashboardTasks{Items: []DashboardTask{}}}
	libraries, err := s.db.AdminLibraries(ctx, "")
	if err != nil {
		return out, err
	}
	sources, err := s.db.RemoteSources(ctx)
	if err != nil {
		return out, err
	}
	jobs, err := s.db.UnfinishedJobs(ctx)
	if err != nil {
		return out, err
	}
	if out.OpenReports, err = s.db.OpenReports(ctx); err != nil {
		return out, err
	}
	out.Alerts = s.alerts(libraries, sources)

	byID := map[string]db.AdminLibrary{}
	for _, l := range libraries {
		byID[l.PublicID] = l
	}
	var tasks []DashboardTask
	// An upgrade whose slow part runs after the server started is a task until it ends.
	// One that stopped on an error waits for the next start, and says so.
	if version, reached, running, ok, err := s.db.UpgradeLeft(ctx); err != nil {
		return out, err
	} else if ok {
		state := db.JobRunning
		if !running {
			state = db.JobWaiting
		}
		tasks = append(tasks, DashboardTask{ID: "upgrade-" + strconv.Itoa(version), Kind: "upgrade", Phase: "reached " + strconv.FormatInt(reached, 10), State: state})
	}
	if p := s.backups.Running(); p != nil {
		tasks = append(tasks, DashboardTask{ID: p.ID, Kind: "backup", Phase: "copying", State: db.JobRunning})
	}
	for _, j := range jobs {
		lib, isLibrary := byID[j.Target]
		if j.Kind == db.JobScan && isLibrary {
			out.Scans = append(out.Scans, s.scanLine(j, lib))
			continue
		}
		t := DashboardTask{ID: j.PublicID, Kind: j.Kind, Phase: j.State, State: j.State}
		if isLibrary {
			t.LibraryID, t.LibraryName = lib.PublicID, lib.Name
		}
		if j.State == db.JobRunning || j.Done > 0 {
			t.Processed, t.Unit = &j.Done, taskUnits[j.Kind]
			if j.Total > 0 {
				t.Total = &j.Total
			}
		}
		tasks = append(tasks, t)
	}
	// Running first, then waiting, each oldest first as listed.
	slices.SortStableFunc(tasks, func(a, b DashboardTask) int { return btoi(b.State == db.JobRunning) - btoi(a.State == db.JobRunning) })
	out.Tasks.Count = len(tasks)
	out.Tasks.Items = append(out.Tasks.Items, tasks[:min(len(tasks), dashboardTasks)]...)

	for _, v := range s.playback.All() {
		out.NowPlaying.Streams++
		if v.Network != playback.NetworkLocal {
			out.NowPlaying.Away++
		}
		if v.Plan.Route == playback.RouteConverted {
			out.NowPlaying.Converting++
		}
		if !v.BufferingSince.IsZero() && s.now().Sub(v.BufferingSince) >= ProblemAfter {
			out.NowPlaying.Problems++
		}
	}
	for _, v := range s.viewings.All() {
		out.NowPlaying.Streams++
		if v.Away {
			out.NowPlaying.Away++
		}
		if v.Play != nil && v.Play.Route == playback.RouteConverted {
			out.NowPlaying.Converting++
		}
	}
	last, read := s.backups.Newest()
	out.Backups.Read = read
	if last != nil {
		out.Backups.Last = &DashboardBackup{ID: last.ID, CreatedAt: last.CreatedAt.UTC(), Scheduled: last.Scheduled}
	}
	return out, nil
}

// scanLine is one library's scan, with how far it has got where the scan is running here.
func (s *Server) scanLine(j db.Job, lib db.AdminLibrary) DashboardScan {
	out := DashboardScan{Kind: lib.Kind, LibraryID: lib.PublicID, LibraryName: lib.Name, Phase: "waiting"}
	if j.State != db.JobRunning {
		return out
	}
	out.Phase = "walking"
	if stats, ok := s.scans.Stats(lib.PublicID); ok {
		out.Discovered, out.Analyzed, out.Current = stats.Files, stats.Probed, stats.Current
		if stats.Unread > 0 {
			out.Phase = "reading"
		}
		if total := stats.Probed + stats.Unread; total > 0 {
			out.Total = &total
		}
	}
	return out
}

// alerts are the causes that last now, most urgent first: free space, a failed backup, a data
// folder other accounts can open, a library folder or remote source that cannot be reached,
// then a new version.
func (s *Server) alerts(libraries []db.AdminLibrary, sources []db.RemoteSource) []DashboardAlert {
	out := []DashboardAlert{}
	add := func(code, severity string, params map[string]string, kind, target string, count int) {
		a := DashboardAlert{Code: code, Severity: severity, Major: severity == "major", Params: params, Action: AlertAction{Kind: kind, Target: target}}
		if count > 1 {
			a.Count = &count
		}
		out = append(out, a)
	}
	// Free space is two calls on the data folder's disk; the sizes of its folders are left
	// to the Storage page, which walks them.
	disk := Storage{}
	disk.DiskBytes, _ = diskspace.Total(s.dataDir)
	disk.FreeBytes, _ = diskspace.Free(s.dataDir)
	if level := level(disk, storageDocument.Get(s.settings)); level != "ok" {
		add("storage_nearly_full", level, map[string]string{"freeBytes": strconv.FormatInt(disk.FreeBytes, 10),
			"diskBytes": strconv.FormatInt(disk.DiskBytes, 10)}, "storage", "", 0)
	}
	if failed := s.backups.Failed(); failed != nil {
		add("backup_failed", "major", map[string]string{"reason": clip(failed.Error())}, "backups", "", 0)
	}
	// One look at the data folder's own mode (data_folder.go).
	if p := s.dataFolderPermissions(); p.ReadableByOthers {
		params, action := map[string]string{"path": p.Path, "owner": p.Owner}, "fix-permissions"
		if !p.Fixable {
			params["reason"], params["message"], action = p.Reason, p.Message, "none"
		}
		add("data_folder_readable", "major", params, action, "", 0)
	}
	for _, l := range libraries {
		unreachable := 0
		for _, f := range l.Folders {
			if f.State == "unavailable" {
				unreachable++
			}
		}
		if unreachable > 0 {
			add("library_unreachable", "major", map[string]string{"library": l.Name}, "library-source", l.PublicID, unreachable)
		}
	}
	for _, src := range sources {
		if src.State == "unreachable" || src.State == "sign_in" {
			add("remote_unreachable", "major", map[string]string{"source": src.Name, "reason": src.State, "problem": clip(src.LastError)},
				"remote-source", src.PublicID, 0)
		}
	}
	if u := s.updates.State(); u.Newer && u.Newest != nil {
		add("update_available", "warning", map[string]string{"current": u.Version, "latest": u.Newest.Version}, "update", "", 0)
	}
	return out[:min(len(out), dashboardAlerts)]
}

// clip keeps an alert's text to 1,000 characters.
func clip(text string) string {
	if runes := []rune(text); len(runes) > 1000 {
		return string(runes[:1000])
	}
	return text
}

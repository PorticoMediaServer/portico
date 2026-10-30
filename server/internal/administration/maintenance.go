package administration

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"sort"
	"time"
)

// StorageCategory names one kind of bytes the server keeps, where it keeps them,
// and whether the maintenance page may remove them.
type StorageCategory struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Directories []string `json:"directories"`
	Cleanable   bool     `json:"cleanable"`
	// Regenerable says removing these bytes costs work, not content: the server
	// can produce them again from the media.
	Regenerable bool `json:"regenerable"`
	// RetentionKey names the retention setting that governs this category, or is
	// empty when the category has no age policy.
	RetentionKey string `json:"retentionKey"`
}

// StorageCategories is every category the storage care page measures. The
// directory names are the ones the composition root creates under the server
// state directory.
func StorageCategories() []StorageCategory {
	return []StorageCategory{
		{"artwork", "Artwork", "Posters, backdrops and thumbnails the server has fetched, extracted or been given.", []string{"artwork", "local-artwork"}, false, false, ""},
		{"trickplay", "Trickplay tiles", "Scrubbing tiles generated per file. This is usually the largest regenerable category.", []string{"analysis", "trickplay"}, false, true, "trickplay"},
		{"prepared-media", "Prepared media", "Optimised versions prepared for download or for a device that cannot play the original.", []string{"prepared-media"}, false, true, "prepared"},
		{"conversions", "Conversion working files", "Segments written while a stream was being converted. Safe to remove when nothing is playing.", []string{"hls", "transcode"}, false, true, "conversions"},
		{"subtitles", "Subtitles", "Extracted, downloaded and rendered subtitle tracks.", []string{"subtitles", "subtitle-video"}, false, true, "subtitles"},
		{"live-cache", "Live buffer", "Buffered live and channel segments.", []string{"linear-media"}, false, true, "conversions"},
		{"downloads", "Downloads", "Packages staged for a client to take offline.", []string{"downloads"}, false, true, "downloads"},
		{"logs", "Logs", "Diagnostic records written to disk.", []string{"logs"}, true, false, "logs"},
		{"backups", "Backups", "Server backups.", []string{"backups"}, false, false, ""},
		{"recordings", "Recordings", "Captured recordings. These are content: the maintenance page never removes them.", []string{"recordings"}, false, false, ""},
	}
}

// StorageUsage is one category's measurement.
type StorageUsage struct {
	StorageCategory
	Bytes      int64    `json:"bytes"`
	BytesText  string   `json:"bytesText"`
	FileCount  int64    `json:"fileCount"`
	Paths      []string `json:"paths"`
	Present    bool     `json:"present"`
	OldestDays int      `json:"oldestDays"`
}

// StorageReport is the whole storage care page.
type StorageReport struct {
	// Present only when the resolved state folder is observed on a known shared or network filesystem.
	StateDirectoryOnNetwork *bool          `json:"stateDirectoryOnNetwork,omitempty"`
	StateDirectory          string         `json:"stateDirectory"`
	Categories              []StorageUsage `json:"categories"`
	TotalBytes              int64          `json:"totalBytes"`
	TotalBytesText          string         `json:"totalBytesText"`
	ObservedAt              string         `json:"observedAt"`
	// Truncated says at least one category held more files than one sweep read.
	Truncated bool `json:"truncated"`
	// ArtworkEncoding counts every artwork rendition published, by slot and
	// format: within its byte cap, a counted outlier inside 1.5x the cap, or
	// refused because the cap was unreachable at the quality floor.
	ArtworkEncoding []metadata.ArtworkEncodingCounter `json:"artworkEncoding"`
}

// maxMeasuredFiles bounds one sweep so a huge artwork tree cannot hold a
// request open.
const maxMeasuredFiles = 200000

func measure(ctx context.Context, root string, now time.Time) (int64, int64, int, bool, error) {
	var bytes, count int64
	oldest, truncated := 0, false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if err != nil {
			// An unreadable subtree is skipped rather than failing the report.
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if count >= maxMeasuredFiles {
			truncated = true
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		bytes += info.Size()
		count++
		if age := int(now.Sub(info.ModTime()).Hours() / 24); age > oldest {
			oldest = age
		}
		return nil
	})
	return bytes, count, oldest, truncated, err
}

// Storage reports every category from the maintained storage snapshot. The
// owner is checked first; nothing is removed and nothing is written.
func (s *Service) Storage(ctx context.Context, auth Authorize) (StorageReport, error) {
	if s.StateDirectory() == "" {
		return StorageReport{StateDirectory: s.StateDirectory(), Categories: []StorageUsage{}, ObservedAt: timeFromMilliseconds(s.milliseconds())}, ErrUnavailable
	}
	// The owner is checked before anything is measured.
	if err := s.snapshot(ctx, auth, func(*sql.Tx) error { return nil }); err != nil {
		return StorageReport{}, err
	}
	snapshot, err := s.storageSnapshot(ctx)
	return snapshot.report, err
}

// measureStorage walks every category once. It reads the filesystem only.
// Artwork is measured as files: each blob is one content-addressed file,
// whichever sources and slots name it, so shared files count once.
func (s *Service) measureStorage(ctx context.Context) (StorageReport, error) {
	out := StorageReport{StateDirectory: s.StateDirectory(), Categories: []StorageUsage{}, ObservedAt: timeFromMilliseconds(s.milliseconds())}
	out.StateDirectoryOnNetwork = observedNetworkDirectory(out.StateDirectory)
	now := s.now()
	for _, category := range StorageCategories() {
		usage := StorageUsage{StorageCategory: category, Paths: []string{}}
		for _, name := range category.Directories {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			path := storagepaths.Path(s.StateDirectory(), name)
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				continue
			}
			usage.Present = true
			usage.Paths = append(usage.Paths, path)
			bytes, count, oldest, truncated, err := measure(ctx, path, now)
			if err != nil && ctx.Err() != nil {
				return out, ctx.Err()
			}
			usage.Bytes += bytes
			usage.FileCount += count
			if oldest > usage.OldestDays {
				usage.OldestDays = oldest
			}
			out.Truncated = out.Truncated || truncated
		}
		usage.BytesText = formatBytes(usage.Bytes)
		out.TotalBytes += usage.Bytes
		out.Categories = append(out.Categories, usage)
	}
	out.TotalBytesText = formatBytes(out.TotalBytes)
	counters, err := metadata.New(s.db, "").ArtworkEncodingCounts(ctx)
	if err != nil {
		return out, err
	}
	out.ArtworkEncoding = counters
	return out, nil
}

// CleanupRequest asks for one category to be swept. olderThanDays of zero uses
// the category's configured retention; a category with no retention and no
// explicit age is swept completely.
type CleanupRequest struct {
	OlderThanDays int    `json:"olderThanDays"`
	Confirmation  string `json:"confirmation"`
	OperationID   string `json:"operationId"`
}

// CleanupResult reports the sweep.
type CleanupResult struct {
	Category   string `json:"category"`
	FilesFreed int64  `json:"filesRemoved"`
	BytesFreed int64  `json:"bytesFreed"`
	BytesText  string `json:"bytesFreedText"`
	Skipped    int64  `json:"skipped"`
	OlderThan  int    `json:"olderThanDays"`
}

// Cleanup only removes unowned diagnostic files. Media, artwork and artifacts
// belong to reference/lease-aware services and must never be swept by age here. Content categories
// (recordings, backups) refuse.
func (s *Service) Cleanup(ctx context.Context, auth Authorize, category string, request CleanupRequest) (CleanupResult, error) {
	out := CleanupResult{Category: category}
	if !validOperationID(request.OperationID) || !safeText(request.Confirmation, 80) {
		return out, ErrInput
	}
	var chosen *StorageCategory
	for _, c := range StorageCategories() {
		if c.ID == category {
			copied := c
			chosen = &copied
		}
	}
	if chosen == nil {
		return out, ErrNotFound
	}
	if !chosen.Cleanable {
		return out, ErrDenied
	}
	if s.StateDirectory() == "" {
		return out, ErrUnavailable
	}
	if request.Confirmation != chosen.ID {
		return out, ErrConfirmation
	}
	days := request.OlderThanDays
	if days < 0 || days > 3650 {
		return out, ErrInput
	}
	settings, err := s.MaintenanceSettings(ctx, auth)
	if err != nil {
		return out, err
	}
	if days == 0 && chosen.RetentionKey != "" {
		days = settings.Settings.Retention[chosen.RetentionKey]
	}
	out.OlderThan = days
	cutoff := s.now().AddDate(0, 0, -days)
	s.files.Lock()
	defer s.files.Unlock()
	for _, name := range chosen.Directories {
		root := storagepaths.Path(s.StateDirectory(), name)
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || path == root {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			if days > 0 && info.ModTime().After(cutoff) {
				out.Skipped++
				return nil
			}
			if os.Remove(path) != nil {
				out.Skipped++
				return nil
			}
			out.FilesFreed++
			out.BytesFreed += info.Size()
			return nil
		})
		// Empty directories left behind after a sweep are removed bottom up.
		removeEmptyDirectories(root)
	}
	out.BytesText = formatBytes(out.BytesFreed)
	return out, nil
}

func removeEmptyDirectories(root string) {
	directories := []string{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() && path != root {
			directories = append(directories, path)
		}
		return nil
	})
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, path := range directories {
		_ = os.Remove(path)
	}
}

// MaintenanceWindow is when the server may do heavy work.
type MaintenanceWindow struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	Cadence         string   `json:"cadence"`
	Days            []string `json:"days"`
	StartMinute     int      `json:"startMinute"`
	DurationMinutes int      `json:"durationMinutes"`
	Timezone        string   `json:"timezone"`
	Tasks           []string `json:"tasks"`
}

// MaintenanceCadence is one published cadence preset with the day set it means.
type MaintenanceCadence struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Days        []string `json:"days"`
	Description string   `json:"description"`
}

// MaintenanceCadences are the presets a client offers instead of asking an owner
// to tick seven boxes.
func MaintenanceCadences() []MaintenanceCadence {
	return []MaintenanceCadence{
		{"nightly", "Every night", Days, "Runs every day inside the window."},
		{"weekdays", "Weekdays", Days[:5], "Monday to Friday."},
		{"weekends", "Weekends", Days[5:], "Saturday and Sunday."},
		{"weekly", "Once a week", []string{"sunday"}, "One night a week."},
		{"custom", "Chosen days", []string{}, "Exactly the days selected."},
	}
}

// MaintenanceTasks are the jobs a window may admit.
var MaintenanceTasks = []string{"backup", "analysis", "trickplay"}

// RetentionCategories bounds legacy cache settings. Artwork has no age policy;
// its owner retains referenced objects indefinitely.
func RetentionCategories() map[string]int {
	return map[string]int{"trickplay": 3650, "prepared": 3650, "conversions": 90, "subtitles": 3650, "downloads": 365, "logs": 365}
}

// MaintenanceSettingsDocument is the maintenance configuration.
type MaintenanceSettingsDocument struct {
	Windows   []MaintenanceWindow `json:"windows"`
	Retention map[string]int      `json:"retention"`
	// BackupKeepCount is how many backups the server keeps before the oldest is
	// offered for removal. Nothing is deleted automatically.
	BackupKeepCount int    `json:"backupKeepCount"`
	BackgroundSpeed string `json:"backgroundSpeed"`
}

// DefaultMaintenanceSettings is the answer before any owner write: one nightly
// window and the shipped retention values.
func DefaultMaintenanceSettings() MaintenanceSettingsDocument {
	retention := map[string]int{"trickplay": 365, "prepared": 90, "conversions": 7, "subtitles": 365, "downloads": 30, "logs": 30}
	return MaintenanceSettingsDocument{
		Windows: []MaintenanceWindow{
			{ID: "nightly", Name: "Backups", Enabled: true, Cadence: "nightly", Days: append([]string{}, Days...), StartMinute: 180, DurationMinutes: 240, Timezone: "", Tasks: []string{"backup"}},
			{ID: "heavy", Name: "Heavy background work", Enabled: false, Cadence: "nightly", Days: append([]string{}, Days...), StartMinute: 60, DurationMinutes: 300, Timezone: "", Tasks: []string{"analysis", "trickplay"}},
		},
		Retention:       retention,
		BackupKeepCount: 7,
		BackgroundSpeed: "normal",
	}
}

func normalizeMaintenance(v *MaintenanceSettingsDocument) {
	if v.BackgroundSpeed == "" {
		v.BackgroundSpeed = "normal"
	}
	if v.Windows == nil {
		v.Windows = []MaintenanceWindow{}
	}
	if v.Retention == nil {
		v.Retention = map[string]int{}
	}
	// Migrate the obsolete artwork age setting out of effective configuration.
	delete(v.Retention, "artwork")
	// The Plex-model backup keeps a count, not an age: drop the old retention
	// days key the same way.
	delete(v.Retention, "backups")
	defaults := DefaultMaintenanceSettings().Retention
	for key, value := range defaults {
		if _, ok := v.Retention[key]; !ok {
			v.Retention[key] = value
		}
	}
	for i := range v.Windows {
		sort.Strings(v.Windows[i].Days)
		sort.Strings(v.Windows[i].Tasks)
	}
}

func validateMaintenance(v *MaintenanceSettingsDocument) error {
	fields := []string{}
	if v.BackgroundSpeed != "gentle" && v.BackgroundSpeed != "normal" && v.BackgroundSpeed != "fast" {
		fields = append(fields, "settings.backgroundSpeed")
	}
	if len(v.Windows) != 2 {
		fields = append(fields, "settings.windows")
	}
	cadences := map[string]bool{}
	for _, c := range MaintenanceCadences() {
		cadences[c.ID] = true
	}
	seen := map[string]bool{}
	for i := range v.Windows {
		window := &v.Windows[i]
		if window.ID == "" || !safeText(window.ID, 64) || seen[window.ID] || window.Name == "" || !safeText(window.Name, 120) {
			fields = append(fields, "settings.windows.id")
			break
		}
		seen[window.ID] = true
		if window.ID != "nightly" && window.ID != "heavy" {
			fields = append(fields, "settings.windows.id")
			break
		}
		expected := []string{"backup"}
		if window.ID == "heavy" {
			expected = []string{"analysis", "trickplay"}
		}
		if len(window.Tasks) != len(expected) {
			fields = append(fields, "settings.windows.tasks")
			break
		}
		for _, task := range expected {
			if !oneOf(task, window.Tasks...) {
				fields = append(fields, "settings.windows.tasks")
				break
			}
		}

		if !cadences[window.Cadence] {
			fields = append(fields, "settings.windows.cadence")
			break
		}
		days := map[string]bool{}
		for _, day := range window.Days {
			if !oneOf(day, Days...) || days[day] {
				fields = append(fields, "settings.windows.days")
				break
			}
			days[day] = true
		}
		if len(window.Days) == 0 {
			fields = append(fields, "settings.windows.days")
			break
		}
		for _, task := range window.Tasks {
			if !oneOf(task, MaintenanceTasks...) {
				fields = append(fields, "settings.windows.tasks")
				break
			}
		}
		if window.StartMinute < 0 || window.StartMinute > 1439 {
			fields = append(fields, "settings.windows.startMinute")
			break
		}
		if window.Timezone != "" {
			if _, err := time.LoadLocation(window.Timezone); err != nil {
				fields = append(fields, "settings.windows.timezone")
				break
			}
		}
		clampInt(&window.DurationMinutes, 15, 1440)
	}
	maxima := RetentionCategories()
	for key, value := range v.Retention {
		limit, ok := maxima[key]
		if !ok {
			fields = append(fields, "settings.retention."+key)
			break
		}
		if value < 0 || value > limit {
			fields = append(fields, "settings.retention."+key)
			break
		}
	}
	if len(fields) > 0 {
		return invalid(fields...)
	}
	clampInt(&v.BackupKeepCount, 1, 365)
	return nil
}

const maintenanceScope = "maintenance"

// MaintenanceDocument is the maintenance page: settings plus the vocabularies.
type MaintenanceDocument struct {
	Revision    int64                       `json:"revision"`
	Digest      string                      `json:"digest"`
	Settings    MaintenanceSettingsDocument `json:"settings"`
	Cadences    []MaintenanceCadence        `json:"cadences"`
	Tasks       []string                    `json:"tasks"`
	Days        []string                    `json:"days"`
	Maxima      map[string]int              `json:"retentionMaxima"`
	Categories  []StorageCategory           `json:"storageCategories"`
	PausedUntil int64                       `json:"pausedUntilUnixMillis"`
}

func maintenanceDocument(d Document[MaintenanceSettingsDocument]) MaintenanceDocument {
	return MaintenanceDocument{d.Revision, d.Digest, d.Settings, MaintenanceCadences(), MaintenanceTasks, Days, RetentionCategories(), StorageCategories(), 0}
}

// MaintenanceSettings reads the maintenance page.
func (s *Service) MaintenanceSettings(ctx context.Context, auth Authorize) (MaintenanceDocument, error) {
	return s.loadMaintenanceRuntime(ctx, auth)
}

// SaveMaintenanceSettings writes the maintenance page.
func (s *Service) SaveMaintenanceSettings(ctx context.Context, auth Authorize, change Change[MaintenanceSettingsDocument]) (MaintenanceDocument, error) {
	return s.saveMaintenanceRuntime(ctx, auth, change)
}

// UpdateBuild describes one build.
type UpdateBuild struct {
	Version      string `json:"version"`
	BuildID      string `json:"buildId"`
	SourceDigest string `json:"sourceDigest"`
	BuiltAt      string `json:"builtAt"`
	Channel      string `json:"channel"`
	Notes        string `json:"notes,omitempty"`
	URL          string `json:"url,omitempty"`
}

// UpdateReport is the updates page. The server never installs anything: it
// reports what it is running and, when a feed is configured, what the feed says
// is newest on the configured channel.
type UpdateReport struct {
	Current         UpdateBuild  `json:"current"`
	Channel         string       `json:"channel"`
	FeedConfigured  bool         `json:"feedConfigured"`
	Latest          *UpdateBuild `json:"latest,omitempty"`
	State           string       `json:"state"`
	NotesURL        string       `json:"notesUrl,omitempty"`
	UpdateAvailable bool         `json:"updateAvailable"`
	CheckedAt       string       `json:"checkedAt"`
	Status          string       `json:"status"`
	Message         string       `json:"message,omitempty"`
	// AutoInstall is always false and is published so a client never implies
	// otherwise: applying an update is an operator action.
	AutoInstall bool `json:"autoInstall"`
}

// updateFeed is the document shape a release feed must publish.
type updateFeed struct {
	Channels map[string][]UpdateBuild `json:"channels"`
}

// Updates reports the running build and, when the owner configured a feed URL,
// the newest build the feed lists for the configured channel.
func (s *Service) Updates(ctx context.Context, current UpdateBuild, channel, feedURL string) UpdateReport {
	return s.updatesFrom(ctx, current, channel, feedURL, s.Fetch)
}

func (s *Service) updatesFrom(ctx context.Context, current UpdateBuild, channel, feedURL string, fetch func(context.Context, string) ([]byte, error)) UpdateReport {
	out := UpdateReport{Current: current, Channel: channel, CheckedAt: timeFromMilliseconds(s.milliseconds()), Status: "current", State: "current"}
	out.Current.Channel = channel
	if feedURL == "" {
		out.State = "unconfigured"
		out.Status = "no-feed"
		out.CheckedAt = ""
		out.Message = "No release feed is configured, so this server only reports the build it is running."
		return out
	}
	out.FeedConfigured = true
	if fetch == nil {
		out.State = "unavailable"
		out.Status = "unavailable"
		out.CheckedAt = ""
		out.Message = "This server is not configured to contact a release feed."
		return out
	}
	raw, err := fetch(ctx, feedURL)
	if err != nil {
		out.State = "unavailable"
		out.Status = "unavailable"
		out.Message = "The release feed could not be read."
		return out
	}
	var feed updateFeed
	if json.Unmarshal(raw, &feed) != nil || len(feed.Channels) == 0 {
		out.State = "unavailable"
		out.Status = "unavailable"
		out.Message = "The release feed was not in a shape this server understands."
		return out
	}
	builds := make([]UpdateBuild, 0, len(feed.Channels[channel]))
	for _, build := range feed.Channels[channel] {
		if _, valid := parseVersion(build.Version); valid {
			builds = append(builds, build)
		}
	}
	if len(builds) == 0 {
		out.Status = "no-release"
		out.Message = "The feed lists no release on the " + channel + " channel."
		return out
	}
	sort.Slice(builds, func(i, j int) bool { return compareVersions(builds[i].Version, builds[j].Version) > 0 })
	latest := builds[0]
	latest.Channel = channel
	latest.URL = safeUpdateURL(latest.URL)
	out.Latest = &latest
	out.NotesURL = latest.URL
	if _, valid := parseVersion(current.Version); !valid {
		out.Status = "unknown-version"
		out.Message = "The installed version is not a release version; compare its build identity before updating."
		return out
	}
	if compareVersions(latest.Version, current.Version) > 0 {
		out.State = "available"
		out.Status, out.UpdateAvailable = "update-available", true
	}
	return out
}

func safeUpdateURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Hostname() == "" || u.Scheme != "https" {
		return ""
	}
	return u.String()
}

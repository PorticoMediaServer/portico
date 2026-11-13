package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/downloads"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/prepare"
	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
)

func (s *Server) downloadRoutes() {
	// Downloads belong to a device, so these take a signed-in person and not a key.
	for _, route := range []*Route{
		Handle(s, "GET /v1/items/{id}/download-options", WithProfile, s.downloadOptions),
		Handle(s, "GET /v1/downloads", WithProfile, s.listDownloads),
		Handle(s, "POST /v1/downloads", WithProfile, s.askDownload),
		Handle(s, "GET /v1/downloads/usage", WithProfile, s.downloadUsage),
		Handle(s, "GET /v1/downloads/{id}", WithProfile, s.getDownload),
		Handle(s, "PATCH /v1/downloads/{id}", WithProfile, s.changeDownload),
		Handle(s, "DELETE /v1/downloads/{id}", WithProfile, s.deleteDownload),
		Handle(s, "GET /v1/downloads/{id}/items/{itemId}", WithProfile, s.downloadedItem),
		Handle(s, "DELETE /v1/downloads/{id}/items/{itemId}", WithProfile, s.dropDownloadTitle),
		Handle(s, "POST /v1/downloads/sync", WithProfile, s.syncDownloads),
	} {
		route.Scope = ScopeSession
	}
	Handle(s, "GET /v1/admin/downloads", Owner, s.downloadsByPerson)
	Handle(s, "DELETE /v1/admin/members/{id}/downloads", Owner, s.removePersonDownloads)
}

// PersonDownloads is one person's downloads on the owner's Storage page.
type PersonDownloads struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	Titles      int    `json:"titles"`
	CopiesBytes int64  `json:"copiesBytes"`
}

// DownloadsByPerson is the owner's view of downloads: by person, and the server's copies in
// all, each counted once.
type DownloadsByPerson struct {
	Items       []PersonDownloads `json:"items"`
	CopiesBytes int64             `json:"copiesBytes"`
	RoomBytes   *int64            `json:"roomBytes,omitempty"`
}

func (s *Server) downloadsByPerson(r *http.Request, _ None) (DownloadsByPerson, error) {
	people, err := s.db.DownloadsByPerson(r.Context())
	if err != nil {
		return DownloadsByPerson{}, err
	}
	out := DownloadsByPerson{Items: make([]PersonDownloads, 0, len(people))}
	for _, p := range people {
		out.Items = append(out.Items, PersonDownloads{UserID: p.UserPublicID, Username: p.Username, Titles: p.Titles, CopiesBytes: p.CopiesBytes})
	}
	if out.CopiesBytes, err = s.db.PreparedSpace(r.Context()); err != nil {
		return out, err
	}
	out.RoomBytes, err = s.downloadRoom(r.Context())
	return out, err
}

// removePersonDownloads frees the person's prepared server copies, preserving
// requests and files already held on devices. Other people's shared copies stay.
func (s *Server) removePersonDownloads(r *http.Request, _ None) (None, error) {
	err := s.prepare.ExpirePersonDownloads(r.Context(), r.PathValue("id"))
	if errors.Is(err, prepare.ErrReading) {
		return None{}, ErrConflict("download_in_use", "A prepared download is being fetched. Try again when the transfer has finished.")
	}
	return None{}, refuse(err)
}

// DownloadAsk is the body of POST /v1/downloads.
type DownloadAsk struct {
	// ItemID is a title, show, season, album or artist; ListID a playlist. One of the two.
	ItemID string `json:"itemId,omitempty"`
	ListID string `json:"listId,omitempty"`
	// VersionID chooses the version of one title; with none the server picks, as for playing.
	VersionID string `json:"versionId,omitempty"`
	// Quality is "original", a video quality ("720p-high") or a music bitrate ("320").
	Quality string `json:"quality"`
	// Selection, for more than one title: all (the default), unwatched, or next.
	Selection string `json:"selection,omitempty"`
	// KeepNext, with selection next: keep that many (1, 3 or 5) ready as they are watched.
	KeepNext int `json:"keepNext,omitempty"`
}

// DownloadChange is the body of PATCH /v1/downloads/{id}. Only what is sent changes.
type DownloadChange struct {
	Paused *bool `json:"paused,omitempty"`
	// Retry asks again for every copy that could not be made or has expired.
	Retry bool `json:"retry,omitempty"`
	// KeepNext sets the count, or 0 to stop keeping the next ones (what is there stays).
	KeepNext *int `json:"keepNext,omitempty"`
}

// DownloadTarget is what a request is for.
type DownloadTarget struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

// DownloadCounts are a request's titles by how far they have got.
type DownloadCounts struct {
	Titles  int `json:"titles"`
	Ready   int `json:"ready"`
	Waiting int `json:"waiting"`
	Failed  int `json:"failed"`
}

// Download is one request.
type Download struct {
	ID        string         `json:"id"`
	DeviceID  string         `json:"deviceId"`
	Target    DownloadTarget `json:"target"`
	VersionID string         `json:"versionId,omitempty"`
	Quality   string         `json:"quality"`
	Selection string         `json:"selection"`
	KeepNext  int            `json:"keepNext,omitempty"`
	Paused    bool           `json:"paused"`
	Counts    DownloadCounts `json:"counts"`
	CreatedAt time.Time      `json:"createdAt"`
	// Items is every title, on the list and the detail read.
	Items  []DownloadTitle `json:"items,omitempty"`
	status int
}

func (d Download) reply(http.Header) int { return d.status }

// DownloadTitle is one title of a request and where it stands: waiting (another is being
// prepared first), preparing, ready, paused, expired (the server's copy was removed: retry),
// failed (could not be prepared, with the reason), or leftOut (could not be included).
type DownloadTitle struct {
	ItemID string `json:"itemId"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	// ParentTitle is an episode's show or a track's album; SeasonNumber and EpisodeNumber an
	// episode's, Artist a track's. Each only for a title the profile may see.
	ParentTitle   string `json:"parentTitle,omitempty"`
	SeasonNumber  *int   `json:"seasonNumber,omitempty"`
	EpisodeNumber *int   `json:"episodeNumber,omitempty"`
	Artist        string `json:"artist,omitempty"`
	State         string `json:"state"`
	Reason        string `json:"reason,omitempty"`
	// DoneBytes and TotalBytes are how far a copy being prepared has got; the total is an
	// estimate.
	DoneBytes  int64 `json:"doneBytes,omitempty"`
	TotalBytes int64 `json:"totalBytes,omitempty"`
	// SizeBytes is the ready copy's size. KeptUntil is when the server removes its copy if
	// nobody fetches it again: what a browser shows as "kept until".
	SizeBytes int64      `json:"sizeBytes,omitempty"`
	KeptUntil *time.Time `json:"keptUntil,omitempty"`
}

// DownloadChoice is one quality a title can be downloaded at.
type DownloadChoice struct {
	Quality string `json:"quality"`
	Label   string `json:"label"`
	// SizeBytes is the size of the whole request; Estimated says it is "about".
	SizeBytes int64 `json:"sizeBytes"`
	Estimated bool  `json:"estimated"`
	// Prepared says the server makes the copy first.
	Prepared bool `json:"prepared"`
	// Available is false with a Reason when this choice cannot be asked for.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// DownloadOptions are the choices for downloading a title or a holder.
type DownloadOptions struct {
	ItemID string           `json:"itemId"`
	Kind   string           `json:"kind"`
	Titles int              `json:"titles"`
	Items  []DownloadChoice `json:"items"`
	// Default is the quality this device saved for downloads ("ask" to ask each time).
	Default string `json:"default"`
	// RoomBytes is what is left of the owner's download space; absent when there is no limit.
	RoomBytes *int64 `json:"roomBytes,omitempty"`
}

// DownloadUsage is the room the profile's downloads take on the server.
type DownloadUsage struct {
	// CopiesBytes is what the server's copies for this profile's requests take, each once.
	CopiesBytes int64  `json:"copiesBytes"`
	RoomBytes   *int64 `json:"roomBytes,omitempty"`
}

// DownloadFile is one file to fetch, by range, from an address that carries its own
// permission. SHA256 is given for a copy the server made; an original is checked by size.
type DownloadFile struct {
	URL       string `json:"url"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256,omitempty"`
	Container string `json:"container,omitempty"`
}

// DownloadSubtitle is a subtitle that comes with a download: inside the file, or a file
// beside it with its own address.
type DownloadSubtitle struct {
	PlaybackSubtitle
	URL string `json:"url,omitempty"`
	// Part is the file it goes with (from 1), for a smaller copy of a title in several files,
	// whose subtitles come from inside each part's own file.
	Part int `json:"part,omitempty"`
}

// DownloadedItem is everything one title needs to play with no server: its details and
// artwork, its files in order (several parts play as one title), chapters, intro and credit
// markers, audio, subtitles and lyrics. Seek previews are not part of a download.
type DownloadedItem struct {
	Item      Item               `json:"item"`
	VersionID string             `json:"versionId"`
	Quality   string             `json:"quality"`
	Files     []DownloadFile     `json:"files"`
	Audio     []PlaybackAudio    `json:"audio"`
	Subtitles []DownloadSubtitle `json:"subtitles"`
	Chapters  []PlaybackChapter  `json:"chapters"`
	Markers   []PlaybackMarker   `json:"markers"`
	Lyrics    []Lyrics           `json:"lyrics,omitempty"`
}

// DownloadSync is the body of POST /v1/downloads/sync: every title the device holds for this
// profile.
type DownloadSync struct {
	Holds []HeldTitle `json:"holds"`
}

// HeldTitle is one title a device holds, with the request it came with.
type HeldTitle struct {
	DownloadID string `json:"downloadId"`
	ItemID     string `json:"itemId"`
}

// HeldRemoval is a title the device must remove now: downloads_off (turned off for the
// account or the profile) or no_access (the profile may no longer see it).
type HeldRemoval struct {
	HeldTitle
	Reason string `json:"reason"`
}

// DownloadSyncReply says what the device must remove, what it can fetch now, and until when
// everything it keeps plays without reaching the server. PlayableForMs is the same span
// from the moment of the answer, for the device to count on its own clock that cannot be
// turned back; after it, downloads do not play until the device connects once, and nothing
// is deleted.
type DownloadSyncReply struct {
	PlayableUntil time.Time     `json:"playableUntil"`
	PlayableForMs int64         `json:"playableForMs"`
	Remove        []HeldRemoval `json:"remove"`
	Fetch         []HeldTitle   `json:"fetch"`
}

// maxHeld bounds what one device can say it holds.
const maxHeld = 20_000

// syncDownloads is a device reaching the server: every contact renews all of the profile's
// downloads on it. It answers even when the profile may download nothing more, so a person
// who lost access has their downloads removed.
func (s *Server) syncDownloads(r *http.Request, req DownloadSync) (DownloadSyncReply, error) {
	c := CallerFrom(r.Context())
	if c.SessionID == 0 {
		return DownloadSyncReply{}, downloadError(downloads.ErrNoDevice)
	}
	if len(req.Holds) > maxHeld {
		return DownloadSyncReply{}, ErrField("holds", "That is more titles than one device can hold.")
	}
	device, err := s.db.SessionDevice(r.Context(), c.SessionID)
	if err != nil {
		return DownloadSyncReply{}, playbackError(err)
	}
	held := make([]downloads.Held, len(req.Holds))
	for n, h := range req.Holds {
		held[n] = downloads.Held{Download: h.DownloadID, Item: h.ItemID}
	}
	synced, err := s.downloads.Sync(r.Context(), db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, device.ID, device.Platform, held)
	if err != nil {
		return DownloadSyncReply{}, err
	}
	out := DownloadSyncReply{PlayableUntil: synced.Until.UTC(), PlayableForMs: downloads.OfflineFor.Milliseconds(),
		Remove: make([]HeldRemoval, 0, len(synced.Remove)), Fetch: make([]HeldTitle, 0, len(synced.Fetch))}
	for _, rm := range synced.Remove {
		out.Remove = append(out.Remove, HeldRemoval{HeldTitle: HeldTitle{DownloadID: rm.Download, ItemID: rm.Item}, Reason: rm.Reason})
	}
	for _, f := range synced.Fetch {
		out.Fetch = append(out.Fetch, HeldTitle{DownloadID: f.Download, ItemID: f.Item})
	}
	return out, nil
}

func downloadError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, downloads.ErrOff):
		return refusal(http.StatusForbidden, "downloads_off", "Downloads are turned off on this server.", false)
	case errors.Is(err, downloads.ErrNotAllowed):
		return refusal(http.StatusForbidden, "downloads_not_allowed", "Downloads are turned off for this profile.", false)
	case errors.Is(err, downloads.ErrTV):
		return refusal(http.StatusForbidden, "downloads_not_on_tv", "This device can't download.", false)
	case errors.Is(err, downloads.ErrNoDevice):
		return refusal(http.StatusForbidden, "session_required", "Downloads belong to a device: sign in on it.", false)
	case errors.Is(err, downloads.ErrQuality):
		return ErrField("quality", "Choose original, a video quality or a music bitrate.")
	case errors.Is(err, downloads.ErrAboveLimit):
		return refusal(http.StatusForbidden, "above_download_limit", "This server doesn't allow downloads at this quality.", false)
	case errors.Is(err, db.ErrQueueFull):
		return refusal(http.StatusUnprocessableEntity, "too_many_titles", "That is more titles than one download can hold.", false)
	case errors.Is(err, db.ErrKeyReused):
		return errKeyReused()
	case errors.Is(err, db.ErrConflict):
		return ErrConflict("download_exists", "This is already being downloaded to this device with other choices. Change that download, or remove it first.")
	case errors.Is(err, prepare.ErrSource):
		e := refusal(http.StatusServiceUnavailable, "source_unavailable", "The file for this title can't be reached right now.", true)
		e.RetryAfterMs = 30_000
		return e
	}
	return refuse(err)
}

// downloadCaller is the viewer and the device a downloads route acts for. A device that may
// not download is refused here, before anything is read.
func (s *Server) downloadCaller(r *http.Request) (db.Viewer, db.SessionDevice, error) {
	c := CallerFrom(r.Context())
	v := db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}
	if c.SessionID == 0 {
		return v, db.SessionDevice{}, downloadError(downloads.ErrNoDevice)
	}
	device, err := s.db.SessionDevice(r.Context(), c.SessionID)
	if err != nil {
		return v, device, playbackError(err)
	}
	return v, device, downloadError(s.downloads.May(r.Context(), v, device.Platform))
}

func (s *Server) askDownload(r *http.Request, req DownloadAsk) (Download, error) {
	v, device, err := s.downloadCaller(r)
	if err != nil {
		return Download{}, err
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return Download{}, err
	}
	if req.Selection == "" {
		req.Selection = "all"
	}
	switch {
	case (req.ItemID == "") == (req.ListID == ""):
		return Download{}, ErrField("itemId", "Say which title, show, season, album, artist or playlist to download.")
	case !slices.Contains([]string{"all", "unwatched", "next"}, req.Selection):
		return Download{}, ErrField("selection", "The selection is all, unwatched or next.")
	case req.KeepNext != 0 && (req.Selection != "next" || !slices.Contains([]int{1, 3, 5}, req.KeepNext)):
		return Download{}, ErrField("keepNext", "Keep the next 1, 3 or 5, with the selection next.")
	case req.VersionID != "" && req.ListID != "":
		return Download{}, ErrField("versionId", "A version is chosen for one title.")
	}
	dl, created, err := s.downloads.Create(r.Context(), downloads.Ask{Viewer: v, DeviceID: device.ID, Item: req.ItemID, List: req.ListID,
		Version: req.VersionID, Quality: req.Quality, Selection: req.Selection, KeepNext: req.KeepNext, Key: key, Hash: requestHash(req)})
	if err != nil {
		return Download{}, downloadError(err)
	}
	out := downloadShape(dl)
	if created {
		out.status = http.StatusCreated
	}
	return out, nil
}

func downloadShape(d db.Download) Download {
	out := Download{ID: d.PublicID, DeviceID: d.DevicePublicID, VersionID: d.VersionPublicID, Quality: d.Quality, Selection: d.Selection,
		KeepNext: d.KeepNext, Paused: d.Paused, CreatedAt: d.CreatedAt.UTC(),
		Counts: DownloadCounts{Titles: d.Titles, Ready: d.Ready, Waiting: d.Waiting, Failed: d.Failed}}
	out.Target = DownloadTarget{Kind: d.Kind.String(), ID: d.ItemPublicID, Title: d.Title}
	if d.ListPublicID != "" {
		out.Target = DownloadTarget{Kind: "playlist", ID: d.ListPublicID, Title: d.Title}
	}
	return out
}

func (s *Server) listDownloads(r *http.Request, _ None) (Page[Download], error) {
	v, device, err := s.downloadCaller(r)
	if err != nil {
		return Page[Download]{}, err
	}
	// The browser's Downloads page shows every request of the profile; an app shows its own.
	deviceID := device.ID
	if r.URL.Query().Get("scope") == "profile" {
		deviceID = 0
	}
	list, err := s.db.Downloads(r.Context(), v.ProfileID, deviceID)
	if err != nil {
		return Page[Download]{}, err
	}
	// Every request's titles in one read, so the page needs no read per request.
	byRequest, err := s.db.DeviceDownloadTitles(r.Context(), v.ProfileID, deviceID)
	if err != nil {
		return Page[Download]{}, err
	}
	shaped, err := s.titlesShape(r.Context(), v, list, byRequest)
	return Page[Download]{Items: shaped}, err
}

func (s *Server) getDownload(r *http.Request, _ None) (Download, error) {
	v, _, err := s.downloadCaller(r)
	if err != nil {
		return Download{}, err
	}
	dl, titles, err := s.db.DownloadOf(r.Context(), v.ProfileID, r.PathValue("id"))
	if err != nil {
		return Download{}, downloadError(err)
	}
	shaped, err := s.titlesShape(r.Context(), v, []db.Download{dl}, map[int64][]db.DownloadTitle{dl.ID: titles})
	if err != nil {
		return Download{}, err
	}
	if len(shaped) == 0 {
		return Download{}, ErrNotFound()
	}
	return shaped[0], nil
}

// titlesShape shapes requests with their titles and says where each title sits (show, season
// and episode; album and artist). What the profile may not see is absent, not locked: a
// request for a show, season, album, artist or title it may no longer see is left out (it
// comes back if access does, and the sync has the device remove the files), as is any title
// it may not see or the person took out, and the counts are of the titles listed. One read
// for what the profile may see, whatever the number of requests and titles.
func (s *Server) titlesShape(ctx context.Context, v db.Viewer, list []db.Download, byRequest map[int64][]db.DownloadTitle) ([]Download, error) {
	var ids []int64
	for _, dl := range list {
		if dl.ItemID != 0 {
			ids = append(ids, dl.ItemID)
		}
		for _, t := range byRequest[dl.ID] {
			ids = append(ids, t.ItemID)
		}
	}
	places, err := s.db.TitlePlaces(ctx, v, ids)
	if err != nil {
		return nil, err
	}
	keep := time.Duration(s.downloads.Owner().KeepDays) * 24 * time.Hour
	out := make([]Download, 0, len(list))
	for _, dl := range list {
		if _, visible := places[dl.ItemID]; dl.ItemID != 0 && !visible {
			continue
		}
		shaped := downloadShape(dl)
		shaped.Items, shaped.Counts = []DownloadTitle{}, DownloadCounts{}
		for _, t := range byRequest[dl.ID] {
			place, visible := places[t.ItemID]
			if !visible || t.State == db.DownloadDropped {
				continue
			}
			title := s.titleShape(dl, t, keep)
			title.ParentTitle, title.SeasonNumber, title.EpisodeNumber, title.Artist = place.ParentTitle, place.SeasonNumber, place.EpisodeNumber, place.Artist
			shaped.Items = append(shaped.Items, title)
			shaped.Counts.Titles++
			switch title.State {
			case "ready":
				shaped.Counts.Ready++
			case "waiting", "preparing", "paused":
				shaped.Counts.Waiting++
			default:
				shaped.Counts.Failed++
			}
		}
		out = append(out, shaped)
	}
	return out, nil
}

func (s *Server) titleShape(dl db.Download, t db.DownloadTitle, keep time.Duration) DownloadTitle {
	out := DownloadTitle{ItemID: t.ItemPublicID, Kind: t.Kind.String(), Title: t.Title}
	p := t.Prepared
	switch {
	case t.State != db.DownloadWanted:
		out.State, out.Reason = "leftOut", t.Reason
	case dl.Quality == downloads.Original:
		out.State = "ready"
	case p == nil:
		out.State = "expired"
	case p.State == db.PreparedReady:
		until := p.LastUsedAt.Add(keep).UTC()
		out.State, out.SizeBytes, out.KeptUntil = "ready", p.Size, &until
	case p.State == db.PreparedFailed:
		out.State, out.Reason = "failed", p.Error
	case dl.Paused:
		out.State = "paused"
	default:
		out.State = "waiting"
		if done, total, running := s.prepare.Progress(p.ID); running {
			out.State, out.DoneBytes, out.TotalBytes = "preparing", done, max(total, done)
		}
	}
	return out
}

func (s *Server) changeDownload(r *http.Request, req DownloadChange) (Download, error) {
	v, _, err := s.downloadCaller(r)
	if err != nil {
		return Download{}, err
	}
	if req.KeepNext != nil && !slices.Contains([]int{0, 1, 3, 5}, *req.KeepNext) {
		return Download{}, ErrField("keepNext", "Keep the next 1, 3 or 5, or 0 to stop.")
	}
	id := r.PathValue("id")
	if req.KeepNext != nil && *req.KeepNext > 0 {
		// Keeping the next ones is a rule for a "next unwatched" download; set on one of all
		// the titles it would remove the rest.
		dl, _, err := s.db.DownloadOf(r.Context(), v.ProfileID, id)
		if err != nil {
			return Download{}, downloadError(err)
		}
		if dl.Selection != "next" {
			return Download{}, ErrField("keepNext", "Keeping the next ones is for a download of the next unwatched titles.")
		}
	}
	if req.Paused != nil || req.KeepNext != nil {
		if err := s.downloads.Change(r.Context(), v, id, req.Paused, req.KeepNext); err != nil {
			return Download{}, downloadError(err)
		}
	}
	if req.Retry {
		if err := s.downloads.Retry(r.Context(), v, id); err != nil {
			return Download{}, downloadError(err)
		}
	}
	return s.getDownload(r, None{})
}

func (s *Server) deleteDownload(r *http.Request, _ None) (None, error) {
	v, _, err := s.downloadCaller(r)
	if err != nil {
		return None{}, err
	}
	return None{}, downloadError(s.downloads.Delete(r.Context(), v, r.PathValue("id")))
}

// dropDownloadTitle takes one title out of a show, season, album, artist or playlist download:
// the device is told to remove it at its next contact and it does not come back. Safe to
// repeat: a title already taken out, or not in the download, succeeds.
func (s *Server) dropDownloadTitle(r *http.Request, _ None) (None, error) {
	v, _, err := s.downloadCaller(r)
	if err != nil {
		return None{}, err
	}
	return None{}, downloadError(s.downloads.Drop(r.Context(), v, r.PathValue("id"), r.PathValue("itemId")))
}

func (s *Server) downloadRoom(ctx context.Context) (*int64, error) {
	room, limited, err := s.prepare.Room(ctx)
	if !limited {
		return nil, err
	}
	return &room, err
}

func (s *Server) downloadUsage(r *http.Request, _ None) (DownloadUsage, error) {
	v, _, err := s.downloadCaller(r)
	if err != nil {
		return DownloadUsage{}, err
	}
	used, err := s.db.ProfileCopiesSpace(r.Context(), v.ProfileID)
	if err != nil {
		return DownloadUsage{}, err
	}
	room, err := s.downloadRoom(r.Context())
	return DownloadUsage{CopiesBytes: used, RoomBytes: room}, err
}

// downloadOptions answers the choices for a title, or for a holder with ?selection= and
// ?keepNext=, with the size of each for the whole request.
func (s *Server) downloadOptions(r *http.Request, _ None) (DownloadOptions, error) {
	v, device, err := s.downloadCaller(r)
	if err != nil {
		return DownloadOptions{}, err
	}
	ctx, q := r.Context(), r.URL.Query()
	_, kind, err := s.db.VisibleItem(ctx, v, r.PathValue("id"))
	if err != nil {
		return DownloadOptions{}, downloadError(err)
	}
	selection := q.Get("selection")
	if selection == "" {
		selection = "all"
	}
	keepNext, _ := strconv.Atoi(q.Get("keepNext"))
	sources, err := s.downloads.Titles(ctx, v, r.PathValue("id"), "", selection, keepNext)
	if err != nil {
		return DownloadOptions{}, downloadError(err)
	}
	owner := s.downloads.Owner()
	room, err := s.downloadRoom(ctx)
	if err != nil {
		return DownloadOptions{}, err
	}
	out := DownloadOptions{ItemID: r.PathValue("id"), Kind: kind.String(), Titles: len(sources), Items: []DownloadChoice{}, RoomBytes: room}
	prefs, err := s.db.Preferences(ctx, v.ProfileID, device.ID)
	if err != nil {
		return out, err
	}
	music := len(sources) > 0 && prepare.Music(sources[0].Kind)
	out.Default = savedChoice(prefs, music)

	original := DownloadChoice{Quality: downloads.Original, Label: "Original", Available: owner.HighestQuality == downloads.Original}
	if !original.Available {
		original.Reason = "This server doesn't allow downloads of the original."
	}
	var recipes []string
	if music {
		for _, p := range qualityladder.Music() {
			if p.AudioKbps > 0 {
				recipes = append(recipes, p.ID)
			}
		}
	} else {
		for _, p := range qualityladder.Fixed() {
			recipes = append(recipes, p.ID)
		}
	}
	choices := map[string]*DownloadChoice{}
	offered := map[string]bool{}
	for _, source := range sources {
		version, ok := downloads.Pick(source.Versions, q.Get("versionId"))
		if !ok {
			continue
		}
		for _, part := range version.Parts {
			original.SizeBytes += part.Size
		}
		for _, recipe := range recipes {
			c := choices[recipe]
			if c == nil {
				c = &DownloadChoice{Quality: recipe, Label: qualityLabel(recipe)}
				choices[recipe] = c
			}
			if !slices.Contains(prepare.Recipes(version, source.Kind, downloads.Original), recipe) {
				// This title is already this small: it comes as its original.
				for _, part := range version.Parts {
					c.SizeBytes += part.Size
				}
				continue
			}
			offered[recipe] = true
			if p, err := s.db.FindPrepared(ctx, version.MediaID(), recipe); len(sources) == 1 && err == nil && p.State == db.PreparedReady {
				c.SizeBytes += p.Size
				continue
			}
			c.SizeBytes += prepare.EstimatedSize(version, recipe)
			c.Estimated, c.Prepared = true, true
		}
	}
	out.Items = append(out.Items, original)
	converts := s.prepare.Converts()
	for _, recipe := range recipes {
		c := choices[recipe]
		if c == nil || !offered[recipe] {
			continue // a step above every title's own quality is not offered
		}
		c.Available = true
		switch {
		case !prepare.WithinLimit(recipe, owner.HighestQuality):
			c.Available, c.Reason = false, "This server doesn't allow downloads at this quality."
		case c.Prepared && !converts:
			c.Available, c.Reason = false, "Converting is off on this server."
		case c.Prepared && room != nil && c.SizeBytes > *room:
			c.Available, c.Reason = false, "The server's download space is full."
		}
		out.Items = append(out.Items, *c)
	}
	return out, nil
}

func qualityLabel(recipe string) string {
	if p, ok := qualityladder.Find(recipe); ok {
		return p.Label
	}
	return recipe + " kbps"
}

// savedChoice is the device's saved download quality, as stored or its default.
func savedChoice(prefs map[string]string, music bool) string {
	key, fallback := "downloads.quality", downloads.Original
	if music {
		key, fallback = "downloads.musicQuality", "320"
	}
	if raw, ok := prefs[key]; ok {
		if value, err := strconv.Unquote(raw); err == nil {
			return value
		}
	}
	return fallback
}

// downloadedItem answers what to fetch for one title of a request.
func (s *Server) downloadedItem(r *http.Request, _ None) (DownloadedItem, error) {
	v, _, err := s.downloadCaller(r)
	if err != nil {
		return DownloadedItem{}, err
	}
	ctx := r.Context()
	dl, title, err := s.db.DownloadedTitle(ctx, v.ProfileID, r.PathValue("id"), r.PathValue("itemId"))
	if err != nil || title.State != db.DownloadWanted {
		return DownloadedItem{}, ErrNotFound()
	}
	detail, err := s.db.Item(ctx, v, title.ItemPublicID)
	if err != nil {
		return DownloadedItem{}, refuse(err)
	}
	item, err := s.db.PlaybackItem(ctx, v, title.ItemPublicID)
	if err != nil {
		return DownloadedItem{}, refuse(err)
	}
	var version db.PlaybackVersion
	for _, candidate := range item.Versions {
		if candidate.MediaID() == title.MediaID {
			version = candidate
		}
	}
	if version.PublicID == "" {
		return DownloadedItem{}, downloadError(prepare.ErrSource)
	}
	grant := s.downloads.Grant(v, CallerFrom(ctx).SessionID, dl.PublicID, title.ItemPublicID)
	base := "/v1/media/" + grant + "/"
	shaped := versionOut(version)
	out := DownloadedItem{Item: detailShape(detail), VersionID: version.PublicID, Quality: dl.Quality, Files: []DownloadFile{},
		Audio: shaped.Audio, Subtitles: []DownloadSubtitle{}, Chapters: []PlaybackChapter{}, Markers: []PlaybackMarker{}}
	if dl.Quality == downloads.Original {
		for n, part := range version.Parts {
			out.Files = append(out.Files, DownloadFile{URL: base + "file/" + strconv.Itoa(n+1), SizeBytes: part.Size,
				Container: strings.TrimPrefix(extensionOf(part.Name), ".")})
		}
	} else {
		p := title.Prepared
		if p == nil || p.State != db.PreparedReady {
			return DownloadedItem{}, ErrConflict("not_ready", "This title is not ready to download yet.")
		}
		hashes := strings.Fields(p.ContentHash)
		for n, file := range s.prepare.Files(*p) {
			info, err := os.Stat(file)
			if err != nil || n >= len(hashes) {
				return DownloadedItem{}, ErrConflict("not_ready", "This title is not ready to download yet.")
			}
			out.Files = append(out.Files, DownloadFile{URL: base + "file/" + strconv.Itoa(n+1), SizeBytes: info.Size(), SHA256: hashes[n],
				Container: p.Container})
		}
	}
	inside := 0 // a track's place among the subtitle tracks inside the file
	for n, subtitle := range shaped.Subtitles {
		track := subtitleTrack(version, n)
		switch {
		case subtitle.Source == "sidecar":
			out.Subtitles = append(out.Subtitles, DownloadSubtitle{PlaybackSubtitle: subtitle, URL: base + "subtitles/" + playback.TrackID(track)})
		case dl.Quality == downloads.Original:
			// Inside the file the device keeps.
			out.Subtitles = append(out.Subtitles, DownloadSubtitle{PlaybackSubtitle: subtitle})
		default:
			// A copy has no subtitle tracks; a text one was written out beside it.
			if _, err := os.Stat(s.prepare.SubtitleFile(*title.Prepared, 1, inside)); err == nil {
				subtitle.Format = "vtt"
				out.Subtitles = append(out.Subtitles, DownloadSubtitle{PlaybackSubtitle: subtitle, URL: base + "subtitles/" + playback.TrackID(track),
					Part: partOf(len(version.Parts), 1)})
			}
		}
		if subtitle.Source != "sidecar" {
			inside++
		}
	}
	// The subtitles inside the later files of a title in several files, for a copy.
	for n := 1; dl.Quality != downloads.Original && n < len(version.Parts); n++ {
		place := 0
		for _, stream := range version.Parts[n].Streams {
			if stream.Kind != db.StreamSubtitle {
				continue
			}
			id := downloads.PartSubtitleID(n+1, place)
			if _, err := os.Stat(s.prepare.SubtitleFile(*title.Prepared, n+1, place)); err == nil {
				out.Subtitles = append(out.Subtitles, DownloadSubtitle{PlaybackSubtitle: PlaybackSubtitle{ID: id, Format: "vtt", Source: "embedded",
					Language: stream.Language, Title: stream.Title, Forced: stream.Forced, SDH: stream.HearingImpaired, Default: stream.Default},
					URL: base + "subtitles/" + id, Part: n + 1})
			}
			place++
		}
	}
	for _, m := range version.Markers {
		if m.Kind == db.MarkerChapter {
			out.Chapters = append(out.Chapters, PlaybackChapter{StartMs: m.StartMS, Title: m.Title})
			continue
		}
		out.Markers = append(out.Markers, PlaybackMarker{ID: m.Kind.String() + "-" + strconv.FormatInt(m.StartMS, 10), Type: playbackMarkerType(m.Kind),
			StartMs: m.StartMS, EndMs: m.EndMS, Confidence: markerConfidence[m.Source], AutomaticSafe: m.AutomaticSafe})
	}
	if title.Kind == db.KindTrack {
		if out.Lyrics, err = s.lyricsOf(ctx, v, title.ItemPublicID); err != nil {
			return DownloadedItem{}, err
		}
	}
	return out, nil
}

// partOf is the part a subtitle goes with, said only for a title in several files.
func partOf(parts, part int) int {
	if parts > 1 {
		return part
	}
	return 0
}

// subtitleTrack is the nth subtitle track of a version, in the order versionOut lists them.
func subtitleTrack(v db.PlaybackVersion, n int) db.Track {
	for _, t := range v.Tracks {
		if t.Kind != db.StreamSubtitle {
			continue
		}
		if n == 0 {
			return t
		}
		n--
	}
	return db.Track{}
}

func extensionOf(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i:]
	}
	return ""
}

// serveDownload answers a download's media address: the bytes of one file, by range, so a
// transfer carries on where it stopped. With ?save=1 a browser saves it as a file.
func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request, grant, file string) {
	f, err := s.downloads.Open(r.Context(), grant, file, s.fromHome(r))
	switch {
	case errors.Is(err, db.ErrAwayFromHome), errors.Is(err, db.ErrOutsideHours):
		s.writeError(w, r, err)
		return
	case errors.Is(err, downloads.ErrNotReady):
		s.writeError(w, r, ErrConflict("not_ready", "This title is not ready to download yet."))
		return
	case errors.Is(err, prepare.ErrSource):
		s.writeError(w, r, downloadError(err))
		return
	case err != nil:
		s.writeError(w, r, ErrNotFound())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", f.ContentType)
	w.Header().Set("ETag", f.ETag)
	w.Header().Set("Cache-Control", "private, no-store")
	if r.URL.Query().Get("save") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+pathEscape(f.Name))
	}
	// ServeContent reads the range itself and serves exactly what it read, so what is sent is
	// what was asked for, If-Range against the ETag included.
	http.ServeContent(w, r, "", f.Modified, f.ReadSeeker)
}

// pathEscape writes a file name for Content-Disposition's filename* (RFC 5987).
func pathEscape(name string) string {
	var b strings.Builder
	for _, c := range []byte(name) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)|0x100, 16)[1:]))
		}
	}
	return b.String()
}

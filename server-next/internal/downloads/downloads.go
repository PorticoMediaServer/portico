// Package downloads is what a profile asks to have on a device, and the bytes it fetches.
//
// A request (a downloads row) is a title, or a show, season, album, artist or playlist with
// a selection of its titles, at a quality, on one device. Its titles (download_items) are
// worked out when it is made: those the profile may see, in their own order. The original
// needs no work; a smaller copy comes from package prepare and is shared. What a device has
// actually fetched is known to the device alone.
package downloads

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/prepare"
	"github.com/PorticoMediaServer/portico/server-next/internal/qualityladder"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Original is the quality that is the file as it is.
const Original = "original"

var (
	// ErrOff says the owner turned downloads off on this server.
	ErrOff = errors.New("downloads are turned off on this server")
	// ErrNotAllowed says downloads are turned off for this account or profile.
	ErrNotAllowed = errors.New("downloads are turned off for this profile")
	// ErrNoDevice says the request did not come from a device: a key cannot download.
	ErrNoDevice = errors.New("downloads belong to a device")
	// ErrTV says this device is a TV, which does not download.
	ErrTV = errors.New("TVs don't download")
	// ErrQuality says the quality is not one a download can be.
	ErrQuality = errors.New("not a download quality")
	// ErrAboveLimit says the owner allows no download this good.
	ErrAboveLimit = errors.New("above the owner's highest download quality")
)

// Service answers the downloads routes.
type Service struct {
	DB       *db.DB
	Prepare  *prepare.Service
	Settings *settings.Settings
	Now      func() time.Time
	grants   grants
}

// Owner is the owner's downloads settings as they stand.
func (s *Service) Owner() prepare.Settings { return prepare.Document.Get(s.Settings) }

// tvPlatforms are the platforms apps send for a TV. A TV has no Download button; this is the
// server saying so too.
var tvPlatforms = []string{"tv", "tvos", "androidtv", "android-tv", "firetv", "fire-tv", "roku", "tizen", "webos"}

// May says whether the viewer may download on this device now.
func (s *Service) May(ctx context.Context, v db.Viewer, platform string) error {
	if !s.Owner().On {
		return ErrOff
	}
	if slices.Contains(tvPlatforms, platform) {
		return ErrTV
	}
	allowed, err := s.DB.DownloadsAllowed(ctx, v)
	if err == nil && !allowed {
		err = ErrNotAllowed
	}
	return err
}

// Ask is a request to download.
type Ask struct {
	db.Viewer
	DeviceID int64
	// Item or List is what to download; Version, for one title, is the version chosen.
	Item, List, Version string
	Quality             string
	// Selection is all, unwatched or next; KeepNext is 1, 3 or 5 to keep that many ready.
	Selection string
	KeepNext  int
	Key, Hash string
}

// ValidQuality says whether a quality is one a download can be: the original, a step of the
// video ladder, or a music bitrate.
func ValidQuality(q string) bool {
	if q == Original {
		return true
	}
	if _, ok := qualityladder.Find(q); ok {
		return true
	}
	p, ok := qualityladder.FindMusic(q)
	return ok && p.AudioKbps > 0
}

// Titles is the titles a request would hold, in order, before any copy is asked for:
// those the viewer may see under the item or in the list, cut to the selection.
func (s *Service) Titles(ctx context.Context, v db.Viewer, item, list, selection string, keepNext int) ([]db.DownloadSource, error) {
	var ids []int64
	var err error
	if list != "" {
		for after := int64(0); ; {
			var page []int64
			page, after, err = s.DB.ListItems(ctx, v, list, after, 500)
			if err != nil || len(page) == 0 {
				break
			}
			ids = append(ids, page...)
			if len(ids) > db.QueueMax {
				return nil, db.ErrQueueFull
			}
		}
	} else {
		ids, err = s.DB.PlayableUnder(ctx, v, item)
	}
	if err != nil {
		return nil, err
	}
	// A selection chooses among a holder's titles; one title asked for by itself is that title.
	holder := list != ""
	if !holder {
		_, kind, err := s.DB.VisibleItem(ctx, v, item)
		if err != nil {
			return nil, err
		}
		holder = !kind.Playable()
	}
	if holder && selection != "all" {
		watched, err := s.DB.Watched(ctx, v.ProfileID, ids)
		if err != nil {
			return nil, err
		}
		ids = slices.DeleteFunc(ids, func(id int64) bool { return watched[id] })
		if selection == "next" {
			ids = ids[:min(len(ids), max(keepNext, 1))]
		}
	}
	sources, err := s.DB.DownloadSources(ctx, ids)
	out := make([]db.DownloadSource, 0, len(ids))
	for _, id := range ids {
		if source, ok := sources[id]; ok {
			out = append(out, source)
		}
	}
	return out, err
}

// Pick is the version a download fetches: the one named, or the best, as playing picks:
// the taller picture, then the higher bitrate.
func Pick(versions []db.Version, named string) (db.Version, bool) {
	var best db.Version
	found := false
	for _, v := range versions {
		if named != "" && v.PublicID == named {
			return v, true
		}
		if !found || v.Height > best.Height || v.Height == best.Height && v.Bitrate > best.Bitrate {
			best, found = v, true
		}
	}
	return best, found && named == ""
}

// plan works out one title of a request: the version and, below Original, the copy.
func (s *Service) plan(ctx context.Context, source db.DownloadSource, version, quality string, position int) db.DownloadTitle {
	t := db.DownloadTitle{ItemID: source.ID, ItemPublicID: source.PublicID, Kind: source.Kind, Title: source.Title, Position: position}
	v, ok := Pick(source.Versions, version)
	if !ok {
		t.State, t.Reason = db.DownloadLeftOut, "This title's file can't be found right now."
		return t
	}
	t.MediaID = v.MediaID()
	if quality == Original {
		return t
	}
	highest := s.Owner().HighestQuality
	if !slices.Contains(prepare.Recipes(v, source.Kind, highest), quality) {
		// Already at or below this quality, or not something a copy is made of (a book):
		// its original is the download, where the owner allows originals.
		if highest == Original && !slices.Contains(prepare.Recipes(v, source.Kind, Original), quality) {
			return t
		}
		t.State, t.Reason = db.DownloadLeftOut, "This title can't be downloaded at this quality on this server."
		return t
	}
	p, err := s.Prepare.Want(ctx, v, source.Kind, quality)
	switch {
	case errors.Is(err, prepare.ErrSpaceFull):
		t.State, t.Reason = db.DownloadLeftOut, "The server's download space is full."
	case errors.Is(err, prepare.ErrNoConverter):
		t.State, t.Reason = db.DownloadLeftOut, "This server can't make smaller copies."
	case err != nil:
		t.State, t.Reason = db.DownloadLeftOut, "This title can't be made smaller at this quality."
	default:
		t.Prepared = &p
	}
	return t
}

// Create stores a request. created is false for the same thing asked again.
func (s *Service) Create(ctx context.Context, a Ask) (db.Download, bool, error) {
	if !ValidQuality(a.Quality) {
		return db.Download{}, false, ErrQuality
	}
	if a.Quality == Original && s.Owner().HighestQuality != Original {
		return db.Download{}, false, ErrAboveLimit
	}
	sources, err := s.Titles(ctx, a.Viewer, a.Item, a.List, a.Selection, a.KeepNext)
	if err != nil {
		return db.Download{}, false, err
	}
	n := db.NewDownload{Viewer: a.Viewer, DeviceID: a.DeviceID, ItemPublicID: a.Item, ListPublicID: a.List, VersionPublicID: a.Version,
		Quality: a.Quality, Selection: a.Selection, KeepNext: a.KeepNext, IdempotencyKey: a.Key, Hash: a.Hash}
	for position, source := range sources {
		n.Titles = append(n.Titles, s.plan(ctx, source, a.Version, a.Quality, position))
	}
	dl, created, err := s.DB.CreateDownload(ctx, n, s.Now())
	// The copies wait for a request that wants them; now there is one.
	s.Prepare.Wake()
	return dl, created, err
}

// Change pauses or resumes a request, or changes its "keep the next". A copy nobody waits
// for any more stops being made, and waits until someone does.
func (s *Service) Change(ctx context.Context, v db.Viewer, id string, paused *bool, keepNext *int) error {
	copies, err := s.DB.SetDownload(ctx, v.ProfileID, id, paused, keepNext, s.Now())
	if err != nil {
		return err
	}
	s.stopUnwanted(ctx, copies)
	return nil
}

// Retry asks again for every copy of a request that could not be made or has expired.
func (s *Service) Retry(ctx context.Context, v db.Viewer, id string) error {
	dl, titles, err := s.DB.DownloadOf(ctx, v.ProfileID, id)
	if err != nil || dl.Quality == Original {
		return err
	}
	var redo []int64
	for _, t := range titles {
		switch {
		case t.State != db.DownloadWanted:
		case t.Prepared == nil:
			redo = append(redo, t.ItemID)
		case t.Prepared.State == db.PreparedFailed:
			if err := s.DB.RetryPrepared(ctx, t.Prepared.ID, s.Now()); err != nil {
				return err
			}
		}
	}
	sources, err := s.DB.DownloadSources(ctx, redo)
	if err != nil {
		return err
	}
	for _, t := range titles {
		source, ok := sources[t.ItemID]
		if !ok {
			continue
		}
		planned := s.plan(ctx, source, dl.VersionPublicID, dl.Quality, t.Position)
		if planned.Prepared != nil {
			if err := s.DB.UseCopy(ctx, dl.ID, t.ItemID, planned.Prepared.ID); err != nil {
				return err
			}
		}
	}
	s.Prepare.Wake()
	return nil
}

// Delete removes a request. The server's copies stay for anyone else who asked for them.
func (s *Service) Delete(ctx context.Context, v db.Viewer, id string) error {
	copies, err := s.DB.DeleteDownload(ctx, v.ProfileID, id)
	if err == nil {
		s.stopUnwanted(ctx, copies)
	}
	return err
}

func (s *Service) stopUnwanted(ctx context.Context, copies []int64) {
	for _, id := range copies {
		if wanted, err := s.DB.CopyWanted(ctx, id); err == nil && !wanted {
			s.Prepare.Stop(id)
		}
	}
	s.Prepare.Wake()
}

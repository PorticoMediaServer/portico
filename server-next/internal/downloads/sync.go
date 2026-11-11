package downloads

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// OfflineFor is how long a device's downloads play without reaching the server. Every sync
// starts it again for all of them, without the person doing anything; past it they will not
// play until the device connects once, and nothing is deleted. There is no signed permission:
// the app keeps the time and removes what a sync tells it to, which is all a person sees.
const OfflineFor = 30 * 24 * time.Hour

// Held is one title a device holds: the request it came with and the title.
type Held struct{ Download, Item string }

// The reasons a device is told to remove a title.
const (
	// RemoveDownloadsOff: downloads were turned off for the account or the profile.
	RemoveDownloadsOff = "downloads_off"
	// RemoveNoAccess: the profile may no longer see the title.
	RemoveNoAccess = "no_access"
	// RemoveWatched: the title was watched, under "keep the next" or with the device's
	// "Delete watched downloads" on.
	RemoveWatched = "watched"
	// RemoveNotNext: under "keep the next", the title is no longer among the next ones.
	RemoveNotNext = "not_next"
)

// Removal is a title the device must remove, and why.
type Removal struct {
	Held
	Reason string
}

// Synced is the server's answer to a device saying what it holds.
type Synced struct {
	// Until is when its downloads stop playing if it does not reach the server again.
	Until  time.Time
	Remove []Removal
	// Fetch is what this device's requests hold that it does not have yet and can fetch now.
	Fetch []Held
}

// Sync answers a device of the viewer's that says which titles it holds. It is the device's
// renewal, so every contact should send one; it changes nothing on the server and is safe
// to repeat. A device that may download nothing more (a TV, or a server with downloads off)
// is still told what to remove and until when the rest plays.
func (s *Service) Sync(ctx context.Context, v db.Viewer, deviceID int64, platform string, held []Held) (Synced, error) {
	out := Synced{Until: s.Now().Add(OfflineFor), Remove: []Removal{}, Fetch: []Held{}}
	// Downloads taken away from this person are removed whatever else is so: the account
	// and profile switches are read before the server's own.
	err := s.May(ctx, v, platform)
	if allowed, readErr := s.DB.DownloadsAllowed(ctx, v); readErr != nil {
		return out, readErr
	} else if !allowed {
		err = ErrNotAllowed
	}
	switch {
	case errors.Is(err, ErrNotAllowed):
		for _, h := range held {
			out.Remove = append(out.Remove, Removal{Held: h, Reason: RemoveDownloadsOff})
		}
		return out, nil
	case errors.Is(err, ErrOff), errors.Is(err, ErrTV):
		// What is on the device stays; nothing new is offered.
		_, err := s.removeHidden(ctx, v, held, &out)
		return out, err
	case err != nil:
		return out, err
	}
	deleteWatched, err := s.deleteWatched(ctx, v.ProfileID, deviceID)
	if err != nil {
		return out, err
	}
	watched, err := s.removeHidden(ctx, v, held, &out)
	if err != nil {
		return out, err
	}
	have := map[Held]bool{}
	for _, h := range held {
		have[h] = true
	}
	for _, r := range out.Remove {
		have[r.Held] = false // removed, so not offered again
	}
	if deleteWatched {
		for _, h := range held {
			if have[h] && watched[h.Item] {
				out.Remove, have[h] = append(out.Remove, Removal{Held: h, Reason: RemoveWatched}), false
			}
		}
	}
	requests, err := s.DB.Downloads(ctx, v.ProfileID, deviceID)
	if err != nil {
		return out, err
	}
	byRequest, err := s.DB.DeviceDownloadTitles(ctx, v.ProfileID, deviceID)
	if err != nil {
		return out, err
	}
	for _, request := range requests {
		if request.KeepNext > 0 {
			if err := s.keepNext(ctx, v, request, held, have, watched, &out); err != nil {
				return out, err
			}
		}
		if request.Paused {
			continue
		}
		for _, t := range byRequest[request.ID] {
			h := Held{Download: request.PublicID, Item: t.ItemPublicID}
			ready := t.State == db.DownloadWanted && (request.Quality == Original || t.Prepared != nil && t.Prepared.State == db.PreparedReady)
			if _, known := have[h]; ready && !known {
				out.Fetch = append(out.Fetch, h)
			}
		}
	}
	return out, nil
}

// keepNext brings a "keep the next" request up to date: its titles become the next ones the
// profile has not watched, new episodes included as they arrive. A held title that is no
// longer among them is removed from the device: watched, or passed over. Nothing is stored
// for the titles beyond the next ones; they are worked out again at every sync.
func (s *Service) keepNext(ctx context.Context, v db.Viewer, request db.Download, held []Held, have map[Held]bool, watched map[string]bool, out *Synced) error {
	next, err := s.Titles(ctx, v, request.ItemPublicID, request.ListPublicID, "next", request.KeepNext)
	if err != nil {
		return err
	}
	_, titles, err := s.DB.DownloadOf(ctx, v.ProfileID, request.PublicID)
	if err != nil {
		return err
	}
	stored := map[int64]bool{}
	for _, t := range titles {
		stored[t.ItemID] = true
	}
	wanted := map[string]bool{}
	var keep []int64
	var add []db.DownloadTitle
	for position, source := range next {
		wanted[source.PublicID] = true
		if stored[source.ID] {
			keep = append(keep, source.ID)
		} else {
			add = append(add, s.plan(ctx, source, "", request.Quality, position))
		}
	}
	if len(add) > 0 || len(keep) < len(titles) {
		if err := s.DB.KeepTitles(ctx, request.ID, keep, add); err != nil {
			return err
		}
		s.Prepare.Wake()
	}
	for _, h := range held {
		if h.Download != request.PublicID || wanted[h.Item] || !have[h] {
			continue
		}
		reason := RemoveNotNext
		if watched[h.Item] {
			reason = RemoveWatched
		}
		out.Remove, have[h] = append(out.Remove, Removal{Held: h, Reason: reason}), false
	}
	return nil
}

// deleteWatched is the device's "Delete watched downloads" rule, off unless it was turned on.
func (s *Service) deleteWatched(ctx context.Context, profileID, deviceID int64) (bool, error) {
	prefs, err := s.DB.Preferences(ctx, profileID, deviceID)
	return prefs["downloads.deleteWatched"] == "true", err
}

// removeHidden adds to the removals every held title the profile may no longer see, and
// returns which held titles the profile has watched.
func (s *Service) removeHidden(ctx context.Context, v db.Viewer, held []Held, out *Synced) (map[string]bool, error) {
	items := make([]string, 0, len(held))
	for _, h := range held {
		items = append(items, h.Item)
	}
	hidden, watched, err := s.DB.HeldTitles(ctx, v, items)
	for _, h := range held {
		if hidden[h.Item] {
			out.Remove = append(out.Remove, Removal{Held: h, Reason: RemoveNoAccess})
		}
	}
	return watched, err
}

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
		return out, s.removeHidden(ctx, v, held, &out)
	case err != nil:
		return out, err
	}
	if err := s.removeHidden(ctx, v, held, &out); err != nil {
		return out, err
	}
	have := map[Held]bool{}
	for _, h := range held {
		have[h] = true
	}
	for _, r := range out.Remove {
		have[r.Held] = true // removed, so not offered again
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
		if request.Paused {
			continue
		}
		for _, t := range byRequest[request.ID] {
			h := Held{Download: request.PublicID, Item: t.ItemPublicID}
			ready := t.State == db.DownloadWanted && (request.Quality == Original || t.Prepared != nil && t.Prepared.State == db.PreparedReady)
			if ready && !have[h] {
				out.Fetch = append(out.Fetch, h)
			}
		}
	}
	return out, nil
}

// removeHidden adds to the removals every held title the profile may no longer see.
func (s *Service) removeHidden(ctx context.Context, v db.Viewer, held []Held, out *Synced) error {
	items := make([]string, 0, len(held))
	for _, h := range held {
		items = append(items, h.Item)
	}
	hidden, err := s.DB.HiddenTitles(ctx, v, items)
	for _, h := range held {
		if hidden[h.Item] {
			out.Remove = append(out.Remove, Removal{Held: h, Reason: RemoveNoAccess})
		}
	}
	return err
}

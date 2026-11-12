package api

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// Server activity: Now playing with Stop, for the owner and administrators, and the activity
// record of who did what, for the owner.

// NowPlaying is one stream on the server.
type NowPlaying struct {
	ID string `json:"id"`
	// ItemID and Title are absent for a title in a library the asking administrator's account
	// does not have. Kind is still given: it names no title.
	ItemID     string `json:"itemId,omitempty"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	PositionMs int64  `json:"positionMs"`
	DurationMs int64  `json:"durationMs,omitempty"`
	// For an episode named here: its show and its season and episode numbers.
	ShowID        string `json:"showId,omitempty"`
	ShowTitle     string `json:"showTitle,omitempty"`
	SeasonNumber  *int   `json:"seasonNumber,omitempty"`
	EpisodeNumber *int   `json:"episodeNumber,omitempty"`
	// ConvertingToHeight is the height of the picture the stream is converted to, while its
	// picture is converted: "Converting to 1080p".
	ConvertingToHeight int `json:"convertingToHeight,omitempty"`
	// Username, then Profile when its name differs, then DeviceName name the person.
	Username   string `json:"username"`
	Profile    string `json:"profile,omitempty"`
	DeviceName string `json:"deviceName"`
	// Away is true for a stream outside the home networks.
	Away        bool `json:"away"`
	BitrateKbps int  `json:"bitrateKbps,omitempty"`
	// Route is how it reaches the device: original, repackaged or converted; Delivery says
	// it for the picture, the sound and the subtitles.
	Route     string            `json:"route"`
	Delivery  PlaybackDecisions `json:"delivery"`
	StartedAt time.Time         `json:"startedAt"`
}

// StreamStop is what the viewer is told when the owner or an administrator stops a stream.
type StreamStop struct {
	Message string `json:"message,omitempty"`
}

// ActivityEntry is one entry of the activity record.
type ActivityEntry struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target,omitempty"`
	Detail string    `json:"detail,omitempty"`
}

const activityPage = 100

func (s *Server) activityRoutes() {
	Handle(s, "GET /v1/admin/sessions", Admin, s.nowPlaying)
	Handle(s, "GET /v1/admin/activity", Owner, s.activitySummary)
	Handle(s, "DELETE /v1/admin/sessions/{id}", Admin, func(r *http.Request, req StreamStop) (None, error) {
		req.Message = strings.TrimSpace(req.Message)
		if utf8.RuneCountInString(req.Message) > 500 {
			return None{}, ErrField("message", "Write a message of up to 500 characters.")
		}
		id := r.PathValue("id")
		if live.IsLive(id) {
			if !s.viewings.StopFor(id, req.Message) {
				return None{}, nil
			}
			return None{}, s.db.Record(r.Context(), CallerFrom(r.Context()).UserID, "stream.stop", id, req.Message, s.now())
		}
		// Stopping a stream that has already ended is fine: the viewer was told once.
		if !slices.ContainsFunc(s.playback.All(), func(v playback.Session) bool { return v.ID == id }) {
			return None{}, nil
		}
		s.playback.Terminate(r.Context(), id, req.Message)
		return None{}, s.db.Record(r.Context(), CallerFrom(r.Context()).UserID, "stream.stop", id, req.Message, s.now())
	})
	Handle(s, "GET /v1/admin/audit", Owner, func(r *http.Request, _ None) (Page[ActivityEntry], error) {
		after, err := cursorID(r)
		if err != nil {
			return Page[ActivityEntry]{}, err
		}
		list, err := s.db.ActivityRecord(r.Context(), r.URL.Query().Get("action"), after, activityPage+1)
		out := Page[ActivityEntry]{Items: []ActivityEntry{}}
		if len(list) > activityPage {
			list = list[:activityPage]
			out.NextCursor = strconv.FormatInt(list[len(list)-1].ID, 10)
		}
		for _, a := range list {
			out.Items = append(out.Items, ActivityEntry{At: a.At.UTC(), Actor: a.Actor, Action: a.Action, Target: a.Target, Detail: a.Detail})
		}
		return out, err
	})
}

// nowPlaying lists every stream: conversions first, then the newest start. Paused streams
// count; a title in a library the asking administrator's account does not have is unnamed.
func (s *Server) nowPlaying(r *http.Request, _ None) (Page[NowPlaying], error) {
	sessions := s.playback.All()
	ids, profiles := make([]int64, len(sessions)), make([]int64, len(sessions))
	for n, v := range sessions {
		ids[n], profiles[n] = v.Item.ID, v.ProfileID
	}
	titles, err := s.db.AccountTitles(r.Context(), CallerFrom(r.Context()).UserID, ids)
	if err != nil {
		return Page[NowPlaying]{}, err
	}
	people, err := s.db.ViewingNames(r.Context(), profiles)
	if err != nil {
		return Page[NowPlaying]{}, err
	}
	numbers, err := s.db.IndexNumbers(r.Context(), ids)
	if err != nil {
		return Page[NowPlaying]{}, err
	}
	slices.SortStableFunc(sessions, func(a, b playback.Session) int {
		if converting := cmp.Compare(btoi(b.Plan.Route == playback.RouteConverted), btoi(a.Plan.Route == playback.RouteConverted)); converting != 0 {
			return converting
		}
		return b.StartedAt.Compare(a.StartedAt)
	})
	out := Page[NowPlaying]{Items: make([]NowPlaying, 0, len(sessions))}
	for _, v := range sessions {
		person := people[v.ProfileID]
		item := NowPlaying{ID: v.ID, Kind: sessionKind(v.Item.Kind), State: v.State, PositionMs: v.PositionMs, Username: person.Username,
			Profile: person.Profile, DeviceName: v.Device.Name, Away: v.Network != playback.NetworkLocal, BitrateKbps: v.Plan.BitrateKbps,
			Route: v.Plan.Route, Delivery: decisions(v.Plan), StartedAt: v.StartedAt.UTC()}
		if title, ok := titles[v.Item.ID]; ok {
			item.ItemID, item.Title, item.DurationMs = v.Item.PublicID, title, v.Item.DurationMS
			if v.Item.Kind == db.KindEpisode && v.Item.Root != nil {
				item.ShowID, item.ShowTitle = v.Item.Root.PublicID, v.Item.Root.Title
				if v.Item.Parent != nil && v.Item.Parent.Kind == db.KindSeason {
					item.SeasonNumber = v.Item.Parent.Index
				}
				if n, ok := numbers[v.Item.ID]; ok {
					item.EpisodeNumber = &n
				}
			}
		}
		item.ConvertingToHeight = convertedHeight(v.Plan.PlaybackInfo)
		out.Items = append(out.Items, item)
	}
	channels, err := s.nowWatchingLive(r)
	if err != nil {
		return Page[NowPlaying]{}, err
	}
	// A channel watched has no conversion state to order by beside the titles: the newest
	// start first among them, after the titles' own order.
	out.Items = append(out.Items, channels...)
	return out, nil
}

// convertedHeight is the height of the picture a stream delivers when its picture is
// converted, as the plan in force says (its PlaybackInfo), or 0.
func convertedHeight(info json.RawMessage) int {
	var plan struct {
		Routes struct {
			Picture string `json:"picture"`
		} `json:"routes"`
		Delivered struct {
			Video *struct {
				Height int `json:"height"`
			} `json:"video"`
		} `json:"delivered"`
	}
	if len(info) == 0 || json.Unmarshal(info, &plan) != nil || plan.Routes.Picture != "converted" || plan.Delivered.Video == nil {
		return 0
	}
	return plan.Delivered.Video.Height
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// cursorID reads a list's cursor: the id of the last row of the page before, 0 for the first.
func cursorID(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, ErrField("cursor", "This is not a cursor this list gave.")
	}
	return n, nil
}

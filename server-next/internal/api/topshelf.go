package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// The Apple TV Top Shelf (definition 05-browse rule 45a; decision, Justin 18 Nov 2026, A13):
// the signed-in profile's Continue Watching, with progress, and its next Home row, posters
// only, at most 10 of each, only titles the profile may see. The tvOS extension that draws it
// runs while nobody is using the app and holds no sign-in, so the app gives it a feed token.

// TopShelfTokenRequest names the installation asking; it must be the one the sign-in is on.
type TopShelfTokenRequest struct {
	InstallationID string `json:"installationId"`
}

// TopShelfToken reads GET /v1/topshelf, and nothing else, for one sign-in on one profile. It
// stops working when that sign-in ends or chooses another profile. ExpiresAt is when the
// sign-in ends if it is not renewed; the app asks for a new token before then.
type TopShelfToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
	FeedURL   string    `json:"feedUrl"`
}

// TopShelf is the shelf: Continue Watching first, then the next Home row, each left out
// when it has nothing to show.
type TopShelf struct {
	Sections []TopShelfSection `json:"sections"`
}

// TopShelfSection is one row of the shelf, named as Home names it.
type TopShelfSection struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// TitleText.Code is the key the extension translates the title by; an unknown key shows Title.
	TitleText TopShelfText    `json:"titleText"`
	Entries   []TopShelfEntry `json:"entries"`
}

// TopShelfText is a row title's translation key.
type TopShelfText struct {
	Code string `json:"code"`
}

// TopShelfEntry is one poster. DisplayURL opens the title in the app and PlayURL plays it.
type TopShelfEntry struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ShowTitle string `json:"showTitle,omitempty"`
	// ImageURL is the poster: an episode's is its show's.
	ImageURL        string `json:"imageUrl"`
	DisplayURL      string `json:"displayUrl"`
	PlayURL         string `json:"playUrl,omitempty"`
	ProgressPercent int    `json:"progressPercent,omitempty"`
}

type topShelfClaim struct {
	Session int64 `json:"s"`
	Profile int64 `json:"p"`
}

const topShelfMost = 10

// topShelfTitleCodes are the extension's own keys for the rows whose titles never change.
var topShelfTitleCodes = map[string]string{"continue": "home.row.continueWatching", "recently_added": "home.row.recentlyAdded",
	"popular": "home.row.popularOnServer", "trending": "home.row.trending"}

func (s *Server) topShelfRoutes() {
	token := Handle(s, "POST /v1/topshelf/token", WithProfile, func(r *http.Request, req TopShelfTokenRequest) (TopShelfToken, error) {
		c := CallerFrom(r.Context())
		st, err := s.db.SessionState(r.Context(), c.SessionID, s.now())
		if errors.Is(err, db.ErrNotFound) {
			return TopShelfToken{}, errSignInEnded()
		}
		if err != nil {
			return TopShelfToken{}, err
		}
		if req.InstallationID != st.InstallationID {
			return TopShelfToken{}, ErrField("installationId", "Send the id of the app this sign-in is on.")
		}
		return TopShelfToken{Token: s.tokens.Remember("topshelf", topShelfClaim{Session: st.ID, Profile: st.ProfileID}),
			ExpiresAt: st.ExpiresAt.UTC(), FeedURL: "/v1/topshelf"}, nil
	})
	// A key or personal token never makes one: the shelf belongs to a device's sign-in.
	token.Scope = ScopeSession
	// The feed token is not a sign-in, so the route checks it itself, as sign-out does.
	Handle(s, "GET /v1/topshelf", Public, s.topShelf)
}

func (s *Server) topShelf(r *http.Request, _ None) (TopShelf, error) {
	var claim topShelfClaim
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !s.tokens.Recall("topshelf", bearer, &claim) {
		return TopShelf{}, ErrUnauthorized()
	}
	st, err := s.db.SessionState(r.Context(), claim.Session, s.now())
	if errors.Is(err, db.ErrNotFound) || err == nil && st.ProfileID != claim.Profile {
		return TopShelf{}, errSignInEnded()
	}
	if err != nil {
		return TopShelf{}, err
	}
	c := claimsOf(st)
	if err := s.refuseNonOwnerAway(r, c.UserID); err != nil {
		return TopShelf{}, err
	}
	r = r.WithContext(context.WithValue(r.Context(), callerKey, Caller{SessionID: c.SessionID, UserID: c.UserID, ProfileID: c.ProfileID,
		Role: c.Role, Manage: c.Manage}))
	rows, _, err := s.homeRows(r, false)
	if err != nil {
		return TopShelf{}, err
	}
	out := TopShelf{Sections: []TopShelfSection{}}
	seen, posters := map[string]bool{}, map[string]string{}
	// Continue Watching, then the first other row with something new to show. The other
	// Continue rows are songs and books, which the shelf does not hold.
	add := func(row HomeRow) (bool, error) {
		section, err := s.topShelfSection(r, row, seen, posters)
		if err == nil && len(section.Entries) > 0 {
			out.Sections = append(out.Sections, section)
		}
		return len(section.Entries) > 0, err
	}
	for _, row := range rows {
		if row.ID == "continue" {
			if _, err := add(row); err != nil {
				return TopShelf{}, err
			}
		}
	}
	for _, row := range rows {
		if strings.HasPrefix(row.ID, "continue") {
			continue
		}
		if added, err := add(row); added || err != nil {
			return out, err
		}
	}
	return out, nil
}

// topShelfSection is a Home row's first ten titles with a poster that are not already on the shelf.
func (s *Server) topShelfSection(r *http.Request, row HomeRow, seen map[string]bool, posters map[string]string) (TopShelfSection, error) {
	section := TopShelfSection{ID: row.ID, Title: row.Title, TitleText: TopShelfText{Code: topShelfTitleCodes[row.ID]}, Entries: []TopShelfEntry{}}
	if section.TitleText.Code == "" {
		section.TitleText.Code = row.ID
	}
	for _, item := range row.Items {
		if len(section.Entries) == topShelfMost {
			break
		}
		poster, err := s.topShelfPoster(r, item, posters)
		if err != nil {
			return section, err
		}
		if seen[item.ID] || poster == "" {
			continue
		}
		seen[item.ID] = true
		entry := TopShelfEntry{ID: item.ID, Title: item.Title, ShowTitle: item.RootTitle, ImageURL: poster, DisplayURL: "portico://item/" + item.ID}
		if item.Kind == "movie" || item.Kind == "episode" {
			entry.PlayURL = "portico://play/" + item.ID
		}
		if row.ID == "continue" && item.DurationMs > 0 {
			entry.ProgressPercent = int(min(100, item.State.PositionMs*100/item.DurationMs))
		}
		section.Entries = append(section.Entries, entry)
	}
	return section, nil
}

// topShelfPoster is a title's poster, or its show's for an episode, which has only a still.
// known keeps the shows already read.
func (s *Server) topShelfPoster(r *http.Request, item Item, known map[string]string) (string, error) {
	if poster := item.Images["poster"]; poster != "" || item.Kind != "episode" || item.RootID == "" {
		return poster, nil
	}
	if poster, ok := known[item.RootID]; ok {
		return poster, nil
	}
	show, err := s.db.Item(r.Context(), viewer(r), item.RootID)
	if errors.Is(err, db.ErrNotFound) {
		// The profile may see the episode but not its show: the episode is left out.
		known[item.RootID] = ""
		return "", nil
	}
	if err != nil {
		return "", err
	}
	known[item.RootID] = imageMap(show.Images)["poster"]
	return known[item.RootID], nil
}

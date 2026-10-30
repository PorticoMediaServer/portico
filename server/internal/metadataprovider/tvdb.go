package metadataprovider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TVDB struct {
	screenCache        screenCache
	http               *transport
	projectKey         string
	credentialMu       sync.RWMutex
	credentialRevision uint64
	tokenRevision      uint64
	auth               chan struct{}
	token              string
	expires            time.Time
}

func NewTVDB(projectKey string) (*TVDB, error) {
	if strings.TrimSpace(projectKey) == "" {
		return nil, errors.New("Portico TVDB project credential is missing")
	}
	return &TVDB{http: newTransport("tvdb", "https://api4.thetvdb.com/v4", "Portico/0.1 (https://getportico.tv)"), projectKey: projectKey, auth: make(chan struct{}, 1)}, nil
}
func (s *TVDB) access(ctx context.Context, invalid string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	select {
	case s.auth <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.auth }()
	if invalid != "" && s.token == invalid {
		s.token = ""
	}
	key, revision := s.credential()
	if s.token != "" && s.tokenRevision == revision && time.Now().Before(s.expires) {
		return s.token, nil
	}
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := s.http.request(ctx, "POST", "/login", "", map[string]string{"apikey": key}, &envelope); err != nil {
		return "", err
	}
	if envelope.Status != "success" || len(envelope.Data.Token) < 8 || len(envelope.Data.Token) > 8192 {
		return "", &Error{Provider: "tvdb", Code: "invalid_login"}
	}
	for _, r := range envelope.Data.Token {
		if r < 33 || r > 126 {
			return "", &Error{Provider: "tvdb", Code: "invalid_login"}
		}
	}
	s.token = envelope.Data.Token
	s.tokenRevision = revision
	s.expires = time.Now().Add(27 * 24 * time.Hour)
	return s.token, nil
}
func (s *TVDB) get(ctx context.Context, route string, out any) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	token, err := s.access(ctx, "")
	if err != nil {
		return err
	}
	err = s.http.request(ctx, "GET", route, token, nil, out)
	var problem *Error
	if errors.As(err, &problem) && problem.Status == 401 {
		token, err = s.access(ctx, token)
		if err == nil {
			err = s.http.request(ctx, "GET", route, token, nil, out)
		}
	}
	return err
}

type SeriesCandidate struct {
	ID       string   `json:"tvdb_id"`
	Name     string   `json:"name"`
	Year     string   `json:"year"`
	Type     string   `json:"type"`
	Aliases  []string `json:"aliases"`
	Overview string   `json:"overview"`
	Image    string   `json:"image_url"`
}

func (s *TVDB) SearchSeries(ctx context.Context, title string, year int) ([]SeriesCandidate, error) {
	if strings.TrimSpace(title) == "" || len(title) > 512 || year < 0 || year > 9999 {
		return nil, errors.New("invalid series query")
	}
	query := url.Values{"query": {title}, "type": {"series"}, "limit": {"25"}}
	if year > 0 {
		query.Set("year", strconv.Itoa(year))
	}
	var envelope struct {
		Status string            `json:"status"`
		Data   []SeriesCandidate `json:"data"`
	}
	if err := s.get(ctx, "/search?"+query.Encode(), &envelope); err != nil {
		return nil, err
	}
	if envelope.Status != "success" || len(envelope.Data) > 25 {
		return nil, &Error{Provider: "tvdb", Code: "invalid_search"}
	}
	for _, candidate := range envelope.Data {
		id, err := strconv.ParseInt(candidate.ID, 10, 64)
		if err != nil || id <= 0 || candidate.Type != "series" || strings.TrimSpace(candidate.Name) == "" || len(candidate.Name) > 2048 || len(candidate.Aliases) > 128 {
			return nil, &Error{Provider: "tvdb", Code: "invalid_search"}
		}
	}
	return envelope.Data, nil
}

type EpisodeOrder string

// TVDBEpisodeCharacter is one person TVDB credits on an episode (guest stars,
// and where TVDB carries them, the episode's director and writer).
type TVDBEpisodeCharacter struct {
	PersonID   int64
	PersonName string
	Character  string
	Type       string
	Sort       int
}

// EpisodeCharacters reads one episode's extended record and returns its
// characters in billing order. It rides the shared screen cache, so a refresh
// that fetched the record costs no second request.
func (s *TVDB) EpisodeCharacters(ctx context.Context, id int64) ([]TVDBEpisodeCharacter, error) {
	if id <= 0 || id > 9007199254740991 {
		return nil, errors.New("invalid TVDB episode")
	}
	var page struct {
		Status string `json:"status"`
		Data   struct {
			ID         int64 `json:"id"`
			Characters []struct {
				PersonID   int64  `json:"peopleId"`
				PersonName string `json:"personName"`
				Name       string `json:"name"`
				Type       string `json:"peopleType"`
				Sort       int    `json:"sort"`
			} `json:"characters"`
		} `json:"data"`
	}
	if err := s.screenGet(ctx, "/episodes/"+strconv.FormatInt(id, 10)+"/extended", &page); err != nil {
		return nil, err
	}
	if page.Status != "success" || page.Data.ID != id {
		return nil, &Error{Provider: "tvdb", Code: "invalid_episode"}
	}
	if page.Data.Characters == nil {
		return nil, nil
	}
	if len(page.Data.Characters) > 1000 {
		return nil, &Error{Provider: "tvdb", Code: "invalid_episode"}
	}
	out := []TVDBEpisodeCharacter{}
	for _, c := range page.Data.Characters {
		if c.PersonID <= 0 || c.PersonID > 9007199254740991 || strings.TrimSpace(c.PersonName) == "" || len(c.PersonName) > 512 || len(c.Name) > 512 || len(c.Type) > 64 {
			continue
		}
		out = append(out, TVDBEpisodeCharacter{c.PersonID, strings.TrimSpace(c.PersonName), strings.TrimSpace(c.Name), c.Type, c.Sort})
		// The episode view shows guest stars, the director and the writer;
		// 64 bounds one episode's fetch, as the show page bounds its cast.
		if len(out) == 64 {
			break
		}
	}
	return out, nil
}

const (
	Official  EpisodeOrder = "official"
	DVD       EpisodeOrder = "dvd"
	Absolute  EpisodeOrder = "absolute"
	Default   EpisodeOrder = "default"
	Alternate EpisodeOrder = "alternate"
	Regional  EpisodeOrder = "regional"
)

type Episode struct {
	ID             int64  `json:"id"`
	SeriesID       int64  `json:"seriesId"`
	Name           string `json:"name"`
	SeasonNumber   *int   `json:"seasonNumber"`
	Number         *int   `json:"number"`
	AbsoluteNumber *int   `json:"absoluteNumber"`
	Aired          string `json:"aired"`
	Overview       string `json:"overview"`
	Image          string `json:"image"`
	RuntimeMinutes *int   `json:"runtime"`
}
type EpisodePage struct {
	SeriesID int64
	Order    EpisodeOrder
	Page     int
	Episodes []Episode
	NextPage *int
}

func (s *TVDB) Episodes(ctx context.Context, seriesID int64, order EpisodeOrder, page int) (EpisodePage, error) {
	result := EpisodePage{SeriesID: seriesID, Order: order, Page: page}
	if seriesID <= 0 || page < 0 || page > 1000000 {
		return result, errors.New("invalid episode page")
	}
	switch order {
	case Official, DVD, Absolute, Default, Alternate, Regional:
	default:
		return result, errors.New("invalid TVDB episode ordering")
	}
	route := fmt.Sprintf("/series/%d/episodes/%s", seriesID, order)
	var envelope struct {
		Status string `json:"status"`
		Data   struct {
			Series struct {
				ID int64 `json:"id"`
			} `json:"series"`
			Episodes []Episode `json:"episodes"`
		} `json:"data"`
		Links struct {
			Next *string `json:"next"`
		} `json:"links"`
	}
	if err := s.get(ctx, route+"?page="+strconv.Itoa(page), &envelope); err != nil {
		return result, err
	}
	if envelope.Status != "success" || envelope.Data.Series.ID != seriesID || len(envelope.Data.Episodes) > 1000 {
		return result, &Error{Provider: "tvdb", Code: "invalid_episodes"}
	}
	seen := map[int64]bool{}
	for _, episode := range envelope.Data.Episodes {
		if episode.ID <= 0 || episode.SeriesID != seriesID || seen[episode.ID] || len(episode.Name) > 2048 || len(episode.Overview) > 65536 {
			return result, &Error{Provider: "tvdb", Code: "invalid_episodes"}
		}
		if (episode.SeasonNumber != nil && (*episode.SeasonNumber < 0 || *episode.SeasonNumber > 9999)) || (episode.Number != nil && (*episode.Number < 0 || *episode.Number > 9999)) || (episode.AbsoluteNumber != nil && (*episode.AbsoluteNumber < 0 || *episode.AbsoluteNumber > 1000000)) || (episode.RuntimeMinutes != nil && (*episode.RuntimeMinutes < 0 || *episode.RuntimeMinutes > 10080)) {
			return result, &Error{Provider: "tvdb", Code: "invalid_episodes"}
		}
		seen[episode.ID] = true
	}
	if envelope.Links.Next != nil && *envelope.Links.Next != "" {
		base, _ := url.Parse(s.http.base + route)
		next, err := base.Parse(*envelope.Links.Next)
		if err != nil || next.Scheme != base.Scheme || next.Host != base.Host || next.Path != base.Path || next.User != nil || next.Fragment != "" || len(next.Query()) != 1 || len(next.Query()["page"]) != 1 {
			return result, &Error{Provider: "tvdb", Code: "invalid_pagination"}
		}
		values, queryErr := url.ParseQuery(next.RawQuery)
		if queryErr != nil {
			return result, &Error{Provider: "tvdb", Code: "invalid_pagination"}
		}
		number, err := strconv.Atoi(values.Get("page"))
		if err != nil || number != page+1 || number > 1000000 {
			return result, &Error{Provider: "tvdb", Code: "invalid_pagination"}
		}
		result.NextPage = &number
	}
	result.Episodes = envelope.Data.Episodes
	return result, nil
}

func (s *TVDB) SetBackgroundSpeed(speed string) { s.http.setSpeed(speed) }

// Installing a key does not wait for an in-flight login. access owns tokens
// under the auth gate and rejects a token from an older credential revision.
func (s *TVDB) SetCredential(ctx context.Context, key string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	s.credentialMu.Lock()
	changed := s.projectKey != key
	if changed {
		s.projectKey = key
		s.credentialRevision++
	}
	s.credentialMu.Unlock()
	if changed {
		s.screenCache.invalidate()
	}
	return nil
}
func (s *TVDB) credential() (string, uint64) {
	s.credentialMu.RLock()
	defer s.credentialMu.RUnlock()
	return s.projectKey, s.credentialRevision
}

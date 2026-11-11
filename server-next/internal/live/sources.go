package live

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/secrets"
)

// Config is a source's settings, stored as JSON. The sign-in is never in it.
type Config struct {
	// PlaylistURL is a playlist's address; Uploaded says the owner sent the playlist itself.
	PlaylistURL string `json:"playlistUrl,omitempty"`
	Uploaded    bool   `json:"uploaded,omitempty"`
	// GuideURL is listings by address; GuideUploaded says the owner sent the guide itself.
	GuideURL      string `json:"guideUrl,omitempty"`
	GuideUploaded bool   `json:"guideUploaded,omitempty"`
	// ServerURL is an Xtream provider's address, TunerURL an HDHomeRun's.
	ServerURL string `json:"serverUrl,omitempty"`
	TunerURL  string `json:"tunerUrl,omitempty"`
	// RefreshHours is how often the guide is refreshed: 6 unless set, 1 to 168.
	RefreshHours int `json:"refreshHours,omitempty"`
	// Groups and Channels are what comes in: none named is everything.
	Groups   []string `json:"groups,omitempty"`
	Channels []string `json:"channels,omitempty"`
	// Numbering is source (as the source sends them) or sequential from FirstNumber.
	Numbering   string `json:"numbering,omitempty"`
	FirstNumber int    `json:"firstNumber,omitempty"`
}

// SignIn is an Xtream sign-in. It is stored sealed and never shown again.
type SignIn struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Source states, in the words a source's row shows.
const (
	StateNew      = "new" // waiting for its first refresh
	StateUpToDate = "up_to_date"
	StateTrouble  = "trouble"     // the last refresh failed; the guide shown is older
	StateBroken   = "not_working" // no refresh has ever worked
	StateSignIn   = "needs_sign_in"
)

// Limits on what is read from a source.
const (
	maxPlaylistBytes = 256 << 20
	maxListBytes     = 32 << 20 // a provider's or tuner's channel list, read whole: 50,000 channels is about 25 MB
	maxGuideBytes    = 2 << 30
	retryAfter       = 30 * time.Minute
)

// Service refreshes sources in the background and answers previews.
type Service struct {
	db      *db.DB
	log     *slog.Logger
	now     func() time.Time
	dir     string // where uploaded playlists and guides are kept
	client  *http.Client
	box     *secrets.Box
	wake    chan struct{}
	mu      sync.Mutex
	tried   map[int64]time.Time // when each source was last tried, so a failing one waits
	running map[int64]chan struct{}
}

// New makes the service. dataDir is the folder beside the database: sign-ins are sealed with
// the key file kept there, so a copy of the database alone opens none of them.
func New(d *db.DB, log *slog.Logger, now func() time.Time, dataDir string) (*Service, error) {
	box, err := secrets.Open(dataDir)
	if err != nil {
		return nil, err
	}
	return &Service{db: d, log: log, now: now, dir: filepath.Join(dataDir, "live"), client: newClient(10 * time.Minute), box: box,
		wake: make(chan struct{}, 1), tried: map[int64]time.Time{}, running: map[int64]chan struct{}{}}, nil
}

// SetClient replaces the HTTP client, for tests that answer from fake servers.
func (s *Service) SetClient(c *http.Client) { s.client = c }

// newClient reads what sources send. A redirect may not leave http and https, nor go from
// https to plain http, where an address carrying a sign-in would travel in clear.
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		switch {
		case len(via) >= 5:
			return errors.New("too many redirects")
		case r.URL.Scheme != "http" && r.URL.Scheme != "https", via[0].URL.Scheme == "https" && r.URL.Scheme == "http":
			return errors.New("a redirect to a less safe address")
		}
		return nil
	}}
}

// Seal seals a source's sign-in for storing, bound to that source, so it can be opened only
// for it. Nothing ever shows it to an app or writes it to the log.
func (s *Service) Seal(sourceID string, in SignIn) []byte {
	raw, _ := json.Marshal(in)
	return s.box.Seal("live-source:"+sourceID, raw)
}

// SignIn opens a source's sign-in, to read the source or to tune one of its channels.
func (s *Service) SignIn(sourceID string, sealed []byte) (SignIn, error) {
	var in SignIn
	raw, err := s.box.Open("live-source:"+sourceID, sealed)
	if err != nil {
		return in, ErrSignIn
	}
	return in, json.Unmarshal(raw, &in)
}

// StreamAddress is where a channel's stream is read from. A source that signs in keeps no
// address in its channels, only the stream's id ("xtream:<id>"); the address, with the
// sign-in in it as the provider wants, is made here in memory each time a channel is tuned.
func StreamAddress(kind string, cfg Config, signIn SignIn, locator string) (string, error) {
	id, ok := strings.CutPrefix(locator, "xtream:")
	if kind != "xtream" || !ok {
		return locator, nil
	}
	u, err := url.Parse(cfg.ServerURL)
	if err != nil || signIn.Username == "" {
		return "", ErrSignIn
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/live/" + url.PathEscape(signIn.Username) + "/" + url.PathEscape(signIn.Password) + "/" + id + ".ts"
	return u.String(), nil
}

// SameServer says whether two addresses reach the same scheme, host and port: a stored
// sign-in is sent only there.
func SameServer(a, b string) bool {
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

// Masked shows an address with its query values hidden: a provider's playlist address often
// carries the sign-in there.
func Masked(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.RawQuery == "" {
		return address
	}
	q := u.Query()
	for k := range q {
		q.Set(k, "…")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// streamKey names a channel sent without a guide id by a hash of its address, which may hold
// a sign-in and so never appears itself.
func streamKey(address string) string {
	sum := sha256.Sum256([]byte(address))
	return "stream:" + hex.EncodeToString(sum[:8])
}

// UploadPath is where a source's uploaded playlist or guide is kept.
func (s *Service) UploadPath(sourceID, what string) string {
	return filepath.Join(s.dir, sourceID+"-"+what)
}

// SaveUpload keeps an uploaded playlist or guide for a source, replacing any before.
func (s *Service) SaveUpload(sourceID, what, text string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp := s.UploadPath(sourceID, what) + ".new"
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.UploadPath(sourceID, what))
}

// RemoveUploads deletes a removed source's uploaded files.
func (s *Service) RemoveUploads(sourceID string) {
	for _, what := range []string{"playlist", "guide"} {
		os.Remove(s.UploadPath(sourceID, what))
	}
}

// ErrSignIn: the provider refused the sign-in.
var ErrSignIn = errors.New("the provider refused the sign-in")

// fetch reads an address the owner gave, up to limit bytes, unpacking a gzip file.
func (s *Service) fetch(ctx context.Context, address string, limit int64) (io.ReadCloser, error) {
	if !validLocator(address) {
		return nil, fmt.Errorf("use an http or https address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Portico")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the address could not be reached")
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		resp.Body.Close()
		return nil, ErrSignIn
	case resp.StatusCode != http.StatusOK:
		resp.Body.Close()
		return nil, fmt.Errorf("the address answered %d", resp.StatusCode)
	}
	body := bufio.NewReader(io.LimitReader(resp.Body, limit))
	var r io.Reader = body
	if magic, _ := body.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		z, err := gzip.NewReader(body)
		if err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("the file could not be unpacked")
		}
		r = io.LimitReader(z, limit)
	}
	return struct {
		io.Reader
		io.Closer
	}{r, resp.Body}, nil
}

func joinPath(base, path string, q url.Values) string {
	u, _ := url.Parse(base)
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	if q != nil {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// Pulled is what a source sends: its channels, how many it delivers at once (0 when the
// source does not say), and where its guide is.
type Pulled struct {
	Channels []Channel
	Tuners   int
	guideURL string
}

// pull reads a source's channels. upload is the playlist the owner sent, when there is one.
func (s *Service) pull(ctx context.Context, kind string, cfg Config, signIn SignIn, upload io.Reader, report *Report) (Pulled, error) {
	var out Pulled
	add := func(c Channel) error { out.Channels = append(out.Channels, c); return nil }
	out.guideURL = cfg.GuideURL
	switch kind {
	case "playlist":
		r := upload
		if r == nil {
			body, err := s.fetch(ctx, cfg.PlaylistURL, maxPlaylistBytes)
			if err != nil {
				return out, err
			}
			defer body.Close()
			r = body
		}
		return out, ParsePlaylist(r, report, add)
	case "xtream":
		if !validLocator(cfg.ServerURL) {
			return out, fmt.Errorf("use an http or https address for the server")
		}
		q := url.Values{"username": {signIn.Username}, "password": {signIn.Password}, "action": {"get_live_streams"}}
		body, err := s.fetch(ctx, joinPath(cfg.ServerURL, "/player_api.php", q), maxListBytes)
		if err != nil {
			return out, err
		}
		defer body.Close()
		var streams []struct {
			ID       json.Number `json:"stream_id"`
			Name     string      `json:"name"`
			Number   json.Number `json:"num"`
			EPG      string      `json:"epg_channel_id"`
			Icon     string      `json:"stream_icon"`
			Category string      `json:"category_name"`
		}
		dec := json.NewDecoder(body)
		dec.UseNumber()
		if err := dec.Decode(&streams); err != nil {
			return out, ErrSignIn // a refused sign-in answers something other than the list
		}
		for _, st := range streams {
			id, err := st.ID.Int64()
			if err != nil || id <= 0 || !validText(st.Name, 256) {
				report.SkippedChannels++
				report.note("A channel's id or name could not be read.")
				continue
			}
			if len(out.Channels) >= MaxChannels {
				report.SkippedChannels++
				continue
			}
			stream := "xtream:" + strconv.FormatInt(id, 10)
			c := Channel{Key: stream, Name: st.Name, Number: st.Number.String(), GuideKey: st.EPG, Locator: stream, Group: st.Category}
			if validLocator(st.Icon) {
				c.Logo = st.Icon
			}
			report.group(c.Group)
			out.Channels = append(out.Channels, c)
		}
		if out.guideURL == "" {
			out.guideURL = joinPath(cfg.ServerURL, "/xmltv.php", url.Values{"username": {signIn.Username}, "password": {signIn.Password}})
		}
		return out, nil
	case "hdhomerun":
		if !validLocator(cfg.TunerURL) {
			return out, fmt.Errorf("use the tuner's http address")
		}
		body, err := s.fetch(ctx, joinPath(cfg.TunerURL, "/discover.json", nil), 64<<10)
		if err != nil {
			return out, err
		}
		var info struct {
			TunerCount int `json:"TunerCount"`
		}
		err = json.NewDecoder(body).Decode(&info)
		body.Close()
		if err != nil || info.TunerCount < 0 || info.TunerCount > 256 {
			return out, fmt.Errorf("this is not an HDHomeRun tuner")
		}
		out.Tuners = info.TunerCount
		if body, err = s.fetch(ctx, joinPath(cfg.TunerURL, "/lineup.json", nil), maxListBytes); err != nil {
			return out, err
		}
		defer body.Close()
		var rows []struct {
			Number string `json:"GuideNumber"`
			Name   string `json:"GuideName"`
			URL    string `json:"URL"`
			DRM    int    `json:"DRM"`
		}
		if err := json.NewDecoder(body).Decode(&rows); err != nil {
			return out, fmt.Errorf("the tuner's channel list could not be read")
		}
		for _, r := range rows {
			if r.DRM != 0 || !validText(r.Number, 32) || !validText(r.Name, 256) || !validLocator(r.URL) {
				report.SkippedChannels++
				report.note("A channel is protected, or its number, name or address could not be read.")
				continue
			}
			out.Channels = append(out.Channels, Channel{Key: "hdhr:" + r.Number, Name: r.Name, Number: r.Number, Locator: r.URL})
		}
		return out, nil
	case "guide":
		return out, nil
	}
	return out, fmt.Errorf("unknown kind of source")
}

func brought(cfg Config, c Channel) bool {
	if len(cfg.Channels) > 0 {
		for _, k := range cfg.Channels {
			if k == c.Key {
				return true
			}
		}
		return false
	}
	if len(cfg.Groups) > 0 {
		for _, g := range cfg.Groups {
			if g == c.Group {
				return true
			}
		}
		return false
	}
	return true
}

func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

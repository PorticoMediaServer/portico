package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// LiveSource is a source of channels or listings as the owner's Sources page shows it. Its
// sign-in is never shown: HasSignIn says one is stored.
type LiveSource struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // playlist, xtream, hdhomerun or guide
	Name string `json:"name"`
	// Audience is everyone or owner: who may watch its channels.
	Audience   string             `json:"audience"`
	TunerCount int                `json:"tunerCount"`
	Paused     bool               `json:"paused"`
	State      string             `json:"state"` // new, refreshing, up_to_date, trouble, not_working, needs_sign_in, paused
	LastError  string             `json:"lastError,omitempty"`
	Report     *live.SourceReport `json:"report,omitempty"`
	Refreshed  *time.Time         `json:"refreshedAt,omitempty"`
	Config     live.SourceConfig  `json:"config"`
	HasSignIn  bool               `json:"hasSignIn"`
	revision   int64
	status     int
}

func (s LiveSource) reply(h http.Header) int {
	etag(h, strconv.FormatInt(s.revision, 10))
	return s.status
}

// LiveSourceSave adds a source, previews one, or changes one (only what is sent changes).
// Playlist and Guide are an uploaded playlist and guide, as text.
type LiveSourceSave struct {
	Kind       string             `json:"kind,omitempty"`
	Name       *string            `json:"name,omitempty"`
	Audience   *string            `json:"audience,omitempty"`
	TunerCount *int               `json:"tunerCount,omitempty"`
	Paused     *bool              `json:"paused,omitempty"`
	Config     *live.SourceConfig `json:"config,omitempty"`
	SignIn     *live.SignIn       `json:"signIn,omitempty"`
	Playlist   string             `json:"playlist,omitempty"`
	Guide      string             `json:"guide,omitempty"`
}

// SourcePreview is what a source would bring in and leave out, and why.
type SourcePreview struct {
	Report   live.SourceReport  `json:"report"`
	Channels []live.SentChannel `json:"channels"`
	Tuners   int                `json:"tuners"`
}

// SourceChannel is one channel on the owner's channel map.
type SourceChannel struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Number       string   `json:"number"`
	SourceNumber string   `json:"sourceNumber,omitempty"`
	Group        string   `json:"group,omitempty"`
	BroughtIn    bool     `json:"broughtIn"`
	Hidden       bool     `json:"hidden"`
	Missing      bool     `json:"missing,omitempty"`
	GuideSource  string   `json:"guideSource,omitempty"`
	GuideKey     string   `json:"guideKey,omitempty"`
	Edited       []string `json:"edited,omitempty"`
}

// SourceChannelEdits changes many of a source's channels at once.
type SourceChannelEdits struct {
	Channels []SourceChannelEdit `json:"channels"`
}

// SourceChannelEdit is one channel's change: only what is sent changes.
type SourceChannelEdit struct {
	ID          string  `json:"id"`
	Name        *string `json:"name,omitempty"`
	Number      *string `json:"number,omitempty"`
	GuideSource *string `json:"guideSource,omitempty"`
	GuideKey    *string `json:"guideKey,omitempty"`
	Hidden      *bool   `json:"hidden,omitempty"`
	BroughtIn   *bool   `json:"broughtIn,omitempty"`
}

const maxUpload = 128 << 20

func (s *Server) liveSourceRoutes() {
	Handle(s, "GET /v1/admin/live/sources", Owner, s.listLiveSources)
	Handle(s, "POST /v1/admin/live/sources", Owner, s.createLiveSource).BodyLimit = maxUpload
	Handle(s, "POST /v1/admin/live/sources/preview", Owner, s.previewLiveSource).BodyLimit = maxUpload
	Handle(s, "GET /v1/admin/live/sources/{id}", Owner, s.getLiveSource)
	Handle(s, "PATCH /v1/admin/live/sources/{id}", Owner, s.saveLiveSource).BodyLimit = maxUpload
	Handle(s, "DELETE /v1/admin/live/sources/{id}", Owner, s.deleteLiveSource)
	Handle(s, "POST /v1/admin/live/sources/{id}/refresh", Owner, s.refreshLiveSource)
	Handle(s, "GET /v1/admin/live/sources/{id}/channels", Owner, s.listSourceChannels)
	Handle(s, "PATCH /v1/admin/live/sources/{id}/channels", Owner, s.editSourceChannels)
}

func (s *Server) liveSourceOut(src db.LiveSource) LiveSource {
	out := LiveSource{ID: src.PublicID, Kind: src.Kind, Name: src.Name, Audience: src.Audience, TunerCount: src.TunerCount, Paused: src.Paused,
		State: src.State, LastError: src.LastError, HasSignIn: len(src.Secret) > 0, revision: src.Revision}
	json.Unmarshal([]byte(src.Config), &out.Config)
	out.Config.PlaylistURL, out.Config.GuideURL = live.Masked(out.Config.PlaylistURL), live.Masked(out.Config.GuideURL)
	if src.Report != "" {
		out.Report = &live.SourceReport{}
		json.Unmarshal([]byte(src.Report), out.Report)
	}
	if !src.RefreshedAt.IsZero() {
		at := src.RefreshedAt.UTC()
		out.Refreshed = &at
	}
	switch {
	case src.Paused:
		out.State = "paused"
	case s.sources.Refreshing(src.ID):
		out.State = "refreshing"
	}
	return out
}

func sourceError(err error) error {
	switch {
	case errors.Is(err, db.ErrStale):
		return refusal(http.StatusPreconditionFailed, "changed_elsewhere", "This source was changed somewhere else. Read it again.", false)
	case errors.Is(err, db.ErrKeyReused):
		return errKeyReused()
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound()
	case errors.Is(err, db.ErrInvalid):
		return ErrInvalid("Check the channels and try again.")
	}
	return err
}

// checkSource fills in a source's settings from what was sent and refuses what cannot be saved.
func checkSource(save *db.LiveSourceSave, cfg *live.SourceConfig, req LiveSourceSave, adding bool) error {
	if save.Name == "" || len([]rune(save.Name)) > 120 {
		return ErrField("name", "Give the source a name of up to 120 characters.")
	}
	if save.Audience != "everyone" && save.Audience != "owner" {
		return ErrField("audience", "Choose Everyone on this server or Only me.")
	}
	if save.TunerCount < 1 || save.TunerCount > 256 {
		return ErrField("tunerCount", "A source delivers 1 to 256 channels at once.")
	}
	if cfg.RefreshHours != 0 && (cfg.RefreshHours < 1 || cfg.RefreshHours > 168) {
		return ErrField("config.refreshHours", "Refresh every 1 to 168 hours.")
	}
	if cfg.Numbering != "" && cfg.Numbering != "source" && cfg.Numbering != "sequential" {
		return ErrField("config.numbering", "Number channels as the source sends them, or in order.")
	}
	cfg.Uploaded = cfg.Uploaded || req.Playlist != ""
	cfg.GuideUploaded = cfg.GuideUploaded || req.Guide != ""
	switch save.Kind {
	case "playlist":
		if !cfg.Uploaded && cfg.PlaylistURL == "" {
			return ErrField("config.playlistUrl", "Give the playlist's address, or upload it.")
		}
	case "xtream":
		if cfg.ServerURL == "" {
			return ErrField("config.serverUrl", "Give the provider's server address.")
		}
		if adding && (req.SignIn == nil || req.SignIn.Username == "" || req.SignIn.Password == "") {
			return ErrField("signIn", "Enter the sign-in the provider gave you.")
		}
	case "hdhomerun":
		if cfg.TunerURL == "" {
			return ErrField("config.tunerUrl", "Give the tuner's address.")
		}
	case "guide":
		if !cfg.GuideUploaded && cfg.GuideURL == "" {
			return ErrField("config.guideUrl", "Give the guide's address, or upload it.")
		}
	default:
		return ErrField("kind", "A source is a playlist, an Xtream sign-in, an HDHomeRun tuner or a guide.")
	}
	raw, _ := json.Marshal(cfg)
	save.Config = string(raw)
	return nil
}

func (s *Server) listLiveSources(r *http.Request, _ None) (Page[LiveSource], error) {
	sources, err := s.db.LiveSources(r.Context())
	if err != nil {
		return Page[LiveSource]{}, err
	}
	out := Page[LiveSource]{Items: []LiveSource{}}
	for _, src := range sources {
		out.Items = append(out.Items, s.liveSourceOut(src))
	}
	return out, nil
}

func (s *Server) createLiveSource(r *http.Request, req LiveSourceSave) (LiveSource, error) {
	key, err := idempotencyKey(r)
	if err != nil {
		return LiveSource{}, err
	}
	save := db.LiveSourceSave{PublicID: db.NewID("lsr"), Kind: req.Kind, Audience: "everyone", TunerCount: 1, IdempotencyKey: key,
		RequestHash: requestHash(req), Now: s.now()}
	if req.Name != nil {
		save.Name = *req.Name
	}
	if req.Audience != nil {
		save.Audience = *req.Audience
	}
	if req.TunerCount != nil {
		save.TunerCount = *req.TunerCount
	}
	if req.Paused != nil {
		save.Paused = *req.Paused
	}
	cfg := live.SourceConfig{}
	if req.Config != nil {
		cfg = *req.Config
	}
	if err := checkSource(&save, &cfg, req, true); err != nil {
		return LiveSource{}, err
	}
	if req.SignIn != nil {
		save.Secret = s.sources.Seal(save.PublicID, *req.SignIn)
	}
	src, err := s.db.CreateLiveSource(r.Context(), CallerFrom(r.Context()).UserID, save)
	if err != nil {
		return LiveSource{}, sourceError(err)
	}
	if err := s.keepUploads(src.PublicID, req); err != nil {
		return LiveSource{}, err
	}
	s.sources.Wake()
	out := s.liveSourceOut(src)
	out.status = http.StatusCreated
	return out, nil
}

func (s *Server) keepUploads(sourceID string, req LiveSourceSave) error {
	if req.Playlist != "" {
		if err := s.sources.SaveUpload(sourceID, "playlist", req.Playlist); err != nil {
			return err
		}
	}
	if req.Guide != "" {
		return s.sources.SaveUpload(sourceID, "guide", req.Guide)
	}
	return nil
}

func (s *Server) previewLiveSource(r *http.Request, req LiveSourceSave) (SourcePreview, error) {
	cfg := live.SourceConfig{}
	if req.Config != nil {
		cfg = *req.Config
	}
	var signIn live.SignIn
	if req.SignIn != nil {
		signIn = *req.SignIn
	}
	p, err := s.sources.Preview(r.Context(), req.Kind, cfg, signIn, req.Playlist, req.Guide)
	if errors.Is(err, live.ErrSignIn) {
		return SourcePreview{}, ErrField("signIn", "The provider refused this sign-in.")
	}
	if err != nil {
		return SourcePreview{}, ErrInvalid(err.Error())
	}
	if p.Channels == nil {
		p.Channels = []live.SentChannel{}
	}
	return SourcePreview{Report: p.Report, Channels: p.Channels, Tuners: p.Tuners}, nil
}

func (s *Server) getLiveSource(r *http.Request, _ None) (LiveSource, error) {
	src, err := s.db.LiveSource(r.Context(), r.PathValue("id"))
	if err != nil {
		return LiveSource{}, sourceError(err)
	}
	return s.liveSourceOut(src), nil
}

func (s *Server) saveLiveSource(r *http.Request, req LiveSourceSave) (LiveSource, error) {
	ctx := r.Context()
	match, err := channelIfMatch(r)
	if err != nil {
		return LiveSource{}, err
	}
	src, err := s.db.LiveSource(ctx, r.PathValue("id"))
	if err != nil {
		return LiveSource{}, sourceError(err)
	}
	save := db.LiveSourceSave{Kind: src.Kind, Name: src.Name, Audience: src.Audience, TunerCount: src.TunerCount, Paused: src.Paused,
		KeepSecret: true, Now: s.now()}
	if req.Name != nil {
		save.Name = *req.Name
	}
	if req.Audience != nil {
		save.Audience = *req.Audience
	}
	if req.TunerCount != nil {
		save.TunerCount = *req.TunerCount
	}
	if req.Paused != nil {
		save.Paused = *req.Paused
	}
	var cfg live.SourceConfig
	json.Unmarshal([]byte(src.Config), &cfg)
	if req.Config != nil {
		stored := cfg
		uploaded, guideUploaded := cfg.Uploaded, cfg.GuideUploaded
		cfg = *req.Config
		// An address shown masked and sent back unchanged keeps the stored one.
		if cfg.PlaylistURL == live.Masked(stored.PlaylistURL) {
			cfg.PlaylistURL = stored.PlaylistURL
		}
		if cfg.GuideURL == live.Masked(stored.GuideURL) {
			cfg.GuideURL = stored.GuideURL
		}
		// A stored sign-in is sent only to the server it was entered for.
		if src.Kind == "xtream" && req.SignIn == nil && !live.SameServer(stored.ServerURL, cfg.ServerURL) {
			return LiveSource{}, ErrField("signIn", "Enter the sign-in again for the new server address.")
		}
		cfg.Uploaded, cfg.GuideUploaded = cfg.Uploaded || uploaded && cfg.PlaylistURL == "", cfg.GuideUploaded || guideUploaded && cfg.GuideURL == ""
	}
	if err := checkSource(&save, &cfg, req, false); err != nil {
		return LiveSource{}, err
	}
	if req.SignIn != nil {
		save.Secret, save.KeepSecret = s.sources.Seal(src.PublicID, *req.SignIn), false
	}
	saved, err := s.db.SaveLiveSource(ctx, CallerFrom(ctx).UserID, src.PublicID, save, match)
	if err != nil {
		return LiveSource{}, sourceError(err)
	}
	if err := s.keepUploads(src.PublicID, req); err != nil {
		return LiveSource{}, err
	}
	return s.liveSourceOut(saved), nil
}

func (s *Server) deleteLiveSource(r *http.Request, _ None) (None, error) {
	ctx := r.Context()
	id := r.PathValue("id")
	sourceID, err := s.db.DeleteLiveSource(ctx, CallerFrom(ctx).UserID, id, s.now())
	if err != nil || sourceID == 0 {
		return None{}, err
	}
	s.sources.RemoveUploads(id)
	return None{}, s.db.DropChannelGuide(ctx, sourceID, "")
}

func (s *Server) refreshLiveSource(r *http.Request, _ None) (LiveSource, error) {
	src, err := s.db.LiveSource(r.Context(), r.PathValue("id"))
	if err != nil {
		return LiveSource{}, sourceError(err)
	}
	go func() {
		if err := s.sources.Refresh(s.background, src.PublicID); err != nil {
			s.log.Warn("refreshing a Live TV source failed", "source", src.PublicID, "error", err)
		}
	}()
	out := s.liveSourceOut(src)
	out.State, out.status = "refreshing", http.StatusAccepted
	return out, nil
}

func formatNumber(n int) string {
	if n%1000 == 0 {
		return strconv.Itoa(n / 1000)
	}
	return strconv.Itoa(n/1000) + "." + strconv.Itoa(n%1000)
}

func (s *Server) listSourceChannels(r *http.Request, _ None) (Page[SourceChannel], error) {
	src, err := s.db.LiveSource(r.Context(), r.PathValue("id"))
	if err != nil {
		return Page[SourceChannel]{}, sourceError(err)
	}
	c, err := readLiveCursor(r)
	if err != nil {
		return Page[SourceChannel]{}, err
	}
	rows, err := s.db.SourceChannels(r.Context(), src.ID, c.Number, c.ID, 201)
	if err != nil {
		return Page[SourceChannel]{}, err
	}
	out := Page[SourceChannel]{Items: []SourceChannel{}}
	for n, row := range rows {
		if n == 200 {
			out.NextCursor = encodeOpaque(liveCursor{Number: rows[n-1].Number, ID: rows[n-1].ID})
			break
		}
		item := SourceChannel{ID: row.PublicID, Name: row.Name, Number: formatNumber(row.Number), Group: row.Group,
			BroughtIn: row.State != db.ChannelNotIn, Hidden: row.State == db.ChannelHidden, Missing: row.Missing, GuideSource: row.GuideSource,
			GuideKey: row.GuideKey, Edited: row.Edited}
		if row.SourceNumber != 0 {
			item.SourceNumber = formatNumber(row.SourceNumber)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (s *Server) editSourceChannels(r *http.Request, req SourceChannelEdits) (None, error) {
	src, err := s.db.LiveSource(r.Context(), r.PathValue("id"))
	if err != nil {
		return None{}, sourceError(err)
	}
	if len(req.Channels) > 1000 {
		return None{}, ErrField("channels", "Change up to 1,000 channels at a time.")
	}
	edits := make([]db.ChannelEdit, 0, len(req.Channels))
	for n, c := range req.Channels {
		e := db.ChannelEdit{PublicID: c.ID, Name: c.Name, GuideSource: c.GuideSource, GuideKey: c.GuideKey, Hidden: c.Hidden, BroughtIn: c.BroughtIn}
		if c.Name != nil && (*c.Name == "" || len([]rune(*c.Name)) > 256) {
			return None{}, ErrField("channels["+strconv.Itoa(n)+"].name", "Give the channel a name.")
		}
		if c.Number != nil {
			number, ok := live.ChannelNumber(*c.Number)
			if !ok {
				return None{}, ErrField("channels["+strconv.Itoa(n)+"].number", "Use a number such as 5 or 5.1.")
			}
			e.Number = &number
		}
		edits = append(edits, e)
	}
	return None{}, sourceError(s.db.EditSourceChannels(r.Context(), src.ID, edits, s.now()))
}

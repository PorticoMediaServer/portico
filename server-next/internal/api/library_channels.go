package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// LibraryChannel is a Library Channel as the owner's list and editor show it. The editor's
// read adds the whole builder and the channel's health, and carries an ETag.
type LibraryChannel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Number      int    `json:"number"`
	On          bool   `json:"on"`
	Description string `json:"description,omitempty"`
	// Summary is one sentence saying what it plays: the description, or one made from the builder.
	Summary   string   `json:"summary"`
	TimeZone  string   `json:"timeZone"`
	Libraries []string `json:"libraries"`
	// Logo is the uploaded logo's address; without one the apps draw a monogram.
	Logo string `json:"logo,omitempty"`
	// State is onAir, building, needsSchedule, needsAttention or off.
	State            string     `json:"state"`
	Problem          string     `json:"problem,omitempty"`
	Titles           int        `json:"titles"`
	ScheduledThrough *time.Time `json:"scheduledThrough,omitempty"`
	// UnknownConditions says the builder holds conditions this server cannot test; they are
	// kept as they are.
	UnknownConditions bool `json:"unknownConditions,omitempty"`
	// DroppedTitles is how many pinned or weighted titles that no longer exist this save took
	// out of the builder.
	DroppedTitles int                      `json:"droppedTitles,omitempty"`
	Builder       *librarychannels.Builder `json:"builder,omitempty"`
	Health        *ChannelHealth           `json:"health,omitempty"`
	revision      int64
	status        int
}

func (c LibraryChannel) reply(h http.Header) int {
	etag(h, strconv.FormatInt(c.revision, 10))
	return c.status
}

// ChannelHealth answers "is this channel actually on the air?".
type ChannelHealth struct {
	OnAir bool `json:"onAir"`
	// Message is the one sentence why not, when it is not.
	Message     string     `json:"message,omitempty"`
	Reaches     *time.Time `json:"reaches,omitempty"`
	Programmes  int        `json:"programmes"`
	Titles      int        `json:"titles"`
	Unplaceable int        `json:"unplaceable"`
	Building    bool       `json:"building"`
}

// LibraryChannelSave makes a channel, or changes one: only what is sent changes, and a builder
// is always sent whole.
type LibraryChannelSave struct {
	Name        *string                  `json:"name,omitempty"`
	Description *string                  `json:"description,omitempty"`
	TimeZone    *string                  `json:"timeZone,omitempty"`
	On          *bool                    `json:"on,omitempty"`
	Builder     *librarychannels.Builder `json:"builder,omitempty"`
}

// LibraryChannelOrder is the owner's order: every Library Channel's id, once.
type LibraryChannelOrder struct {
	IDs []string `json:"ids"`
}

// ChannelTemplate is a whole channel filled in for this server, to review before saving.
type ChannelTemplate struct {
	Key         string                  `json:"key"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Available   bool                    `json:"available"`
	Reason      string                  `json:"reason,omitempty"`
	Builder     librarychannels.Builder `json:"builder"`
}

// ChannelPreviewAsk is a channel as it is being edited. ChannelID, for one that exists,
// previews it as saving would: from the end of the programme now on, with its own places.
type ChannelPreviewAsk struct {
	ChannelID string                  `json:"channelId,omitempty"`
	TimeZone  string                  `json:"timeZone"`
	Builder   librarychannels.Builder `json:"builder"`
}

// ChannelPreview is what the channel being edited would play.
type ChannelPreview struct {
	Titles      int                `json:"titles"`
	Unplaceable int                `json:"unplaceable"`
	Hours       int                `json:"hours"`
	Sample      []PreviewTitle     `json:"sample"`
	Today       []PreviewProgramme `json:"today"`
}

// PreviewTitle is a title in a preview.
type PreviewTitle struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	Season       *int   `json:"season,omitempty"`
	Episode      *int   `json:"episode,omitempty"`
	Year         int    `json:"year,omitempty"`
}

// PreviewProgramme is one programme of today's schedule; Title is absent for the slate.
type PreviewProgramme struct {
	StartsAt time.Time     `json:"startsAt"`
	EndsAt   time.Time     `json:"endsAt"`
	Title    *PreviewTitle `json:"title,omitempty"`
}

// BlockPreset is a block the owner saved to reuse on any Library Channel.
type BlockPreset struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Library     string                 `json:"library"`
	Recipe      librarychannels.Recipe `json:"recipe"`
	revision    int64
	status      int
}

func (p BlockPreset) reply(h http.Header) int {
	etag(h, strconv.FormatInt(p.revision, 10))
	return p.status
}

// BlockPresetSave makes a preset, or changes one: only what is sent changes.
type BlockPresetSave struct {
	Name        *string                 `json:"name,omitempty"`
	Description *string                 `json:"description,omitempty"`
	Library     *string                 `json:"library,omitempty"`
	Recipe      *librarychannels.Recipe `json:"recipe,omitempty"`
}

const libraryChannelPage = 50

func (s *Server) libraryChannelRoutes() {
	builderBody := int64(librarychannels.MaxBuilderSize + 64<<10)
	Handle(s, "GET /v1/admin/library-channels", Owner, s.listLibraryChannels)
	Handle(s, "POST /v1/admin/library-channels", Owner, s.createLibraryChannel).MaxBody = builderBody
	Handle(s, "PUT /v1/admin/library-channels/order", Owner, s.orderLibraryChannels)
	Handle(s, "GET /v1/admin/library-channels/templates", Owner, s.channelTemplates)
	Handle(s, "POST /v1/admin/library-channels/preview", Owner, s.previewLibraryChannel).MaxBody = builderBody
	Handle(s, "GET /v1/admin/library-channels/{id}", Owner, s.getLibraryChannel)
	Handle(s, "PATCH /v1/admin/library-channels/{id}", Owner, s.saveLibraryChannel).MaxBody = builderBody
	Handle(s, "DELETE /v1/admin/library-channels/{id}", Owner, s.deleteLibraryChannel)
	Handle(s, "POST /v1/admin/library-channels/{id}/regenerate", Owner, s.regenerateLibraryChannel)
	s.logoRoute()
	Handle(s, "DELETE /v1/admin/library-channels/{id}/logo", Owner, s.deleteChannelLogo)
	Handle(s, "GET /v1/admin/block-presets", Owner, s.listBlockPresets)
	Handle(s, "POST /v1/admin/block-presets", Owner, s.createBlockPreset)
	Handle(s, "PATCH /v1/admin/block-presets/{id}", Owner, s.saveBlockPreset)
	Handle(s, "DELETE /v1/admin/block-presets/{id}", Owner, s.deleteBlockPreset)
}

func channelError(err error) error {
	var field *librarychannels.FieldError
	switch {
	case errors.As(err, &field):
		return ErrField(field.Field, field.Message)
	case errors.Is(err, db.ErrInvalid):
		return ErrInvalid("The server didn't accept this channel. Check its name, libraries, conditions and time zone, then try again.")
	case errors.Is(err, db.ErrChannelLimit):
		return ErrConflict("channel_capacity", "A server can have up to 1,000 Library Channels.")
	case errors.Is(err, db.ErrPresetLimit):
		return ErrConflict("preset_capacity", "A server can have up to 1,000 block presets.")
	case errors.Is(err, db.ErrStale):
		return refusal(http.StatusPreconditionFailed, "changed_elsewhere", "This was changed somewhere else. Read it again.", false)
	case errors.Is(err, db.ErrKeyReused):
		return errKeyReused()
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound()
	}
	return err
}

func channelIfMatch(r *http.Request) (int64, error) {
	match, err := settingsIfMatch(r)
	if err != nil {
		return 0, channelError(db.ErrStale)
	}
	return match, nil
}

func stateWord(ch db.LibraryChannel, now time.Time) string {
	switch {
	case !ch.On:
		return "off"
	case ch.State == db.LibraryChannelBuilding:
		return "building"
	case ch.State == db.LibraryChannelAttention:
		return "needsAttention"
	case ch.ScheduledThrough.Before(now.Add(24 * time.Hour)):
		return "needsSchedule"
	}
	return "onAir"
}

func (s *Server) libraryChannelOut(ch db.LibraryChannel, b *librarychannels.Builder, detail bool) LibraryChannel {
	out := LibraryChannel{ID: ch.PublicID, Name: ch.Name, Number: ch.Number, On: ch.On, Description: ch.Description, Summary: ch.Description,
		TimeZone: ch.TimeZone, Libraries: ch.Libraries, State: stateWord(ch, s.now()), Problem: ch.Problem, Titles: ch.Titles,
		revision: ch.Revision}
	if ch.LogoHash != "" {
		out.Logo = "/v1/images/" + ch.LogoHash
	}
	if !ch.ScheduledThrough.IsZero() {
		through := ch.ScheduledThrough.UTC()
		out.ScheduledThrough = &through
	}
	if b != nil {
		if out.Summary == "" {
			out.Summary = librarychannels.Describe(b)
		}
		out.UnknownConditions = b.HasUnknownConditions()
		if detail {
			out.Builder = b
		}
	}
	return out
}

func (s *Server) listLibraryChannels(r *http.Request, _ None) (Page[LibraryChannel], error) {
	after := 0
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return Page[LibraryChannel]{}, ErrField("cursor", "This cursor is not one this list gave.")
		}
		after = n
	}
	rows, err := s.db.LibraryChannels(r.Context(), after, libraryChannelPage+1, true)
	if err != nil {
		return Page[LibraryChannel]{}, err
	}
	out := Page[LibraryChannel]{Items: []LibraryChannel{}}
	for n, ch := range rows {
		if n == libraryChannelPage {
			out.NextCursor = strconv.Itoa(rows[n-1].Number)
			break
		}
		b, _ := librarychannels.Parse(ch.Builder)
		out.Items = append(out.Items, s.libraryChannelOut(ch, b, false))
	}
	return out, nil
}

// checkBuilder refuses a builder that cannot be saved and answers it as stored, with how many
// pinned or weighted titles that no longer exist it took out. Blocks added from a preset are
// filled in from it first. The on-screen logo may be on without a logo: the session carries
// none until one is uploaded, so deleting a logo never blocks a save.
func (s *Server) checkBuilder(ctx context.Context, b *librarychannels.Builder, zone string) (string, int, error) {
	if _, err := librarychannels.LoadZone(zone); err != nil {
		return "", 0, channelError(err)
	}
	kinds, err := s.db.LibraryKinds(ctx)
	if err != nil {
		return "", 0, err
	}
	for n := range b.Blocks {
		blk := &b.Blocks[n]
		if blk.FromPreset == "" {
			continue
		}
		preset, err := s.db.BlockPreset(ctx, blk.FromPreset)
		if errors.Is(err, db.ErrNotFound) {
			return "", 0, ErrField("builder.blocks["+strconv.Itoa(n)+"].fromPreset", "That block preset no longer exists.")
		}
		if err != nil {
			return "", 0, err
		}
		var recipe librarychannels.Recipe
		if err := json.Unmarshal([]byte(preset.Recipe), &recipe); err != nil {
			return "", 0, err
		}
		recipe.Copy(blk, preset.Name, preset.Library, kinds[preset.Library])
	}
	titles, err := s.db.TitleLibraries(ctx, append(b.Pinned(), b.Weighted()...))
	if err != nil {
		return "", 0, err
	}
	dropped := b.DropGone(titles)
	if err := b.Check(kinds, titles, s.db.HasTitleData()); err != nil {
		var field *librarychannels.FieldError
		if errors.As(err, &field) {
			return "", 0, ErrField("builder."+field.Field, field.Message)
		}
		return "", 0, err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return "", 0, err
	}
	if len(raw) > librarychannels.MaxBuilderSize {
		return "", 0, ErrField("builder", "This channel's settings are larger than 2 MB.")
	}
	return string(raw), dropped, nil
}

func checkChannelName(name string) error {
	if name == "" {
		return ErrField("name", "Give the channel a name.")
	}
	if len([]rune(name)) > 120 {
		return ErrField("name", "A channel's name is up to 120 characters.")
	}
	return nil
}

// serverZone is the time zone a new channel starts with: the server's own when it can be named.
func (s *Server) serverZone() string {
	for _, zone := range []string{os.Getenv("TZ"), time.Local.String()} {
		if _, err := librarychannels.LoadZone(zone); err == nil {
			return zone
		}
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if _, zone, ok := strings.Cut(link, "zoneinfo/"); ok {
			if _, err := librarychannels.LoadZone(zone); err == nil {
				return zone
			}
		}
	}
	return "UTC"
}

// encodeOpaque and decodeOpaque make a list's cursor, which apps never read.
func encodeOpaque(v any) string {
	raw, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeOpaque(text string, v any) error {
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

func (s *Server) createLibraryChannel(r *http.Request, req LibraryChannelSave) (LibraryChannel, error) {
	key, err := idempotencyKey(r)
	if err != nil {
		return LibraryChannel{}, err
	}
	save := db.LibraryChannelSave{TimeZone: s.serverZone(), On: true, IdempotencyKey: key, RequestHash: requestHash(req), Now: s.now()}
	if req.Name != nil {
		save.Name = *req.Name
	}
	if err := checkChannelName(save.Name); err != nil {
		return LibraryChannel{}, err
	}
	if req.Description != nil {
		save.Description = *req.Description
	}
	if req.TimeZone != nil {
		save.TimeZone = *req.TimeZone
	}
	if req.On != nil {
		save.On = *req.On
	}
	if req.Builder == nil {
		return LibraryChannel{}, ErrField("builder.libraries", "Choose at least one library.")
	}
	var dropped int
	if save.Builder, dropped, err = s.checkBuilder(r.Context(), req.Builder, save.TimeZone); err != nil {
		return LibraryChannel{}, err
	}
	save.Libraries = req.Builder.Libraries()
	ch, err := s.db.CreateLibraryChannel(r.Context(), CallerFrom(r.Context()).UserID, save)
	if err != nil {
		return LibraryChannel{}, channelError(err)
	}
	s.channels.Wake()
	b, _ := librarychannels.Parse(ch.Builder)
	out := s.libraryChannelOut(ch, b, true)
	out.status, out.DroppedTitles = http.StatusCreated, dropped
	return out, nil
}

func (s *Server) getLibraryChannel(r *http.Request, _ None) (LibraryChannel, error) {
	ctx := r.Context()
	ch, err := s.db.LibraryChannel(ctx, r.PathValue("id"), true)
	if err != nil {
		return LibraryChannel{}, channelError(err)
	}
	b, err := librarychannels.Parse(ch.Builder)
	if err != nil {
		return LibraryChannel{}, err
	}
	out := s.libraryChannelOut(ch, b, true)
	h := &ChannelHealth{Titles: ch.Titles, Unplaceable: ch.Unplaceable, Building: ch.State == db.LibraryChannelBuilding,
		Reaches: out.ScheduledThrough}
	if h.Programmes, err = s.db.ScheduledProgrammes(ctx, ch.ID, s.now()); err != nil {
		return LibraryChannel{}, err
	}
	switch out.State {
	case "onAir", "needsSchedule":
		h.OnAir = h.Programmes > 0
	case "off":
		h.Message = "This channel is turned off."
	case "building":
		h.Message = "This channel's schedule is being made."
	default:
		h.Message = ch.Problem
	}
	if !h.OnAir && h.Message == "" {
		h.Message = "This channel isn't playing."
	}
	out.Health = h
	return out, nil
}

func (s *Server) saveLibraryChannel(r *http.Request, req LibraryChannelSave) (LibraryChannel, error) {
	ctx := r.Context()
	match, err := channelIfMatch(r)
	if err != nil {
		return LibraryChannel{}, err
	}
	ch, err := s.db.LibraryChannel(ctx, r.PathValue("id"), true)
	if err != nil {
		return LibraryChannel{}, channelError(err)
	}
	if match >= 0 && match != ch.Revision {
		return LibraryChannel{}, channelError(db.ErrStale)
	}
	save := db.LibraryChannelSave{Name: ch.Name, Description: ch.Description, TimeZone: ch.TimeZone, On: ch.On, Builder: ch.Builder,
		Libraries: ch.Libraries, Now: s.now()}
	if req.Name != nil {
		save.Name = *req.Name
	}
	if err := checkChannelName(save.Name); err != nil {
		return LibraryChannel{}, err
	}
	if req.Description != nil {
		save.Description = *req.Description
	}
	if req.TimeZone != nil {
		save.TimeZone = *req.TimeZone
	}
	if req.On != nil {
		save.On = *req.On
	}
	// Only a builder that was sent is checked: a rename, a new description or Turn off saves even
	// when a library the stored builder names has gone since. A new time zone is checked alone.
	b := req.Builder
	dropped := 0
	if b != nil {
		if save.Builder, dropped, err = s.checkBuilder(ctx, b, save.TimeZone); err != nil {
			return LibraryChannel{}, err
		}
		save.Libraries = b.Libraries()
	} else {
		if _, err := librarychannels.LoadZone(save.TimeZone); err != nil {
			return LibraryChannel{}, channelError(err)
		}
		if b, err = librarychannels.Parse(ch.Builder); err != nil {
			return LibraryChannel{}, err
		}
	}
	saved, err := s.db.SaveLibraryChannel(ctx, CallerFrom(ctx).UserID, ch.PublicID, save, match)
	if err != nil {
		return LibraryChannel{}, channelError(err)
	}
	s.channels.Wake()
	out := s.libraryChannelOut(saved, b, true)
	out.DroppedTitles = dropped
	return out, nil
}

func (s *Server) deleteLibraryChannel(r *http.Request, _ None) (None, error) {
	ctx := r.Context()
	id := r.PathValue("id")
	if err := s.db.DeleteLibraryChannel(ctx, CallerFrom(ctx).UserID, id, s.now()); err != nil {
		return None{}, err
	}
	return None{}, s.channels.Dropped(ctx, id)
}

func (s *Server) regenerateLibraryChannel(r *http.Request, _ None) (LibraryChannel, error) {
	ch, err := s.db.RegenerateLibraryChannel(r.Context(), r.PathValue("id"), s.now())
	if err != nil {
		return LibraryChannel{}, channelError(err)
	}
	s.channels.Wake()
	out := s.libraryChannelOut(ch, nil, false)
	out.status = http.StatusAccepted
	return out, nil
}

func (s *Server) orderLibraryChannels(r *http.Request, req LibraryChannelOrder) (None, error) {
	return None{}, channelError(s.db.OrderLibraryChannels(r.Context(), req.IDs, s.now()))
}

// enough is how many matching titles a template needs to be offered.
const enough = 3

func (s *Server) channelTemplates(r *http.Request, _ None) (Page[ChannelTemplate], error) {
	ctx := r.Context()
	kinds, err := s.db.LibraryKinds(ctx)
	if err != nil {
		return Page[ChannelTemplate]{}, err
	}
	out := Page[ChannelTemplate]{Items: []ChannelTemplate{}}
	for _, t := range librarychannels.Templates(kinds) {
		count, err := s.channels.CountUpTo(ctx, &t.Builder.Selection, enough)
		if err != nil {
			return Page[ChannelTemplate]{}, err
		}
		item := ChannelTemplate{Key: t.Key, Name: t.Name, Description: t.Description, Available: count >= enough, Builder: t.Builder}
		if !item.Available {
			item.Reason = "Not enough media"
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func previewTitle(t *db.ProgrammeTitle) *PreviewTitle {
	if t == nil {
		return nil
	}
	return &PreviewTitle{ID: t.PublicID, Title: t.Title, EpisodeTitle: t.EpisodeTitle, Season: t.Season, Episode: t.Episode, Year: t.Year}
}

func (s *Server) previewLibraryChannel(r *http.Request, req ChannelPreviewAsk) (ChannelPreview, error) {
	ctx := r.Context()
	zone := req.TimeZone
	if zone == "" {
		zone = s.serverZone()
	}
	if _, _, err := s.checkBuilder(ctx, &req.Builder, zone); err != nil {
		return ChannelPreview{}, err
	}
	loc, _ := librarychannels.LoadZone(zone)
	p, err := s.channels.Preview(ctx, &req.Builder, loc, req.ChannelID)
	if errors.Is(err, db.ErrNotFound) {
		return ChannelPreview{}, ErrField("channelId", "This channel no longer exists.")
	}
	if err != nil {
		return ChannelPreview{}, err
	}
	out := ChannelPreview{Titles: p.Titles, Unplaceable: p.Unplaceable, Hours: p.Hours, Sample: []PreviewTitle{}, Today: []PreviewProgramme{}}
	for n := range p.Sample {
		out.Sample = append(out.Sample, *previewTitle(&p.Sample[n]))
	}
	for _, row := range p.Today {
		out.Today = append(out.Today, PreviewProgramme{StartsAt: row.StartsAt.UTC(), EndsAt: row.EndsAt.UTC(), Title: previewTitle(row.Title)})
	}
	return out, nil
}

func presetOut(p db.BlockPreset) (BlockPreset, error) {
	out := BlockPreset{ID: p.PublicID, Name: p.Name, Description: p.Description, Library: p.Library, revision: p.Revision}
	return out, json.Unmarshal([]byte(p.Recipe), &out.Recipe)
}

func (s *Server) listBlockPresets(r *http.Request, _ None) (Page[BlockPreset], error) {
	var afterName string
	var afterID int64
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		var c struct {
			Name string `json:"n"`
			ID   int64  `json:"i"`
		}
		if err := decodeOpaque(cursor, &c); err != nil {
			return Page[BlockPreset]{}, ErrField("cursor", "This cursor is not one this list gave.")
		}
		afterName, afterID = c.Name, c.ID
	}
	rows, err := s.db.BlockPresets(r.Context(), afterName, afterID, libraryChannelPage+1)
	if err != nil {
		return Page[BlockPreset]{}, err
	}
	out := Page[BlockPreset]{Items: []BlockPreset{}}
	for n, p := range rows {
		if n == libraryChannelPage {
			last := rows[n-1]
			out.NextCursor = encodeOpaque(map[string]any{"n": last.Name, "i": last.ID})
			break
		}
		item, err := presetOut(p)
		if err != nil {
			return Page[BlockPreset]{}, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func checkPreset(name string, recipe *librarychannels.Recipe) error {
	if name == "" {
		return ErrField("name", "Give the preset a name.")
	}
	if len([]rune(name)) > 120 {
		return ErrField("name", "A preset's name is up to 120 characters.")
	}
	if recipe == nil {
		return ErrField("recipe", "Say what the preset plays.")
	}
	return channelError(librarychannels.CheckRecipe(recipe))
}

func (s *Server) createBlockPreset(r *http.Request, req BlockPresetSave) (BlockPreset, error) {
	key, err := idempotencyKey(r)
	if err != nil {
		return BlockPreset{}, err
	}
	save := db.BlockPresetSave{IdempotencyKey: key, RequestHash: requestHash(req), Now: s.now()}
	if req.Name != nil {
		save.Name = *req.Name
	}
	if req.Description != nil {
		save.Description = *req.Description
	}
	if req.Library != nil {
		save.Library = *req.Library
	}
	if err := checkPreset(save.Name, req.Recipe); err != nil {
		return BlockPreset{}, err
	}
	raw, _ := json.Marshal(req.Recipe)
	save.Recipe = string(raw)
	p, err := s.db.CreateBlockPreset(r.Context(), CallerFrom(r.Context()).UserID, save)
	if err != nil {
		return BlockPreset{}, channelError(err)
	}
	out, err := presetOut(p)
	out.status = http.StatusCreated
	return out, err
}

func (s *Server) saveBlockPreset(r *http.Request, req BlockPresetSave) (BlockPreset, error) {
	ctx := r.Context()
	match, err := channelIfMatch(r)
	if err != nil {
		return BlockPreset{}, err
	}
	p, err := s.db.BlockPreset(ctx, r.PathValue("id"))
	if err != nil {
		return BlockPreset{}, channelError(err)
	}
	current, err := presetOut(p)
	if err != nil {
		return BlockPreset{}, err
	}
	save := db.BlockPresetSave{Name: p.Name, Description: p.Description, Library: p.Library, Now: s.now()}
	recipe := &current.Recipe
	if req.Name != nil {
		save.Name = *req.Name
	}
	if req.Description != nil {
		save.Description = *req.Description
	}
	if req.Library != nil {
		save.Library = *req.Library
	}
	if req.Recipe != nil {
		recipe = req.Recipe
	}
	if err := checkPreset(save.Name, recipe); err != nil {
		return BlockPreset{}, err
	}
	raw, _ := json.Marshal(recipe)
	save.Recipe = string(raw)
	saved, err := s.db.SaveBlockPreset(ctx, p.PublicID, save, match)
	if err != nil {
		return BlockPreset{}, channelError(err)
	}
	return presetOut(saved)
}

func (s *Server) deleteBlockPreset(r *http.Request, _ None) (None, error) {
	return None{}, s.db.DeleteBlockPreset(r.Context(), r.PathValue("id"))
}

// logoRoute takes the picture itself as the body, so it is registered by hand, with the same
// sign-in check as every route.
func (s *Server) logoRoute() {
	route := &Route{Pattern: "PUT /v1/admin/library-channels/{id}/logo", Access: Owner, Scope: ScopeAdmin,
		Response: reflect.TypeFor[LibraryChannel]()}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, route)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, librarychannels.MaxLogoBytes))
		if err != nil {
			s.writeError(w, r, refusal(http.StatusRequestEntityTooLarge, "body_too_large", "A logo is up to 10 MB.", false))
			return
		}
		hash, err := librarychannels.LogoID(data)
		if err == nil {
			_, _, err = s.artwork.PutSized(hash, data, librarychannels.LogoSizes)
		}
		if errors.Is(err, librarychannels.ErrLogo) || errors.Is(err, artwork.ErrNotAPicture) {
			s.writeError(w, r, ErrInvalid("Upload a JPEG, PNG, GIF or WebP from 16 to 4,096 pixels a side."))
			return
		}
		if err == nil {
			err = s.db.SetLibraryChannelLogo(r.Context(), r.PathValue("id"), hash, s.now())
		}
		var ch db.LibraryChannel
		if err == nil {
			ch, err = s.db.LibraryChannel(r.Context(), r.PathValue("id"), false)
		}
		if err != nil {
			s.writeError(w, r, channelError(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(s.libraryChannelOut(ch, nil, false))
	})
}

// deleteChannelLogo goes back to the monogram; with no logo the apps draw none on screen.
func (s *Server) deleteChannelLogo(r *http.Request, _ None) (LibraryChannel, error) {
	ctx := r.Context()
	if err := s.db.SetLibraryChannelLogo(ctx, r.PathValue("id"), "", s.now()); err != nil {
		return LibraryChannel{}, channelError(err)
	}
	ch, err := s.db.LibraryChannel(ctx, r.PathValue("id"), false)
	if err != nil {
		return LibraryChannel{}, channelError(err)
	}
	return s.libraryChannelOut(ch, nil, false), nil
}

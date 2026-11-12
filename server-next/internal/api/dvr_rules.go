package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// DvrRule is a series rule.
type DvrRule struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Source  string `json:"source"`
	Channel string `json:"channel,omitempty"`
	// ChannelName is the channel's name, sent with Channel.
	ChannelName string `json:"channelName,omitempty"`
	ByTitle     bool   `json:"byTitle"` // the guide has no series id: it matches by title on the same channel
	NewOnly     bool   `json:"newOnly"`
	KeepNewest  *int   `json:"keepNewest,omitempty"`
	KeepDays    *int   `json:"keepDays,omitempty"`
	Priority    int    `json:"priority"`
	PadBeforeMs *int64 `json:"padBeforeMs,omitempty"`
	PadAfterMs  *int64 `json:"padAfterMs,omitempty"`
	Paused      bool   `json:"paused"`
	// Preview is sent only by a preview (?preview=true): what the rule would record.
	Preview *DvrRulePreview `json:"preview,omitempty"`
	status  int
}

// DvrRulePreview is what a rule being made or edited would record over the next 14 days:
// how many airings, and the first of them.
type DvrRulePreview struct {
	Matches int         `json:"matches"`
	Airings []DvrAiring `json:"airings"`
}

// DvrAiring is one airing a rule would record. AlreadyBooked says it is booked already.
type DvrAiring struct {
	Title         string    `json:"title"`
	EpisodeTitle  string    `json:"episodeTitle,omitempty"`
	Season        *int      `json:"season,omitempty"`
	Episode       *int      `json:"episode,omitempty"`
	Channel       string    `json:"channel"`
	StartsAt      time.Time `json:"startsAt"`
	EndsAt        time.Time `json:"endsAt"`
	AlreadyBooked bool      `json:"alreadyBooked"`
}

func (r DvrRule) reply(http.Header) int { return r.status }

// DvrRuleSave makes a rule from a programme in the guide, or changes one. AnyChannel follows
// the series on every channel of its source; Channel (a channel's id, on a change) follows it
// on that one channel of the same source.
type DvrRuleSave struct {
	Programme   string  `json:"programme,omitempty"`
	AnyChannel  *bool   `json:"anyChannel,omitempty"`
	Channel     *string `json:"channel,omitempty"`
	NewOnly     *bool   `json:"newOnly,omitempty"`
	KeepNewest  *int    `json:"keepNewest,omitempty"`
	KeepDays    *int    `json:"keepDays,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
	PadBeforeMs *int64  `json:"padBeforeMs,omitempty"`
	PadAfterMs  *int64  `json:"padAfterMs,omitempty"`
	Paused      *bool   `json:"paused,omitempty"`
}

func ruleOut(rule db.RecordingRule, source, channel string) DvrRule {
	out := DvrRule{ID: rule.PublicID, Title: rule.Title, Source: source, Channel: channel, ByTitle: rule.SeriesKey == "", NewOnly: rule.NewOnly,
		KeepNewest: rule.KeepNewest, KeepDays: rule.KeepDays, Priority: rule.Priority, Paused: rule.Paused}
	if rule.PadBefore != nil {
		ms := rule.PadBefore.Milliseconds()
		out.PadBeforeMs = &ms
	}
	if rule.PadAfter != nil {
		ms := rule.PadAfter.Milliseconds()
		out.PadAfterMs = &ms
	}
	return out
}

func (s *Server) listRules(r *http.Request, _ None) (Page[DvrRule], error) {
	if _, err := s.liveAllowed(r, true); err != nil {
		return Page[DvrRule]{}, err
	}
	profile := viewer(r).ProfileID
	if r.URL.Query().Get("scope") == "everyone" && CallerFrom(r.Context()).Role == "owner" {
		profile = 0
	}
	rules, err := s.db.Rules(r.Context(), profile)
	if err != nil {
		return Page[DvrRule]{}, err
	}
	out := Page[DvrRule]{Items: []DvrRule{}}
	for _, rule := range rules {
		o, err := s.ruleNamed(r.Context(), rule)
		if err != nil {
			return Page[DvrRule]{}, err
		}
		out.Items = append(out.Items, o)
	}
	return out, nil
}

// ruleNamed is a rule with its source's and channel's ids; one whose channel has gone is sent
// without a channel.
func (s *Server) ruleNamed(ctx context.Context, rule db.RecordingRule) (DvrRule, error) {
	var source, channel string
	if src, err := s.db.LiveSourceByID(ctx, rule.SourceID); err == nil {
		source = src.PublicID
	} else if !errors.Is(err, db.ErrNotFound) {
		return DvrRule{}, err
	}
	var name string
	if rule.ChannelID != 0 {
		var err error
		if channel, err = s.db.ChannelPublicID(ctx, rule.ChannelID); errors.Is(err, db.ErrNotFound) {
			channel = ""
		} else if err != nil {
			return DvrRule{}, err
		} else if name, err = s.db.ChannelName(ctx, rule.ChannelID); err != nil && !errors.Is(err, db.ErrNotFound) {
			return DvrRule{}, err
		}
	}
	out := ruleOut(rule, source, channel)
	out.ChannelName = name
	return out, nil
}

// rulePreview is what a rule would record, as far as the viewer may know: airings on channels
// it may watch, within its rating limit.
func (s *Server) rulePreview(r *http.Request, lv db.LiveViewer, rule db.RecordingRule) (*DvrRulePreview, error) {
	ctx := r.Context()
	airings, err := s.recorder.RuleAirings(ctx, rule, s.now())
	if err != nil {
		return nil, err
	}
	out := &DvrRulePreview{Airings: []DvrAiring{}}
	watchable := map[int64]bool{}
	for _, a := range airings {
		if !rated(lv, a.Programme.RatingAge) {
			continue
		}
		ok, known := watchable[a.Channel.ID]
		if !known {
			ch, err := s.db.LiveChannelByID(ctx, viewer(r), a.Channel.PublicID)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return nil, err
			}
			ok = err == nil && mayWatch(lv, ch)
			watchable[a.Channel.ID] = ok
		}
		if !ok {
			continue
		}
		out.Matches++
		if len(out.Airings) < 10 {
			p := a.Programme
			out.Airings = append(out.Airings, DvrAiring{Title: p.Title, EpisodeTitle: p.EpisodeTitle, Season: p.Season, Episode: p.Episode,
				Channel: a.Channel.Name, StartsAt: p.StartsAt.UTC(), EndsAt: p.EndsAt.UTC(), AlreadyBooked: a.Booked})
		}
	}
	return out, nil
}

// ruleChannel applies a change of channel: one channel of the rule's own source, or with
// AnyChannel every channel of it, which needs the guide's series id.
func (s *Server) ruleChannel(r *http.Request, lv db.LiveViewer, rule *db.RecordingRule, req DvrRuleSave) error {
	switch {
	case req.Channel != nil && *req.Channel != "":
		ch, err := s.db.LiveChannelByID(r.Context(), viewer(r), *req.Channel)
		if errors.Is(err, db.ErrNotFound) || err == nil && !mayWatch(lv, ch) {
			return ErrField("channel", "Choose a channel from the guide.")
		}
		if err != nil {
			return err
		}
		if ch.Kind != db.ChannelFromSource || ch.SourceID != rule.SourceID {
			return ErrField("channel", "Choose a channel from the same source as this rule.")
		}
		rule.ChannelID = ch.ID
	case req.AnyChannel != nil && *req.AnyChannel:
		if rule.SeriesKey == "" {
			return ErrField("anyChannel", "This guide doesn't identify the series, so the rule follows one channel.")
		}
		rule.ChannelID = 0
	case req.AnyChannel != nil && rule.ChannelID == 0:
		return ErrField("channel", "Choose the channel to follow.")
	}
	return nil
}

func applyRule(rule *db.RecordingRule, req DvrRuleSave) error {
	if req.NewOnly != nil {
		rule.NewOnly = *req.NewOnly
	}
	// 0 clears a keep choice, and the owner's default applies again.
	if req.KeepNewest != nil {
		switch {
		case *req.KeepNewest < 0:
			return ErrField("keepNewest", "Keep at least one episode, or 0 for the default.")
		case *req.KeepNewest == 0:
			rule.KeepNewest = nil
		default:
			rule.KeepNewest = req.KeepNewest
		}
	}
	if req.KeepDays != nil {
		switch {
		case *req.KeepDays < 0:
			return ErrField("keepDays", "Keep for at least a day, or 0 for the default.")
		case *req.KeepDays == 0:
			rule.KeepDays = nil
		default:
			rule.KeepDays = req.KeepDays
		}
	}
	if req.Priority != nil {
		rule.Priority = max(min(*req.Priority, 1), -1)
	}
	if req.PadBeforeMs != nil {
		d := time.Duration(max(*req.PadBeforeMs, 0)) * time.Millisecond
		rule.PadBefore = &d
	}
	if req.PadAfterMs != nil {
		d := time.Duration(max(*req.PadAfterMs, 0)) * time.Millisecond
		rule.PadAfter = &d
	}
	if req.Paused != nil {
		rule.Paused = *req.Paused
	}
	return nil
}

// createRule makes a series rule from a programme: it follows the guide's series id, or where
// the guide has none, the same title on the same channel. With ?preview=true it answers what
// it would record and makes nothing.
func (s *Server) createRule(r *http.Request, req DvrRuleSave) (DvrRule, error) {
	lv, err := s.liveAllowed(r, true)
	if err != nil {
		return DvrRule{}, err
	}
	ctx := r.Context()
	ch, p, err := s.programme(ctx, viewer(r), lv, req.Programme)
	if err != nil {
		return DvrRule{}, err
	}
	if ch.Kind != db.ChannelFromSource {
		return DvrRule{}, ErrConflict("cannot_record", "This program can't be recorded.")
	}
	rule := db.RecordingRule{ProfileID: viewer(r).ProfileID, SourceID: ch.SourceID, ChannelID: ch.ID, SeriesKey: p.SeriesKey, Title: p.Title}
	if req.AnyChannel != nil && *req.AnyChannel && p.SeriesKey != "" {
		rule.ChannelID = 0
	}
	if req.NewOnly != nil && *req.NewOnly && p.Flags == 0 {
		return DvrRule{}, ErrField("newOnly", "This guide doesn't say which episodes are new.")
	}
	if err := applyRule(&rule, req); err != nil {
		return DvrRule{}, err
	}
	if r.URL.Query().Get("preview") == "true" {
		out, err := s.ruleNamed(ctx, rule)
		if err == nil {
			out.Preview, err = s.rulePreview(r, lv, rule)
		}
		return out, err
	}
	saved, err := s.db.SaveRule(ctx, rule, s.now())
	if err != nil {
		return DvrRule{}, err
	}
	if err := s.recorder.FollowRules(ctx); err != nil {
		return DvrRule{}, err
	}
	out, err := s.ruleNamed(ctx, saved)
	out.status = http.StatusCreated
	return out, err
}

// changeRule edits a rule: its channel choice and options (its title and source stay). With
// ?preview=true it answers what the edited rule would record and saves nothing.
func (s *Server) changeRule(r *http.Request, req DvrRuleSave) (DvrRule, error) {
	lv, err := s.liveAllowed(r, true)
	if err != nil {
		return DvrRule{}, err
	}
	ctx := r.Context()
	rule, err := s.db.Rule(ctx, r.PathValue("id"))
	if err != nil {
		return DvrRule{}, sourceError(err)
	}
	if rule.ProfileID != viewer(r).ProfileID && CallerFrom(ctx).Role != "owner" {
		return DvrRule{}, ErrNotPermitted()
	}
	before := rule.ChannelID
	if err := s.ruleChannel(r, lv, &rule, req); err != nil {
		return DvrRule{}, err
	}
	if err := applyRule(&rule, req); err != nil {
		return DvrRule{}, err
	}
	if r.URL.Query().Get("preview") == "true" {
		out, err := s.ruleNamed(ctx, rule)
		if err == nil {
			out.Preview, err = s.rulePreview(r, lv, rule)
		}
		return out, err
	}
	saved, err := s.db.SaveRule(ctx, rule, s.now())
	if err != nil {
		return DvrRule{}, err
	}
	// A rule moved to one channel lets go of its bookings not yet started on the others.
	if saved.ChannelID != before {
		if err := s.db.LetGoOfOtherChannels(ctx, saved.ID, saved.ChannelID, s.now()); err != nil {
			return DvrRule{}, err
		}
	}
	// The edited rule books what it now matches at once, as a new rule does.
	if err := s.recorder.FollowRules(ctx); err != nil {
		return DvrRule{}, err
	}
	return s.ruleNamed(ctx, saved)
}

func (s *Server) deleteRule(r *http.Request, _ None) (None, error) {
	if _, err := s.liveAllowed(r, true); err != nil {
		return None{}, err
	}
	ctx := r.Context()
	rule, err := s.db.Rule(ctx, r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return None{}, nil
	}
	if err != nil {
		return None{}, err
	}
	if rule.ProfileID != viewer(r).ProfileID && CallerFrom(ctx).Role != "owner" {
		return None{}, ErrNotPermitted()
	}
	return None{}, s.db.DeleteRule(ctx, rule.PublicID, r.URL.Query().Get("bookings") == "cancel", s.now())
}

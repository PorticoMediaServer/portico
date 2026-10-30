package librarychannels

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type Template struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	MinimumCandidates int    `json:"minimumCandidates"`
	Applicable        bool   `json:"applicable"`
	Reason            string `json:"reason"`
	Config            Config `json:"config"`
}

func (s *Store) Templates(ctx context.Context, a Authority) ([]Template, error) {
	out := []Template{}
	e := s.snapshot(ctx, a, true, func(tx *sql.Tx, scope Scope) error {
		rows, e := tx.QueryContext(ctx, `SELECT id,kind FROM libraries WHERE kind IN('movie','tv','anime') AND NOT EXISTS(SELECT 1 FROM catalog_libraries gone WHERE gone.library_id=libraries.id AND gone.retired=1) ORDER BY id`)
		if e != nil {
			return unavailable(e)
		}
		byKind := map[string][]string{}
		for rows.Next() {
			var id, kind string
			if cause := rows.Scan(&id, &kind); cause != nil {
				rows.Close()
				return unavailable(cause)
			}
			if scope.AllowsLibrary != nil && scope.AllowsLibrary(id) {
				byKind[kind] = append(byKind[kind], id)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return unavailable(e)
		}
		add := func(id, name, description, kind string, query Query, mode, episodes string) {
			query.LibraryIDs = append([]string{}, byKind[kind]...)
			if query.Kinds == nil {
				query.Kinds = []string{"movie"}
				if kind != "movie" {
					query.Kinds = []string{"episode"}
				}
			}
			if query.Order == "" {
				query.Order = "title"
			}
			if query.ShowIDs == nil {
				query.ShowIDs = []string{}
			}
			if query.Genres == nil {
				query.Genres = []string{}
			}
			cid := digest("library-template-v1", id, strings.Join(query.LibraryIDs, ","))[:48]
			rid := digest(cid, "main")[:48]
			c := Config{Version: ProtocolVersion, ID: cid, Name: name, Enabled: true, Timezone: "UTC", Seed: cid, DefaultRuleID: rid, ViewerAccess: "server-members", Quality: Quality{Mode: "automatic", AllowLossy: true}, Overlay: Overlay{Corner: "top-right", SizePercent: 10, InsetPercent: 3, Treatment: "original"}, Rules: []Rule{{ID: rid, Name: name, Query: query, Mode: mode, EpisodeMode: episodes, Exhaustion: "loop", DeduplicationWindow: 1, MaxConsecutive: 1, Weights: []ItemWeight{}}}, Blocks: []Block{}, TemplateID: id}
			if episodes == "marathon" {
				c.Rules[0].MaxConsecutive = 12
			}
			t := Template{ID: id, Name: name, Description: description, MinimumCandidates: 3, Applicable: len(query.LibraryIDs) > 0, Config: c}
			if !t.Applicable {
				t.Reason = "No matching library is configured."
			}
			out = append(out, t)
		}
		add("movies", "Movie channel", "All eligible movies, with a shuffle bag and no immediate repeats.", "movie", Query{}, "shuffle-bag", "none")
		add("television", "Television channel", "Episodes progress in show order while rotating among shows.", "tv", Query{Order: "episode"}, "shuffle-bag", "in-order")
		add("eighties", "Eighties movies", "Movies released from 1980 through 1989.", "movie", Query{YearFrom: 1980, YearThrough: 1989}, "shuffle-bag", "none")
		add("nineties", "Nineties movies", "Movies released from 1990 through 1999.", "movie", Query{YearFrom: 1990, YearThrough: 1999}, "shuffle-bag", "none")
		add("thrillers", "Thriller channel", "Normalized Thriller genre evidence; missing genre evidence is not guessed.", "movie", Query{Genres: []string{"Thriller"}}, "shuffle-bag", "none")
		add("family", "Family movies", "Movies tagged Family in normalized metadata. This is a genre selection, not an age-rating guarantee.", "movie", Query{Genres: []string{"Family"}}, "shuffle-bag", "none")
		add("anime", "Anime channel", "Episodes from explicitly configured anime libraries, in show order.", "anime", Query{Order: "episode"}, "shuffle-bag", "in-order")
		add("recent", "Recently added movies", "Eligible movies added during the last 30 days; no hidden candidate cutoff.", "movie", Query{RecentDays: 30, Order: "recent"}, "sequential", "none")
		add("marathon", "TV marathons", "Up to twelve consecutive episodes from a show before rotating.", "tv", Query{Order: "episode"}, "shuffle-bag", "marathon")
		return nil
	})
	if e != nil {
		return nil, e
	}
	// This is an existence probe, not a hidden scheduling cutoff. The returned
	// configurations keep their complete queries; only the temporary probe stops
	// after the documented semantic minimum of three eligible, resolved items.
	for i := range out {
		if !out[i].Applicable {
			continue
		}
		probe := out[i].Config
		probe.Rules = append([]Rule(nil), probe.Rules...)
		probe.Rules[0].Query.Limit = out[i].MinimumCandidates
		preview, err := s.eligibility(ctx, a, probe)
		if err != nil {
			return nil, err
		}
		out[i].Applicable = len(preview.Rules) == 1 && preview.Rules[0].Eligible >= out[i].MinimumCandidates
		if !out[i].Applicable {
			out[i].Reason = "Needs at least three eligible items with resolved duration matching this template."
		}
	}
	return out, nil
}

// Installation is explicit and idempotent. Existing owner-customized channels
// are returned unchanged; a template refresh cannot replace their configuration.
func (s *Store) InstallTemplate(ctx context.Context, a Authority, templateID, requestID, timezone string) (Channel, error) {
	templates, e := s.Templates(ctx, a)
	if e != nil {
		return Channel{}, e
	}
	var selected *Template
	for i := range templates {
		if templates[i].ID == templateID {
			selected = &templates[i]
			break
		}
	}
	if selected == nil || !selected.Applicable {
		return Channel{}, ErrInvalid
	}
	c := selected.Config
	c.Timezone = timezone
	var existing Channel
	found := false
	e = s.transaction(ctx, a, true, func(tx *sql.Tx, _ Scope) error {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT config_json FROM lc_channels WHERE id=? AND removed=0`, c.ID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return unavailable(err)
		}
		if cause := json.Unmarshal([]byte(raw), &existing.Config); cause != nil {
			return unavailable(cause)
		}
		existing, err = readChannel(ctx, tx, c.ID)
		found = err == nil
		return err
	})
	if e != nil {
		return existing, e
	}
	if found {
		return existing, nil
	}
	preview, e := s.Preview(ctx, a, c)
	if e != nil {
		return Channel{}, e
	}
	if len(preview.Rules) != 1 || preview.Rules[0].Eligible < selected.MinimumCandidates {
		return Channel{}, ErrTemplateEmpty
	}
	sort.Strings(c.Rules[0].Query.LibraryIDs)
	return s.Save(ctx, a, SaveInput{RequestID: requestID, Config: c})
}

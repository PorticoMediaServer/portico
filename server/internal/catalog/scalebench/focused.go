package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Repeated service reads supplement the one-observation full vocabulary matrix.
// Their route names distinguish real per-request medians from medians over routes.
var focusedSamples int

func focusedReads(db *sql.DB, s *catalog.Service) ([]routeResult, error) {
	if focusedSamples == 0 {
		return nil, nil
	}
	var results []routeResult
	add := func(name string, read func(context.Context, *catalog.Service) error) error {
		if !routeSelected("focus/" + name) {
			return nil
		}
		r, err := exerciseRead(db, s, "focus/"+name, focusedSamples, read)
		results = append(results, r)
		return err
	}
	age := 13
	for _, restricted := range []bool{false, true} {
		v := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
		prefix := "open/"
		if restricted {
			v.Profile = "viewer-pg13"
			v.Fence = "scale-pg13"
			v.Restrictions = identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}
			prefix = "pg13/"
		}
		for _, scope := range [][2]string{{"movies", "movies"}, {"tv", "shows"}, {"tv", "episodes"}, {"music", "artists"}, {"music", "albums"}, {"music", "songs"}} {
			caps, err := s.BrowseCapabilitiesFor(scope[0], scope[1])
			if err != nil {
				return results, err
			}
			measure := func(name string, request catalog.BrowseRequest) error {
				total := 0
				for _, deep := range []bool{false, true} {
					suffix := "first"
					if deep {
						suffix = "deep"
						request.Range = &catalog.BrowseRange{Start: min(50000, max(0, total-40))}
						fmt.Fprintf(os.Stderr, "FOCUS_RANGE %s%s/%s/%s total=%d start=%d\n", prefix, scope[0], scope[1], name, total, request.Range.Start)
					}
					if err := add(prefix+scope[0]+"/"+scope[1]+"/"+name+"/"+suffix, func(_ context.Context, s *catalog.Service) error {
						out, err := s.BrowseEntities(v, request)
						if err == nil {
							if !deep {
								total = out.PageInfo.Total
							}
							if total > 0 && len(out.Entries) == 0 {
								return fmt.Errorf("counted %d visible entries but selected an empty page", total)
							}
						}
						return err
					}); err != nil {
						return err
					}
				}
				return nil
			}
			for _, sort := range caps.Sorts {
				for _, direction := range sort.Directions {
					seed := uint32(0)
					if sort.ID == "random" {
						seed = 123456789
					}
					r := catalog.BrowseRequest{Library: scope[0], Pivot: scope[1], Limit: 40, Sort: []catalog.BrowseSortSelection{{Field: sort.ID, Direction: direction, Seed: seed}}}
					if err := measure("sort/"+sort.ID+"/"+direction, r); err != nil {
						return results, err
					}
				}
			}
			for _, state := range []string{"available", "partial", "unavailable"} {
				raw, _ := json.Marshal(map[string]any{"field": "availability", "operator": "equals", "value": state})
				node, err := catalog.ParseBrowseQuery(raw, "query")
				if err != nil {
					return results, err
				}
				r := catalog.BrowseRequest{Library: scope[0], Pivot: scope[1], Query: node, Limit: 40}
				if err := measure("availability/"+state, r); err != nil {
					return results, err
				}
			}
		}
		for _, query := range []string{"T", "Ti", "Tit", "Titl", "Title", "Title 0", "Title 00", "Title 000", "Title 0000", "Title 00000", "Title 000000", "S", "Sh", "Sho", "Show", "A", "Al", "Alb", "Albu", "Album", "Ar", "Art", "Arti", "Artis", "Artist", "P", "Pe", "Per", "Pers", "Perso", "Person"} {
			if err := add(prefix+"search/"+query, func(ctx context.Context, s *catalog.Service) error {
				out, err := s.Search(ctx, catalog.SearchRequest{Viewer: v, Q: query, Groups: []string{"movies", "shows", "episodes", "artists", "albums", "songs", "books", "collections", "authors", "people"}, Limit: 12})
				if err != nil {
					return err
				}
				for _, g := range out.Groups {
					if g.ErrorCode != "" {
						return fmt.Errorf("search group %s: %s", g.ID, g.ErrorCode)
					}
				}
				return nil
			}); err != nil {
				return results, err
			}
		}
	}
	return results, nil
}

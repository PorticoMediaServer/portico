package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// The refresh path for an item whose identity is already accepted. Before this, every wake of
// such a row called the provider again — a scan, a sidecar appearing, a policy edit, a show
// fanning out to its episodes — and asked for a document it had fetched minutes earlier
// (screen_worker.go:344). Doc 46 named it: "A legitimately woken row always calls the provider
// even if it holds an accepted identity and an hour-old publication."
//
// Three things now stand between a wake and a request, in order: is the document still inside
// its freshness window, is there any allowance left for refreshing documents we already hold,
// and can the provider answer "not modified" instead of sending it again.

// screenConditionalProvider is a provider that can be asked conditionally. It is a separate,
// optional interface so ScreenProvider stays exactly as it was: a provider that does not offer
// validators, or a fake in a test, simply takes the unconditional path.
type screenConditionalProvider interface {
	ScreenDetailsConditional(ctx context.Context, kind, id, language, region string, in metadataprovider.Conditional) (metadataprovider.ScreenRecord, metadataprovider.Conditional, error)
}

// rememberedScreenRecord is the accepted identity's last stored provider document. It is the
// evidence a publication was built from, so republishing from it produces exactly what the
// provider would have produced, without asking.
func (s *Service) rememberedScreenRecord(ctx context.Context, b screenBase, id metadataprovider.ScreenID) (metadataprovider.ScreenRecord, bool, error) {
	var payload string
	e := dbwork.QueryRow(ctx, s.db, `SELECT payload FROM screen_metadata_candidates WHERE target_kind=? AND target_id=? AND provider=? AND provider_type=? AND provider_id=? AND language=? AND region=? LIMIT 1`,
		b.TargetKind, b.Entity, id.Provider, id.Type, id.ID, b.Language, b.Region).Scan(&payload)
	if errors.Is(e, sql.ErrNoRows) {
		return metadataprovider.ScreenRecord{}, false, nil
	}
	if e != nil {
		return metadataprovider.ScreenRecord{}, false, e
	}
	var r metadataprovider.ScreenRecord
	if json.Unmarshal([]byte(payload), &r) != nil || r.Identity != id {
		return metadataprovider.ScreenRecord{}, false, nil
	}
	if metadataprovider.ValidateScreenRecord(r) != nil {
		return metadataprovider.ScreenRecord{}, false, nil
	}
	return r, true, nil
}

// airingStatuses are the provider statuses that mean "another episode is coming". They are the
// one case where a fortnight is too long to wait: somebody watching a current series expects
// this week's episode to be there.
// AniList's own words are kept on its records, so its airing and announced statuses are listed too.
var airingStatuses = map[string]bool{"Returning Series": true, "In Production": true, "Planned": true, "Continuing": true, "Upcoming": true, "RELEASING": true, "NOT_YET_RELEASED": true}

// recentlyAdded is how long after arriving in the library an item counts as the thing somebody
// is most likely about to open.
const recentlyAdded = 7 * 24 * time.Hour

// screenRefreshPriority decides how hard this row's freshness window is. Nothing here reaches
// a provider; it reads what the library already knows.
func (s *Service) screenRefreshPriority(ctx context.Context, b screenBase, remembered metadataprovider.ScreenRecord, now time.Time) RefreshPriority {
	if airingStatuses[remembered.Status] {
		return RefreshAiring
	}
	var added sql.NullString
	query := `SELECT added_text FROM catalog_item_details WHERE entity_id=?`
	if b.TargetKind == "show" {
		query = `SELECT min(d.added_text) FROM catalog_episodes e JOIN catalog_item_details d ON d.entity_id=e.entity_id WHERE e.show_id=?`
	}
	if e := dbwork.QueryRow(ctx, s.db, query, b.Entity).Scan(&added); e != nil || !added.Valid {
		return RefreshOrdinary
	}
	at, e := time.Parse(time.RFC3339, added.String)
	if e != nil || now.Sub(at) > recentlyAdded {
		return RefreshOrdinary
	}
	return RefreshRecentlyAdded
}

// screenRefreshRecord produces the accepted identity's current document, asking the provider
// only when the rules allow it. It reports the record and whether it came from the provider.
func (s *Service) screenRefreshRecord(ctx context.Context, p *screenClaim, id metadataprovider.ScreenID, provider ScreenProvider) (metadataprovider.ScreenRecord, error) {
	ctx = s.lookupContext(ctx)
	b := p.Base
	now := s.publicationTime().UTC()
	remembered, held, e := s.rememberedScreenRecord(ctx, b, id)
	if e != nil {
		return metadataprovider.ScreenRecord{}, e
	}
	kind := id.Type
	resource := screenDocumentResource(id.ID, b.Language, b.Region)
	if b.ForceRefresh {
		held = false
	}
	// A refresh the allowance admitted asks the provider itself, never the short
	// response cache, or Refresh all could spend its allowance on cached answers.
	refreshing := false
	if held {
		priority := s.screenRefreshPriority(ctx, b, remembered, now)
		fresh, validators, err := s.documentFresh(ctx, id.Provider, kind, resource, priority, now)
		if err != nil {
			return metadataprovider.ScreenRecord{}, err
		}
		if fresh {
			// Inside the window the provider is not asked at all. The stored document is
			// republished, so a local change that woke this row still reaches the catalogue.
			return remembered, nil
		}
		// A refresh of something already held is what the budget is for. A first fetch never
		// reaches here, because there is nothing remembered to fall back to.
		admitted, err := s.admitRefresh(ctx, id.Provider, now)
		if err != nil {
			return metadataprovider.ScreenRecord{}, err
		}
		if !admitted {
			return remembered, nil
		}
		refreshing = true
		if conditional, ok := provider.(screenConditionalProvider); ok && !toConditional(validators).Empty() {
			r, out, err := conditional.ScreenDetailsConditional(ctx, kind, id.ID, b.Language, b.Region, toConditional(validators))
			if errors.Is(err, metadataprovider.ErrNotModified) {
				// The cheapest possible answer: no body, nothing to parse, and the window
				// starts again from now.
				if noteErr := s.noteDocumentChecked(ctx, id.Provider, kind, resource, fromConditional(out), false, now); noteErr != nil {
					return metadataprovider.ScreenRecord{}, noteErr
				}
				return remembered, nil
			}
			if err != nil {
				return metadataprovider.ScreenRecord{}, err
			}
			if noteErr := s.noteDocumentChecked(ctx, id.Provider, kind, resource, fromConditional(out), true, now); noteErr != nil {
				return metadataprovider.ScreenRecord{}, noteErr
			}
			return r, nil
		}
	}
	var r metadataprovider.ScreenRecord
	validators := documentValidators{}
	if conditional, ok := provider.(screenConditionalProvider); ok && (b.ForceRefresh || refreshing) {
		var out metadataprovider.Conditional
		r, out, e = conditional.ScreenDetailsConditional(ctx, kind, id.ID, b.Language, b.Region, metadataprovider.Conditional{})
		// The validators it answered with make the next refresh conditional.
		validators = fromConditional(out)
	} else if fresh, ok := provider.(interface {
		ScreenDetailsFresh(context.Context, string, string, string, string) (metadataprovider.ScreenRecord, error)
	}); ok && (b.ForceRefresh || refreshing) {
		r, e = fresh.ScreenDetailsFresh(ctx, kind, id.ID, b.Language, b.Region)
	} else {
		r, e = provider.ScreenDetails(ctx, kind, id.ID, b.Language, b.Region)
	}
	if e != nil {
		return metadataprovider.ScreenRecord{}, e
	}
	if e = s.noteDocumentChecked(ctx, id.Provider, kind, resource, validators, true, now); e != nil {
		return metadataprovider.ScreenRecord{}, e
	}
	return r, nil
}

// rememberedEpisodeRecord is the episode document this row last published, if it still answers
// the same question: the same show, order and local coordinates, in the same language. A
// re-identified show, a changed order, a renumbered file or a new metadata language makes it
// somebody else's answer, and the provider is asked.
func (s *Service) rememberedEpisodeRecord(ctx context.Context, b screenBase) (metadataprovider.ScreenRecord, bool, error) {
	var payload string
	e := dbwork.QueryRow(ctx, s.db, `SELECT payload FROM screen_metadata_candidates WHERE target_kind=? AND target_id=? AND provider=? AND provider_type='episode' AND language=? AND region=? ORDER BY observed_at DESC LIMIT 1`,
		b.TargetKind, b.Entity, b.ParentProvider, b.Language, b.Region).Scan(&payload)
	if errors.Is(e, sql.ErrNoRows) {
		return metadataprovider.ScreenRecord{}, false, nil
	}
	if e != nil {
		return metadataprovider.ScreenRecord{}, false, e
	}
	var r metadataprovider.ScreenRecord
	if json.Unmarshal([]byte(payload), &r) != nil || metadataprovider.ValidateScreenRecord(r) != nil {
		return metadataprovider.ScreenRecord{}, false, nil
	}
	c := r.Coordinates
	matches := c != nil && c.ShowID == b.ParentProviderID && c.Order == b.ParentOrder && c.Episode == b.Number
	if b.Numbering == "absolute" {
		matches = matches && c.Absolute != nil && *c.Absolute == b.Number
	} else {
		matches = matches && c.Season != nil && *c.Season == b.Season
	}
	if !matches {
		return metadataprovider.ScreenRecord{}, false, nil
	}
	return r, true, nil
}

// screenEpisodeRecord is screenRefreshRecord for an episode of a matched show. A show that is
// woken fans out to every one of its episodes, so without this one new file cost a request for
// each episode the show has. An episode whose document is held and inside its window is
// republished from it; one past its window is refreshed only while the provider's refresh
// allowance lasts; an episode never fetched is always asked for.
func (s *Service) screenEpisodeRecord(ctx context.Context, p *screenClaim, provider screenEpisodeProvider) (metadataprovider.ScreenRecord, error) {
	ctx = s.lookupContext(ctx)
	b := p.Base
	now := s.publicationTime().UTC()
	remembered, held, e := s.rememberedEpisodeRecord(ctx, b)
	if e != nil {
		return metadataprovider.ScreenRecord{}, e
	}
	if b.ForceRefresh {
		held = false
	}
	resource := screenDocumentResource(remembered.Identity.ID, b.Language, b.Region)
	if held {
		fresh, validators, err := s.documentFresh(ctx, remembered.Identity.Provider, "episode", resource, s.screenRefreshPriority(ctx, b, remembered, now), now)
		if err != nil {
			return metadataprovider.ScreenRecord{}, err
		}
		if fresh {
			return remembered, nil
		}
		admitted, err := s.admitRefresh(ctx, remembered.Identity.Provider, now)
		if err != nil {
			return metadataprovider.ScreenRecord{}, err
		}
		if !admitted {
			return remembered, nil
		}
		if conditional, ok := provider.(interface {
			ScreenEpisodeConditional(context.Context, metadataprovider.ScreenRecord, string, metadataprovider.Conditional) (metadataprovider.ScreenRecord, metadataprovider.Conditional, error)
		}); ok {
			r, out, err := conditional.ScreenEpisodeConditional(ctx, remembered, b.Language, toConditional(validators))
			unchanged := errors.Is(err, metadataprovider.ErrNotModified)
			if err != nil && !unchanged {
				return r, err
			}
			if !unchanged && (r.Identity != remembered.Identity || metadataprovider.ValidateScreenRecord(r) != nil) {
				return r, &metadataprovider.Error{Provider: b.ParentProvider, Code: "episode_identity_mismatch"}
			}
			if err = s.noteDocumentChecked(ctx, remembered.Identity.Provider, "episode", resource, fromConditional(out), !unchanged, now); err != nil {
				return r, err
			}
			if unchanged {
				return remembered, nil
			}
			return r, nil
		}
	}
	var r metadataprovider.ScreenRecord
	if b.ForceRefresh {
		if b.Numbering == "absolute" {
			if fresh, ok := provider.(interface {
				ScreenAbsoluteEpisodeFresh(context.Context, string, int, string, string) (metadataprovider.ScreenRecord, error)
			}); ok {
				r, e = fresh.ScreenAbsoluteEpisodeFresh(ctx, b.ParentProviderID, b.Number, b.ParentOrder, b.Language)
			}
		} else {
			if fresh, ok := provider.(interface {
				ScreenEpisodeFresh(context.Context, string, int, int, string, string) (metadataprovider.ScreenRecord, error)
			}); ok {
				r, e = fresh.ScreenEpisodeFresh(ctx, b.ParentProviderID, b.Season, b.Number, b.ParentOrder, b.Language)
			}
		}
	}
	if r.Identity.ID != "" || e != nil {
	} else if b.Numbering == "absolute" {
		if absolute, ok := provider.(interface {
			ScreenAbsoluteEpisode(context.Context, string, int, string, string) (metadataprovider.ScreenRecord, error)
		}); ok {
			r, e = absolute.ScreenAbsoluteEpisode(ctx, b.ParentProviderID, b.Number, b.ParentOrder, b.Language)
		} else {
			return r, &metadataprovider.Error{Provider: b.ParentProvider, Code: "order_unavailable"}
		}
	} else {
		r, e = provider.ScreenEpisode(ctx, b.ParentProviderID, b.Season, b.Number, b.ParentOrder, b.Language)
	}
	if e != nil {
		return r, e
	}
	if e = s.noteDocumentChecked(ctx, r.Identity.Provider, "episode", screenDocumentResource(r.Identity.ID, b.Language, b.Region), documentValidators{}, true, now); e != nil {
		return metadataprovider.ScreenRecord{}, e
	}
	return r, nil
}

func toConditional(v documentValidators) metadataprovider.Conditional {
	return metadataprovider.Conditional{ETag: v.ETag, LastModified: v.LastModified}
}

func fromConditional(c metadataprovider.Conditional) documentValidators {
	return documentValidators{ETag: c.ETag, LastModified: c.LastModified}
}

func screenDocumentResource(id, language, region string) string {
	return id + "@" + language + ":" + region
}

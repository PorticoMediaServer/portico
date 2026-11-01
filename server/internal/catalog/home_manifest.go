package catalog

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// HomeManifest publishes the layout without waiting for shelf composition.
// Every row's existing endpoint composes its preview in a separate request;
// the server still decides row policy, order, membership and family expansion.
func (s *Service) HomeManifest(r HomeRequest) (HomeDocument, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out HomeDocument
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).HomeManifest(r)
			return err
		})
		return out, err
	}
	r = r.scoped()
	if err := s.prepareViewer(r.Viewer); err != nil {
		return HomeDocument{}, err
	}
	r.Libraries = homeUnique(r.Libraries)
	r.Now = s.recommendationNow(r.Now)
	revision, err := s.homeRevision(r.Libraries, r.Profile)
	if err != nil {
		return HomeDocument{}, err
	}
	paging, err := s.homePagingRevision(r.Libraries, r.Profile)
	if err != nil {
		return HomeDocument{}, err
	}
	specs, err := s.homeSpecs(r)
	if err != nil {
		return HomeDocument{}, err
	}
	hidden := map[string]bool{}
	for _, id := range r.HiddenRowIDs {
		hidden[id] = true
	}
	ordered := homeOrdered(specs, r.RowOrder)
	maximum := operations.HomeLayoutMaxRows
	for _, spec := range ordered {
		if spec.ID == recFamily && spec.PolicyState == "available" && !spec.hiddenIn(r.RowOrder, hidden) {
			maximum -= recPersonalRows - 1
			break
		}
	}
	out := HomeDocument{ServerID: r.ServerID, ViewerFence: r.ViewerFence, PrivacyFence: r.PrivacyFence, Revision: revision, Rows: []HomeRow{}, Layout: HomeLayout{Revision: r.LayoutRevision, RowOrder: append([]string{}, r.RowOrder...), HiddenRowIDs: append([]string{}, r.HiddenRowIDs...)}, GeneratedAt: r.now().UTC().Format("2006-01-02T15:04:05.000Z")}
	for _, spec := range ordered {
		if spec.PolicyState != "available" || spec.hiddenIn(r.RowOrder, hidden) {
			continue
		}
		if len(out.Rows) == maximum && !spec.Required {
			continue
		}
		if len(out.Rows) == maximum {
			for i := len(out.Rows) - 1; i >= 0; i-- {
				if !out.Rows[i].Required {
					out.Rows = append(out.Rows[:i], out.Rows[i+1:]...)
					break
				}
			}
		}
		row := spec.descriptor()
		row.Deferred = true
		row.ViewerFence = r.ViewerFence
		row.Limit = homeLimit(r.Limit)
		row.Revision = paging
		out.Rows = append(out.Rows, row)
	}
	composed := s.WithRecommendationRestrictions(r.Restrictions)
	out.Spotlight, err = composed.homeSpotlight(r)
	return out, err
}

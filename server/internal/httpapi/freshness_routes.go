package httpapi

import (
	"context"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
)

type derivationFailure struct {
	Domain   int             `json:"domain"`
	EntityID string          `json:"entityId,omitempty"`
	Title    string          `json:"title,omitempty"`
	Reason   string          `json:"reason"`
	Attempts int             `json:"attempts"`
	FailedAt apiwire.Instant `json:"failedAt"`
}
type derivationReport struct {
	Failures []derivationFailure `json:"failures"`
	More     bool                `json:"more"`
}

func registerFreshnessRoutes(registry *apikit.Registry, d Dependencies) {
	err := apikit.Register(registry, apikit.Route[struct{}, derivationReport]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "get_catalogue_failures", Method: "GET", Path: "/v1/admin/catalogue/failures", Summary: "Read titles set aside by catalogue workers", Access: apikit.Admin, Requires: apikit.ScopeAdmin, AdminScope: "library", Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (derivationReport, error) {
		out := derivationReport{Failures: []derivationFailure{}}
		if _, err := d.owner(r); err != nil {
			return out, foundationAuthError(err)
		}
		rows, err := d.DB.QueryContext(ctx, `SELECT f.domain,COALESCE(pid(e.public_id),''),COALESCE(e.title,''),f.reason,f.attempts,f.failed_ms FROM catalog_derivation_failures f LEFT JOIN catalog_entities e ON e.id=f.entity_id AND f.domain NOT IN(26,28,29) ORDER BY f.failed_ms DESC,f.domain,f.entity_id LIMIT 101`)
		if err != nil {
			return out, err
		}
		defer rows.Close()
		for rows.Next() {
			var failure derivationFailure
			if err = rows.Scan(&failure.Domain, &failure.EntityID, &failure.Title, &failure.Reason, &failure.Attempts, &failure.FailedAt); err != nil {
				return out, err
			}
			out.Failures = append(out.Failures, failure)
		}
		if err = rows.Err(); err != nil {
			return out, err
		}
		if len(out.Failures) > 100 {
			out.Failures = out.Failures[:100]
			out.More = true
		}
		return out, nil
	}})
	if err != nil {
		panic(err)
	}
}

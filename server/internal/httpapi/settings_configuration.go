package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func registerSettingsConfiguration(registry *apikit.Registry, d Dependencies) error {
	begin := conversionSettingsAdmission(d)
	return apikit.Register(registry, apikit.Route[struct{}, ConsoleReply[operations.SettingsConfiguration]]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_settings_configuration", Method: "GET", Path: "/v1/admin/settings/configuration", Summary: "Read settings values, defaults and configuration sources", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: conversionSettingsFailures()}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (ConsoleReply[operations.SettingsConfiguration], error) {
		ctx, p, auth, done, err := begin(ctx, r, true)
		if err != nil {
			return ConsoleReply[operations.SettingsConfiguration]{}, err
		}
		defer done()
		out, err := d.Console.Configuration(ctx, auth)
		if err != nil {
			return ConsoleReply[operations.SettingsConfiguration]{}, conversionSettingsError(err)
		}
		if err = d.ConsoleCheck(ctx, auth); err != nil {
			return ConsoleReply[operations.SettingsConfiguration]{}, conversionSettingsError(err)
		}
		return consoleTypedReply(d, p, out), nil
	}})
}

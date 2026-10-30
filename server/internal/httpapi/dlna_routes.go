package httpapi

import (
	"context"
	"net/http"
	"os"

	"github.com/PorticoMediaServer/portico/server/internal/buildinfo"
	"github.com/PorticoMediaServer/portico/server/internal/dlna"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// dlnaRoutes installs the DLNA server. Its routes carry no credential: the
// dlna package admits a request only while the owner's switch is on and the
// socket peer is on the home network. Media bodies get their own pattern so
// they run in the media-body lane, without the default lane's request budget.
func (d Dependencies) dlnaRoutes(mux *http.ServeMux) {
	server := &dlna.Server{DB: d.DB, Storage: d.Storage, Version: buildinfo.Info()["version"], Settings: d.dlnaSettings}
	if d.Identity != nil {
		server.ServerID, server.Name = d.Identity.ID, d.Identity.Name
	}
	if d.Metadata != nil {
		server.Artwork = func(ctx context.Context, namespace, id, role string) (*os.File, string, error) {
			return d.Metadata.EntityArtworkVariant(ctx, metadata.RepairTarget{Kind: namespace, ID: id}, role, "", metadata.ArtworkSmall, "")
		}
	}
	mux.Handle(dlna.Prefix, server)
	mux.Handle(dlna.Prefix+"media/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := withRollingDeadline(w)
		defer body.release()
		server.ServeHTTP(body, r)
	}))
}

// dlnaSettings is the cached switch and home networks; without a console
// DLNA is off.
func (d Dependencies) dlnaSettings(ctx context.Context) (dlna.Settings, error) {
	setting, err := d.connectionSetting(ctx)
	if err != nil || setting == nil {
		return dlna.Settings{}, err
	}
	return dlna.Settings{Enabled: setting.dlna, Home: setting.locality.lan}, nil
}

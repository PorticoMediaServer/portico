package httpapi

import (
	"context"
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"time"
)

type ConnectionUsage struct {
	Ordinary       int   `json:"ordinary"`
	Total          int64 `json:"total"`
	OrdinaryLimit  int   `json:"ordinaryLimit"`
	ConsoleReserve int   `json:"consoleReserve"`
}
type StorageUsage struct {
	Measured     bool  `json:"measured"`
	TrackedBytes int64 `json:"trackedBytes"`
	LimitBytes   int64 `json:"limitBytes"`
}
type ResourceUsage struct {
	Connections           *ConnectionUsage `json:"connections"`
	LocalArtwork          StorageUsage     `json:"localArtwork"`
	Analysis              StorageUsage     `json:"analysis"`
	Streams               int              `json:"streams"`
	StreamLimit           *int             `json:"streamLimit"`
	SubtitleTracksPerFile int              `json:"subtitleTracksPerFile"`
	SubtitleFileLimitMiB  int              `json:"subtitleFileLimitMiB"`
}

func registerResourceUsage(registry *apikit.Registry, d Dependencies) error {
	return apikit.Register(registry, apikit.Route[struct{}, ResourceUsage]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_admin_resource_usage", Method: "GET", Path: "/v1/admin/resource-usage", Summary: "Read current resource usage and owner limits", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: []string{"unauthorized", "not_permitted", "internal"}}, Handler: func(ctx context.Context, r *http.Request, _ struct{}) (ResourceUsage, error) {
		out := ResourceUsage{}
		p, err := d.ownerContext(ctx, r)
		if err != nil {
			return out, foundationAuthError(err)
		}
		if d.Console == nil || d.DB == nil {
			return out, &apierror.Error{Code: "internal"}
		}
		auth := d.consoleAuthority(p, true)
		document, err := d.Console.Settings(ctx, auth)
		if err != nil {
			return out, conversionSettingsError(err)
		}
		settings := document.Effective
		out.Analysis.Measured = true
		out.LocalArtwork.LimitBytes = metadata.LocalArtworkLimit(ctx, d.DB)
		if d.LocalArtworkUsage != nil {
			out.LocalArtwork.TrackedBytes, out.LocalArtwork.Measured = d.LocalArtworkUsage()
		}
		out.Analysis.LimitBytes = int64(settings.AnalysisStorageGiB) << 30
		out.StreamLimit = settings.ServerCap
		out.SubtitleFileLimitMiB = settings.SubtitleFileLimitMiB
		if d.ConnectionUsage != nil {
			value := d.ConnectionUsage()
			out.Connections = &value
		}
		err = dbwork.WithReadSnapshot(ctx, d.DB, func(ctx context.Context) error {
			if err := dbwork.QueryRow(ctx, d.DB, `SELECT COALESCE(SUM(size),0) FROM (SELECT MAX(size) AS size FROM analysis_artifacts GROUP BY digest)`).Scan(&out.Analysis.TrackedBytes); err != nil {
				return err
			}
			if err := dbwork.QueryRow(ctx, d.DB, `SELECT COUNT(*) FROM playback_v1_sessions WHERE ended_ms=0 AND lease_expires_ms>?`, time.Now().UnixMilli()).Scan(&out.Streams); err != nil {
				return err
			}
			return dbwork.QueryRow(ctx, d.DB, `SELECT COALESCE(MAX(tracks),0) FROM (SELECT COUNT(*) AS tracks FROM asset_subtitles GROUP BY asset_id)`).Scan(&out.SubtitleTracksPerFile)
		})
		if err != nil {
			return out, &apierror.Error{Code: "internal", Cause: err}
		}
		if err := d.ConsoleCheck(ctx, auth); err != nil {
			return out, conversionSettingsError(err)
		}
		return out, nil
	}})
}

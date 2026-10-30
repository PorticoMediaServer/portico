package mediaanalysis

import (
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

// SonicStatusView is the narrow owner projection. It exposes neither raw work
// policy rows nor paths/errors, and never treats installed bytes as a song.
// HTTP authentication/registration belongs at the normal owner boundary.
type SonicStatusView struct {
	LibraryID string `json:"libraryId"`
	// Available reports verified runtime readiness independently of the library choice.
	Available                 bool            `json:"available"`
	DefaultEnabled            bool            `json:"defaultEnabled"`
	Enabled                   bool            `json:"enabled"`
	Revision                  int64           `json:"revision"`
	PolicyAllows              bool            `json:"policyAllows"`
	State                     string          `json:"state"`
	Done                      int64           `json:"done"`
	Total                     int64           `json:"total"`
	Model                     *sonic.Identity `json:"model,omitempty"`
	ReasonCode                string          `json:"reasonCode,omitempty"`
	PolicyRevision            int64           `json:"policyRevision"`
	ObservedAtMS              int64           `json:"observedAtMs"`
	PausedUntilMS             *int64          `json:"pausedUntilMs,omitempty"`
	EstimatedRemainingSeconds *int64          `json:"estimatedRemainingSeconds,omitempty"`
}

func (status SonicStatus) OwnerView(library string) SonicStatusView {
	out := SonicStatusView{LibraryID: library, Available: status.Runtime.State == "ready", DefaultEnabled: catalog.SonicDefaultEnabled(), Enabled: status.Settings.Enabled, Revision: status.Settings.Revision,
		PolicyAllows: status.PolicyAllows, Done: status.Analyzed, Total: status.Total, Model: status.Runtime.Model,
		PolicyRevision: status.Work.Revision, ObservedAtMS: status.Work.ObservedAt.UnixMilli()}
	switch {
	case !status.Settings.Enabled:
		out.State = "disabled"
	case !status.CanAnalyze:
		out.State = "unavailable"
		// Only bounded server codes leave the domain. No loader diagnostic or
		// source URL can become the row's text through this projection.
		switch status.Runtime.Reason {
		case "inference_failed", "inference_not_verified", "runtime_or_model_missing":
			out.ReasonCode = status.Runtime.Reason
		default:
			out.ReasonCode = "runtime_or_model_missing"
		}
	case !status.PolicyAllows:
		out.State = "disabled"
		out.ReasonCode = "scan_policy_disallows"
	case status.Analyzed == status.Total:
		out.State = "complete"
	case !status.Work.WindowOpen || status.Work.PausedUntil > status.Work.ObservedAt.UnixMilli():
		out.State = "paused"
		if next := status.Work.NextAllowedAt; next != nil {
			ms := next.UnixMilli()
			out.PausedUntilMS = &ms
		}
	default:
		out.State = "analyzing"
		out.EstimatedRemainingSeconds = status.EstimatedRemainingSeconds
	}
	return out
}

package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	identity "github.com/PorticoMediaServer/portico/server/internal/identity"
	networking "github.com/PorticoMediaServer/portico/server/internal/networking"
)

func registerSetupHTTPContracts(registry *apikit.Registry) error {
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct{}, struct {
		Alive bool `json:"alive"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_health_live", Method: "GET", Path: "/health/live", Summary: "Read server liveness", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:32
	if err := apikit.RegisterHTTP[struct{}, struct {
		Ready bool `json:"ready"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_health_ready", Method: "GET", Path: "/health/ready", ResponseStatuses: []int{503}, Summary: "Read server readiness", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:51
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_subtitle_fonts_cjk", Method: "GET", Path: "/v1/subtitle-fonts/cjk", Summary: "Read the Chinese, Japanese and Korean font for subtitles drawn in a browser", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Media, Admission: laneMediaBody, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "font/otf", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/onboarding.go:47
	if err := apikit.RegisterHTTP[struct {
		RequestID  string "json:\"requestId\""
		SetupToken string "json:\"setupToken\""
	}, identity.SetupResult](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_setup_resume", Method: "POST", Path: "/v1/setup/resume", Summary: "Run setup resume", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/onboarding.go:68
	if err := apikit.RegisterHTTP[struct{}, identity.OnboardingState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_setup_state", Method: "GET", Path: "/v1/setup/state", Summary: "Read setup state", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/onboarding.go:113
	if err := apikit.RegisterHTTP[identity.QuickStart, identity.QuickAuthorization](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_quick_connect", Method: "POST", Path: "/v1/quick-connect", Summary: "Run quick connect", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 201, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/onboarding.go:139
	if err := apikit.RegisterHTTP[struct {
		UserCode string "json:\"userCode\""
	}, identity.QuickPreview](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_quick_connect_review", Method: "POST", Path: "/v1/quick-connect/review", Summary: "Run quick connect review", Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/onboarding.go:166
	if err := apikit.RegisterHTTP[struct {
		RequestID string "json:\"requestId\""
		UserCode  string "json:\"userCode\""
		Decision  string "json:\"decision\""
	}, struct{}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_quick_connect_decision", Method: "POST", Path: "/v1/quick-connect/decision", Summary: "Run quick connect decision", Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/topshelf.go:69
	if err := apikit.RegisterHTTP[struct {
		InstallationID string "json:\"installationId\""
	}, TopShelfToken](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_topshelf_token", Method: "POST", Path: "/v1/topshelf/token", Summary: "Run topshelf token", Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 201, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/topshelf.go:90
	if err := apikit.RegisterHTTP[struct{}, TopShelfFeed](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_topshelf", Method: "GET", Path: "/v1/topshelf", Summary: "Read topshelf", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/topshelf.go:136
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_topshelf_art_id", Method: "GET", Path: "/v1/topshelf/art/{id}", Summary: "Read topshelf art", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Media, Admission: laneMedia, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"grant"}, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		Revision int64 `json:"revision,string"`
	}, identity.OnboardingState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_setup_finish", Method: "POST", Path: "/v1/setup/finish", Summary: "Run setup finish", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28

	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		DeviceCode string `json:"deviceCode"`
	}, identity.Envelope](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_quick_connect_token", Method: "POST", Path: "/v1/quick-connect/token", Summary: "Run quick connect token", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		DeviceCode string `json:"deviceCode"`
	}, struct{}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_quick_connect_cancel", Method: "POST", Path: "/v1/quick-connect/cancel", Summary: "Run quick connect cancel", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct{}, networking.CertificateStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_networking_certificate", Method: "GET", Path: "/v1/networking/certificate", Summary: "Read network certificate", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		AuthorityID string                       `json:"authorityId"`
		Config      networking.CertificateConfig `json:"config"`
	}, networking.CertificateStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_certificate_config", Method: "POST", Path: "/v1/networking/certificate/config", Summary: "Run network certificate config", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct{}, networking.CertificateStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_certificate_retry", Method: "POST", Path: "/v1/networking/certificate/retry", Summary: "Run network certificate retry", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct{}, networking.ClaimStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_networking_claim", Method: "GET", Path: "/v1/networking/claim", Summary: "Read network claim", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		Expected networking.ClaimExpected `json:"expected"`
	}, networking.ClaimStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_claim_await_approval", Method: "POST", Path: "/v1/networking/claim/await-approval", Summary: "Run network claim await approval", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Realtime, Admission: laneRealtime, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		Expected networking.ClaimExpected `json:"expected"`
		BaseURL  string                   `json:"baseUrl"`
	}, networking.EndpointReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_claim_endpoint", Method: "POST", Path: "/v1/networking/claim/endpoint", Summary: "Run network claim endpoint", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[networking.ClaimActionRequest, networking.ClaimStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_claim_web_approval", Method: "POST", Path: "/v1/networking/claim/web-approval", Summary: "Run network claim web approval", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Headers: []string{"X-Portico-Installation-Id"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		Kind                 string `json:"kind"`
		ServerID             string `json:"serverId"`
		AccountID            string `json:"accountId"`
		OperationID          string `json:"operationId"`
		ClaimGeneration      string `json:"claimGeneration"`
		CredentialGeneration string `json:"credentialGeneration"`
		BaseURL              string `json:"baseUrl"`
		RegistrationID       string `json:"registrationId"`
		Nonce                string `json:"nonce"`
		ExpiresAt            string `json:"expiresAt"`
	}, struct {
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_endpoint_proof", Method: "POST", Path: "/v1/networking/endpoint-proof", Summary: "Run network endpoint proof", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct{}, networking.RemoteStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_networking_remote", Method: "GET", Path: "/v1/networking/remote", Summary: "Read network remote", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct{}, networking.RemoteStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_remote_check", Method: "POST", Path: "/v1/networking/remote/check", Summary: "Run network remote check", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		AuthorityID string                  `json:"authorityId"`
		Config      networking.RemoteConfig `json:"config"`
	}, networking.RemoteStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_remote_config", Method: "POST", Path: "/v1/networking/remote/config", Summary: "Run network remote config", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[networking.ClaimPrepareRequest, networking.ClaimStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_claim_prepare", Method: "POST", Path: "/v1/networking/claim/prepare", Summary: "Run network claim prepare", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "claim_move_required", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		Expected         networking.ClaimExpected `json:"expected"`
		ApprovalEnvelope string                   `json:"approvalEnvelope,omitempty"`
	}, networking.ClaimStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_claim_approve", Method: "POST", Path: "/v1/networking/claim/approve", Summary: "Run network claim approve", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		Expected         networking.ClaimExpected `json:"expected"`
		ApprovalEnvelope string                   `json:"approvalEnvelope,omitempty"`
	}, networking.ClaimStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_claim_continue", Method: "POST", Path: "/v1/networking/claim/continue", Summary: "Run network claim continue", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/installation.go:28
	if err := apikit.RegisterHTTP[struct {
		Expected         networking.ClaimExpected `json:"expected"`
		ApprovalEnvelope string                   `json:"approvalEnvelope,omitempty"`
	}, networking.ClaimStatus](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_networking_claim_cancel", Method: "POST", Path: "/v1/networking/claim/cancel", Summary: "Run network claim cancel", Access: apikit.Owner, Requires: apikit.ScopeSession, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	return nil
}

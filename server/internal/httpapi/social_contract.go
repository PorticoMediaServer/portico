package httpapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/social"
)

// Social handlers keep their bounded protocol envelopes while the registry owns
// their routes, authorization and generated request/response contracts.
func socialContract[Req, Resp any](r *apikit.Registry, pattern string, h http.HandlerFunc, status int) {
	parts := strings.SplitN(pattern, " ", 2)
	lane := apikit.Default
	if parts[0] == "GET" {
		lane = apikit.Browsing
	}
	m := apikit.Metadata{Audience: apikit.Clients, ID: "social_" + strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(strings.ToLower(parts[0])+parts[1]), Method: parts[0], Path: parts[1], Summary: "Manage shared playback for the signed-in profile", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: lane, Cost: apikit.PageSized, BodyLimit: 256 << 10, Status: status, Errors: socialErrorCodes()}
	if reflect.TypeFor[Req]() == reflect.TypeFor[noBody]() {
		m.BodyLimit = 0
	}
	if parts[1] == "/v1/cast/preview" || parts[1] == "/v1/cast/redeem" || parts[1] == "/v1/cast/reconnect" {
		m.Access = apikit.Public
		m.Lane = apikit.Security
	}
	if parts[0] == "PUT" && parts[1] == "/v1/cast/configuration" {
		m.Access = apikit.Owner
		m.Cost = apikit.Constant
	}
	if strings.HasSuffix(parts[1], "/events") {
		m.SSE = true
		m.Lane = apikit.Realtime
	}
	if strings.HasSuffix(parts[1], "/inbox/wait") {
		m.Query = []string{"waitMs"}
		m.Lane = apikit.Realtime
	}
	if parts[0] == "GET" {
		m.Requires = apikit.ScopeRead
	}
	switch parts[1] {
	case "/v1/cast/bootstrap", "/v1/cast/bootstrap/{id}", "/v1/cast/reconnect", "/v1/cast/redeem", "/v1/cast/preview":
		// Pairing a screen issues it a session: a credential never mints another.
		m.Requires = apikit.ScopeSession
	case "/v1/cast/devices/{id}":
		// Forgetting a paired screen is ordinary account work.
		m.Requires = apikit.ScopeAdmin
	case "/v1/cast/configuration":
		if parts[0] == "PUT" {
			m.Requires = apikit.ScopeAdmin
		}
	}
	m.Admission = laneDefault
	if parts[0] == "GET" {
		m.Admission = laneBrowsing
	}
	if m.Lane == apikit.Realtime {
		m.Admission = laneRealtime
	}
	if e := apikit.RegisterRaw(r, apikit.RawRoute[Req, Resp]{Metadata: m, Serve: func(w http.ResponseWriter, q *http.Request) { w.Header().Set("Cache-Control", "no-store"); h(w, q) }}); e != nil {
		panic(e)
	}
}

// Frames have an SSE ordinal and event name; data is the event payload. The
// group event endpoint serves only text/event-stream.
type groupStreamFrame struct {
	ID    string          `json:"id"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}
type groupStreamContract struct {
	Events []groupStreamFrame `json:"events"`
}

type receiverHeartbeatBody struct {
	ProtocolVersion string `json:"protocolVersion"`
	KeyFingerprint  string `json:"keyFingerprint"`
}
type castConfiguration struct {
	ProtocolVersion string `json:"protocolVersion"`
	ApplicationID   string `json:"applicationId"`
	ReceiverURL     string `json:"receiverUrl"`
}
type castConfigurationBody struct {
	ProtocolVersion string `json:"protocolVersion"`
	ApplicationID   string `json:"applicationId"`
}

func registerSocial(r *apikit.Registry, d Dependencies) {
	store := d.Social()
	viewer := func(w http.ResponseWriter, q *http.Request) (identity.Principal, bool) {
		p, e := d.principal(q)
		if e != nil {
			socialFailure(w, e)
			return p, false
		}
		return p, true
	}
	handle := func(pattern string, h http.HandlerFunc) {
		switch pattern {
		case "POST /v1/groups":
			socialContract[social.CreateGroupRequest, social.GroupResponse](r, pattern, h, 201)
		case "GET /v1/groups":
			socialContract[noBody, social.GroupListResponse](r, pattern, h, 200)
		case "POST /v1/groups/join":
			socialContract[social.JoinRequest, social.GroupResponse](r, pattern, h, 200)
		case "GET /v1/groups/{id}":
			socialContract[noBody, social.GroupResponse](r, pattern, h, 200)
		case "POST /v1/groups/{id}/heartbeat", "POST /v1/groups/{id}/leave":
			socialContract[noBody, social.GroupResponse](r, pattern, h, 200)
		case "POST /v1/groups/{id}/end":
			socialContract[social.EndRequest, social.GroupResponse](r, pattern, h, 200)
		case "POST /v1/groups/{id}/host-transfer":
			socialContract[social.TransferRequest, social.GroupResponse](r, pattern, h, 200)
		case "POST /v1/groups/{id}/invites":
			socialContract[social.InviteRequest, social.InviteResponse](r, pattern, h, 201)
		case "DELETE /v1/groups/{id}/invites/{inviteId}":
			socialContract[noBody, noBody](r, pattern, h, 204)
		case "GET /v1/groups/{id}/events":
			socialContract[noBody, groupStreamContract](r, pattern, h, 200)
		case "POST /v1/groups/{id}/transport":
			socialContract[social.TransportRequest, social.TransportReceipt](r, pattern, h, 200)
		case "POST /v1/groups/{id}/settings":
			socialContract[social.SettingsRequest, social.GroupResponse](r, pattern, h, 200)
		case "POST /v1/groups/{id}/readiness":
			socialContract[social.ReadinessRequest, social.ReadinessReceipt](r, pattern, h, 200)
		case "GET /v1/groups/{id}/queue":
			socialContract[noBody, social.QueueResponse](r, pattern, h, 200)
		case "POST /v1/groups/{id}/queue":
			socialContract[social.QueueRequest, social.QueueResponse](r, pattern, h, 200)
		case "GET /v1/receivers":
			socialContract[noBody, social.ReceiverListResponse](r, pattern, h, 200)
		case "POST /v1/receivers/{id}/heartbeat":
			socialContract[receiverHeartbeatBody, social.ReceiverResponse](r, pattern, h, 200)
		case "GET /v1/receivers/{id}/inbox", "GET /v1/receivers/{id}/inbox/wait":
			socialContract[noBody, social.ReceiverInbox](r, pattern, h, 200)
		case "DELETE /v1/receivers/{id}":
			socialContract[noBody, noBody](r, pattern, h, 204)
		case "GET /v1/receivers/{id}/grants":
			socialContract[noBody, social.GrantListResponse](r, pattern, h, 200)
		case "POST /v1/receivers":
			socialContract[social.ReceiverRequest, social.ReceiverResponse](r, pattern, h, 201)
		case "POST /v1/receivers/{id}/grants":
			socialContract[social.GrantRequest, social.GrantResponse](r, pattern, h, 201)
		case "POST /v1/receivers/{id}/grants/{grantId}/decision":
			socialContract[social.GrantDecision, social.GrantResponse](r, pattern, h, 200)
		case "GET /v1/handoffs/{id}":
			socialContract[noBody, social.HandoffResponse](r, pattern, h, 200)
		case "POST /v1/handoffs/{id}/rollback":
			socialContract[social.HandoffRollback, social.HandoffResponse](r, pattern, h, 200)
		case "POST /v1/handoffs":
			socialContract[social.HandoffRequest, social.HandoffResponse](r, pattern, h, 201)
		case "POST /v1/handoffs/{id}/readiness":
			socialContract[social.HandoffReadiness, social.HandoffResponse](r, pattern, h, 200)
		case "POST /v1/handoffs/{id}/commit":
			socialContract[social.HandoffCommit, social.HandoffResponse](r, pattern, h, 200)
		case "POST /v1/cast/controllers/register":
			socialContract[social.CastControllerRegister, social.CastControllerResponse](r, pattern, h, 200)
		case "POST /v1/cast/controllers/validate":
			socialContract[social.CastControllerValidation, social.CastControllerResponse](r, pattern, h, 200)
		case "POST /v1/cast/controllers/check":
			socialContract[social.CastControllerCheck, social.CastControllerResponse](r, pattern, h, 200)
		case "POST /v1/cast/bootstrap":
			socialContract[social.CastBootstrapRequest, social.CastBootstrapResponse](r, pattern, h, 201)
		case "GET /v1/cast/bootstrap/{id}", "DELETE /v1/cast/bootstrap/{id}":
			socialContract[noBody, social.CastBootstrapResponse](r, pattern, h, 200)
		case "POST /v1/cast/preview":
			socialContract[social.CastPreviewRequest, social.CastPreviewResponse](r, pattern, h, 200)
		case "POST /v1/cast/redeem":
			socialContract[social.CastRedeemRequest, social.CastReceiverSession](r, pattern, h, 200)
		case "POST /v1/cast/reconnect":
			socialContract[social.CastReconnectRequest, social.CastReceiverSession](r, pattern, h, 200)
		case "DELETE /v1/cast/devices/{id}":
			socialContract[noBody, noBody](r, pattern, h, 204)
		case "GET /v1/cast/configuration":
			socialContract[noBody, castConfiguration](r, pattern, h, 200)
		case "PUT /v1/cast/configuration":
			socialContract[castConfigurationBody, castConfiguration](r, pattern, h, 200)
		default:
			panic("unregistered social route: " + pattern)
		}
	}
	d.groupRoutes(handle, viewer, store)
	d.receiverRoutes(handle, viewer, store)
	d.castRoutes(handle, viewer, store)
}

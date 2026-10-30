package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

func (d Dependencies) compatibilityHierarchy(w http.ResponseWriter, r *http.Request, q url.Values, mode string) {
	show, err := d.compatibilityNativeID(r, "item", r.PathValue("id"))
	if err != nil {
		compatibilityFailure(w, &apierror.Error{Code: "not_found"})
		return
	}
	query := url.Values{"limit": {"100"}}
	if fields := strings.ToLower(q.Get("fields")); strings.Contains(fields, "mediasources") || strings.Contains(fields, "path") {
		query.Set("includeFiles", "true")
	}
	if season := q.Get("season"); season != "" {
		query.Set("season", season)
	}
	if season := q.Get("seasonid"); season != "" {
		id, e := d.compatibilityNativeID(r, "item", season)
		if e != nil {
			compatibilityFailure(w, &apierror.Error{Code: "not_found"})
			return
		}
		query.Set("seasonId", id)
	}
	start, err := compatibilityInteger(q, "startindex", 0, 5000000)
	if err != nil {
		compatibilityFailure(w, err)
		return
	}
	limit, err := compatibilityInteger(q, "limit", 0, 5000000)
	if err != nil {
		compatibilityFailure(w, err)
		return
	}
	d.compatibilityStream(w, r, q, query, "/v1/integrations/shows/"+url.PathEscape(show)+"/"+mode, false, start, limit, q.Get("limit") != "", func(row map[string]any) (any, error) {
		if row["kind"] == nil {
			if mode == "seasons" {
				row["kind"] = "season"
			} else {
				row["kind"] = "episode"
			}
		}
		if row["title"] == nil {
			row["title"] = row["name"]
		}
		return d.compatibilityItem(r, row)
	})
}
func (d Dependencies) compatibilitySessions(w http.ResponseWriter, r *http.Request) {
	d.compatibilityStream(w, r, nil, url.Values{"limit": {"100"}}, "/v1/integrations/sessions", true, 0, 0, false, func(row map[string]any) (any, error) {
		nativeID := compatText(row["id"])
		if nativeID == "" {
			nativeID = compatText(row["sessionId"])
		}
		guid, err := d.compatibilityID(r, "session", nativeID)
		if err != nil {
			return nil, err
		}
		device := compatObject(row["device"])
		profile := compatObject(row["profile"])
		user := compatObject(row["user"])
		username := compatText(user["name"])
		if username == "" {
			username = compatText(row["username"])
		}
		profileID := compatText(profile["id"])
		if profileID == "" {
			profileID = compatText(row["profileId"])
		}
		userGUID := ""
		if profileID != "" {
			userGUID, err = d.compatibilityID(r, "user", profileID)
			if err != nil {
				return nil, err
			}
		}
		state := compatText(row["state"])
		playState := map[string]any{"PositionTicks": int64(compatNumber(row["positionMs"]) * 10000), "IsPaused": state == "paused", "CanSeek": false}
		supported := []string{}
		if actions, ok := row["actions"].([]any); ok {
			for _, a := range actions {
				if a == "seek" {
					playState["CanSeek"] = true
				}
				if a == "pause" || a == "resume" || a == "stop" {
					supported = append(supported, compatText(a))
				}
			}
		}
		out := map[string]any{"Id": guid, "DeviceId": device["id"], "DeviceName": device["name"], "Client": row["app"], "UserId": userGUID, "UserName": username, "PlayState": playState, "SupportsMediaControl": len(supported) > 0, "SupportsRemoteControl": len(supported) > 0, "SupportedCommands": []string{}}
		if out["Client"] == nil {
			out["Client"] = "Portico"
		}
		if item := compatObject(row["item"]); len(item) > 0 {
			if item["kind"] == nil {
				item["kind"] = row["kind"]
			}
			if item["title"] == nil {
				item["title"] = item["name"]
			}
			translated, e := d.compatibilityItem(r, item)
			if e != nil {
				return nil, e
			}
			translated["RunTimeTicks"] = int64(compatNumber(row["durationMs"]) * 10000)
			out["NowPlayingItem"] = translated
		}
		if delivery := compatObject(row["delivery"]); len(delivery) > 0 {
			playState["PlayMethod"] = delivery["method"]
		}
		return out, nil
	})
}
func (d Dependencies) compatibilityRefresh(r *http.Request, q url.Values) (any, error) {
	// ValidationOnly is Bazarr's sidecar refresh; it never authorizes metadata
	// enrichment or replacing art. Unsupported modes fail instead of broadening.
	if mode := strings.ToLower(q.Get("metadatarefreshmode")); mode != "validationonly" && mode != "" {
		return nil, &apierror.Error{Code: "invalid_request"}
	}
	if q.Get("replaceallmetadata") == "true" || q.Get("replaceallimages") == "true" || (q.Get("imagerefreshmode") != "" && !strings.EqualFold(q.Get("imagerefreshmode"), "none")) {
		return nil, &apierror.Error{Code: "invalid_request"}
	}
	id, err := d.compatibilityNativeID(r, "item", r.PathValue("id"))
	if err != nil {
		return nil, &apierror.Error{Code: "not_found"}
	}
	return d.integrationCall(r, "POST", "/v1/integrations/items/"+url.PathEscape(id)+"/subtitles/refresh", nil, map[string]any{})
}
func (d Dependencies) compatibilityControl(r *http.Request, q url.Values) (any, error) {
	id, err := d.compatibilityNativeID(r, "session", r.PathValue("id"))
	if err != nil {
		return nil, &apierror.Error{Code: "not_found"}
	}
	cmd := strings.ToLower(r.PathValue("command"))
	if cmd != "pause" && cmd != "unpause" && cmd != "playpause" && cmd != "stop" && cmd != "seek" {
		return nil, &apierror.Error{Code: "invalid_request"}
	}
	if cmd == "stop" {
		_, e := d.integrationCall(r, "POST", "/v1/playback/controls/"+url.PathEscape(id)+":stop", nil, map[string]any{})
		if e == nil {
			return map[string]any{}, nil
		}
		refusal, ok := e.(*apierror.Error)
		if !ok || (refusal.Code != "not_found" && refusal.Code != "not_permitted") {
			return nil, e
		}
		_, e = d.integrationCall(r, "POST", "/v1/admin/sessions/"+url.PathEscape(id)+":terminate", nil, map[string]any{})
		return map[string]any{}, e
	}
	path := "/v1/playback/sessions/" + url.PathEscape(id)
	current, err := d.integrationCall(r, "GET", path, nil, nil)
	admin := false
	if err != nil {
		current, err = d.integrationCall(r, "GET", "/v1/admin/sessions/"+url.PathEscape(id)+"/controls", nil, nil)
		admin = true
	}
	if err != nil {
		return nil, err
	}

	control := compatObject(current["control"])
	revision := compatText(current["revision"])
	state := compatText(control["state"])
	if admin {
		revision = compatText(current["revision"])
		state = compatText(current["state"])
	}
	action := "pause"
	body := map[string]any{"state": "paused"}
	if cmd == "unpause" || cmd == "playpause" && state == "paused" {
		action = "resume"
		body["state"] = "playing"
	}
	if cmd == "seek" {
		ticks, e := strconv.ParseInt(q.Get("seekpositionticks"), 10, 64)
		if e != nil || ticks < 0 {
			return nil, &apierror.Error{Code: "invalid_request"}
		}
		action = "seek"
		body = map[string]any{"seek": map[string]any{"positionMs": ticks / 10000, "id": "integration-" + revision}}
	}
	if admin {
		request := map[string]any{"action": action, "expectedRevision": revision}
		if action == "seek" {
			request["positionMs"] = compatObject(body["seek"])["positionMs"]
		}
		return d.integrationCall(r, "POST", "/v1/admin/sessions/"+url.PathEscape(id)+"/controls", nil, request)
	}
	status, _, raw, err := d.mcpInvoke(r)(mcpCall{Method: "PATCH", Path: path, Body: body, Header: map[string]string{"If-Match": revision}})
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		var response struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &response)
		if response.Error.Code == "" {
			response.Error.Code = "internal"
		}
		return nil, &apierror.Error{Code: response.Error.Code}
	}
	// The foreign command returns no native session/media/delivery data.
	return map[string]any{}, nil
}
func (d Dependencies) compatibilityImage(w http.ResponseWriter, r *http.Request, q url.Values, mode string) {
	kind := "item"
	path := ""
	if mode == "user_image" {
		kind = "user"
	}
	id, err := d.compatibilityNativeID(r, kind, r.PathValue("id"))
	if err != nil {
		compatibilityFailure(w, &apierror.Error{Code: "not_found"})
		return
	}
	query := url.Values{}
	if kind == "user" {
		path = "/v1/profiles/" + url.PathEscape(id) + "/avatar"
	} else {
		art := map[string]string{"primary": "poster", "backdrop": "backdrop"}[strings.ToLower(r.PathValue("type"))]
		if art == "" || (r.PathValue("index") != "" && r.PathValue("index") != "0") {
			compatibilityFailure(w, &apierror.Error{Code: "not_found"})
			return
		}
		path = "/v1/items/" + url.PathEscape(id) + "/art/" + art
		query.Set("size", "small")
		if q.Get("fillwidth") != "" || q.Get("maxwidth") != "" {
			width := q.Get("maxwidth")
			if width == "" {
				width = q.Get("fillwidth")
			}
			if n, e := strconv.Atoi(width); e == nil && n > 480 {
				query.Set("size", "large")
			}
		}
	}
	// A same-process native request preserves all original authority and streams
	// only the permitted art. No client-supplied URL is opened or redirected.
	inner := r.Clone(withNestedCredential(r.Context()))
	u, _ := url.Parse(path)
	u.RawQuery = query.Encode()
	inner.URL = u
	inner.RequestURI = u.RequestURI()
	inner.Method = "GET"
	inner.Body = nil
	inner.ContentLength = 0
	d.self.h.ServeHTTP(w, inner)
}

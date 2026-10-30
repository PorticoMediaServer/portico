package httpapi

import "net/http"

func (d Dependencies) allowTransport(w http.ResponseWriter, r *http.Request) bool {
	setting, err := d.connectionSetting(r.Context())
	if err != nil {
		write(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]any{"code": "connection_policy_unavailable", "message": "Connection settings are unavailable.", "retryable": true}})
		return false
	}
	if setting != nil && setting.secureConnections == "required" && !d.secureTransport(r) {
		w.Header().Set("Cache-Control", "no-store")
		write(w, http.StatusUpgradeRequired, map[string]any{"error": map[string]any{"code": "secure_connection_required", "message": "A secure connection is required.", "retryable": false}})
		return false
	}
	return true
}

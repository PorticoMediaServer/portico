package httpapi

import "net/http"

// HTTP and HTTPS have identical first-party credential semantics. Credentials
// are returned only by the authorized issuance path and are never cached.
func writeAuth(w http.ResponseWriter, _ *http.Request, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	write(w, status, value)
}

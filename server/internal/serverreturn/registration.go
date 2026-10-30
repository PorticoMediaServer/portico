package serverreturn

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

// This pure leaf is also consumed by the signed claim/owner-start protocol.
// It has no Hosted trust, database, handler or generator dependencies.

// Origin accepts an exact HTTPS origin, including its registered port. ASCII
// canonical spelling prevents URL-parser normalization from widening authority.
func Origin(v string) bool {
	for _, c := range v {
		if c < 33 || c > 126 {
			return false
		}
	}
	u, e := url.Parse(v)
	return e == nil && u.Scheme == "https" && u.User == nil && u.Host != "" && u.Host == strings.ToLower(u.Host) && u.Hostname() != "" && !strings.HasSuffix(u.Host, ":") && u.Path == "" && u.RawQuery == "" && u.ForceQuery == false && u.Fragment == "" && u.Opaque == "" && u.String() == v && !strings.HasSuffix(u.Hostname(), ".")
}

func RegistrationID(server, operation string, claim, credential, generation int64, origin string) string {
	raw, _ := json.Marshal([]any{server, operation, claim, credential, generation, origin})
	hash := sha256.Sum256(raw)
	return "ret_" + base64.RawURLEncoding.EncodeToString(hash[:])
}

type Registration struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
}

type RegistrationRequest struct {
	Origin string `json:"origin"`
}

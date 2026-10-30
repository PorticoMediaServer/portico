package persistence

import (
	"encoding/base64"
	"encoding/json"
)

// PersonalOwnerKey is the persisted, lossless owner tuple in one server database.
// Shared by authenticated identity callers.
// Authorization epochs and tokens deliberately are not part of durable identity.
func PersonalOwnerKey(authority, account, profile string) string {
	raw, _ := json.Marshal([3]string{authority, account, profile})
	return "viewer:" + base64.RawURLEncoding.EncodeToString(raw)
}

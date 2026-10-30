package identity

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/apikit"
	"reflect"
)

// PendingAccountSetup is a resumable initialization, never a local owner or a
// session. Only the existing origin-bound secret authorizes its continuation.
type PendingAccountSetup struct {
	ServerID      string `json:"serverId"`
	RequestID     string `json:"requestId"`
	AuthMode      string `json:"authMode"`
	ClaimRequired bool   `json:"claimRequired"`
	Revision      int64  `json:"revision,string"`
}
type SetupResult struct {
	Envelope
	Pending *PendingAccountSetup
}

func (r SetupResult) MarshalJSON() ([]byte, error) {
	if r.Pending != nil {
		return json.Marshal(r.Pending)
	}
	return json.Marshal(r.Envelope)
}
func (SetupResult) JSONSchema() map[string]any {
	local, e := apikit.Schema(reflect.TypeFor[Envelope]())
	if e != nil {
		panic(e)
	}
	pending, e := apikit.Schema(reflect.TypeFor[PendingAccountSetup]())
	if e != nil {
		panic(e)
	}
	props := pending["properties"].(map[string]any)
	props["authMode"].(map[string]any)["enum"] = []string{"hosted"}
	props["claimRequired"].(map[string]any)["const"] = true
	return map[string]any{"oneOf": []any{local, pending}}
}

func (OwnerResetLocation) JSONSchema() map[string]any {
	type location OwnerResetLocation
	shape, e := apikit.Schema(reflect.TypeFor[location]())
	if e != nil {
		panic(e)
	}
	props := shape["properties"].(map[string]any)
	props["systemKind"].(map[string]any)["enum"] = []string{"linux", "windows", "macos", "docker"}
	props["fileName"].(map[string]any)["const"] = resetFileName
	props["retryAfterSeconds"].(map[string]any)["minimum"] = 0
	props["retryAfterSeconds"].(map[string]any)["maximum"] = 60
	return shape
}

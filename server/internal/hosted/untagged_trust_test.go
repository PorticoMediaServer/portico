//go:build !release && !devtrust

package hosted

import (
	"encoding/base64"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"testing"
)

func TestUntaggedTrustHasNoDefaultAndRejectsDevProduction(t *testing.T) {
	if _, e := trust.DefaultRoot(); e == nil {
		t.Fatal("untagged build trusted a root")
	}
	origin, pin, id, e := DefaultConfig("", "", "")
	if e != nil || origin != "" || pin != "" || id != "" {
		t.Fatal("untagged Hosted unexpectedly configured", e)
	}
	raw, _ := base64.RawURLEncoding.DecodeString("Uj7jC87DWZ0XbmIlQudnqU2O3N148wQ9eX4YgmU_Ppo")
	if _, _, _, e = DefaultConfig(trust.Origin, base64.RawURLEncoding.EncodeToString(raw), trust.KeyID(raw)); e == nil {
		t.Fatal("development root paired with production Hosted")
	}
}

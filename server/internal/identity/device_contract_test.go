package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"testing"
	"time"
)

func TestDeviceControlCharactersAndLiveFamilyContract(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	for r := rune(0); r <= 127; r++ {
		if r >= 32 && r != 127 {
			continue
		}
		t.Run(fmt.Sprint(r), func(t *testing.T) {
			bad := "Den" + string(r) + "TV"
			for field := 0; field < 4; field++ {
				q := registration(Token())
				switch field {
				case 0:
					q.Name = bad
				case 1:
					q.Platform = bad
				case 2:
					q.App = bad
				case 3:
					q.AppVersion = bad
				}
				if _, err := s.RegisterDevice(ctx, login.AccountToken, q, ""); !errors.Is(err, ErrDeviceInput) {
					t.Fatalf("field %d accepted control: %v", field, err)
				}
			}
			if _, err := s.EditDevice(ctx, login.AccountToken, login.DeviceID, DeviceEdit{Name: &bad}); !errors.Is(err, ErrDeviceInput) {
				t.Fatal("rename accepted control", err)
			}
		})
	}
	devices, err := s.Devices(ctx, login.AccountToken, "")
	if err != nil {
		t.Fatal(err)
	}
	var before int
	for _, d := range devices {
		if d.ID == login.DeviceID {
			before = d.Sessions
		}
	}
	if before < 1 {
		t.Fatal("missing live fixture family")
	}
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, err = db.Exec(`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) SELECT 'expired-family',server_id,account_id,profile_id,authority,role,epoch,?,1,0 FROM authorization_session_families WHERE id=?`, past, login.Session.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_device_families VALUES('expired-family',?)`, login.DeviceID); err != nil {
		t.Fatal(err)
	}
	devices, err = s.Devices(ctx, login.AccountToken, "")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := apispec.Documents()
	if err != nil {
		t.Fatal(err)
	}
	var doc apispec.Document
	found := false
	for _, d := range docs {
		if d.File == "portico.openapi.json" {
			doc = d
			found = true
		}
	}
	if !found {
		t.Fatal("generated API document missing")
	}
	deviceSchema := map[string]any{"$ref": "#/components/schemas/PatchDeviceResponse"}
	for _, d := range devices {
		b, _ := json.Marshal(d)
		if issues := doc.ValidateJSON(deviceSchema, b); len(issues) > 0 {
			t.Fatal(issues)
		}
		if d.ID == login.DeviceID && d.Sessions != before {
			t.Fatalf("expired family counted %+v", d)
		}
	}
}

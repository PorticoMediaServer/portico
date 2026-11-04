package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestSignInAdvancedConfigurationPublishesTheExistingPolicy(t *testing.T) {
	f := administrationFixtureNew(t)
	expected := []struct {
		id, key          string
		factory, changed any
		options          []string
	}{
		{"signin.stayLength", "signin.stay_signed_in_days", float64(90), float64(7), []string{"7", "30", "90", "365"}},
		{"signin.pinRemember", "signin.pin_remember_days", float64(30), float64(0), []string{"0", "7", "30", "90"}},
		{"signin.passwordAttempts", "signin.password_attempts", "standard", "strict", []string{"relaxed", "standard", "strict"}},
		{"signin.pinAttempts", "signin.pin_attempts", "standard", "relaxed", []string{"relaxed", "standard", "strict"}},
		{"signin.passwordLength", "signin.password_min_length", float64(8), float64(12), []string{"8", "10", "12", "16"}},
		{"signin.resetCodeLife", "signin.member_reset_code_minutes", float64(60), float64(15), []string{"15", "60", "1440"}},
		{"signin.requireTwoStep", "signin.require_two_step", "nobody", "everyone", []string{"nobody", "administrators", "everyone"}},
		{"signin.rememberAccounts", "signin.remember_accounts", true, false, nil},
		{"signin.endedKeep", "signin.ended_sessions_days", float64(90), float64(30), []string{"30", "90", "365"}},
	}
	var expectedPolicyRevision int64 = 1
	read := func(changed bool) {
		t.Helper()
		w := f.call("GET", "/v1/admin/settings/configuration", f.owner, "")
		var reply ConsoleReply[operations.SettingsConfiguration]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil {
			t.Fatalf("configuration: %d %s", w.Code, w.Body)
		}
		if reply.Data.SignInPolicyRevision != expectedPolicyRevision {
			t.Fatalf("policy revision = %d, want %d", reply.Data.SignInPolicyRevision, expectedPolicyRevision)
		}
		byKey := map[string]operations.ConfigurationValue{}
		for _, field := range reply.Data.Fields {
			byKey[field.Key] = field
		}
		for _, row := range expected {
			field, ok := byKey[row.key]
			if !ok {
				t.Fatalf("configuration omitted %s", row.key)
			}
			want, source := row.factory, "default"
			if changed {
				want, source = row.changed, "server"
			}
			if field.ID != row.id || field.Current == nil || field.Effective == nil || field.Default == nil || *field.Current != want || *field.Effective != want || *field.Default != row.factory || field.Source != source || field.ConfigOnly {
				t.Fatalf("%s: %+v, want current=%v default=%v source=%s", row.key, field, want, row.factory, source)
			}
		}
	}
	read(false)
	w := f.call("GET", "/v1/console/registry", f.owner, "")
	var registry ConsoleReply[ConsoleRegistryDocument]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &registry) != nil {
		t.Fatalf("registry: %d %s", w.Code, w.Body)
	}
	byID := map[string]operations.Field{}
	for _, field := range registry.Data.Fields {
		byID[field.ID] = field
	}
	for _, row := range expected {
		field, ok := byID[row.id]
		if !ok || field.Scope != "server" || field.Permission != "owner" || fmt.Sprint(field.Options) != fmt.Sprint(row.options) || field.Default != row.factory {
			t.Fatalf("registry omitted/misdescribed %s: %+v", row.id, field)
		}
	}
	w = f.call("GET", "/v1/admin/sign-in/policy", f.owner, "")
	var policy identity.SignInPolicy
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &policy) != nil {
		t.Fatal(w.Code, w.Body)
	}
	policy.Values = identity.SignInPolicyValues{StaySignedInDays: 7, PINRememberDays: 0, PasswordAttempts: "strict", PINAttempts: "relaxed", PasswordMinLength: 12, MemberResetCodeMinutes: 15, RequireTwoStep: "everyone", RememberAccounts: false, EndedSessionsDays: 30}
	body, err := json.Marshal(identity.SignInPolicyPatch{ExpectedRevision: policy.Revision, Values: policy.Values})
	if err != nil {
		t.Fatal(err)
	}
	w = f.call("PATCH", "/v1/admin/sign-in/policy", f.owner, string(body))
	if w.Code != 200 {
		t.Fatalf("existing owner policy writer: %d %s", w.Code, w.Body)
	}
	var saved identity.SignInPolicy
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	expectedPolicyRevision = saved.Revision
	read(true)
	for _, token := range []string{f.admin, f.member} {
		if w := f.call("GET", "/v1/admin/settings/configuration", token, ""); w.Code != 403 {
			t.Fatalf("non-owner configuration: %d %s", w.Code, w.Body)
		}
	}
}

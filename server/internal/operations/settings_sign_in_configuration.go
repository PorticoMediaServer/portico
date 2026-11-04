package operations

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type signInChoice interface{ JSONSchema() map[string]any }

type signInConfigurationBinding struct {
	ID, Key, Field string
	Choice         signInChoice
	Apply          string
}

func signInConfigurationBindings() []signInConfigurationBinding {
	return []signInConfigurationBinding{
		{"signin.stayLength", "signin.stay_signed_in_days", "staySignedInDays", identity.StaySignedInDays(0), "next-operation"},
		{"signin.pinRemember", "signin.pin_remember_days", "pinRememberDays", identity.PINRememberDays(0), "next-operation"},
		{"signin.passwordAttempts", "signin.password_attempts", "passwordAttempts", identity.AttemptPolicy(""), "next-operation"},
		{"signin.pinAttempts", "signin.pin_attempts", "pinAttempts", identity.AttemptPolicy(""), "next-operation"},
		{"signin.passwordLength", "signin.password_min_length", "passwordMinLength", identity.PasswordMinimum(0), "next-operation"},
		{"signin.resetCodeLife", "signin.member_reset_code_minutes", "memberResetCodeMinutes", identity.MemberResetMinutes(0), "next-operation"},
		{"signin.requireTwoStep", "signin.require_two_step", "requireTwoStep", identity.TwoStepRequirement(""), "next-operation"},
		{"signin.rememberAccounts", "signin.remember_accounts", "rememberAccounts", nil, "hot"},
		{"signin.endedKeep", "signin.ended_sessions_days", "endedSessionsDays", identity.EndedSessionsDays(0), "next-maintenance"},
	}
}

// Keep choice vocabulary with the identity contract that validates writes.
func signInChoiceOptions(choice signInChoice) []string {
	if choice == nil {
		return nil
	}
	switch values := choice.JSONSchema()["enum"].(type) {
	case []string:
		return values
	case []int:
		out := make([]string, len(values))
		for i, n := range values {
			out[i] = strconv.Itoa(n)
		}
		return out
	}
	return nil
}

func signInConfigurationValues(v identity.SignInPolicyValues) map[string]any {
	return map[string]any{
		"staySignedInDays": int(v.StaySignedInDays), "pinRememberDays": int(v.PINRememberDays),
		"passwordAttempts": string(v.PasswordAttempts), "pinAttempts": string(v.PINAttempts),
		"passwordMinLength": int(v.PasswordMinLength), "memberResetCodeMinutes": int(v.MemberResetCodeMinutes),
		"requireTwoStep": string(v.RequireTwoStep), "rememberAccounts": v.RememberAccounts,
		"endedSessionsDays": int(v.EndedSessionsDays),
	}
}

// SignInConfigurationFields describe the existing identity policy's controls.
// Writes use its own revision and /v1/admin/sign-in/policy, never SettingsChange.
func SignInConfigurationFields() []Field {
	defaults := signInConfigurationValues(identity.DefaultSignInPolicy())
	fields := []Field{}
	for _, b := range signInConfigurationBindings() {
		kind := "enumeration"
		if b.Choice == nil {
			kind = "boolean"
		}
		fields = append(fields, Field{ID: b.ID, Group: "signin", Scope: "server", Overrides: []string{}, Type: kind, Default: defaults[b.Field], Options: signInChoiceOptions(b.Choice), Save: "explicit-apply", Apply: b.Apply, Consumer: "identity.SignInPolicyValues." + b.Field, Permission: "owner", Retention: "configuration", Audit: "administrative"})
	}
	return fields
}

// Read all nine values once, inside the configuration reader's authorized
// snapshot. Identity remains the sole stored authority and write path.
func readSignInConfiguration(ctx context.Context, tx *sql.Tx) (identity.SignInPolicy, []ConfigurationValue, error) {
	policy, err := identity.ReadSignInPolicyTx(ctx, tx)
	if err != nil {
		return policy, nil, err
	}
	defaults := signInConfigurationValues(identity.DefaultSignInPolicy())
	values := signInConfigurationValues(policy.Values)
	source := "server"
	if policy.Revision == 1 && policy.Values == identity.DefaultSignInPolicy() {
		source = "default"
	}
	fields := []ConfigurationValue{}
	for _, b := range signInConfigurationBindings() {
		fields = append(fields, ConfigurationValue{ID: b.ID, Key: b.Key, Current: configurationAtom(values[b.Field]), Effective: configurationAtom(values[b.Field]), Default: configurationAtom(defaults[b.Field]), Source: source, ApplicationMode: b.Apply})
	}
	return policy, fields, nil
}

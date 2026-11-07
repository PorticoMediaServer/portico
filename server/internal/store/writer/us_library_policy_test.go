package writer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestUSLibraryPolicyIntersection(t *testing.T) {
	for _, tc := range []struct {
		name, role, member   string
		profile              sql.NullString
		movies, music, other bool
	}{
		{"member_inherits", "member", `["movies","music"]`, sql.NullString{}, true, true, false},
		{"member_empty", "member", `[]`, sql.NullString{}, false, false, false},
		{"member_null_denies", "member", `null`, sql.NullString{}, false, false, false},
		{"profile_narrows", "member", `["movies","music"]`, sql.NullString{String: `["movies","other"]`, Valid: true}, true, false, false},
		{"profile_empty_denies", "member", `["movies"]`, sql.NullString{String: `[]`, Valid: true}, false, false, false},
		{"profile_null_denies", "member", `["movies"]`, sql.NullString{String: `null`, Valid: true}, false, false, false},
		{"owner_all", "owner", `[]`, sql.NullString{}, true, true, true},
		{"admin_all", "admin", `[]`, sql.NullString{}, true, true, true},
		{"admin_profile_narrows", "admin", `[]`, sql.NullString{String: `["movies"]`, Valid: true}, true, false, false},
		{"owner_profile_narrows", "owner", `[]`, sql.NullString{String: `["music"]`, Valid: true}, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := decodeUSLibraryAccess(tc.role, tc.member, tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			if policy.allows("movies") != tc.movies || policy.allows("music") != tc.music || policy.allows("other") != tc.other || policy.allows("") || policy.allows("../private") {
				t.Fatal("incorrect library intersection", policy)
			}
		})
	}
}

func TestUSLibraryPolicyRejectsInvalidFacts(t *testing.T) {
	for _, raw := range []string{`{}`, `[1]`, `["movies","movies"]`, `["../private"]`, `[""]`, `["música"]`, `["movies"] []`, strings.Repeat(" ", usLibraryPolicyByteLimit+1)} {
		for _, profile := range []bool{false, true} {
			member, restricted := raw, sql.NullString{}
			if profile {
				member, restricted = `["movies"]`, sql.NullString{String: raw, Valid: true}
			}
			policy, err := decodeUSLibraryAccess("owner", member, restricted)
			if !errors.Is(err, ErrDisclosureDenied) || policy.all || len(policy.libraries) != 0 {
				t.Fatal("malformed policy granted authority", profile, err)
			}
		}
	}
	for _, role := range []string{"", "account", "hosted-owner", "future-admin"} {
		policy, err := decodeUSLibraryAccess(role, `["movies"]`, sql.NullString{})
		if !errors.Is(err, ErrDisclosureDenied) || policy.allows("movies") {
			t.Fatal("unknown/narrow role accepted", role, err)
		}
	}
}

func TestUSLibraryPolicyBoundedEnvelope(t *testing.T) {
	ids := make([]string, usMaxPolicyLibraries)
	for n := range ids {
		ids[n] = fmt.Sprintf("library-%d", n)
	}
	data, err := json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := decodeUSLibraryAccess("member", string(data), sql.NullString{})
	if err != nil || !policy.allows(ids[len(ids)-1]) {
		t.Fatal("legal raw policy envelope refused", err)
	}
	ids = append(ids, "extra")
	data, err = json.Marshal(ids)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeUSLibraryAccess("member", string(data), sql.NullString{}); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("unbounded policy admitted", err)
	}
}

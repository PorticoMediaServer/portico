package recordingaccess

import (
	"database/sql"
	"testing"
)

func TestDirectRecordingLibraryPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, role, membership, profile, library string
		restricted, want                         bool
	}{
		{name: "admin unrestricted", role: "admin", membership: "[]", library: "library", want: true},
		{name: "admin profile restriction", role: "admin", membership: "[]", profile: `["other"]`, library: "library", restricted: true},
		{name: "admin empty profile denies", role: "admin", membership: "[]", profile: "[]", library: "library", restricted: true},
		{name: "owner unrestricted", role: "owner", membership: "[]", library: "library", want: true},
		{name: "owner child restriction", role: "owner", membership: "[]", profile: `["other"]`, library: "library", restricted: true},
		{name: "member granted", role: "member", membership: `["library"]`, library: "library", want: true},
		{name: "member denied", role: "member", membership: `["other"]`, library: "library"},
		{name: "profile cannot expand member", role: "member", membership: `["other"]`, profile: `["library"]`, library: "library", restricted: true},
		{name: "intersection granted", role: "member", membership: `["library","other"]`, profile: `["library"]`, library: "library", restricted: true, want: true},
		{name: "empty profile denies", role: "owner", membership: "[]", profile: "[]", library: "library", restricted: true},
		{name: "private overlay separate from shared grants", role: "member", membership: "[]", profile: "[]", restricted: true, want: true},
		{name: "malformed member fails closed", role: "owner", membership: "invalid", library: "library"},
		{name: "malformed profile fails closed for private overlay", role: "member", membership: "[]", profile: "invalid", restricted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := directLibraryAllowed(tt.role, tt.membership, sql.NullString{String: tt.profile, Valid: tt.restricted}, tt.library)
			if got != tt.want {
				t.Fatalf("allowed=%v want %v", got, tt.want)
			}
		})
	}
}

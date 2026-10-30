//go:build windows

package backup

import (
	"errors"
	"testing"
)

func TestStatePermissionModesDoNotClaimToAssessOrRepairWindowsACLs(t *testing.T) {
	state := t.TempDir()
	if exposed, err := CheckPermissions(state); !errors.Is(err, ErrStatePermissionsUnsupported) || exposed {
		t.Fatalf("synthetic modes claimed a checked ACL result: %t %v", exposed, err)
	}
	if err := FixPermissions(state); !errors.Is(err, ErrStatePermissionsUnsupported) {
		t.Fatalf("ACL repair claimed success: %v", err)
	}
}

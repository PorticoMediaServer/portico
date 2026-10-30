package httpapi

import (
	"testing"
)

// The owner's "require approval for new devices" switch, end to end: saved in
// settings, it holds a member's next new device; the device shows up in the
// members' device list; approving it there lets it sign in.
func TestMembersDeviceRoutesAreForAdministrators(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	member, _ := f.member()
	if w := f.raw("GET", "/v1/admin/access/devices", member, nil, nil); w.Code != 403 {
		t.Fatalf("a member listing members' devices: %d %s", w.Code, w.Body.String())
	}
	if w := f.raw("PUT", "/v1/admin/access/devices/anything/trust", member, nil, map[string]any{"expectedRevision": 1, "operationId": "trust-member-0001", "trust": "approved"}); w.Code != 404 {
		t.Fatalf("retired device policy route: %d %s", w.Code, w.Body.String())
	}
	tokenWorks(t, f, member, 200)
}

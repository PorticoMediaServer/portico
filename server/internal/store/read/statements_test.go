package read

import "testing"

func TestReadStatementIDPreservesCapturedNamespaces(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"rs.people.credit_octets", "read.browse.rs.people.credit_octets"},
		{"read.browse.rs.people.credit_octets", "read.browse.rs.people.credit_octets"},
		{"read.full.native.item", "read.full.native.item"},
		{"full.native.item", "read.full.native.item"},
		{"foreground.detail", "read.foreground.detail"},
		{"principal", "read.browse.principal"},
	} {
		if got := readStatementID(tc.name); string(got) != tc.want {
			t.Errorf("%q: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

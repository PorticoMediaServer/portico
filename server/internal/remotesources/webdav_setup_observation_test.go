package remotesources

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

func TestWebDAVSetupRequiresEveryActualLeaf(t *testing.T) {
	for _, test := range []struct {
		states []remotemedia.SetupRangeSupport
		want   remotemedia.SetupRangeSupport
	}{
		{nil, remotemedia.SetupRangeUnknown},
		{[]remotemedia.SetupRangeSupport{remotemedia.SetupRangeSupported}, remotemedia.SetupRangeSupported},
		{[]remotemedia.SetupRangeSupport{remotemedia.SetupRangeSupported, remotemedia.SetupRangeUnknown}, remotemedia.SetupRangeUnknown},
		{[]remotemedia.SetupRangeSupport{remotemedia.SetupRangeUnknown, remotemedia.SetupRangeUnsupported}, remotemedia.SetupRangeUnsupported},
		{[]remotemedia.SetupRangeSupport{remotemedia.SetupRangeSupported, remotemedia.SetupRangeSupported}, remotemedia.SetupRangeSupported},
	} {
		var rows []webDAVSetupObservation
		for _, state := range test.states {
			rows = append(rows, webDAVSetupObservation{Support: state})
		}
		if got := aggregateWebDAVSetup(rows); got != test.want {
			t.Fatal("leaf coverage changed", got, test.want)
		}
	}
}

func TestWebDAVSetupNeverGuessesNestedWrapperRoot(t *testing.T) {
	for _, test := range []struct {
		config, remote string
		known          bool
	}{
		{"[leaf]\ntype=webdav\nurl=https://source.fixture/dav\n", "leaf:root", true},
		{"[outer]\ntype=crypt\nremote=leaf:cipher\n[leaf]\ntype=webdav\nurl=https://source.fixture/dav\n", "outer:", true},
		{"[outer]\ntype=crypt\nremote=leaf:cipher\n[leaf]\ntype=webdav\nurl=https://source.fixture/dav\n", "outer:unknown-raw-child", false},
		{"[outer]\ntype=crypt\nremote=middle:unknown-raw-child\n[middle]\ntype=alias\nremote=leaf:cipher\n[leaf]\ntype=webdav\nurl=https://source.fixture/dav\n", "outer:", false},
	} {
		leaves, err := configuredBackends([]byte(test.config), test.remote)
		if err != nil || len(leaves) != 1 {
			t.Fatal("invalid test graph", err)
		}
		if leaves[0].RawRootKnown != test.known {
			t.Fatal("wrapper root was guessed")
		}
	}
}

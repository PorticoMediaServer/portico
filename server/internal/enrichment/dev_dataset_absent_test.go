//go:build !devtrust || release

package enrichment

import (
	"context"
	"net/http/httptest"
	"testing"
)

// A build without the devtrust tag, and every release build, never takes the
// dataset from this machine, whatever PORTICO_DEV_DATASET_LOOPBACK says.
func TestOtherBuildsRefuseALoopbackStandIn(t *testing.T) {
	t.Setenv("PORTICO_DEV_DATASET_LOOPBACK", "1")
	if devLoopback {
		t.Fatal("the development switch is on in a non-development build")
	}
	c, p := newLoopbackFixture(t)
	server := httptest.NewServer(p)
	defer server.Close()
	s := New(c.DB, Config{ManifestURL: server.URL + "/manifest.json", PublicKey: p.public, Dir: t.TempDir()})
	if _, err := s.Step(context.Background(), 100); err == nil {
		t.Fatal("a loopback stand-in was used")
	}
	if total := p.requests["/manifest.json"]; total != 0 {
		t.Fatal("the stand-in was asked", total)
	}
}

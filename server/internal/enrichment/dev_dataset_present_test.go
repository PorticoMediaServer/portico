//go:build devtrust && !release

package enrichment

import (
	"context"
	"net/http/httptest"
	"testing"
)

// In a development build with PORTICO_DEV_DATASET_LOOPBACK=1 the dataset comes
// from a stand-in on this machine, through the full verification path.
func TestADevelopmentBuildTakesTheDatasetFromALoopbackStandIn(t *testing.T) {
	previous := devLoopback
	defer func() { devLoopback = previous }()
	c, p := newLoopbackFixture(t)
	server := httptest.NewServer(p)
	defer server.Close()
	devLoopback = false
	s := New(c.DB, Config{ManifestURL: server.URL + "/manifest.json", PublicKey: p.public, Dir: t.TempDir()})
	if _, err := s.Step(context.Background(), 100); err == nil {
		t.Fatal("a loopback stand-in was used without the development switch")
	}
	devLoopback = true
	s = New(c.DB, Config{ManifestURL: server.URL + "/manifest.json", PublicKey: p.public, Dir: t.TempDir()})
	c.Exec(`DELETE FROM enrichment_state`)
	c.Exec(`DELETE FROM enrichment_domains`)
	if _, err := s.Step(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	var matched int
	c.DB.QueryRow(`SELECT count(*) FROM catalog_dataset_works`).Scan(&matched)
	if matched != 1 {
		t.Fatal("the stand-in's release wasn't imported", matched)
	}
	// Everything else still applies: a tampered manifest is refused.
	p.tamper = true
	c.Exec(`UPDATE enrichment_domains SET checked_at='2000-01-01T00:00:00Z'`)
	if _, err := s.Step(context.Background(), 100); err == nil {
		t.Fatal("a tampered manifest from the stand-in was accepted")
	}
}

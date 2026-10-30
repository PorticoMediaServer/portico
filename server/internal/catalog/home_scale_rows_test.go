package catalog

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// TestHomeRowsAtScale is an opt-in measurement (ARCH-SRV-04): per-row and
// whole-Home latency on a scalebench fixture, for an open and a PG-13 viewer.
// It migrates the fixture in place.
//
//	PORTICO_HOME_ROWS_DB=/path/catalog.sqlite go test ./internal/catalog -run TestHomeRowsAtScale -count=1 -v -timeout 60m
func TestHomeRowsAtScale(t *testing.T) {
	path := os.Getenv("PORTICO_HOME_ROWS_DB")
	if path == "" {
		t.Skip("set PORTICO_HOME_ROWS_DB to a scalebench fixture")
	}
	opened := time.Now()
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Logf("opened (with migrations) in %s", time.Since(opened).Round(time.Millisecond))
	s := New(db)
	if _, err = s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	pg13 := 13
	libraries := []string{"movies", "tv", "music"}
	viewers := []struct {
		name string
		v    Viewer
	}{
		{"open", Viewer{Profile: "viewer", Fence: "scale-open", Libraries: libraries}},
		{"pg13", Viewer{Profile: "viewer", Fence: "scale-pg13", Libraries: libraries, Restrictions: identity.ContentRestrictions{MaximumAge: &pg13, BlockUnrated: true}}},
	}
	for _, library := range libraries {
		built := time.Now()
		if err = s.RebuildVisibilityClass(context.Background(), library, viewers[1].v.EffectiveRestrictions()); err != nil {
			t.Fatal(err)
		}
		t.Logf("pg13 class %s built in %s", library, time.Since(built).Round(time.Millisecond))
	}
	samples := 11
	if raw := os.Getenv("PORTICO_HOME_ROWS_SAMPLES"); raw != "" {
		if _, err := time.ParseDuration("1s"); err == nil {
			samples = len(raw) * 0
			for _, c := range raw {
				samples = samples*10 + int(c-'0')
			}
		}
	}
	quantiles := func(d []time.Duration) (time.Duration, time.Duration) {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[len(d)/2], d[(len(d)*95+99)/100-1]
	}
	for _, viewer := range viewers {
		r := HomeRequest{Viewer: viewer.v, Profile: "viewer", ViewerFence: viewer.v.Fence, Libraries: libraries, Restrictions: viewer.v.Restrictions, Limit: 12}
		specs, err := s.homeSpecs(r.scoped())
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range specs {
			if spec.PolicyState != "available" {
				continue
			}
			times := []time.Duration{}
			for k := 0; k < samples; k++ {
				began := time.Now()
				if _, err := s.HomeSingleRow(r, spec.ID, HomeRowPage{Limit: 12}); err != nil && err != ErrHomeRowUnknown {
					t.Fatalf("%s/%s: %v", viewer.name, spec.ID, err)
				}
				times = append(times, time.Since(began))
				if k == 0 && times[0] > 20*time.Second {
					break
				}
			}
			p50, p95 := quantiles(times)
			t.Logf("%s row %-22s p50 %8s p95 %8s (n=%d)", viewer.name, spec.ID, p50.Round(100*time.Microsecond), p95.Round(100*time.Microsecond), len(times))
		}
		cold := []time.Duration{}
		for k := 0; k < samples; k++ {
			fresh := New(db) // no maintained list: the in-line computation after a viewer's own activity
			began := time.Now()
			if _, err := fresh.HomeSingleRow(r, "recommended", HomeRowPage{Limit: 12}); err != nil {
				t.Fatal(err)
			}
			cold = append(cold, time.Since(began))
		}
		p50c, p95c := quantiles(cold)
		t.Logf("%s row %-22s p50 %8s p95 %8s (n=%d)", viewer.name, "recommended(recompute)", p50c.Round(100*time.Microsecond), p95c.Round(100*time.Microsecond), len(cold))
		times := []time.Duration{}
		for k := 0; k < samples; k++ {
			began := time.Now()
			if _, err := s.HomeRows(r); err != nil {
				t.Fatalf("%s home: %v", viewer.name, err)
			}
			times = append(times, time.Since(began))
			if k == 0 && times[0] > 60*time.Second {
				break
			}
		}
		p50, p95 := quantiles(times)
		t.Logf("%s HOME p50 %s p95 %s (n=%d)", viewer.name, p50.Round(100*time.Microsecond), p95.Round(100*time.Microsecond), len(times))
	}
}

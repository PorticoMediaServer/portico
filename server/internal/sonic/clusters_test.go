package sonic

import (
	"math"
	"reflect"
	"testing"
)

func clusterFixture(t *testing.T, id int64, x, y float32, weight float64) ClusterPoint {
	t.Helper()
	var values [Dimensions]float32
	values[0] = x
	values[1] = y
	vector, err := Normalize(values[:])
	if err != nil {
		t.Fatal(err)
	}
	return ClusterPoint{ID: id, Vector: vector, Weight: weight}
}
func TestListeningClustersSeparateActualDirectionsAndIgnoreInputOrder(t *testing.T) {
	points := []ClusterPoint{}
	for n := int64(1); n <= 6; n++ {
		points = append(points, clusterFixture(t, n, 1, float32(n)/200, 2))
		points = append(points, clusterFixture(t, n+10, float32(n)/200, 1, 1))
	}
	before := append([]ClusterPoint(nil), points...)
	groups, err := ClusterListening(points, 2)
	if err != nil || len(groups) != 2 || len(groups[0].Members) != 6 || len(groups[1].Members) != 6 || groups[0].Weight != 12 || groups[1].Weight != 6 || groups[0].Center[0] < .99 || groups[1].Center[1] < .99 {
		t.Fatal(groups, err)
	}
	for _, id := range groups[0].Members {
		if id < 1 || id > 6 {
			t.Fatal("different direction entered weighted group", groups)
		}
	}
	if !reflect.DeepEqual(points, before) {
		t.Fatal("caller evidence mutated")
	}
	for i, j := 0, len(points)-1; i < j; i, j = i+1, j-1 {
		points[i], points[j] = points[j], points[i]
	}
	reordered, err := ClusterListening(points, 2)
	if err != nil || !reflect.DeepEqual(groups, reordered) {
		t.Fatal("read order changed clusters", groups, reordered, err)
	}
}
func TestListeningClustersDoNotInventVarietyOrAcceptInvalidEvidence(t *testing.T) {
	one := clusterFixture(t, 1, 1, 0, 1)
	points := []ClusterPoint{one}
	two := one
	two.ID = 2
	points = append(points, two)
	groups, err := ClusterListening(points, 6)
	if err != nil || len(groups) != 1 || len(groups[0].Members) != 2 {
		t.Fatal("identical evidence produced fake clusters", groups, err)
	}
	for _, bad := range []ClusterPoint{{ID: 3, Weight: 1}, {ID: 0, Vector: one.Vector, Weight: 1}, {ID: 3, Vector: one.Vector, Weight: 0}, {ID: 3, Vector: one.Vector, Weight: math.NaN()}, {ID: 3, Vector: one.Vector, Weight: math.Inf(1)}} {
		if _, err := ClusterListening([]ClusterPoint{one, bad}, 2); err == nil {
			t.Fatal("invalid point accepted", bad)
		}
	}
	if _, err := ClusterListening([]ClusterPoint{one, one}, 2); err == nil {
		t.Fatal("duplicate track identity accepted")
	}
	if _, err := ClusterListening(points, 7); err == nil {
		t.Fatal("unbounded group count")
	}
	if _, err := ClusterListening(make([]ClusterPoint, MaxClusterPoints+1), 2); err == nil {
		t.Fatal("unbounded input count")
	}
}

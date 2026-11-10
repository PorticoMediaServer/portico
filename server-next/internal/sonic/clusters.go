package sonic

import (
	"math"
	"sort"
)

// ClusterPoint is a permitted, current-model track and its actual play weight.
// The catalogue filters profile/library/source authority before calling this
// numeric helper. It accepts no recording, provider or account information.
type ClusterPoint struct {
	ID     int64
	Vector Embedding
	Weight float64
}
type Cluster struct {
	Members []int64
	Center  Embedding
	Weight  float64
}

const MaxClusterPoints = 2048
const MaxClusters = 6

func clusterDot(a, b Embedding) float64 {
	var value float64
	for i, v := range a {
		value += float64(v) * float64(b[i])
	}
	return min(1, max(-1, value))
}

// ClusterListening uses bounded spherical k-means over actual profile plays.
// Ordering and farthest-point seeds are deterministic, so read/query order
// cannot rename a mix. Identical points produce one cluster, not fake variety.
// Corrupt/empty vectors and invented zero or non-finite weights are refused.
func ClusterListening(input []ClusterPoint, limit int) ([]Cluster, error) {
	if limit < 1 || limit > MaxClusters || len(input) > MaxClusterPoints {
		return nil, ErrEmbedding
	}
	if len(input) == 0 {
		return []Cluster{}, nil
	}
	points := append([]ClusterPoint(nil), input...)
	sort.Slice(points, func(i, j int) bool { return points[i].ID < points[j].ID })
	first := 0
	for i, p := range points {
		if p.ID <= 0 || i > 0 && points[i-1].ID == p.ID || !p.Vector.valid() || math.IsNaN(p.Weight) || math.IsInf(p.Weight, 0) || p.Weight <= 0 || p.Weight > 1e6 {
			return nil, ErrEmbedding
		}
		if p.Weight > points[first].Weight {
			first = i
		}
	}
	centers := []Embedding{points[first].Vector}
	for len(centers) < min(limit, len(points)) {
		best := -1
		distance := 0.0
		for i, p := range points {
			closest := -1.0
			for _, center := range centers {
				closest = max(closest, clusterDot(p.Vector, center))
			}
			separation := 1 - closest
			if separation > distance+1e-12 || math.Abs(separation-distance) <= 1e-12 && best >= 0 && p.Weight > points[best].Weight {
				best = i
				distance = separation
			}
		}
		if best < 0 || distance < 1e-6 {
			break
		}
		centers = append(centers, points[best].Vector)
	}
	assignments := make([]int, len(points))
	for i := range assignments {
		assignments[i] = -1
	}
	assign := func() bool {
		changed := false
		for i, p := range points {
			best := 0
			score := clusterDot(p.Vector, centers[0])
			for k := 1; k < len(centers); k++ {
				if next := clusterDot(p.Vector, centers[k]); next > score+1e-12 {
					best = k
					score = next
				}
			}
			if assignments[i] != best {
				assignments[i] = best
				changed = true
			}
		}
		return changed
	}
	mean := func() (weights []float64, err error) {
		weights = make([]float64, len(centers))
		sums := make([][Dimensions]float64, len(centers))
		for i, p := range points {
			k := assignments[i]
			weights[k] += p.Weight
			for d, v := range p.Vector {
				sums[k][d] += float64(v) * p.Weight
			}
		}
		for k, weight := range weights {
			if weight == 0 {
				continue
			}
			var values [Dimensions]float32
			for d, value := range sums[k] {
				values[d] = float32(value / weight)
			}
			centers[k], err = Normalize(values[:])
			if err != nil {
				return nil, err
			}
		}
		return weights, nil
	}
	for round := 0; round < 8; round++ {
		changed := assign()
		if _, err := mean(); err != nil {
			return nil, err
		}
		if !changed {
			break
		}
	}
	assign()
	weights, err := mean()
	if err != nil {
		return nil, err
	}
	out := make([]Cluster, len(centers))
	for k := range out {
		out[k] = Cluster{Center: centers[k], Weight: weights[k]}
	}
	for i, p := range points {
		k := assignments[i]
		out[k].Members = append(out[k].Members, p.ID)
	}
	kept := out[:0]
	for _, cluster := range out {
		if len(cluster.Members) > 0 {
			kept = append(kept, cluster)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Weight != kept[j].Weight {
			return kept[i].Weight > kept[j].Weight
		}
		return kept[i].Members[0] < kept[j].Members[0]
	})
	return kept, nil
}

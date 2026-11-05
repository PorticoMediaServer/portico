// Package qual contains measurement/reporting helpers for the isolated store
// qualification harness. It is not used by the serving runtime.
package qual

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// Bound is a one-sided 95% upper confidence bound for the population p99.
// Rank is one-based in the sorted observed population.
type Bound struct {
	Rank  int           `json:"rank"`
	Value time.Duration `json:"value_ns"`
}

// Distribution uses nearest-rank quantiles. Durations are encoded as integer
// nanoseconds. A nil bound means there are too few samples for a finite bound;
// it must never be interpreted as a zero-latency bound.
type Distribution struct {
	Count      int           `json:"count"`
	P50        time.Duration `json:"p50_ns"`
	P95        time.Duration `json:"p95_ns"`
	P99        time.Duration `json:"p99_ns"`
	P999       time.Duration `json:"p999_ns"`
	Max        time.Duration `json:"max_ns"`
	P99Upper95 *Bound        `json:"p99_upper_95"`
}

// Summarize retains every observation, including slow tails, and leaves samples
// unchanged. Its caller owns the raw evidence; no trimming or noise exclusion is
// performed here. Empty/nonpositive samples are protocol errors.
func Summarize(samples []time.Duration) (Distribution, error) {
	if len(samples) == 0 {
		return Distribution{}, fmt.Errorf("empty duration population")
	}
	for i, sample := range samples {
		if sample <= 0 {
			return Distribution{}, fmt.Errorf("sample %d has nonpositive duration %s", i, sample)
		}
	}
	xs := slices.Clone(samples)
	slices.Sort(xs)
	quantile := func(p float64) time.Duration {
		return xs[int(math.Ceil(float64(len(xs))*p))-1]
	}
	d := Distribution{
		Count: len(xs), P50: quantile(.5), P95: quantile(.95),
		P99: quantile(.99), P999: quantile(.999), Max: xs[len(xs)-1],
	}
	if rank := p99UpperRank(len(xs)); rank != 0 {
		d.P99Upper95 = &Bound{Rank: rank, Value: xs[rank-1]}
	}
	return d, nil
}

// p99UpperRank implements the accepted S O-08 protocol:
// smallest k with P[Binomial(n,.99) <= k-1] >= .95.
// Using failures Y=Binomial(n,.01), this is the largest f=n-k+1 for
// which P[Y <= f-1] <= .05. The log-space recurrence also works when
// .99^n underflows (unlike the original phase-0 probability recurrence).
func p99UpperRank(n int) int {
	logMass := float64(n) * math.Log(.99)
	logCDF := logMass
	lastRank := 0
	for f := 1; f <= n; f++ {
		if logCDF > math.Log(.05) {
			break
		}
		lastRank = n - f + 1
		logMass += math.Log(float64(n-f+1)) - math.Log(float64(f)) + math.Log(.01) - math.Log(.99)
		// log(exp(a)+exp(b)), avoiding cancellation and underflow.
		a, b := logCDF, logMass
		if a < b {
			a, b = b, a
		}
		logCDF = a + math.Log1p(math.Exp(b-a))
	}
	return lastRank
}

package main

import (
	"math"
	"sort"
)

func upper99(tag string) map[string]any {
	nativeMax.Lock()
	values := append([]float64{}, nativeMax.values[tag]...)
	nativeMax.Unlock()
	n := len(values)
	if n < 1000 {
		return map[string]any{"n": n, "qualified_sample": false, "reason": "requires at least1000holds per VM"}
	}
	sort.Float64s(values)
	k := n
	logProb := float64(n) * math.Log(.99)
	logTail := logProb
	for k > 1 {
		next := logProb + math.Log(float64(k)) - math.Log(float64(n-k+1)) + math.Log(.01/.99)
		hi, lo := logTail, next
		if lo > hi {
			hi, lo = lo, hi
		}
		combined := hi + math.Log1p(math.Exp(lo-hi))
		if combined > math.Log(.05) {
			break
		}
		logTail = combined
		logProb = next
		k--
	}
	return map[string]any{"n": n, "qualified_sample": true, "order_statistic_1based": k, "p99_upper95_ms": values[k-1], "coverage": 1 - math.Exp(logTail)}
}

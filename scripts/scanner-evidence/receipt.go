package evidence

import (
	"fmt"
	"sort"
	"time"
)

var applied = map[string]int64{}
var waiting = map[string]string{}
var readyResume [][2]int64
var receiptPendingCount, lateRegistration int64

func resetReceipts() {
	applied = map[string]int64{}
	waiting = map[string]string{}
	readyResume = nil
	receiptPendingCount = 0
	lateRegistration = 0
}
func assetReceipt(token string, epoch int64) string { return fmt.Sprintf("%s/%d", token, epoch) }
func objectReceipt(job, object, revision string, policy int64) string {
	return fmt.Sprintf("%s/%s/%s/%d", job, object, revision, policy)
}

// ReceiptApplied runs only in the measurement overlay's activation AfterCommit.
func ReceiptApplied(token string, epoch int64) {
	state.Lock()
	defer state.Unlock()
	if !state.on {
		return
	}
	key := assetReceipt(token, epoch)
	if _, ok := applied[key]; !ok {
		applied[key] = time.Since(state.began).Nanoseconds()
	}
}
func PendingReceipt(job, object, revision string, policy int64, token string, epoch int64) {
	state.Lock()
	defer state.Unlock()
	if !state.on {
		return
	}
	key := objectReceipt(job, object, revision, policy)
	if _, ok := waiting[key]; ok {
		return
	}
	asset := assetReceipt(token, epoch)
	waiting[key] = asset
	receiptPendingCount++
	if _, ok := applied[asset]; ok {
		lateRegistration++
	}
}
func ResumeReceipt(job, object, revision string, policy int64) {
	state.Lock()
	defer state.Unlock()
	if !state.on {
		return
	}
	key := objectReceipt(job, object, revision, policy)
	asset, ok := waiting[key]
	if !ok {
		return
	}
	ready, ok := applied[asset]
	if !ok {
		return
	}
	readyResume = append(readyResume, [2]int64{ready, time.Since(state.began).Nanoseconds()})
	delete(waiting, key)
}
func receiptReport() map[string]any {
	delays := make([]int64, 0, len(readyResume))
	var sum int64
	for _, p := range readyResume {
		d := p[1] - p[0]
		delays = append(delays, d)
		sum += d
	}
	sort.Slice(delays, func(i, j int) bool { return delays[i] < delays[j] })
	var p50, p95, max int64
	if len(delays) > 0 {
		p50 = delays[(len(delays)-1)/2]
		p95 = delays[(len(delays)-1)*95/100]
		max = delays[len(delays)-1]
	}
	return map[string]any{"applied": len(applied), "pendingObjects": receiptPendingCount, "resumed": len(delays), "pendingAtStop": len(waiting), "appliedBeforeRegistration": lateRegistration, "delayNanos": sum, "p50Nanos": p50, "p95Nanos": p95, "maxNanos": max, "intervals": readyResume}
}

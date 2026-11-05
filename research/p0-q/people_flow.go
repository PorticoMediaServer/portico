package main

import (
	"database/sql"
	"strings"
	"time"
)

type pendingPerson struct{ accepted []time.Time }

func peopleFlow(db *sql.DB, count, batch, n int) {
	stop := startPublisherReader()
	defer stop()
	rate := 200.0
	if strings.Contains(profileName, "old8g") {
		rate = 80
	}
	duration := 100 * time.Second
	if mixedEnabled {
		duration = mixedDuration
	}
	if n < 10000 {
		duration = 3 * time.Second
	}
	start := time.Now()
	granted := start
	total := 0
	queue := []int{}
	pending := map[int]*pendingPerson{}
	lag := []float64{}
	admission := []float64{}
	maxPending, maxSource, coalesced := 0, 0, 0
	for time.Since(start) < duration || len(queue) > 0 || total < int(duration.Seconds()*rate) {
		scheduled := min(int(time.Since(start).Seconds()*rate), int(duration.Seconds()*rate))
		due := scheduled - total
		maxSource = max(maxSource, due)
		if due > 0 {
			take := min(8, due)
			ids := []int{}
			names := []string{}
			for j := 0; j < take; j++ {
				id := 1 + ((total+j)*17)%count
				ids = append(ids, id)
				names = append(names, renamedPerson(id, total+j)+" "+marker(70000000+total+j))
			}
			at := acceptPeople(db, ids, names, 1, "people.flow")
			for j, id := range ids {
				target := start.Add(time.Duration(float64(total+j+1) / rate * float64(time.Second)))
				admission = append(admission, float64(at.Sub(target).Microseconds())/1000)
				p := pending[id]
				if p == nil {
					p = &pendingPerson{}
					pending[id] = p
					queue = append(queue, id)
				} else {
					coalesced++
				}
				p.accepted = append(p.accepted, at)
			}
			total += take
			maxPending = max(maxPending, len(queue))
		}
		if len(queue) > 0 && !time.Now().Before(granted) {
			k := min(batch, len(queue))
			ids := append([]int{}, queue[:k]...)
			docs := []*pubDoc{}
			for _, id := range ids {
				var name string
				must(db.QueryRow("SELECT name FROM catalog_people WHERE id=?", id).Scan(&name))
				docs = append(docs, capturePub(db, -1, id, 1, name, pending[id].accepted[0]))
			}
			publishDocs(db, docs, "people.flow")
			for i, d := range docs {
				assertQ(!d.visible.IsZero(), "People native visibility receipt")
				for _, a := range pending[ids[i]].accepted {
					lag = append(lag, d.visible.Sub(a).Seconds())
				}
				delete(pending, ids[i])
			}
			queue = queue[k:]
			granted = time.Now().Add(2 * time.Millisecond)
		} else {
			time.Sleep(time.Millisecond)
		}
	}
	emit("people_flow", map[string]any{"target_people_per_s": rate, "accepted": total, "actual_people_per_s": float64(total) / time.Since(start).Seconds(), "publisher_batch": batch, "grant_gap_ms": 2, "max_pending_subjects": maxPending, "max_unaccepted_arrival_backlog": maxSource, "coalesced_revisions": coalesced, "source_admission_ms": stats(admission), "lag_seconds_raw": lag, "lag_seconds": map[string]float64{"p50": stats(lag).P50, "p95": stats(lag).P95, "p99": stats(lag).P99, "max": stats(lag).Max}, "lag_scope": "source revision or a newer coalesced revision native-searchable after accepted head commit", "load": "two changed/new people per100/40items/s; deliberately stresses beyond shared-name dedup workload"})
}

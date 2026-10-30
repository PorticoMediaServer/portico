package httpapi

import (
	"reflect"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
)

// WriterCaller describes current registered HTTP callers, not a new receipt
// protocol. Metadata supplies exact wire status/header/error/type authority;
// laneSpecs supplies the actual original request/queue deadlines and class.
type WriterCaller struct {
	Metadata                                  apikit.Metadata
	RequestType, ResponseType, AdmissionClass string
	QueueWait, ResponseBudget                 time.Duration
	ExplicitLane                              bool
	// These remain blockers for enabling generic intents/ACKs; current
	// endpoint-specific receipts and accepting transactions stay unchanged.
	ReceiptAdapter, AcceptanceProof string
}

// CurrentWriterCallers covers every changing route in the live registry,
// including externally callable operations with no first-party UI. It reports
// fallback lane use explicitly rather than treating a guessed lane as proof.
func CurrentWriterCallers(r *apikit.Registry) []WriterCaller {
	var out []WriterCaller
	for _, d := range r.Definitions() {
		if d.Method == "GET" || d.Method == "HEAD" || d.Method == "OPTIONS" {
			continue
		}
		pattern := d.Method + " " + d.Path
		lane, explicit := routeLanes[pattern]
		if !explicit {
			lane = laneForUnlistedPattern(pattern)
		}
		spec := laneSpecs[lane]
		out = append(out, WriterCaller{Metadata: d.Metadata, RequestType: writerType(d.Request), ResponseType: writerType(d.Response), AdmissionClass: spec.class.String(), QueueWait: spec.queueWait, ResponseBudget: spec.budget, ExplicitLane: explicit, ReceiptAdapter: "endpoint-specific; generic typed result/ACK adapter absent", AcceptanceProof: "handler-owned commit boundary; server/internal/dbwork/testdata/write_census.tsv"})
	}
	slices.SortFunc(out, func(a, b WriterCaller) int {
		if a.Metadata.ID < b.Metadata.ID {
			return -1
		}
		if a.Metadata.ID > b.Metadata.ID {
			return 1
		}
		return 0
	})
	return out
}

func writerType(t reflect.Type) string {
	if t == nil {
		return "none"
	}
	return t.String()
}

// CurrentCallerBlockers must stay visible until a complete client cohort is
// separately implemented. A retry helper is not a durable intent coordinator.
func CurrentCallerBlockers() []string {
	return []string{
		"current HttpLocalApi.request and playback-v1/http.call retry capacity/database_busy with original body/idempotency identity; no generic durable root/lane/ordinal/ACK graph",
		"route-specific commit boundaries and typed receipt adapters must be reviewed before generic intent enablement",
		"native/Vega/CAF durable intent and observer ownership stores are not registered/qualified",
		"legacy auth5s and admin5s route budgets remain current behavior; 10s-acquire/12s-response is not enabled by this census",
		"GET/stream callbacks that mutate and internal/background write callers are tracked by the separate write census; changing-route membership alone is not a complete mutation graph",
	}
}

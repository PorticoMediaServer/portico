package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestEffectiveSourceMemoryPartitionKeepsItsPurpose(t *testing.T) {
	p := effectivePlan{effectiveSourceMemory: commandMemoryBudget{1, 1}}
	p.need[governor.MemoryBytes] = 2
	for _, purpose := range []CommandID{InventoryAccept, InventoryCreate, InventoryClaim, SearchImageStage} {
		_, err := freezeCommandPlan(nil, p, nil, purpose)
		var policy *PolicyError
		if !errors.As(err, &policy) || policy.Field != "memory_purpose" || !errors.Is(err, ErrInvalid) {
			t.Fatal("Effective proof memory entered another domain before registration/admission", purpose, err)
		}
	}
	// This fails before touching a registry, clock, database or governor. The
	// partition's typed error remains domain-specific, including Limit/Cause.
	p.need[governor.MemoryBytes] = 3
	_, err := freezeCommandPlan(nil, p, nil, EffectiveStage)
	var policy *PolicyError
	if !errors.As(err, &policy) || policy.Field != "effective_source_memory_partition" || policy.Limit != 0 || policy.Cause != ErrInvalid {
		t.Fatal("malformed proof partition lost its exact typed refusal", err)
	}
}

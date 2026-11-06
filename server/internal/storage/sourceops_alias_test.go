package storage

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/sourceops"
)

func TestOperationScopeAliasPreservesPhysicalOwnerAndError(t *testing.T) {
	actual := sourceops.New()
	var legacy *OperationScope = actual // the same concrete owner, no adapter
	legacy.Close()
	if done, err := actual.Begin(); done != nil || err != ErrRootLease || err != sourceops.ErrRootLease {
		t.Fatal("legacy caller lost the actual owner or error identity", err)
	}
}

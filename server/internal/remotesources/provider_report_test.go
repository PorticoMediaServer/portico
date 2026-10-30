package remotesources

import (
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestLocalCapacityRemainsRetriableWithoutProviderState(t *testing.T) {
	for _, err := range []error{storage.ErrBusy, remotemedia.ErrBusy, fmt.Errorf("owned request: %w", remotemedia.ErrBusy)} {
		if code := ErrorCode(err); code != "capacity_busy" {
			t.Fatal("local capacity became provider state", code)
		}
		if restored := nativeListingError(ErrorCode(err)); restored != storage.ErrBusy {
			t.Fatal("local capacity lost its pending/retry outcome", restored)
		}
	}
}

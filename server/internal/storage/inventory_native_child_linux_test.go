package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/storage/sourceops"
)

var _ nativeinventory.Transport = (*Client)(nil)

func TestNativeChildObserverUsesOriginalSupervisorAndScope(t *testing.T) {
	c, owner := nativeClient(t)
	var actual *sourceops.Scope = owner
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Film.mkv"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	r := nativeinventory.Request{Root: []byte(root), RelativePath: []byte("."), Limit: 1}
	observer := c.NativeInventoryObserver()
	page, err := observer.Observe(t.Context(), actual, "child", r)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := c.NativeInventoryPage(t.Context(), owner, "legacy", r)
	if err != nil || !reflect.DeepEqual(page, legacy) {
		t.Fatal("legacy delegation changed the owned observation", page, legacy, err)
	}
	if owner.Active() != 0 || c.Supervisor.Active() != 0 || c.Supervisor.Stats().Admitted != 2 ||
		c.NativeInventorySettings().Monitor != &c.Supervisor.nativeMonitor {
		t.Fatal("child replaced physical scope, supervisor or retirement accounting", c.Supervisor.Stats())
	}
	if ErrNativeInventory != nativeinventory.ErrNativeInventory || ErrInventoryChanged != nativeinventory.ErrInventoryChanged ||
		ErrHelperRetirementPending != nativeinventory.ErrHelperRetirementPending || ErrRootLease != sourceops.ErrRootLease {
		t.Fatal("extraction changed an error identity")
	}
}

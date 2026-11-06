package nativeinventory

import (
	"os/exec"
	"strings"
	"testing"
)

// W must be able to import the owned observer without registering the legacy
// database functions. Check the production dependency graph, not test imports.
func TestObserverImportDoesNotLoadLegacyDatabaseInitialization(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("observer dependencies: %v\n%s", err, out)
	}
	const root = "github.com/PorticoMediaServer/portico/server/internal/"
	for _, dependency := range strings.Fields(string(out)) {
		if dependency == "database/sql" || dependency == root+"storage" ||
			strings.HasPrefix(dependency, root+"dbwork") || strings.HasPrefix(dependency, root+"persistence") ||
			strings.HasPrefix(dependency, root+"compactcatalog") || strings.HasPrefix(dependency, root+"store/") ||
			strings.HasPrefix(dependency, root+"thirdparty/sqlite") {
			t.Errorf("owned observer imports database initialization: %s", dependency)
		}
	}
}

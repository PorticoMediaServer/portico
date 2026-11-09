package storagepaths

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestWindowsDriveAndUNCRootsAreNeverDataOrSpillFolders(t *testing.T) {
	for _, root := range []string{`C:\`, `\\server\share`, `\\server\share\`, `\\?\UNC\server\share`, `\\?\UNC\server\share\`, `\\?\C:\`} {
		if !filesystemRoot(root) {
			t.Errorf("root accepted: %q", root)
		}
		if _, err := Resolve(Options{Platform: "windows", LocalAppData: `C:\Users\owner\AppData\Local`, DataDirectory: root}); err == nil {
			t.Errorf("resolved root: %q", root)
		}
		if err := At(root).Prepare(); err == nil {
			t.Errorf("prepared root: %q", root)
		}
		if err := At(root).ResetTemp(); err == nil {
			t.Errorf("emptied root: %q", root)
		}
	}
}

func TestWindowsRecordingsAllowVerifiedDistinctLocalVolumes(t *testing.T) {
	data := t.TempDir()
	if distinct, err := distinctNativeVolumes(data, filepath.VolumeName(data)+`\`); err != nil || distinct {
		t.Fatal("same native volume or alias misclassified", distinct, err)
	}
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := fmt.Sprintf(`%c:\`, letter)
		distinct, err := distinctNativeVolumes(data, root)
		if err != nil || !distinct {
			continue
		}
		chosen := filepath.Join(root, "Portico-recordings-path-check-only")
		if err := At(data).ValidateRecordings(chosen); err != nil {
			t.Fatal("different native recordings volume rejected", chosen, err)
		}
		return
	}
	t.Skip("no second verified local volume available; no path was created")
}

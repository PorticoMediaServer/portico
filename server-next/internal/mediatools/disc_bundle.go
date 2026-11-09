package mediatools

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
)

// This record binds the packaged helper to the manifest and its four static
// inputs. Actual source, no-decryption and fixture qualification is performed
// by the source builder and target verifier, not inferred from this metadata.
func verifyDiscRecord(root string, m manifest) error {
	file := filepath.Join(root, "portico-disc-build.json")
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if info.Size() > 1<<20 {
		return fmt.Errorf("disc build record exceeds 1 MiB")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var record struct {
		Schema          int    `json:"schema"`
		Tool            string `json:"tool"`
		Target          string `json:"target"`
		SourceCommit    string `json:"sourceCommit"`
		SourceSHA256    string `json:"sourceSha256"`
		BinarySHA256    string `json:"binarySha256"`
		Decryption      *bool  `json:"decryption"`
		StaticLibraries map[string]struct {
			Version string `json:"version"`
			Archive string `json:"archive"`
			SHA256  string `json:"sha256"`
		} `json:"staticLibraries"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return fmt.Errorf("invalid disc build record: %w", err)
	}
	validHash := func(value string, bytes int) bool {
		decoded, err := hex.DecodeString(value)
		return err == nil && len(decoded) == bytes
	}
	if record.Schema != 1 || record.Tool != "portico-disc" || record.Target != m.Target ||
		record.Decryption == nil || *record.Decryption || !validHash(record.SourceCommit, 20) ||
		!validHash(record.SourceSHA256, 32) || record.BinarySHA256 != m.Files["bin/"+Name("portico-disc")] {
		return fmt.Errorf("disc build identity does not match bundle")
	}
	if len(record.StaticLibraries) != 4 {
		return fmt.Errorf("disc build requires four static libraries")
	}
	for _, name := range []string{"libbluray", "libudfread", "dvdnav", "dvdread"} {
		library, ok := record.StaticLibraries[name]
		if !ok || library.Version == "" || library.Archive == "." ||
			path.IsAbs(library.Archive) || path.Clean(library.Archive) != library.Archive ||
			filepath.IsAbs(library.Archive) || !filepath.IsLocal(filepath.FromSlash(library.Archive)) ||
			path.Ext(library.Archive) != ".a" || !validHash(library.SHA256, 32) {
			return fmt.Errorf("invalid static disc library: %s", name)
		}
	}
	return nil
}

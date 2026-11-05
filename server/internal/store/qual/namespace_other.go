//go:build !linux

package qual

func captureNamespaces() map[string]NamespaceEvidence {
	return map[string]NamespaceEvidence{"unsupported": {Errors: []string{"namespace provenance requires Linux NSFS descriptors"}}}
}

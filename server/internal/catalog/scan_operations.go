package catalog

// ScanOperations publishes the exact closed permission vocabulary used by the
// scanner. Internal recipe markers and packet work are not public operations.
type ScanOperations []string

func (ScanOperations) JSONSchema() map[string]any {
	values := append(append([]string{}, BasicScanOperations...), DeepScanOperations...)
	return map[string]any{"type": "array", "uniqueItems": true, "maxItems": len(values), "items": map[string]any{"type": "string", "enum": values}}
}

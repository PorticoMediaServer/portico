package remotesources

import (
	"reflect"
	"sort"

	"github.com/PorticoMediaServer/portico/apikit"
)

func statusWireSchema[T any]() map[string]any {
	shape, err := apikit.Schema(reflect.TypeFor[T]())
	if err != nil {
		panic(err)
	}
	return shape
}

// Use the producer's accepted codes, in deterministic order for official output.
func statusCodes(allowed map[string]bool) []string {
	codes := make([]string, 0, len(allowed))
	for code, accepted := range allowed {
		if accepted {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	return codes
}

func (SourceScanState) JSONSchema() map[string]any {
	type wire SourceScanState
	shape := statusWireSchema[wire]()
	properties := shape["properties"].(map[string]any)
	for name, codes := range map[string]map[string]bool{"state": scanStates, "mode": scanModes, "phase": scanPhases} {
		properties[name].(map[string]any)["enum"] = statusCodes(codes)
	}
	for _, name := range []string{"sourceGeneration", "runPolicyRevision", "processed", "discovered", "analyzed", "warnings"} {
		field := properties[name].(map[string]any)
		field["minimum"], field["maximum"] = int64(0), int64(1)<<40
	}
	for _, name := range []string{"queuedAt", "completedAt", "finishedAt"} {
		properties[name].(map[string]any)["format"] = "date-time"
	}
	return shape
}

func (SourceStatus) JSONSchema() map[string]any {
	type wire SourceStatus
	shape := statusWireSchema[wire]()
	properties := shape["properties"].(map[string]any)
	properties["health"].(map[string]any)["enum"] = statusCodes(sourceHealths)
	properties["lastErrorCode"].(map[string]any)["enum"] = statusCodes(contactErrorCodes)
	for _, name := range []string{"lastCompleteAt", "lastConnectedAt", "lastErrorAt"} {
		properties[name].(map[string]any)["format"] = "date-time"
	}
	return shape
}

func (SourceLibraryUse) JSONSchema() map[string]any {
	type wire SourceLibraryUse
	shape := statusWireSchema[wire]()
	shape["properties"].(map[string]any)["lastCompleteAt"].(map[string]any)["format"] = "date-time"
	return shape
}

func (SavedSourceTestResult) JSONSchema() map[string]any {
	type wire SavedSourceTestResult
	shape := statusWireSchema[wire]()
	shape["properties"].(map[string]any)["errorCode"].(map[string]any)["enum"] = statusCodes(contactErrorCodes)
	return shape
}

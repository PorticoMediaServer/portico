package writer

import (
	"errors"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func missingScanOwnerRetention(field string) error {
	return errors.Join(&Refusal{Code: RefusalUnqualified}, invalid(field))
}

// FIRST has no run/job owner GC. This private witness binds that reviewed
// finite writer inventory to ONE exact qualified registry and executor. It is
// not a caller flag and cannot survive replacing or extending the registry.
// Any future owner deletion/reuse protocol needs a new birth/recovery contract.
type scanOwnerRetention struct {
	self     *scanOwnerRetention
	executor *executor
	registry *stmt.Registry
}

func scanOwnerRetentionFor(w *executor, registry *stmt.Registry) (*scanOwnerRetention, error) {
	if w == nil || registry == nil {
		return nil, missingScanOwnerRetention("scan_owner_retention")
	}
	// The actual closed providers contain no run/job owner DELETE, including
	// cascades from an owner. Receipt-header cleanup preserves the run anchor.
	// Exact definitions, not a SQL substring/verb heuristic, are the boundary.
	allowed := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	allowed = append(allowed, effectiveDispatchDefinitions()...)
	allowed = append(allowed, inventoryCommandDefinitions()...)
	allowed = append(allowed, scanRunLifecycleDefinitions()...)
	allowed = append(allowed, scanRunLifecycleHistoryDefinitions()...)
	allowed = append(allowed, scanBirthDefinitions()...)
	allowed = append(allowed, scanSourceDefinitions()...)
	allowed = append(allowed, scanBytePreflightDefinitions()...)
	allowed = append(allowed, inventoryLibraryRecordDefinition())
	for _, def := range scanReceiptDefinitions() {
		switch def.ID {
		case scanReceiptLookup, scanReceiptAck, scanReceiptReclaimKey, scanReceiptReclaimHeader:
			allowed = append(allowed, def)
		}
	}
	entries := registry.Manifest()
	if len(entries) == 0 {
		return nil, missingScanOwnerRetention("scan_owner_retention")
	}
	for _, entry := range entries {
		if !scanOwnerRetentionID(entry.ID) {
			return nil, missingScanOwnerRetention("scan_owner_retention_definition")
		}
		matched := false
		for _, definition := range allowed {
			if entry.ID == definition.ID && sameNormalizedStatementDefinition(entry.Definition, definition) {
				matched = true
				break
			}
		}
		if !matched {
			return nil, missingScanOwnerRetention("scan_owner_retention_definition")
		}
		if _, err := registry.Lookup(entry.ID); err != nil {
			return nil, err
		}
	}
	p := &scanOwnerRetention{executor: w, registry: registry}
	p.self = p
	return p, nil
}

// Deliberately independent of provider growth: adding an ID to a provider
// cannot automatically preserve this creation-only absence exception.
func scanOwnerRetentionID(id stmt.ID) bool {
	for _, ordinary := range inventoryFreshIDs() {
		if id == ordinary {
			return true
		}
	}
	switch id {
	case scanInventoryLibraryRecordLengths, scanRunByteLengths, scanJobByteLengths, scanRootByteLengths, scanReceiptByteLengths, scanMemberByteLengths, scanReceiptOwnerPrefix, scanReceiptIdentityPrefix, scanMemberKeys,
		enrichmentEffectiveOwner, enrichmentEffectiveReceipt, enrichmentEffectiveEntity,
		enrichmentEffectiveInsert, enrichmentEffectiveAttach, enrichmentEffectiveRecord,
		enrichmentEffectiveLibrary, enrichmentEffectiveLanguage, enrichmentEffectivePolicy,
		enrichmentEffectiveProvider, enrichmentEffectiveConsent, enrichmentEffectiveSelection,
		enrichmentEffectiveEvidence, enrichmentEffectiveSubject, enrichmentEffectivePublication,
		enrichmentEffectiveCreditHead, enrichmentEffectiveCapture, enrichmentEffectiveAssociation,
		enrichmentEffectiveAsset, enrichmentEffectiveObject, enrichmentEffectiveSource,
		effectiveDisclosureLibrary, effectiveDisclosureSubject, effectiveReceiptAck,
		inventoryDisclosureJob, inventoryDisclosureLibrary, inventoryFreshLibrary,
		scanReceiptOwner, scanReceiptLookup, scanReceiptResults, scanReceiptAck,
		scanReceiptReclaimKey, scanReceiptReclaimHeader,
		scanRunHistoryOwner, scanRunHistoryBody,
		scanBirthRun, scanBirthJob, scanBirthOperation, scanBirthActive,
		scanSourceLengths, scanSourcePayload, scanSourcePolicy, scanLibraryRootLengths, scanLibraryRoot,
		scanLifecycleRun, scanLifecycleJob, scanLifecycleRoot, scanLifecycleActive,
		scanLifecycleInsertJob, scanLifecycleInsertRun, scanLifecycleInsertRoot, scanLifecycleInsertActive,
		scanLifecycleClaimNull, scanLifecycleClaimDue, scanLifecycleClaimJob, scanLifecycleReceiptInsert:
		return true
	default:
		return false
	}
}

func (p *scanOwnerRetention) check(w *executor, registry *stmt.Registry) error {
	if p == nil || p.self != p || p.executor != w || p.registry != registry {
		return missingScanOwnerRetention("scan_owner_retention")
	}
	return nil
}

// Compare the registry's own normalized value representation. Describe changes
// zero-arity Params/Bindings from nil to empty slices; it does not authorize SQL
// or supply a Capture receipt. Every byte of SQL and all other frozen metadata
// still participate in the exact comparison, and Lookup above proves Capture.
func sameNormalizedStatementDefinition(actual, expected stmt.Definition) bool {
	catalogue, err := stmt.Describe([]stmt.Definition{expected})
	if err != nil {
		return false
	}
	entries := catalogue.Manifest()
	return len(entries) == 1 && reflect.DeepEqual(actual, entries[0].Definition)
}

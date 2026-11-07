package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Fixed domain inventory plus the existing native library/retained-subject
// disclosure points. No new SQL, dynamic registration or worker budget.
func searchPublisherCommandDefinitions(purpose CommandID) []stmt.Definition {
	var defs []stmt.Definition
	switch purpose {
	case SearchFragmentStage:
		defs = searchFragmentDefinitions()
	case SearchHeadPublish:
		defs = searchHeadDefinitions()
	default:
		return nil
	}
	for _, d := range effectiveDispatchDefinitions() {
		if d.ID == effectiveDisclosureLibrary || d.ID == effectiveDisclosureSubject {
			defs = append(defs, d)
		}
	}
	return defs
}

func searchPublisherMaxUses(purpose CommandID, id stmt.ID) int {
	if !searchPublisherPurpose(purpose) {
		return 0
	}
	found := false
	switch purpose {
	case SearchFragmentStage:
		switch id {
		case "search.publish.ack.publisher",
			"search.publish.cas.fragment_head_due",
			"search.publish.cas.fragment_head_no_due",
			"search.publish.cas.fragment_no_head_due",
			"search.publish.cas.fragment_no_head_no_due",
			"search.publish.cas.fragment_seal_totals",
			"search.publish.fragment_stage.fragment",
			"search.publish.fragment_stage.grams",
			"search.publish.fragment_stage.lexical",
			"search.publish.fragment_stage.prefix",
			"search.publish.owner.fragment_held_custody",
			"search.publish.owner.fragment_image",
			"search.publish.owner.fragment_seal",
			"search.publish.owner.image_bank",
			"search.publish.owner.image_heads",
			"search.publish.owner.image_job",
			"search.publish.owner.image_library",
			"search.publish.owner.image_source",
			"search.publish.owner.image_subject",
			"search.publish.owner.receipt_job",
			"search.publish.receipt.fragment_insert",
			"search.publish.receipt.fragment_result",
			"search.publish.receipt.identity",
			"enrichment.effective_stage.disclosure_library",
			"enrichment.effective_stage.disclosure_subject":
			found = true
		}
	case SearchHeadPublish:
		switch id {
		case "search.publish.ack.publisher",
			"search.publish.cas.head_clock_dual",
			"search.publish.cas.head_clock_only",
			"search.publish.cas.head_has_head_due",
			"search.publish.cas.head_has_head_no_due",
			"search.publish.cas.head_no_head_due",
			"search.publish.cas.head_no_head_no_due",
			"search.publish.cas.head_pending",
			"search.publish.head.no_next_fragment",
			"search.publish.head.old_custody",
			"search.publish.head.own_delete",
			"search.publish.head.own_insert",
			"search.publish.head.own_tokens",
			"search.publish.head.pending",
			"search.publish.head.serving_revision",
			"search.publish.head.wake_due",
			"search.publish.head.wake_no_due",
			"search.publish.owner.fragment_held_custody",
			"search.publish.owner.fragment_image",
			"search.publish.owner.fragment_seal",
			"search.publish.owner.image_bank",
			"search.publish.owner.image_heads",
			"search.publish.owner.image_job",
			"search.publish.owner.image_library",
			"search.publish.owner.image_source",
			"search.publish.owner.image_subject",
			"search.publish.owner.receipt_job",
			"search.publish.receipt.head_insert",
			"search.publish.receipt.head_result",
			"search.publish.receipt.identity",
			"enrichment.effective_stage.disclosure_library",
			"enrichment.effective_stage.disclosure_subject":
			found = true
		}
	}
	if !found {
		return 0
	}
	switch id {
	case searchPublisherOwnerID:
		return 3 // current native scope, original lookup, fresh apply's lookup
	case searchPublisherIdentity:
		return 2 // original lookup and fresh apply's independent original lookup
	case searchImageHeads:
		return 2 // same input/head pair around the native captured vector
	case searchHeadOwnRead:
		return 2 // new and retained old native header
	default:
		return 1
	}
}

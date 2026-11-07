package writer

// CommandID is a compiled purpose, never a caller-provided priority or SQL key.
// A descriptor is metadata only: it neither authorizes nor qualifies execution.
type CommandID uint16

const (
	InventoryAccept CommandID = iota + 1
	BasicAccept
	EvidenceStage
	CreditStage
	PeopleStage
	EnrichAccept
	ProgressAccept
	SecurityAccept
	SearchFragmentStage
	SearchHeadPublish
	SearchFragmentReclaim
	SearchImageReclaim
	SearchGenerationAdvance
	SearchGenerationFlip
	PeopleHeadPublish
	SearchImageStage
	SearchSeal
	EffectiveStage
	InventoryCreate
	InventoryClaim
	BasicStage
	BasicSeal
	EvidenceSeal
	CreditSourceSeal
	ProgressReceiptReclaim
)

type Durability uint8

const (
	Normal Durability = iota + 1
	Full
)

type Descriptor struct {
	ID         CommandID
	Name       string
	Durability Durability
}

// Describe is a closed registry. Qualification budgets are recipe/profile-specific
// and must be supplied by the private executor after measurement; they are not
// inferred from a purpose name. No SQL implementation is registered here yet.
func Describe(id CommandID) (Descriptor, error) {
	var name string
	switch id {
	case InventoryAccept:
		name = "inventory_accept"
	case BasicAccept:
		name = "basic_accept"
	case EvidenceStage:
		name = "evidence_stage"
	case CreditStage:
		name = "credit_stage"
	case PeopleStage:
		name = "people_stage"
	case EnrichAccept:
		name = "enrich_accept"
	case ProgressReceiptReclaim:
		name = "progress_receipt_reclaim"
	case ProgressAccept:
		name = "progress_accept"
	case SecurityAccept:
		name = "security_accept"
	case SearchFragmentStage:
		name = "search_fragment_stage"
	case SearchHeadPublish:
		name = "search_head_publish"
	case SearchFragmentReclaim:
		name = "search_fragment_reclaim"
	case SearchImageReclaim:
		name = "search_image_reclaim"
	case SearchGenerationAdvance:
		name = "search_generation_advance"
	case SearchGenerationFlip:
		name = "search_generation_flip"
	case PeopleHeadPublish:
		name = "people_head_publish"
	case SearchImageStage:
		name = "search_image_stage"
	case SearchSeal:
		name = "search_seal"
	case EffectiveStage:
		name = "effective_stage"
	case InventoryCreate:
		name = "inventory_create"
	case InventoryClaim:
		name = "inventory_claim"
	case BasicStage:
		name = "basic_stage"
	case BasicSeal:
		name = "basic_seal"
	case EvidenceSeal:
		name = "evidence_seal"
	case CreditSourceSeal:
		name = "credit_source_seal"
	default:
		return Descriptor{}, invalid("command")
	}
	d := Normal
	if id == SecurityAccept {
		d = Full
	}
	return Descriptor{ID: id, Name: name, Durability: d}, nil
}

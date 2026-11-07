package writer

// Phase 1a owns IDs 1–99. Existing wire identities are explicit and permanent.
const (
	InventoryAccept            CommandID = 1
	BasicAccept                CommandID = 2
	EvidenceStage              CommandID = 3
	CreditStage                CommandID = 4
	PeopleStage                CommandID = 5
	EnrichAccept               CommandID = 6
	ProgressAccept             CommandID = 7
	SecurityAccept             CommandID = 8
	SearchFragmentStage        CommandID = 9
	SearchHeadPublish          CommandID = 10
	SearchFragmentReclaim      CommandID = 11
	SearchImageReclaim         CommandID = 12
	SearchGenerationAdvance    CommandID = 13
	SearchGenerationFlip       CommandID = 14
	PeopleHeadPublish          CommandID = 15
	SearchImageStage           CommandID = 16
	SearchSeal                 CommandID = 17
	EffectiveStage             CommandID = 18
	InventoryCreate            CommandID = 19
	InventoryClaim             CommandID = 20
	BasicStage                 CommandID = 21
	BasicSeal                  CommandID = 22
	EvidenceSeal               CommandID = 23
	CreditSourceSeal           CommandID = 24
	ProgressReceiptReclaim     CommandID = 25
	InventoryDirectoryDiscover CommandID = 26
	InventoryDirectoryEOF      CommandID = 27
)

var phase1aCommands = commandRegistration{
	owner: "phase1a",
	descriptors: []Descriptor{
		{ID: InventoryAccept, Name: "inventory_accept", Durability: Normal},
		{ID: BasicAccept, Name: "basic_accept", Durability: Normal},
		{ID: EvidenceStage, Name: "evidence_stage", Durability: Normal},
		{ID: CreditStage, Name: "credit_stage", Durability: Normal},
		{ID: PeopleStage, Name: "people_stage", Durability: Normal},
		{ID: EnrichAccept, Name: "enrich_accept", Durability: Normal},
		{ID: ProgressAccept, Name: "progress_accept", Durability: Normal},
		{ID: SecurityAccept, Name: "security_accept", Durability: Full},
		{ID: SearchFragmentStage, Name: "search_fragment_stage", Durability: Normal},
		{ID: SearchHeadPublish, Name: "search_head_publish", Durability: Normal},
		{ID: SearchFragmentReclaim, Name: "search_fragment_reclaim", Durability: Normal},
		{ID: SearchImageReclaim, Name: "search_image_reclaim", Durability: Normal},
		{ID: SearchGenerationAdvance, Name: "search_generation_advance", Durability: Normal},
		{ID: SearchGenerationFlip, Name: "search_generation_flip", Durability: Normal},
		{ID: PeopleHeadPublish, Name: "people_head_publish", Durability: Normal},
		{ID: SearchImageStage, Name: "search_image_stage", Durability: Normal},
		{ID: SearchSeal, Name: "search_seal", Durability: Normal},
		{ID: EffectiveStage, Name: "effective_stage", Durability: Normal},
		{ID: InventoryCreate, Name: "inventory_create", Durability: Normal},
		{ID: InventoryClaim, Name: "inventory_claim", Durability: Normal},
		{ID: BasicStage, Name: "basic_stage", Durability: Normal},
		{ID: BasicSeal, Name: "basic_seal", Durability: Normal},
		{ID: EvidenceSeal, Name: "evidence_seal", Durability: Normal},
		{ID: CreditSourceSeal, Name: "credit_source_seal", Durability: Normal},
		{ID: ProgressReceiptReclaim, Name: "progress_receipt_reclaim", Durability: Normal},
		{ID: InventoryDirectoryDiscover, Name: "inventory_directory_discover", Durability: Normal},
		{ID: InventoryDirectoryEOF, Name: "inventory_directory_eof", Durability: Normal},
	},
}

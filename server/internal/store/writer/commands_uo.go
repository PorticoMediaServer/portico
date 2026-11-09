package writer

// UO owns explicit CommandID constants 600–699.
const SetPersonalFlags CommandID = 600

const ClaimOperationalJob CommandID = 650
const RenewOperationalJob CommandID = 651
const CompleteOperationalJob CommandID = 652
const ReclaimOperationalJob CommandID = 653

var uoCommands = commandRegistration{owner: "UO", descriptors: []Descriptor{
	{ID: SetPersonalFlags, Name: "uo_set_personal_flags", Durability: Normal},
	{ID: ClaimOperationalJob, Name: "uo_claim_operational_job", Durability: Normal},
	{ID: RenewOperationalJob, Name: "uo_renew_operational_job", Durability: Normal},
	{ID: CompleteOperationalJob, Name: "uo_complete_operational_job", Durability: Normal},
	{ID: ReclaimOperationalJob, Name: "uo_reclaim_operational_job", Durability: Normal},
}}

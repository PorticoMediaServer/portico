package writer

// UO owns explicit CommandID constants 600–699.
const SetPersonalFlags CommandID = 600
const BeginPersonalJob CommandID = 607
const StepPersonalJob CommandID = 608
const AdvanceNoticeOperation CommandID = 624
const EnqueueWebhookIntent CommandID = 640
const CompleteWebhookAttempt CommandID = 642
const ApplyServiceOutboxResult CommandID = 643
const ClaimOperationalJob CommandID = 650
const RenewOperationalJob CommandID = 651
const CompleteOperationalJob CommandID = 652
const ReclaimOperationalJob CommandID = 653

var uoCommands = commandRegistration{owner: "UO", descriptors: []Descriptor{
	{ID: SetPersonalFlags, Name: "uo_set_personal_flags", Durability: Normal},
	{ID: BeginPersonalJob, Name: "uo_begin_personal_job", Durability: Normal},
	{ID: StepPersonalJob, Name: "uo_step_personal_job", Durability: Normal},
	{ID: AdvanceNoticeOperation, Name: "uo_advance_notice_operation", Durability: Normal},
	{ID: EnqueueWebhookIntent, Name: "uo_enqueue_webhook_intent", Durability: Normal},
	{ID: CompleteWebhookAttempt, Name: "uo_complete_webhook_attempt", Durability: Normal},
	{ID: ApplyServiceOutboxResult, Name: "uo_apply_service_outbox_result", Durability: Normal},
	{ID: ClaimOperationalJob, Name: "uo_claim_operational_job", Durability: Normal},
	{ID: RenewOperationalJob, Name: "uo_renew_operational_job", Durability: Normal},
	{ID: CompleteOperationalJob, Name: "uo_complete_operational_job", Durability: Normal},
	{ID: ReclaimOperationalJob, Name: "uo_reclaim_operational_job", Durability: Normal},
}}

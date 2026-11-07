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
const RootReceiptReclaim CommandID = 654
const CreateSavedResource CommandID = 655
const PatchSavedResource CommandID = 656
const DeleteSavedResource CommandID = 657
const EditSavedResourceMembers CommandID = 658
const ReorderSavedResource CommandID = 659
const SetSavedResourceSharing CommandID = 660

const SetWatchedIntent CommandID = 601
const DismissContinue CommandID = 602
const ApplyPersonalOfflineEvidence CommandID = 603
const ResolvePersonalConflict CommandID = 604
const ApplyActivityBoundary CommandID = 605
const ClearHistory CommandID = 606
const PatchViewerPreferences CommandID = 610
const PatchServerSettings CommandID = 611
const ApplyConfigurationProjection CommandID = 612
const PublishNotice CommandID = 620
const WithdrawNotice CommandID = 621
const AcknowledgeNotice CommandID = 622
const AcknowledgeInbox CommandID = 623
const ReclaimNotices CommandID = 625
const ClaimWebhookAttempt CommandID = 641

var uoCommands = commandRegistration{owner: "UO", descriptors: []Descriptor{
	// Creation/patch/deletion can change publication or invalidate shared access.
	// These private descriptors conservatively retain FULL security durability;
	// current US authority and the accepting dispatcher remain separate gates.
	{ID: CreateSavedResource, Name: "uo_create_saved_resource", Durability: Full},
	{ID: PatchSavedResource, Name: "uo_patch_saved_resource", Durability: Full},
	{ID: DeleteSavedResource, Name: "uo_delete_saved_resource", Durability: Full},
	{ID: EditSavedResourceMembers, Name: "uo_edit_saved_resource_members", Durability: Normal},
	{ID: ReorderSavedResource, Name: "uo_reorder_saved_resource", Durability: Normal},
	{ID: SetSavedResourceSharing, Name: "uo_set_saved_resource_sharing", Durability: Full},
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
	{ID: RootReceiptReclaim, Name: "uo_root_receipt_reclaim", Durability: Normal},
	{ID: SetWatchedIntent, Name: "uo_set_watched_intent", Durability: Normal},
	{ID: DismissContinue, Name: "uo_dismiss_continue", Durability: Normal},
	{ID: ApplyPersonalOfflineEvidence, Name: "uo_apply_personal_offline_evidence", Durability: Normal},
	{ID: ResolvePersonalConflict, Name: "uo_resolve_personal_conflict", Durability: Normal},
	{ID: ApplyActivityBoundary, Name: "uo_apply_activity_boundary", Durability: Normal},
	{ID: ClearHistory, Name: "uo_clear_history", Durability: Normal},
	{ID: PatchViewerPreferences, Name: "uo_patch_viewer_preferences", Durability: Normal},
	{ID: PatchServerSettings, Name: "uo_patch_server_settings", Durability: Normal},
	{ID: ApplyConfigurationProjection, Name: "uo_apply_configuration_projection", Durability: Normal},
	{ID: PublishNotice, Name: "uo_publish_notice", Durability: Normal},
	{ID: WithdrawNotice, Name: "uo_withdraw_notice", Durability: Normal},
	{ID: AcknowledgeNotice, Name: "uo_acknowledge_notice", Durability: Normal},
	{ID: AcknowledgeInbox, Name: "uo_acknowledge_inbox", Durability: Normal},
	{ID: ReclaimNotices, Name: "uo_reclaim_notices", Durability: Normal},
	{ID: ClaimWebhookAttempt, Name: "uo_claim_webhook_attempt", Durability: Normal},
}}

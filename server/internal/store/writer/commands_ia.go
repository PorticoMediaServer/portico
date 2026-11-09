package writer

// IA owns explicit CommandID constants 100–199. Descriptors are inert metadata:
// they do not register an executing handler, issuer, result codec or admission.
// Private component bodies remain uncalled; descriptors alone admit no turn.
// Refresh Finalize stays closed until the complete IA23 composition exists.
const (
	IAConfigureLibrary CommandID = 100
	IAConfigureSource  CommandID = 101
	IARetireLibrary    CommandID = 102
	IARetireSource     CommandID = 103
	IARetireStep       CommandID = 104
	// IA-10 acknowledges and reclaims one measured receipt unit of a permanent
	// configuration or removal owner. Both are NORMAL: neither grants access to
	// any target and neither can refund a row it did not delete.
	IAConfigureReclaim CommandID = 105
	IARetireReclaim    CommandID = 106
	// A disable revokes current access, so it has its own explicit FULL
	// descriptor. Both descriptors persist the one configure_source result
	// purpose; only durability differs.
	IAConfigureSourceDisable CommandID = 107
	IAAcceptEpisodeInventory CommandID = 110
	IAAcceptAudioInventory   CommandID = 111
	IAAcceptReadingInventory CommandID = 112
	IAAcceptExtraInventory   CommandID = 113
	IAAcceptMembership       CommandID = 114
	IAReconcileObject        CommandID = 120
	IACoverageVerify         CommandID = 121
	IAMarkMissing            CommandID = 122
	IAFinishRescan           CommandID = 123
	IARefreshBegin           CommandID = 124
	IARefreshStep            CommandID = 125
	IARefreshRollback        CommandID = 126
	IARefreshFinalize        CommandID = 127
)

// ConfigureSource is NORMAL: a rename, interval or root change grants nothing.
// Disabling a source revokes current access and runs only on the explicit FULL
// 107 descriptor, so a caller cannot downgrade a denial to NORMAL.
var iaCommands = commandRegistration{owner: "IA", descriptors: []Descriptor{
	{IARefreshBegin, "refresh_begin", Normal},
	{IARefreshStep, "refresh_step", Normal},
	{IARefreshRollback, "refresh_rollback", Normal},
	{IARefreshFinalize, "refresh_finalize", Normal},
	{IAConfigureLibrary, "ia_configure_library", Normal},
	{IAConfigureSource, "ia_configure_source", Normal},
	{IAConfigureSourceDisable, "ia_configure_source_disable", Full},
	{IARetireLibrary, "ia_retire_library", Full},
	{IARetireSource, "ia_retire_source", Full},
	{IARetireStep, "ia_retire_step", Normal},
	{IAConfigureReclaim, "ia_configuration_receipt_reclaim", Normal},
	{IARetireReclaim, "ia_retirement_receipt_reclaim", Normal},
	{IAAcceptEpisodeInventory, "ia_accept_episode_inventory", Normal},
	{IAAcceptAudioInventory, "ia_accept_audio_inventory", Normal},
	{IAAcceptReadingInventory, "ia_accept_reading_inventory", Normal},
	{IAAcceptExtraInventory, "ia_accept_extra_inventory", Normal},
	{IAAcceptMembership, "ia_accept_membership", Normal},
	{IAReconcileObject, "ia_reconcile_object", Normal},
	{IACoverageVerify, "ia_coverage_verify", Normal},
	{IAMarkMissing, "ia_mark_missing", Normal},
	{IAFinishRescan, "ia_finish_rescan", Normal},
}}

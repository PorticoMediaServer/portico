package writer

// IA owns explicit CommandID constants 100–199. Descriptors are inert metadata:
// they do not register an executing handler, issuer, result codec or admission.
const (
	IAConfigureLibrary            CommandID = 100
	IAConfigureSource             CommandID = 101
	IARetireLibrary               CommandID = 102
	IARetireSource                CommandID = 103
	IARetireStep                  CommandID = 104
	IAConfigurationReceiptReclaim CommandID = 105
	IARetirementReceiptReclaim    CommandID = 106
	IAAcceptEpisodeInventory      CommandID = 110
	IAAcceptAudioInventory        CommandID = 111
	IAAcceptReadingInventory      CommandID = 112
	IAAcceptExtraInventory        CommandID = 113
	IAAcceptMembership            CommandID = 114
	IAReconcileObject             CommandID = 120
	IAVerifyCoverage              CommandID = 121
	IAMarkMissing                 CommandID = 122
	IAFinishRescan                CommandID = 123
)

// ConfigureSource includes disable/replacement, so its one static descriptor
// uses FULL for every variant; a caller cannot downgrade a denial action.
var iaCommands = commandRegistration{owner: "IA", descriptors: []Descriptor{
	{IAConfigureLibrary, "ia_configure_library", Normal},
	{IAConfigureSource, "ia_configure_source", Full},
	{IARetireLibrary, "ia_retire_library", Full},
	{IARetireSource, "ia_retire_source", Full},
	{IARetireStep, "ia_retire_step", Normal},
	{IAConfigurationReceiptReclaim, "ia_configuration_receipt_reclaim", Normal},
	{IARetirementReceiptReclaim, "ia_retirement_receipt_reclaim", Normal},
	{IAAcceptEpisodeInventory, "ia_accept_episode_inventory", Normal},
	{IAAcceptAudioInventory, "ia_accept_audio_inventory", Normal},
	{IAAcceptReadingInventory, "ia_accept_reading_inventory", Normal},
	{IAAcceptExtraInventory, "ia_accept_extra_inventory", Normal},
	{IAAcceptMembership, "ia_accept_membership", Normal},
	{IAReconcileObject, "ia_reconcile_object", Normal},
	{IAVerifyCoverage, "ia_verify_coverage", Normal},
	{IAMarkMissing, "ia_mark_missing", Normal},
	{IAFinishRescan, "ia_finish_rescan", Normal},
}}

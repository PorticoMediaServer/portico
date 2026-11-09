package writer

// IA owns explicit CommandID constants 100–199. Descriptors are inert metadata:
// they do not register an executing handler, issuer, result codec or admission.
// Only purposes with declared result codecs and no native body appear here yet.
const (
	IAConfigureLibrary CommandID = 100
	IAConfigureSource  CommandID = 101
	IARetireLibrary    CommandID = 102
	IARetireSource     CommandID = 103
	IARetireStep       CommandID = 104
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
)

// ConfigureSource is NORMAL: a rename, interval or root change grants nothing.
// Disabling a source revokes current access and runs only on the explicit FULL
// 107 descriptor, so a caller cannot downgrade a denial to NORMAL.
var iaCommands = commandRegistration{owner: "IA", descriptors: []Descriptor{
	{IAConfigureLibrary, "ia_configure_library", Normal},
	{IAConfigureSource, "ia_configure_source", Normal},
	{IAConfigureSourceDisable, "ia_configure_source_disable", Full},
	{IARetireLibrary, "ia_retire_library", Full},
	{IARetireSource, "ia_retire_source", Full},
	{IARetireStep, "ia_retire_step", Normal},
	{IAAcceptEpisodeInventory, "ia_accept_episode_inventory", Normal},
	{IAAcceptAudioInventory, "ia_accept_audio_inventory", Normal},
	{IAAcceptReadingInventory, "ia_accept_reading_inventory", Normal},
	{IAAcceptExtraInventory, "ia_accept_extra_inventory", Normal},
	{IAAcceptMembership, "ia_accept_membership", Normal},
	{IAReconcileObject, "ia_reconcile_object", Normal},
}}

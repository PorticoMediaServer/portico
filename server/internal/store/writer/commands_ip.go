package writer

// Only the first owner-edit entry family is declared here. Native handlers,
// profiles and product callers remain uninstalled. Other IP purposes stay shut.
const (
	IPFieldEdit    CommandID = 240
	IPFieldLock    CommandID = 241
	IPFieldRestore CommandID = 242
)

var ipCommands = commandRegistration{owner: "IP", descriptors: []Descriptor{
	{IPFieldEdit, "ip_field_edit", Normal},
	{IPFieldLock, "ip_field_lock", Normal},
	{IPFieldRestore, "ip_field_restore", Normal},
}}

package writer

// IP declares only the provider-policy/sweep family and the owner-edit entry
// family. Native handlers, profiles and product callers remain uninstalled.
// Other IP purposes stay shut.
const (
	IPPolicySet    CommandID = 200
	IPRefreshBegin CommandID = 201

	IPFieldEdit    CommandID = 240
	IPFieldLock    CommandID = 241
	IPFieldRestore CommandID = 242
)

var ipCommands = commandRegistration{owner: "IP", descriptors: []Descriptor{
	{IPPolicySet, "ip_policy_set", Normal},
	{IPRefreshBegin, "ip_refresh_begin", Normal},
	{IPFieldEdit, "ip_field_edit", Normal},
	{IPFieldLock, "ip_field_lock", Normal},
	{IPFieldRestore, "ip_field_restore", Normal},
}}

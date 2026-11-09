package writer

// IP declares the provider-policy/sweep family, the artwork discovery stage
// family and the owner-edit entry family. Native handlers, profiles and product
// callers remain uninstalled. Other IP purposes stay shut.
const (
	IPPolicySet    CommandID = 200
	IPRefreshBegin CommandID = 201
	IPRefreshPage  CommandID = 202

	IPArtworkDiscoveryClaim  CommandID = 220
	IPArtworkDiscoveryPage   CommandID = 221
	IPArtworkDiscoveryFinish CommandID = 222

	IPFieldEdit    CommandID = 240
	IPFieldLock    CommandID = 241
	IPFieldRestore CommandID = 242
)

var ipCommands = commandRegistration{owner: "IP", descriptors: []Descriptor{
	{IPPolicySet, "ip_policy_set", Normal},
	{IPRefreshBegin, "ip_refresh_begin", Normal},
	{IPRefreshPage, "ip_refresh_page", Normal},
	{IPArtworkDiscoveryClaim, "ip_artwork_discovery_claim", Normal},
	{IPArtworkDiscoveryPage, "ip_artwork_discovery_page", Normal},
	{IPArtworkDiscoveryFinish, "ip_artwork_discovery_finish", Normal},
	{IPFieldEdit, "ip_field_edit", Normal},
	{IPFieldLock, "ip_field_lock", Normal},
	{IPFieldRestore, "ip_field_restore", Normal},
}}

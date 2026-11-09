package writer

// IP declares the provider-policy/sweep family, the artwork discovery and
// material custody families and the owner-edit entry family. Native handlers,
// profiles and product callers remain uninstalled. Other IP purposes stay shut.
const (
	IPPolicySet    CommandID = 200
	IPRefreshBegin CommandID = 201
	IPRefreshPage  CommandID = 202
	IPEntityClaim  CommandID = 203
	IPEntityRenew  CommandID = 204
	IPEntityFinish CommandID = 205

	IPEntityEvidenceBegin  CommandID = 210
	IPEntityEvidenceAppend CommandID = 211

	IPArtworkDiscoveryClaim  CommandID = 220
	IPArtworkDiscoveryPage   CommandID = 221
	IPArtworkDiscoveryFinish CommandID = 222

	IPArtworkMaterialIntent    CommandID = 223
	IPArtworkMaterialInstalled CommandID = 224

	IPFieldEdit     CommandID = 240
	IPFieldLock     CommandID = 241
	IPFieldRestore  CommandID = 242
	IPEditBulkBegin CommandID = 243
)

var ipCommands = commandRegistration{owner: "IP", descriptors: []Descriptor{
	{IPPolicySet, "ip_policy_set", Normal},
	{IPRefreshBegin, "ip_refresh_begin", Normal},
	{IPRefreshPage, "ip_refresh_page", Normal},
	{IPEntityClaim, "ip_entity_claim", Normal},
	{IPEntityRenew, "ip_entity_renew", Normal},
	{IPEntityFinish, "ip_entity_finish", Normal},
	{IPEntityEvidenceBegin, "ip_entity_evidence_begin", Normal},
	{IPEntityEvidenceAppend, "ip_entity_evidence_append", Normal},
	{IPArtworkDiscoveryClaim, "ip_artwork_discovery_claim", Normal},
	{IPArtworkDiscoveryPage, "ip_artwork_discovery_page", Normal},
	{IPArtworkDiscoveryFinish, "ip_artwork_discovery_finish", Normal},
	{IPArtworkMaterialIntent, "ip_artwork_material_intent", Normal},
	{IPArtworkMaterialInstalled, "ip_artwork_material_installed", Normal},
	{IPFieldEdit, "ip_field_edit", Normal},
	{IPFieldLock, "ip_field_lock", Normal},
	{IPFieldRestore, "ip_field_restore", Normal},
	{IPEditBulkBegin, "ip_edit_bulk_begin", Normal},
}}

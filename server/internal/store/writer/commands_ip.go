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

	IPPersonClaim         CommandID = 206
	IPPersonRenew         CommandID = 207
	IPPersonFinish        CommandID = 208
	IPPersonEvidenceBegin CommandID = 209

	IPEntityEvidenceBegin  CommandID = 210
	IPEntityEvidenceAppend CommandID = 211
	IPEntitySeal           CommandID = 212

	IPTVSetBegin CommandID = 213
	IPTVSetPage  CommandID = 214
	IPTVSetSeal  CommandID = 215

	IPEntityAccept CommandID = 216

	IPArtworkDiscoveryClaim  CommandID = 220
	IPArtworkDiscoveryPage   CommandID = 221
	IPArtworkDiscoveryFinish CommandID = 222

	IPArtworkMaterialIntent    CommandID = 223
	IPArtworkMaterialInstalled CommandID = 224
	IPArtworkMaterialPublish   CommandID = 225
	IPArtworkSelect            CommandID = 226
	IPArtworkClear             CommandID = 227
	IPArtworkUploadAccept      CommandID = 228
	IPArtworkReencode          CommandID = 231

	IPFieldEdit      CommandID = 240
	IPFieldLock      CommandID = 241
	IPFieldRestore   CommandID = 242
	IPEditBulkBegin  CommandID = 243
	IPEditBulkPage   CommandID = 244
	IPEditBulkFinish CommandID = 245
)

var ipCommands = commandRegistration{owner: "IP", descriptors: []Descriptor{
	{IPPolicySet, "ip_policy_set", Normal},
	{IPRefreshBegin, "ip_refresh_begin", Normal},
	{IPRefreshPage, "ip_refresh_page", Normal},
	{IPEntityClaim, "ip_entity_claim", Normal},
	{IPEntityRenew, "ip_entity_renew", Normal},
	{IPEntityFinish, "ip_entity_finish", Normal},
	{IPPersonClaim, "ip_person_claim", Normal},
	{IPPersonRenew, "ip_person_renew", Normal},
	{IPPersonFinish, "ip_person_finish", Normal},
	{IPPersonEvidenceBegin, "ip_person_evidence_begin", Normal},
	{IPEntityEvidenceBegin, "ip_entity_evidence_begin", Normal},
	{IPEntityEvidenceAppend, "ip_entity_evidence_append", Normal},
	{IPEntitySeal, "ip_entity_seal", Normal},
	{IPTVSetBegin, "ip_tv_set_begin", Normal},
	{IPTVSetPage, "ip_tv_set_page", Normal},
	{IPTVSetSeal, "ip_tv_set_seal", Normal},
	{IPEntityAccept, "ip_entity_accept", Normal},
	{IPArtworkDiscoveryClaim, "ip_artwork_discovery_claim", Normal},
	{IPArtworkDiscoveryPage, "ip_artwork_discovery_page", Normal},
	{IPArtworkDiscoveryFinish, "ip_artwork_discovery_finish", Normal},
	{IPArtworkMaterialIntent, "ip_artwork_material_intent", Normal},
	{IPArtworkMaterialInstalled, "ip_artwork_material_installed", Normal},
	{IPArtworkMaterialPublish, "ip_artwork_material_publish", Normal},
	{IPArtworkSelect, "ip_artwork_select", Normal},
	{IPArtworkClear, "ip_artwork_clear", Normal},
	{IPArtworkUploadAccept, "ip_artwork_upload_accept", Normal},
	{IPArtworkReencode, "ip_artwork_reencode", Normal},
	{IPFieldEdit, "ip_field_edit", Normal},
	{IPFieldLock, "ip_field_lock", Normal},
	{IPFieldRestore, "ip_field_restore", Normal},
	{IPEditBulkBegin, "ip_edit_bulk_begin", Normal},
	{IPEditBulkPage, "ip_edit_bulk_page", Normal},
	{IPEditBulkFinish, "ip_edit_bulk_finish", Normal},
}}

package writer

// IP owns explicit command IDs 200–299; descriptors grant no authority.
const (
	IPPolicySet              CommandID = 200
	IPRefreshRequest         CommandID = 201
	IPRefreshPage            CommandID = 202
	IPProviderClaim          CommandID = 203
	IPProviderRenew          CommandID = 204
	IPProviderFinish         CommandID = 205
	IPEvidenceBegin          CommandID = 210
	IPEvidenceAppend         CommandID = 211
	IPEvidenceSeal           CommandID = 212
	IPSetBegin               CommandID = 213
	IPSetPage                CommandID = 214
	IPSetSeal                CommandID = 215
	IPProviderAccept         CommandID = 216
	IPProviderDelta          CommandID = 217
	IPProviderReclaim        CommandID = 218
	IPArtworkDiscoveryClaim  CommandID = 220
	IPArtworkDiscoveryPage   CommandID = 221
	IPArtworkDiscoveryFinish CommandID = 222
	IPArtworkJobClaim        CommandID = 223
	IPArtworkJobRenew        CommandID = 224
	IPArtworkMaterialPublish CommandID = 225
	IPArtworkSelect          CommandID = 226
	IPArtworkClear           CommandID = 227
	IPArtworkUploadAccept    CommandID = 228
	IPArtworkGCMark          CommandID = 229
	IPArtworkGCFinish        CommandID = 230
	IPArtworkReencode        CommandID = 231
	IPFieldEdit              CommandID = 240
	IPFieldLock              CommandID = 241
	IPFieldRestore           CommandID = 242
	IPEditBulkBegin          CommandID = 243
	IPEditBulkPage           CommandID = 244
	IPEditBulkFinish         CommandID = 245
	IPReceiptACK             CommandID = 250
	IPReceiptReclaim         CommandID = 251
	IPAbandonedReclaim       CommandID = 252
)

var ipCommands = commandRegistration{owner: "IP", descriptors: []Descriptor{
	{IPPolicySet, "ip_policy_set", Normal},
	{IPRefreshRequest, "ip_refresh_request", Normal},
	{IPRefreshPage, "ip_refresh_page", Normal},
	{IPProviderClaim, "ip_provider_claim", Normal},
	{IPProviderRenew, "ip_provider_renew", Normal},
	{IPProviderFinish, "ip_provider_finish", Normal},
	{IPEvidenceBegin, "ip_evidence_begin", Normal},
	{IPEvidenceAppend, "ip_evidence_append", Normal},
	{IPEvidenceSeal, "ip_evidence_seal", Normal},
	{IPSetBegin, "ip_set_begin", Normal},
	{IPSetPage, "ip_set_page", Normal},
	{IPSetSeal, "ip_set_seal", Normal},
	{IPProviderAccept, "ip_provider_accept", Normal},
	{IPProviderDelta, "ip_provider_delta", Normal},
	{IPProviderReclaim, "ip_provider_reclaim", Normal},
	{IPArtworkDiscoveryClaim, "ip_artwork_discovery_claim", Normal},
	{IPArtworkDiscoveryPage, "ip_artwork_discovery_page", Normal},
	{IPArtworkDiscoveryFinish, "ip_artwork_discovery_finish", Normal},
	{IPArtworkJobClaim, "ip_artwork_job_claim", Normal},
	{IPArtworkJobRenew, "ip_artwork_job_renew", Normal},
	{IPArtworkMaterialPublish, "ip_artwork_material_publish", Normal},
	{IPArtworkSelect, "ip_artwork_select", Normal},
	{IPArtworkClear, "ip_artwork_clear", Normal},
	{IPArtworkUploadAccept, "ip_artwork_upload_accept", Normal},
	{IPArtworkGCMark, "ip_artwork_gc_mark", Normal},
	{IPArtworkGCFinish, "ip_artwork_gc_finish", Normal},
	{IPArtworkReencode, "ip_artwork_reencode", Normal},
	{IPFieldEdit, "ip_field_edit", Normal},
	{IPFieldLock, "ip_field_lock", Normal},
	{IPFieldRestore, "ip_field_restore", Normal},
	{IPEditBulkBegin, "ip_edit_bulk_begin", Normal},
	{IPEditBulkPage, "ip_edit_bulk_page", Normal},
	{IPEditBulkFinish, "ip_edit_bulk_finish", Normal},
	{IPReceiptACK, "ip_receipt_ack", Normal},
	{IPReceiptReclaim, "ip_receipt_reclaim", Normal},
	{IPAbandonedReclaim, "ip_abandoned_reclaim", Normal},
}}

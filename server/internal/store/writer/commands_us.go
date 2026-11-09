package writer

// US owns explicit 500–599 identities. Descriptors are preparation metadata,
// never permission, a governor grant or physical qualification.
const (
	usMemberRoleChange     CommandID = 501
	usMembershipDisable    CommandID = 502
	usAccountEpochAdvance  CommandID = 503
	usLocalPasswordReplace CommandID = 520
	usAPICredentialIssue   CommandID = 521
	usAPICredentialRevoke  CommandID = 522
	usPINSet               CommandID = 532
	usPINClear             CommandID = 533
	USMemberLibraries      CommandID = 540
	USProfileLibraries     CommandID = 541
	USProfileRestrictions  CommandID = 542
)

var usCommands = commandRegistration{owner: "US", descriptors: []Descriptor{
	{ID: usMemberRoleChange, Name: "us_member_role_change", Durability: Full},
	{ID: usMembershipDisable, Name: "us_membership_disable", Durability: Full},
	{ID: usAccountEpochAdvance, Name: "us_account_epoch_advance", Durability: Full},
	{ID: usLocalPasswordReplace, Name: "us_local_password_replace", Durability: Full},
	{ID: usAPICredentialIssue, Name: "us_api_credential_issue", Durability: Full},
	{ID: usAPICredentialRevoke, Name: "us_api_credential_revoke", Durability: Full},
	{ID: usPINSet, Name: "us_profile_pin_set", Durability: Full},
	{ID: usPINClear, Name: "us_profile_pin_clear", Durability: Full},
	{ID: USMemberLibraries, Name: "us_member_libraries", Durability: Full},
	{ID: USProfileLibraries, Name: "us_profile_libraries", Durability: Full},
	{ID: USProfileRestrictions, Name: "us_profile_restrictions", Durability: Full},
}}

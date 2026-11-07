package writer

// US owns explicit 500–599 identities. Descriptors are preparation metadata,
// never permission, a governor grant or physical qualification.
const (
	usLocalPasswordReplace CommandID = 520
	usAPICredentialIssue   CommandID = 521
	usAPICredentialRevoke  CommandID = 522
	usPINSet               CommandID = 532
	usPINClear             CommandID = 533
	USMemberLibraries      CommandID = 540
	USProfileLibraries     CommandID = 541
)

var usCommands = commandRegistration{owner: "US", descriptors: []Descriptor{
	{ID: usLocalPasswordReplace, Name: "us_local_password_replace", Durability: Full},
	{ID: usAPICredentialIssue, Name: "us_api_credential_issue", Durability: Full},
	{ID: usAPICredentialRevoke, Name: "us_api_credential_revoke", Durability: Full},
	{ID: usPINSet, Name: "us_profile_pin_set", Durability: Full},
	{ID: usPINClear, Name: "us_profile_pin_clear", Durability: Full},
	{ID: USMemberLibraries, Name: "us_member_libraries", Durability: Full},
	{ID: USProfileLibraries, Name: "us_profile_libraries", Durability: Full},
}}

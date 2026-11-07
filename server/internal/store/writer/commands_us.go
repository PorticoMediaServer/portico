package writer

// US owns explicit 500–599 identities. Descriptors are preparation metadata,
// never permission, a governor grant or physical qualification.
const (
	usPINSet           CommandID = 532
	usPINClear         CommandID = 533
	usInvitationCreate CommandID = 550
	usInvitationEdit   CommandID = 551
	usInvitationRevoke CommandID = 552
	USMemberLibraries  CommandID = 540
	USProfileLibraries CommandID = 541
)

var usCommands = commandRegistration{owner: "US", descriptors: []Descriptor{
	{ID: usPINSet, Name: "us_profile_pin_set", Durability: Full},
	{ID: usPINClear, Name: "us_profile_pin_clear", Durability: Full},
	{ID: usInvitationCreate, Name: "us_invitation_create", Durability: Full},
	{ID: usInvitationEdit, Name: "us_invitation_edit", Durability: Full},
	{ID: usInvitationRevoke, Name: "us_invitation_revoke", Durability: Full},
	{ID: USMemberLibraries, Name: "us_member_libraries", Durability: Full},
	{ID: USProfileLibraries, Name: "us_profile_libraries", Durability: Full},
}}

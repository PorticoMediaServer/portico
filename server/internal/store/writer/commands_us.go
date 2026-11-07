package writer

// US owns explicit 500–599 identities. Descriptors are preparation metadata,
// never permission, a governor grant or physical qualification.
const (
	usMemberRoleChange    CommandID = 501
	usMembershipDisable   CommandID = 502
	usAccountEpochAdvance CommandID = 503
	usSessionRenewal      CommandID = 511
	usSessionRefresh      CommandID = 512
	usPINSet              CommandID = 532
	usPINClear            CommandID = 533
	usInvitationCreate    CommandID = 550
	usInvitationEdit      CommandID = 551
	usInvitationRevoke    CommandID = 552
	USMemberLibraries     CommandID = 540
	USProfileLibraries    CommandID = 541
)

var usCommands = commandRegistration{owner: "US", descriptors: []Descriptor{
	{ID: usMemberRoleChange, Name: "us_member_role_change", Durability: Full},
	{ID: usMembershipDisable, Name: "us_membership_disable", Durability: Full},
	{ID: usAccountEpochAdvance, Name: "us_account_epoch_advance", Durability: Full},
	{ID: usSessionRenewal, Name: "us_session_renewal", Durability: Full},
	{ID: usSessionRefresh, Name: "us_session_refresh", Durability: Full},
	{ID: usPINSet, Name: "us_profile_pin_set", Durability: Full},
	{ID: usPINClear, Name: "us_profile_pin_clear", Durability: Full},
	{ID: usInvitationCreate, Name: "us_invitation_create", Durability: Full},
	{ID: usInvitationEdit, Name: "us_invitation_edit", Durability: Full},
	{ID: usInvitationRevoke, Name: "us_invitation_revoke", Durability: Full},
	{ID: USMemberLibraries, Name: "us_member_libraries", Durability: Full},
	{ID: USProfileLibraries, Name: "us_profile_libraries", Durability: Full},
}}

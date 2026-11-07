package writer

// US owns explicit 500–599 identities. Descriptors are preparation metadata,
// never permission, a governor grant or physical qualification.
const (
	usSignInPolicyReplace     CommandID = 544
	usMemberLimitsEdit        CommandID = 543
	usProfileRestrictionsEdit CommandID = 542
	usSetupCode               CommandID = 561
	usSetupOutsideAdmission   CommandID = 562
	usHostedChallengeIssue    CommandID = 570
	usHostedChallengeSpend    CommandID = 571
	usHostedProfileAccept     CommandID = 574
	usLocalPasswordReplace    CommandID = 520
	usAPICredentialIssue      CommandID = 521
	usAPICredentialRevoke     CommandID = 522
	usProfileCreate           CommandID = 530
	usProfileMetadataEdit     CommandID = 531
	usMemberRoleChange        CommandID = 501
	usMembershipDisable       CommandID = 502
	usAccountEpochAdvance     CommandID = 503
	usSessionRenewal          CommandID = 511
	usSessionRefresh          CommandID = 512
	usPINSet                  CommandID = 532
	usPINClear                CommandID = 533
	usInvitationCreate        CommandID = 550
	usInvitationEdit          CommandID = 551
	usInvitationRevoke        CommandID = 552
	USMemberLibraries         CommandID = 540
	USProfileLibraries        CommandID = 541
)

var usCommands = commandRegistration{owner: "US", descriptors: []Descriptor{
	{ID: usSignInPolicyReplace, Name: "us_sign_in_policy_replace", Durability: Full},
	{ID: usMemberLimitsEdit, Name: "us_member_limits_edit", Durability: Full},
	{ID: usProfileRestrictionsEdit, Name: "us_profile_restrictions_edit", Durability: Full},
	{ID: usSetupCode, Name: "us_setup_code", Durability: Full},
	{ID: usSetupOutsideAdmission, Name: "us_setup_outside_admission", Durability: Full},
	{ID: usHostedChallengeIssue, Name: "us_hosted_challenge_issue", Durability: Full},
	{ID: usHostedChallengeSpend, Name: "us_hosted_challenge_spend", Durability: Full},
	{ID: usHostedProfileAccept, Name: "us_hosted_profile_accept", Durability: Full},
	{ID: usLocalPasswordReplace, Name: "us_local_password_replace", Durability: Full},
	{ID: usAPICredentialIssue, Name: "us_api_credential_issue", Durability: Full},
	{ID: usAPICredentialRevoke, Name: "us_api_credential_revoke", Durability: Full},
	{ID: usProfileCreate, Name: "us_profile_create", Durability: Full},
	{ID: usProfileMetadataEdit, Name: "us_profile_metadata_edit", Durability: Full},
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

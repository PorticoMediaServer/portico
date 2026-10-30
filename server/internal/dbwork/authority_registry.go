package dbwork

// AuthorityEntry records the actual current cache fence and its owning proof.
// A table in this registry is not a new lifetime token or authorization grant.
type AuthorityEntry struct {
	Table, Root, Lifetime, Authorizer, Retirement, Consumers string
	CacheInvalidates                                         bool
	EnablementBlocker                                        string
}

// AuthorityRegistry returns independent descriptors. Cache invalidation is
// intentionally overinclusive, exactly as before this registry was introduced.
// Missing generic mutation/observer lifetime seams remain named blockers.
func AuthorityRegistry() []AuthorityEntry {
	var out []AuthorityEntry
	add := func(tables []string, root, lifetime, authorizer, retirement, consumers, blocker string) {
		for _, table := range tables {
			out = append(out, AuthorityEntry{table, root, lifetime, authorizer, retirement, consumers, true, blocker})
		}
	}
	add([]string{"libraries", "library_sources"}, "library/source", "source incarnation + generation/root identity", "identity.DirectAccess; catalog.InventoryFenceTx", "library retirement/source disable and configuration generation", "access library cache; playback source checks; inventory publish", "full per-stream root-generation closure required before new observer roots")
	add([]string{"accounts", "direct_memberships"}, "account/membership", "account epoch; membership incarnation/revision", "identity.ReauthorizeTx; checkDirectPrincipalTx", "disable/delete; family revocation hooks; restore scrub", "principal/library caches; transaction authorizers; byte-stream guards", "")
	add([]string{"direct_profiles", "direct_profile_trust"}, "profile/PIN trust", "non-reusable profile lifetime; profile revision/pin_revision; device trust revision", "identity.checkDirectPrincipalTx; profile restriction/PIN checks", "deleted root or PIN revision retires trust", "principal/restriction caches; reader/playback profile checks", "")
	add([]string{"restrictions", "profile_restrictions", "restriction_outbox"}, "profile restrictions", "restriction and profile revisions", "identity.WithProfileRestrictions; DirectAccess", "policy/restriction publication and outbox", "library/item visibility; playback/read/download guards", "")
	add([]string{"policy", "library_network_policy", "playback_owner_policy", "dvr_private_libraries"}, "access/playback policy", "current policy/restriction/source revisions", "access policy intersection; identity.DirectAccess", "policy update/private-library retirement", "cached access decisions; parked stream and download rechecks", "")
	add([]string{"identity_installations", "user_mutation_roots"}, "P251 installation lifetime", "server-generated installation/profile incarnation and registration CAS", "identity mutation lifetime owner checks; P251 current binding", "profile/family/device retirement; restore lifetime rotation", "P251 receipt replay and authenticated registration", "")
	add([]string{"authorization_session_families", "authorization_family_tokens", "authorization_family_renewals"}, "authorization family", "random family ID; token generation/expiry; authorization horizon", "identity.ReauthorizeTx and authorization_access", "RevokeFamilyTx hooks; explicit signout/password change/restore", "principal cache; playback/queue/reader family retirement", "")
	add([]string{"onboarding_state_v1", "quick_connect_v1"}, "enrollment", "bounded expiring challenge/code and one-time state", "identity enrollment/quick-connect accepting transactions", "consumption/cancel/expiry; approver deletion", "sign-in/enrollment; principal caches", "")
	add([]string{"api_credentials"}, "API credential", "credential ID/digest, scope set, own expiry/revocation", "httpapi scope gate + identity.WithAPICredential + ReauthorizeTx", "explicit revoke/expiry/member disable or deletion; password change and signout preserve it", "credential scope gate; family hooks and current account/profile policy", "new generic roots must distinguish credential lifetime from session password lifetime")
	add([]string{"identity_account_factors", "identity_browser_accounts"}, "sign-in factors/remembered accounts", "account/factor and browser-account generations", "identity current account/factor checks", "factor/account retirement; restore sign-in scrub", "sign-in and reauthentication", "")
	add([]string{"hosted_profile_revisions", "hosted_profile_erasure_receipts", "social_group_members"}, "hosted/social authority", "hosted revision/erasure receipt; current membership", "hosted/profile and social membership owners", "existing hosted erasure/claim cleanup owns completion", "hosted restrictions and social audience caches", "local erasure cannot certify hosted erasure; generic hosted root mapping absent")
	add([]string{"download_grants"}, "download capability", "exact grant/preparation and live authority generation", "download grant readers + current identity/source policy", "grant revocation/deletion", "open download/reading body guards", "")
	// These roots have current transaction/domain fences; they deliberately do
	// not join the broad cache-head bump set (playback progress is frequent).
	for _, e := range []AuthorityEntry{
		{Table: "authorization_family_grants", Root: "family grant", Lifetime: "family ID + grant kind", Authorizer: "identity.ReauthorizeTx authorization_access join", Retirement: "family revocation and current grant owner", Consumers: "token/family transaction authentication", EnablementBlocker: "class/trigger closure required before generic root issuance"},
		{Table: "identity_devices", Root: "device", Lifetime: "random device ID and current family binding", Authorizer: "identity device/family authentication", Retirement: "device/family revoke and member erasure", Consumers: "remembered sign-in and active families"},
		{Table: "identity_device_families", Root: "device/family binding", Lifetime: "exact device and family tuple", Authorizer: "identity family/device check", Retirement: "family/device revoke", Consumers: "session renewal and device inventory"},
		{Table: "identity_refresh_credentials", Root: "refresh credential", Lifetime: "credential/family rotation generation", Authorizer: "identity refresh/rotation transaction", Retirement: "reuse revokes family; explicit revoke", Consumers: "session refresh"},
		{Table: "identity_move_waiting", Root: "account-move waiting", Lifetime: "account epoch + move ID/state/expiry", Authorizer: "identity.checkDirectPrincipalTx and move frontdoor", Retirement: "move completion/cancel/expiry", Consumers: "direct sign-in and move continuation", EnablementBlocker: "not a reusable installation lifetime"},
		{Table: "networking_server_identities", Root: "server identity", Lifetime: "immutable server identity", Authorizer: "networking and family server-ID equality", Retirement: "restore/identity owner", Consumers: "current connection and family audience"},
		{Table: "networking_claim_identity", Root: "server claim", Lifetime: "current claim identity", Authorizer: "networking claim owner", Retirement: "claim cleanup and restore", Consumers: "hosted enrollment/claim cleanup"},
		{Table: "playback_source_pins", Root: "accepted playback source", Lifetime: "session/asset size and modified witness", Authorizer: "playback source acceptance/serve fences", Retirement: "session/family/source retirement", Consumers: "parked media/reading bodies", EnablementBlocker: "complete per-stream root-generation closure remains required"},
		{Table: "playback_delivery_plans", Root: "playback delivery", Lifetime: "session/generation and current plan", Authorizer: "playback current-viewer/source policy", Retirement: "session/family/source retirement", Consumers: "media bodies and controller reads"},
		{Table: "download_preparation_authority", Root: "download preparation authority", Lifetime: "exact preparation and authority witness", Authorizer: "download preparation/grant checks", Retirement: "profile erase deletes authority; grant revoke", Consumers: "download/reading descriptor and body checks"},
	} {
		out = append(out, e)
	}
	return out
}

func registeredAuthorityTables() []string {
	entries := AuthorityRegistry()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.CacheInvalidates {
			out = append(out, e.Table)
		}
	}
	return out
}

// AuthorityEnablementBlockers lists seams which do not exist in today's SQL
// handlers. In particular no registry entry fabricates generic receipt roots.
func AuthorityEnablementBlockers() []string {
	return []string{
		"account/Hosted retirement completion remains owned separately from the local-profile typed route",
		"P251 profile/installation lifetimes and registration CAS are domain-bound; hosted lifetime mapping remains separate",
		"stream/observer root-generation closure beyond local-profile retirement is not qualified",
		"Keys, portico.toml, certificates and sealed configuration lack a source-complete monotone file-generation registry",
		"trigger-induced and class-only authority effects still require per-operation closure for new root issuance",
	}
}

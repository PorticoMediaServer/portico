package api

// Sign-in, accounts, profiles, members, invitations and keys.
func init() {
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		return []retryCase{
			{route: "POST /v1/profiles", body: ProfileCreate{Name: "Guest"}, other: ProfileCreate{Name: "Other"},
				count: func() int { return th.named("/v1/profiles", "name", "Guest") }},
			{route: "POST /v1/admin/members", body: MemberCreate{Username: "sam", Password: testPassword},
				other: MemberCreate{Username: "alex", Password: testPassword},
				count: func() int { return th.named("/v1/admin/members", "username", "sam") }},
			{route: "POST /v1/admin/invitations", body: InvitationCreate{Note: "for Sam"}, other: InvitationCreate{Note: "for Alex"},
				count: func() int { return th.named("/v1/admin/invitations", "note", "for Sam") }},
			{route: "POST /v1/admin/api-keys", body: KeyCreate{Name: "Tool", Scopes: []string{"read"}},
				other: KeyCreate{Name: "Tool", Scopes: []string{"admin"}},
				count: func() int { return th.named("/v1/admin/api-keys", "name", "Tool") }},
			{route: "POST /v1/me/tokens", body: KeyCreate{Name: "Script", Scopes: []string{"read"}},
				other: KeyCreate{Name: "Other", Scopes: []string{"read"}},
				count: func() int { return th.named("/v1/me/tokens", "name", "Script") }},
			{route: "POST /v1/me/app-passwords", body: KeyCreate{Name: "Old TV"}, other: KeyCreate{Name: "Other"},
				count: func() int { return th.named("/v1/me/app-passwords", "name", "Old TV") }},
		}
	})
	safeToRepeat(map[string]string{
		"POST /v1/auth/remembered":                   "reads the accounts named by device-held tokens; changes nothing",
		"POST /v1/auth/owner-reset":                  "one reset code file, rate limited; a repeat keeps the same pending reset",
		"POST /v1/auth/owner-reset/confirm":          "uses up its reset code: a repeat is refused",
		"POST /v1/setup/owner":                       "happens once: a repeat is told the server is set up",
		"POST /v1/auth/sign-in":                      "a lost answer leaves a sign-in nobody holds, which expires",
		"POST /v1/auth/two-factor":                   "the second step of a sign-in: as sign-in",
		"POST /v1/auth/refresh":                      "carries its own request id: a repeat gets the same tokens",
		"POST /v1/auth/password":                     "sets the password",
		"POST /v1/auth/password-reset/confirm":       "uses up its code: a repeat is refused",
		"POST /v1/auth/codes":                        "a lost answer leaves a TV code nobody holds, which expires",
		"POST /v1/auth/codes/{id}/approve":           "sets the code's state",
		"POST /v1/auth/codes/{id}/redeem":            "uses up its code: a repeat is refused",
		"POST /v1/auth/invitations/{id}/accept":      "a repeat with the same account and password signs in to that account",
		"POST /v1/me/two-factor":                     "starts two-step sign-in again, replacing what was not confirmed",
		"POST /v1/me/two-factor/confirm":             "sets the state",
		"POST /v1/me/two-factor/recovery-codes":      "replaces the codes; the answer is the only copy either way",
		"POST /v1/profiles/{id}/select":              "sets the device's profile",
		"POST /v1/admin/members/{id}/password-reset": "replaces the member's reset code",
		"POST /v1/system/identity-proof":             "changes nothing",
	})
}

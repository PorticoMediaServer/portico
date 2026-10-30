package identity

// bcrypt work factors, here and in Hosted (Justin, 23 Sep): cost 10 for every
// password, cost 8 for every profile PIN. A PIN is an informal second gate behind
// an account sign-in, not a credential on its own. bcrypt salts every hash with a
// random 128-bit salt, and the cost is stored in the hash, so hashes made at
// older costs still verify.
const (
	PasswordCost = 10
	PINCost      = 8
)

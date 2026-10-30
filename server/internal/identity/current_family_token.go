package identity

// CurrentFamilyTokenSQL returns a SQL expression for the current token hash of
// the session family that issued the stored token hash in column. Accepted
// asynchronous work (a transcode, a burn-in input, a stream count) records the
// token that started it; a routine refresh rotates that token within the same
// durable family, and the authorization_access view then marks the stored
// generation revoked. Liveness checks resolve through the family so rotation
// keeps the work alive, while a revoked family, a retired current token or an
// expired current token still ends it. A hash outside any family is kept as is.
// column must be a trusted, fixed SQL column reference, never caller input.
func CurrentFamilyTokenSQL(column string) string {
	return `COALESCE((SELECT current_token.token_hash FROM authorization_family_tokens stored_token
 JOIN authorization_session_families token_family ON token_family.id=stored_token.family_id
 JOIN authorization_family_tokens current_token ON current_token.family_id=token_family.id AND current_token.generation=token_family.current_generation
 WHERE stored_token.token_hash=` + column + `),` + column + `)`
}

package buildinfo

// Values are embedded by the controlled release builder. Development builds never claim a source identity.
var Version = "0.1.0-dev"
var BuildID = "development"
var SourceDigest = "unsealed"
var BuiltAt = ""
var Proof = "unsealed"

func Info() map[string]string {
	return map[string]string{"version": Version, "buildId": BuildID, "sourceDigest": SourceDigest, "builtAt": BuiltAt, "proof": Proof}
}

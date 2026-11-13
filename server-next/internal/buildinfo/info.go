// Package buildinfo holds the identity embedded by the controlled release builder.
package buildinfo

var Version = "0.0.0-dev"
var BuildID = "development"
var SourceDigest = "unsealed"
var BuiltAt = ""
var Proof = "unsealed"

// Info keeps the proof in the executable as well as reporting its build identity.
func Info() map[string]string {
	return map[string]string{"version": Version, "buildId": BuildID, "sourceDigest": SourceDigest, "builtAt": BuiltAt, "proof": Proof}
}

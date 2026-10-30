//go:build devtrust && !release

package networking

import (
	"crypto/x509"
	"os"
)

// A development build trusts one extra certificate authority for its Hosted
// origin, so a local Hosted behind a self-signed TLS proxy can be claimed on a
// Mac (PORTICO_DEV_HOSTED_CA_FILE, a PEM file). Release builds never do: this
// file is excluded from them, and devHostedRoots stays nil (system roots).
func init() {
	file := os.Getenv("PORTICO_DEV_HOSTED_CA_FILE")
	if file == "" {
		return
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		panic("PORTICO_DEV_HOSTED_CA_FILE: " + err.Error())
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		panic("PORTICO_DEV_HOSTED_CA_FILE holds no certificate")
	}
	devHostedRoots = pool
}

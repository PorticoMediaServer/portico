package hosted

import (
	"crypto/ed25519"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"time"
)

func testSigningCertificate(key ed25519.PrivateKey, id string) *trust.Certificate {
	c, err := trust.Certify(key, key.Public().(ed25519.PublicKey), id, "documents", time.Now().Add(-time.Hour), time.Now().Add(120*24*time.Hour), 1, nil)
	if err != nil {
		panic(err)
	}
	return c
}

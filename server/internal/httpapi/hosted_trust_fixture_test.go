package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	hostedtrust "github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"testing"
	"time"
)

// The private root exists only in this test process. Every policy fixture uses
// a separately certified signing leaf, like a deployed Hosted policy.
var testHostedRootPublic, testHostedRootPrivate, testHostedRootError = ed25519.GenerateKey(rand.Reader)

func testHostedRootPin() string {
	if testHostedRootError != nil {
		panic(testHostedRootError)
	}
	return base64.RawURLEncoding.EncodeToString(testHostedRootPublic)
}

// testHostedRootID is the root ID hosted.New requires for testHostedRootPin.
func testHostedRootID() string { return hostedtrust.KeyID(testHostedRootPublic) }

// zeroHostedRootID names the all-zero placeholder root some fixtures pin.
var zeroHostedRootID = hostedtrust.KeyID(make([]byte, 32))

func certifiedHostedPolicy(t *testing.T, leaf ed25519.PrivateKey, id string, payload []byte) hosted.Signed {
	t.Helper()
	if testHostedRootError != nil {
		t.Fatal(testHostedRootError)
	}
	now := time.Now().UTC()
	cert, err := hostedtrust.Certify(testHostedRootPrivate, leaf.Public().(ed25519.PublicKey), id, "documents", now.Add(-time.Minute), now.Add(24*time.Hour), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := hostedtrust.Sign(leaf, cert, payload)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

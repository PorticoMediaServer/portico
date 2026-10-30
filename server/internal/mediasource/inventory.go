package mediasource

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// InventoryEvidence is observation evidence, NOT a byte-continuity grant.
// In particular dev/inode/mtime/ctime do not assert NoInPlaceMutation and must
// never be converted into a playback Version without that owner's validation.
// Scope includes the source incarnation; replacement roots cannot reuse work.
type InventoryEvidence struct {
	Kind            string `json:"kind"`
	Scope           string `json:"scope"`
	Object          string `json:"object"`
	Size            int64  `json:"size"`
	ModifiedNS      int64  `json:"modifiedNs"`
	ChangeToken     string `json:"changeToken"`
	ProviderVersion string `json:"providerVersion,omitempty"`
}

func (e InventoryEvidence) Revision() (string, error) {
	if e.Kind == "" || e.Scope == "" || e.Object == "" || e.Size < 0 || len(e.Scope) > 1024 || len(e.Object) > 8192 || len(e.ChangeToken) > 4096 || len(e.ProviderVersion) > 4096 {
		return "", errors.New("invalid inventory evidence")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(append([]byte("portico.inventory.v1\x00"), b...))
	return hex.EncodeToString(h[:]), nil
}

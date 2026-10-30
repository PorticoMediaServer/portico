package disc

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
)

// CapturedStructureFingerprint binds structure measured from one exact owned
// capture to its unique identity and complete scoped byte version. A digest or
// matching structure alone must not carry facts to another capture or source.
//
// This is neither custody nor authority. The accepting writer must first check
// the exact committed capture receipt, actual artifact, original producer/source
// witnesses and current reader through its owned-capture adapter. It must probe
// those captured bytes, never the provider under an earlier set of facts.
func CapturedStructureFingerprint(captureID string, version mediasource.Version, probe Probe) (string, error) {
	if probe.Packaging != "iso" {
		// One finite object is not a version of a folder and its dependencies.
		return "", ErrRemoteUnqualified
	}
	if !validCaptureIdentity(captureID, version) {
		return "", ErrProtocol
	}
	raw, err := json.Marshal(probe)
	if err != nil {
		return "", ErrProtocol
	}
	if _, err = DecodeProbe(raw); err != nil && !errors.Is(err, ErrEncrypted) {
		return "", err
	}
	hash := sha256.New()
	hash.Write([]byte("disc_structure_capture_v1\x00"))
	var length [4]byte
	for _, identity := range []string{captureID, version.ID()} {
		binary.BigEndian.PutUint32(length[:], uint32(len(identity)))
		hash.Write(length[:])
		hash.Write([]byte(identity))
	}
	hash.Write(raw)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validCaptureIdentity(id string, version mediasource.Version) bool {
	return id != "" && strings.TrimSpace(id) == id && len(id) <= 256 && !strings.ContainsAny(id, "\x00\r\n") &&
		version.ID() != "" && version.Evidence().Kind == mediasource.ContentSHA256 && version.Evidence().Size > 0
}

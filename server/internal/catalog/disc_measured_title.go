package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

var measuredDiscIdentity = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,256}$`)
var measuredDiscDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

// MeasuredDiscTitleRef projects metadata from an original sealed Play
// measurement. The caller must retain and reprove its exact borrow or accepted
// presentation before using it. This pure function establishes no authority,
// readiness, input custody, source currentness or grant, and performs no lookup.
//
// The title identity is stable for this physical observation, measured structure
// and selected key. Facts changes change the offer revision. Session, snapshot,
// capture and actor identities deliberately have no place in this metadata
// projection; equal references cannot transfer their independent byte custody.
// Local inventory selections/tokens and ResolveDiscTitleTx remain unchanged.
func MeasuredDiscTitleRef(assetID, objectID, sourceRevision string, structure disc.Probe, selectedKey, factsRevision string) (*DiscTitleRef, error) {
	for _, value := range []string{assetID, objectID, sourceRevision, selectedKey} {
		if !measuredDiscIdentity.MatchString(value) {
			return nil, ErrDiscSelectionChanged
		}
	}
	if !measuredDiscDigest.MatchString(factsRevision) {
		return nil, ErrDiscSelectionChanged
	}
	checked, structureDigest, err := measuredDiscStructure(structure)
	if err != nil {
		return nil, err
	}
	found := false
	for _, title := range checked.Titles {
		if title.Key(checked.Kind) == selectedKey {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrDiscSelectionChanged
	}
	token := measuredDiscTitleToken(assetID, objectID, sourceRevision, structureDigest, selectedKey)
	return &DiscTitleRef{ID: discPublicID(token), Revision: discTitleRevision(sourceRevision, token, factsRevision)}, nil
}

// MeasuredDiscTitleKey resolves only an exact metadata title ID against this
// physical observation and validated measured structure, before media facts
// exist. It returns no offer revision, readiness or authority. The caller must
// retain its original sealed input/borrow and check the final MeasuredDiscTitleRef
// after measurement. Unknown IDs never select Main or another title.
func MeasuredDiscTitleKey(assetID, objectID, sourceRevision string, structure disc.Probe, id string) (string, error) {
	for _, value := range []string{assetID, objectID, sourceRevision} {
		if !measuredDiscIdentity.MatchString(value) {
			return "", ErrDiscSelectionChanged
		}
	}
	if !measuredDiscDigest.MatchString(id) {
		return "", ErrDiscSelectionChanged
	}
	checked, structureDigest, err := measuredDiscStructure(structure)
	if err != nil {
		return "", err
	}
	for _, title := range checked.Titles {
		key := title.Key(checked.Kind)
		if discPublicID(measuredDiscTitleToken(assetID, objectID, sourceRevision, structureDigest, key)) == id {
			return key, nil
		}
	}
	return "", ErrDiscSelectionChanged
}

func measuredDiscTitleToken(assetID, objectID, sourceRevision, structureDigest, selectedKey string) string {
	metadata, _ := json.Marshal([]string{assetID, objectID, sourceRevision, structureDigest, selectedKey})
	tokenDigest := sha256.Sum256(append([]byte("disc_measured_title_metadata_v1\x00"), metadata...))
	return hex.EncodeToString(tokenDigest[:])
}

func measuredDiscStructure(structure disc.Probe) (disc.Probe, string, error) {
	// Bound the supplied graph before encoding. The actual disc protocol decoder
	// below validates all structure, field sizes, order and selected-title keys.
	if len(structure.Titles) > disc.MaxTitles || len(structure.Kind) > 16 || len(structure.Packaging) > 16 || len(structure.Encryption) > 16 {
		return disc.Probe{}, "", disc.ErrProtocol
	}
	children := 0
	for _, title := range structure.Titles {
		if len(title.PlaylistFile) > 10 {
			return disc.Probe{}, "", disc.ErrProtocol
		}
		for _, n := range []int{len(title.Clips), len(title.Streams), len(title.Chapters)} {
			if n > 131072-children {
				return disc.Probe{}, "", disc.ErrProtocol
			}
			children += n
		}
		for _, clip := range title.Clips {
			if len(clip.File) > 10 {
				return disc.Probe{}, "", disc.ErrProtocol
			}
		}
		for _, stream := range title.Streams {
			if len(stream.Kind) > 11 || len(stream.Language) > 3 || len(stream.VideoRole) > 11 {
				return disc.Probe{}, "", disc.ErrProtocol
			}
		}
	}
	raw, err := json.Marshal(structure)
	if err != nil {
		return disc.Probe{}, "", disc.ErrProtocol
	}
	checked, err := disc.DecodeProbe(raw)
	if err != nil {
		return disc.Probe{}, "", err
	}
	structureDigest := sha256.Sum256(raw)
	return checked, hex.EncodeToString(structureDigest[:]), nil
}

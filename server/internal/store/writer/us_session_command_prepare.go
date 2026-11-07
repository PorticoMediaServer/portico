package writer

import "fmt"

// Frozen original preparation only: no authority, crypto proof, admission or
// saved-result/COMMIT semantics follow from this digest. Fresh same-turn
// admission clocks must never replace these retained inputs on uncertain COMMIT.
type usSessionCommand struct {
	identity CommandIdentity
	request  usSessionCommandRequest
	digest   RequestDigest
}

type usSessionCommandRequest struct{ prepared usSessionRotation }

// Reconstruct through the underlying validator rather than accepting a prepared
// object's fields as evidence. That constructor also clones protected buffers.
func prepareUSSessionCommand(kind CommandID, identity CommandIdentity, in usSessionRotationRequest, clocks usSessionClocks) (*usSessionCommand, error) {
	prepared, err := prepareUSSessionRotation(kind, identity, in, clocks)
	if err != nil {
		return nil, err
	}
	request := usSessionCommandRequest{prepared: *prepared}
	digest, err := canonicalRequestDigest(kind, 1, identity, request)
	if err != nil {
		return nil, err
	}
	return &usSessionCommand{identity: identity, request: request, digest: digest}, nil
}

func (usSessionCommand) String() string             { return "usSessionCommand{redacted}" }
func (usSessionCommand) GoString() string           { return "usSessionCommand{redacted}" }
func (usSessionCommand) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usSessionCommand") }
func (usSessionCommandRequest) String() string      { return "usSessionCommandRequest{redacted}" }
func (usSessionCommandRequest) GoString() string    { return "usSessionCommandRequest{redacted}" }
func (usSessionCommandRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usSessionCommandRequest")
}

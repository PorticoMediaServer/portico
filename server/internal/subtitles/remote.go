package subtitles

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A companion is a basename next to the STRM's approved media URL. It is not a
// client-provided URL, path, credential or network-policy override.
type CompanionAcquirer interface {
	AcquireCompanion(context.Context, string, string, string, string) ([]byte, []byte, string, error)
}
type RemoteRequest struct {
	OperationID string `json:"operationId"`
	SourceID    string `json:"sourceId"`
	Name        string `json:"name"`
	Format      string `json:"format"`
	Language    string `json:"language"`
	Title       string `json:"title"`
	Rights      string `json:"rights"`
	Scope       string `json:"scope"`
}

func (s *Service) ImportRemote(ctx context.Context, p identity.Principal, item string, m RemoteRequest) (Receipt, error) {
	mutation := Mutation{OperationID: m.OperationID, SourceID: m.SourceID, Format: m.Format, Language: m.Language, Title: m.Title, Rights: m.Rights, Scope: m.Scope, OffsetUS: "0"}
	if e := validateMutation(mutation); e != nil {
		return Receipt{}, e
	}
	if !validFormat(m.Format) || len(m.Name) < 1 || len(m.Name) > 255 {
		return Receipt{}, ErrInput
	}
	acquirer, ok := s.Extractor.(CompanionAcquirer)
	if !ok {
		return Receipt{}, ErrUnavailable
	}
	digest := requestDigest([]any{"remote-companion", item, m})
	gated, p, e := s.tx(ctx, p, item)
	if e != nil {
		return Receipt{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	prior, e := receiptTx(ctx, tx, p, m.OperationID, digest, item)
	if e != nil {
		return Receipt{}, e
	}
	if prior != nil {
		return *prior, nil
	}
	if m.Scope == "shared" && !owner(p) {
		return Receipt{}, identity.ErrUnauthorized
	}
	src, e := sourceQuery(ctx, tx, item, m.SourceID)
	if e != nil {
		return Receipt{}, e
	}
	if !src.Available || src.container != "strm" {
		return Receipt{}, ErrUnsupported
	}
	if e = gated.Commit(); e != nil {
		return Receipt{}, e
	}
	data, companion, evidence, e := acquirer.AcquireCompanion(ctx, item, m.SourceID, m.Name, m.Format)
	if e != nil {
		return Receipt{}, e
	}
	canonical, e := CanonicalAsset(data, companion, m.Format, int64(src.duration*1e6))
	if e != nil {
		return Receipt{}, e
	}
	pub := publication{source: src, origin: "sidecar", originalDigest: SidecarFingerprint(data, companion), evidence: evidence, attribution: "Companion from the authorized STRM media origin"}
	return s.publish(ctx, p, item, mutation, digest, pub, canonical)
}

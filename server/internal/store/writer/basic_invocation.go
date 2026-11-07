package writer

import "context"

// A closed private arm of the existing scan invocation. The opaque disclosure
// is still W-issued; these inert values cannot mint authority or open a turn.
// Accept remains closed until its complete activation/cohort contract lands.
type basicInvocation struct {
	request BasicReceiptRequest
	stage   *basicStageCommand
	seal    *basicSealCommand
}

func (in *basicInvocation) validate() (CommandID, error) {
	if in == nil {
		return 0, ErrInvalid
	}
	kind, err := basicResourceCommand(in.request.Purpose)
	if err != nil || kind == BasicAccept {
		return 0, ErrInvalid
	}
	var original BasicReceiptRequest
	switch {
	case in.stage != nil:
		if kind != BasicStage || in.seal != nil {
			return 0, ErrInvalid
		}
		original, err = in.stage.receiptRequest()
	case in.seal != nil:
		if kind != BasicSeal {
			return 0, ErrInvalid
		}
		original, err = in.seal.receiptRequest()
	default:
		return kind, nil // original Resolve/ACK; never a fresh effects request
	}
	if err != nil || original != in.request {
		return 0, ErrInvalid
	}
	return kind, nil
}

func (in *basicInvocation) fresh() bool {
	return in != nil && (in.stage != nil || in.seal != nil)
}

func (in *basicInvocation) leaseExpiresMS() int64 {
	if in != nil && in.stage != nil {
		return in.stage.request.original.run.leaseExpiresMS
	}
	if in != nil && in.seal != nil {
		return in.seal.request.original.run.leaseExpiresMS
	}
	return 0
}

// Domain bodies use the SAME commandTurn after current disclosure and receipt
// classification. They never assign Outcome or perform physical publication.
func (in *basicInvocation) apply(ctx context.Context, turn *commandTurn) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if _, err = in.validate(); err != nil {
		return value, err
	}
	if in.stage != nil {
		return applyBasicStage(ctx, turn, in.stage)
	}
	if in.seal != nil {
		return applyBasicSeal(ctx, turn, in.seal)
	}
	return value, ErrInvalid
}

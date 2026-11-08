package writer

import "context"

// A closed private arm of the existing scan invocation. The opaque disclosure
// is still W-issued; these inert values cannot mint authority or open a turn.
// Accept remains closed until its complete activation/cohort contract lands.
type basicInvocation struct {
	request BasicReceiptRequest
	accept  *basicAcceptCommand
	stage   *basicStageCommand
	seal    *basicSealCommand
	work    *scanDriverWork // W-owned transient envelope; never in the request digest
}

func (in *basicInvocation) validate() (CommandID, error) {
	if in == nil || in.request.Scan.RunID == "" || in.request.Scan.LibraryID <= 0 || in.request.Scan.LibraryKey == "" || in.request.Scan.Receipt.Identity.Sequence <= 0 {
		return 0, ErrInvalid
	}
	kind, err := basicResourceCommand(in.request.Purpose)
	if err != nil {
		return 0, ErrInvalid
	}
	set := 0
	for _, present := range []bool{in.accept != nil, in.stage != nil, in.seal != nil} {
		if present {
			set++
		}
	}
	if set > 1 {
		return 0, ErrInvalid
	}
	var original BasicReceiptRequest
	switch {
	case in.accept != nil:
		if kind != BasicAccept {
			return 0, ErrInvalid
		}
		original, err = in.accept.receiptRequest()
	case in.stage != nil:
		if kind != BasicStage {
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
	return in != nil && (in.accept != nil || in.stage != nil || in.seal != nil)
}

func (in *basicInvocation) leaseExpiresMS() int64 {
	if original, ok := in.freshOriginal(); ok {
		return original.run.leaseExpiresMS
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
	switch {
	case in.accept != nil:
		return applyBasicAccept(ctx, turn, in.accept)
	case in.stage != nil:
		return applyBasicStage(ctx, turn, in.stage)
	case in.seal != nil:
		return applyBasicSeal(ctx, turn, in.seal)
	}
	return value, ErrInvalid
}

// The frozen original scope and source of the ONE fresh command, if any.
func (in *basicInvocation) freshOriginal() (basicOriginalScope, bool) {
	switch {
	case in == nil:
		return basicOriginalScope{}, false
	case in.accept != nil:
		return in.accept.request.original, true
	case in.stage != nil:
		return in.stage.request.original, true
	case in.seal != nil:
		return in.seal.request.original, true
	}
	return basicOriginalScope{}, false
}

func (in *basicInvocation) freshSource() (basicStreamFactStage, bool) {
	switch {
	case in == nil:
		return basicStreamFactStage{}, false
	case in.accept != nil:
		return in.accept.request.activation.source, true
	case in.stage != nil:
		return in.stage.request.source, true
	case in.seal != nil:
		return in.seal.request.source, true
	}
	return basicStreamFactStage{}, false
}

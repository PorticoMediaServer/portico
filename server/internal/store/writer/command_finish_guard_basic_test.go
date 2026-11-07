package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "basic",
		resultTypes: []string{"commandOutput", "AckResult", "BasicResult", "basicReceiptLookup"},
		names:       []string{"acknowledgeBasic", "composeBasicProfiles", "commandTimeMS", "requireCommandMemory", "basicTimeMS", "requireBasicMemory", "checkBasicTerminal"},
		bypasses: []string{
			`func bypass(ctx context.Context)(out InventoryResult,err error){var v InventoryResult;var e error;saved:=InventoryResult{RunID:"saved"};defer func(){v,e=finishCommand(ctx,ctx,out,err);out,err=saved,e}();_=v;return saved,ErrInvalid}`,
			`func bypass(ctx context.Context)(out InventoryResult,err error){var v InventoryResult;var e error;defer func(){v,e=finishCommand(ctx,ctx,out,err);out,err=v,nil}();_=e;return InventoryResult{RunID:"saved"},ErrInvalid}`,
			`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){var out InventoryResult;out,err=finishCommand(ctx,ctx,out,err);_=out}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
			`func bypass(ctx context.Context)(out InventoryResult,err error){var v struct{field,saved InventoryResult};var e error;defer func(){v.field,e=finishCommand(ctx,ctx,out,err);out,err=v.saved,e}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
			`func bypass(ctx context.Context)(InventoryResult,error){defer func(){_,_=finishCommand(ctx,ctx,InventoryResult{},ErrInvalid)}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
			`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){_,_=finishCommand(ctx,ctx,InventoryResult{},ErrInvalid)}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
			`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){other,otherErr:=finishCommand(ctx,ctx,out,err);_,_=other,otherErr}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
			`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){out,err:=finishCommand(ctx,ctx,out,err);_,_=out,err}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
			`func bypass(ctx context.Context)(out InventoryResult,err error){defer func(){v,e:=finishCommand(ctx,ctx,out,err);out,err=v,nil;_=e}();return InventoryResult{RunID:"saved"},ErrInvalid}`,
			`func bypass(ctx context.Context)(InventoryResult,error){return InventoryResult{},nil}`,
			`func bypass(ctx context.Context)(InventoryResult,error){if true{return InventoryResult{},nil};defer func(){finishCommand()}();return InventoryResult{},nil}`,
			`func bypass(ctx context.Context)(InventoryResult,error){defer func(){}();return InventoryResult{},nil}`,
			`func bypass(ctx context.Context)(InventoryResult,error){defer func(){finishCommand()}();return InventoryResult{},nil}`,
			`func commandTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
			`func requireCommandMemory(ctx context.Context)(err error){return nil}`,
		},
	})
}

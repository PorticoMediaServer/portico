package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "search",
		resultTypes: []string{"SearchImageStageResult", "publisherReceiptLookup", "publisherInvocation", "searchFragmentStageResult", "searchHeadPublishResult"},
		names:       []string{"searchImageEngine", "requireSearchImageMemory", "searchImageTimeMS", "acknowledgeSearchPublisher", "searchImageStageFreshFacts", "searchFragmentStageFreshFacts", "searchHeadPublishFreshFacts", "searchPublisherStageFacts"},
		prefixes:    []string{"checkSearchImage"},
		bypasses: []string{
			`func headRelease(ctx context.Context)(out searchHeadPublishResult,err error){return searchHeadPublishResult{},nil}`,
			`func searchHeadPublishFreshFacts(ctx context.Context)(out searchHeadPublishRequest,err error){return searchHeadPublishRequest{},nil}`,
			`func searchPublisherStageFacts(ctx context.Context)(out searchFragmentStageSnapshot,err error){return searchFragmentStageSnapshot{},nil}`,
			`func fragmentRelease(ctx context.Context)(out searchFragmentStageResult,err error){return searchFragmentStageResult{},nil}`,
			`func searchFragmentStageFreshFacts(ctx context.Context,turn *commandTurn,want searchFragmentStageSnapshot)(out searchFragmentStageSnapshot,err error){return want,nil}`,
			`func searchImageEngine(ctx context.Context)(out searchpolicy.Engine,err error){return searchpolicy.Engine{Version:"saved"},nil}`,
			`func acknowledgeSearchPublisher(ctx context.Context,turn *commandTurn,q PublisherReceiptRequest,lookup publisherReceiptLookup)(out int64,err error){return 1,nil}`,
			`func searchImageStageFreshFacts(ctx context.Context,turn *commandTurn,want searchImageStageRequest)(out searchImageStageRequest,err error){return want,nil}`,
			`func searchImageTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
			`func requireSearchImageMemory(ctx context.Context)(err error){return nil}`,
			`func checkSearchImageTerminal(ctx context.Context)(err error){return nil}`,
		},
	})
}

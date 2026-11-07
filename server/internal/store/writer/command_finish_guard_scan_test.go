package writer

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "scan",
		resultTypes: []string{"InventoryResult", "inventoryReceiptLookup", "scanLifecycleReceiptLookup", "scanLifecycleCommandResult", "scanSourceFacts", "ScanRunLease", "ScanRunOperation", "ScanRunResult", "ScanRunSnapshot", "scanDirectoryLookup", "scanDirectorySaved", "scanDirectoryCommandResult"},
		names:       []string{"disclose", "requireInventoryMemory", "inventoryTimeMS", "scanCreateTimeMS", "scanClaimTimeMS", "requireScanLifecycleMemory", "applyInventoryCreate", "applyInventoryClaim", "persistScanRunLifecycleResult", "acknowledgeScanRunLifecycle", "composeScanLifecycleHistory", "ObserveInventory", "newScanPreparation", "newScanDriver", "keepPreparation", "prepareWork", "allocateBirth", "prepareBirth", "checkBirthAbsence", "excludeOldWork", "Lease", "PrepareClaim", "PrepareCreate", "checkScanSourceActive", "composeScanLifecycleProfiles", "configureScanIssuer", "excludePredecessor", "retainIssuerPreparation", "retainSourcePreparation", "composeScanDirectories", "directoryMemory", "acknowledgeScanDirectory"},
		prefixes:    []string{"checkInventory"},
		bypasses: []string{
			`func newScanDriver(ctx context.Context)(out *ScanDriver,err error){return out,nil}`,
			`func keepPreparation(ctx context.Context)(err error){return nil}`,
			`func prepareWork(ctx context.Context)(out *scanDriverWork,err error){return out,nil}`,
			`func allocateBirth(ctx context.Context)(out *scanBirth,err error){return out,nil}`,
			`func prepareBirth(ctx context.Context)(out *inventoryCreateCommand,err error){return out,nil}`,
			`func checkBirthAbsence(ctx context.Context)(err error){return nil}`,
			`func excludeOldWork(ctx context.Context)(err error){return nil}`,
			`func newScanPreparation(ctx context.Context)(out *scanPreparation,err error){return &scanPreparation{},nil}`,
			`func ObserveInventory(ctx context.Context)(value nativeinventory.Page,err error){return nativeinventory.Page{},nil}`,
			`func lookupScanRunLifecycle(ctx context.Context)(value scanLifecycleReceiptLookup,err error){return scanLifecycleReceiptLookup{},nil}`,
			`func captureScanSource(ctx context.Context)(value scanSourceFacts,err error){return scanSourceFacts{},nil}`,
			`func PrepareCreate(ctx context.Context)(value *ScanRunOperation,err error){return &ScanRunOperation{},nil}`,
			`func arbitraryNewEntry(ctx context.Context)(value *ScanRunResult,err error){return &ScanRunResult{},nil}`,
			`func Snapshot(ctx context.Context)(value ScanRunSnapshot,err error){return ScanRunSnapshot{},nil}`,
			`func acknowledgeScanRunLifecycle(ctx context.Context)(value int64,err error){return 1,nil}`,
			`func composeScanLifecycleHistory(ctx context.Context)(err error){return nil}`,
			`func runScanLifecycleHistory(ctx context.Context)(value scanLifecycleCommandResult,ack AckResult,err error){return value,ack,nil}`,
			`func scanCreateTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
			`func scanClaimTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
			`func requireScanLifecycleMemory(ctx context.Context)(err error){return nil}`,
			`func applyInventoryCreate(ctx context.Context)(value scanRunLifecycleResult,err error){return value,nil}`,
			`func applyInventoryClaim(ctx context.Context)(value scanRunLifecycleResult,err error){return value,nil}`,
			`func persistScanRunLifecycleResult(ctx context.Context)(value scanRunLifecycleResult,err error){return value,nil}`,
			`func requireInventoryMemory(ctx context.Context)(err error){return nil}`,
			`func inventoryTimeMS(ctx context.Context)(value int64,err error){return 1,nil}`,
			`func checkInventoryScope(ctx context.Context)(err error){defer func(){_,err=finishCommand(ctx,ctx,InventoryResult{RunID:"saved"},err)}();return nil}`,
		},
	})
}

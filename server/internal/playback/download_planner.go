package playback

import (
	"context"
	"database/sql"
)

// DeliverySourceTx reads one asset's planning facts inside an existing read,
// so a download menu weighs the same facts playback does.
func DeliverySourceTx(ctx context.Context, tx *sql.Tx, asset string) (DeliverySource, error) {
	return loadDeliverySource(ctx, tx, asset)
}

// DownloadPlayable asks the ordinary planner whether a finished file can play
// offline. There is no server converter once the file is on the device.
func DownloadPlayable(source DeliverySource, device CapabilityDocument) bool {
	policy := ResolvedDeliveryPolicy{DirectPlay: "allow", DirectStream: "never", Transcode: "never", AllowHDR: true}
	plan, err := planDelivery(DeliveryInput{Source: &source, Device: &device, NoHLS: true, Policy: policy, Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: DefaultDeliveryConfiguration()})
	return err == nil && plan.Strategy == DeliveryOriginal
}

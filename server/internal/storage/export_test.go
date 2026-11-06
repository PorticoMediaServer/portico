package storage

// External integration tests exercise the portable transport on every host
// without changing production platform selection or exporting a runtime API.
var RetainedInventoryPageForTest = (*Client).retainedInventoryPage

func InventoryRetirementForTest(c *Client, key string) <-chan struct{} {
	inventoryStreams.Lock()
	defer inventoryStreams.Unlock()
	for _, stream := range inventoryStreams.byID {
		if stream.client == c && stream.ownerKey == key {
			return stream.retired
		}
	}
	return nil
}

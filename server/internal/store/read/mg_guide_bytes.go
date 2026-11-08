package read

// The guide reader pairs each wide payload with its exact octet preflight in
// the SAME snapshot. These compiled pairs add no native permission: both
// statements still require an actual Capture receipt, and the octet shapes are
// owned by stmt/plan's MG registrations. Channel columns map name, number,
// group and logo; programme columns map every variable-length display field.
func init() {
	registerRSBytePoint(mgGuideQuery("channel"), rsBytePoint{
		lengths: mgGuideQuery("channel_octets"),
		columns: []int{0, 1, 2, 3, 6},
	})
	registerRSBytePoint(mgGuideQuery("programme"), rsBytePoint{
		lengths: mgGuideQuery("programme_octets"),
		columns: []int{0, 1, 2, 3, 4, 6, 7, 8, 9, 10, 12},
	})
}

package writer

// RetainedReferenceForTest exposes the opaque reference of the current retained
// progress command so an external control can replay its saved receipt. Test
// only; it grants no authority and creates no command.
func (d *WCProgressDriver) RetainedReferenceForTest() (ProgressReceiptRequest, bool) {
	if d == nil || d.work == nil {
		return ProgressReceiptRequest{}, false
	}
	return d.work.reference, true
}

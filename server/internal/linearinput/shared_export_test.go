package linearinput

// SharedInputInGraceForTest observes the last reader's actual lease release.
// It exists only in the test build, including external package tests.
func SharedInputInGraceForTest(source, channel string) bool {
	inputs.Lock()
	defer inputs.Unlock()
	entry := inputs.entries[source+"\n"+channel]
	return entry != nil && entry.readers == 0 && entry.idle != nil && !entry.closing
}

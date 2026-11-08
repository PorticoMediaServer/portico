package read

import "testing"

func TestRSNativeReadingDomainSelection(t *testing.T) {
	for _, kind := range []int64{20, 21} {
		binding, err := bindNativeSearchPolicy(kind, 1101)
		if err != nil || binding.recipe != rsReadingNativePolicy || binding.subject != 1101 || !binding.nativeSearch || binding.state != nil {
			t.Fatal(kind, binding, err)
		}
	}
	readPolicies.Lock()
	converter := readPolicies.recipes[rsReadingMobiPolicy]
	readPolicies.Unlock()
	if converter.nativeSearch {
		t.Fatal("native hit acquired converter permission")
	}
}

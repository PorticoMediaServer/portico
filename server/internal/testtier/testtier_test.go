package testtier

import "testing"

func TestTierSelection(t *testing.T) {
	for value, want := range map[string]bool{"": false, "default": false, "media": true, "all": true} {
		t.Setenv(Env, value)
		if got, err := MediaEnabled(); err != nil || got != want {
			t.Fatalf("%s=%q: %v %v", Env, value, got, err)
		}
	}
	t.Setenv(Env, "medai")
	if _, err := MediaEnabled(); err == nil {
		t.Fatal("a misspelt tier must fail, not silently skip")
	}
}

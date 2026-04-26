package version

import "testing"

func TestStringDefaultsToReleaseTag(t *testing.T) {
	// At test time Tag is the var default (no -ldflags). The default
	// must be a non-empty placeholder so `velocity version` always
	// prints something. Release builds override it.
	if String() == "" {
		t.Error("version.String() returned empty")
	}
}

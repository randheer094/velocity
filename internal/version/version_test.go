package version

import (
	"strings"
	"testing"
)

func TestStringDefaultsToDev(t *testing.T) {
	// At test time Tag is the var default. Tests can override via
	// -ldflags but the default must be a non-empty placeholder so
	// `velocity version` always prints something.
	if String() == "" {
		t.Error("version.String() returned empty")
	}
}

func TestMajorOf(t *testing.T) {
	cases := map[string]struct {
		want int
		ok   bool
	}{
		"v0.6.0":  {0, true},
		"0.6.0":   {0, true},
		"v1.0.0":  {1, true},
		"v10.2.3": {10, true},
		"":        {0, false},
		"dev":     {0, false},
		"abc":     {0, false},
	}
	for in, tc := range cases {
		got, err := MajorOf(in)
		if tc.ok && err != nil {
			t.Errorf("MajorOf(%q) err: %v", in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("MajorOf(%q) should have errored", in)
		}
		if tc.ok && got != tc.want {
			t.Errorf("MajorOf(%q) = %d, want %d", in, got, tc.want)
		}
	}
}

func TestMajorOfReportsTagInError(t *testing.T) {
	_, err := MajorOf("not-a-tag")
	if err == nil || !strings.Contains(err.Error(), "not-a-tag") {
		t.Errorf("expected tag in error, got %v", err)
	}
}

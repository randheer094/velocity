package webhook

import (
	"strings"
	"testing"
)

func TestCapRequirementShortUntouched(t *testing.T) {
	in := "small requirement"
	if got := capRequirement(in); got != in {
		t.Errorf("short input mutated: %q", got)
	}
}

func TestCapRequirementBoundary(t *testing.T) {
	in := strings.Repeat("x", MaxRequirementChars)
	if got := capRequirement(in); got != in {
		t.Errorf("exact-cap input mutated (len %d)", len(got))
	}
}

func TestCapRequirementTruncates(t *testing.T) {
	in := strings.Repeat("x", MaxRequirementChars+1000)
	got := capRequirement(in)
	if len(got) != MaxRequirementChars {
		t.Errorf("capped length = %d, want %d", len(got), MaxRequirementChars)
	}
	if !strings.HasSuffix(got, requirementTruncationMarker) {
		t.Errorf("missing truncation marker: tail=%q", got[len(got)-80:])
	}
}

package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewVersionCmd(t *testing.T) {
	cmd := newVersionCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "velocity ") {
		t.Errorf("output should start with 'velocity ': %q", got)
	}
	if !strings.Contains(got, "manifest major") {
		t.Errorf("output should mention manifest major: %q", got)
	}
}

package llm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/randheer094/velocity/internal/config"
)

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("short = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("long = %q", got)
	}
}

func TestOptionsFromRoleConfig(t *testing.T) {
	r := config.LLMRoleConfig{
		Provider:       "claude_cli",
		Model:          "M",
		AllowedTools:   "Read",
		PermissionMode: "P",
		TimeoutSec:     30,
	}
	got := OptionsFromRoleConfig(r, "/tmp/x")
	if got.WorkingDirectory != "/tmp/x" || got.Model != "M" || got.AllowedTools != "Read" || got.PermissionMode != "P" {
		t.Errorf("opts = %+v", got)
	}
	if got.Timeout != 30*time.Second {
		t.Errorf("timeout = %v", got.Timeout)
	}

	noTimeout := OptionsFromRoleConfig(config.LLMRoleConfig{}, "")
	if noTimeout.Timeout != 0 {
		t.Errorf("expected zero timeout: %v", noTimeout.Timeout)
	}
}

func TestDrainReader(t *testing.T) {
	got := DrainReader(strings.NewReader("hello\n\n"))
	if got != "hello" {
		t.Errorf("DrainReader = %q", got)
	}
}

func TestRunPromptEmpty(t *testing.T) {
	_, err := RunPrompt(context.Background(), "", Options{})
	if err == nil {
		t.Error("expected error on empty prompt")
	}
}

func TestRunPromptCommandNotFound(t *testing.T) {
	// Setting a tiny timeout + invoking real `claude` will likely fail in a
	// test environment without it installed. We pin Timeout > 0 to traverse
	// that branch and rely on exec to fail.
	_, err := RunPrompt(context.Background(), "say hi", Options{
		Model:          "m",
		AllowedTools:   "Read",
		PermissionMode: "p",
		Timeout:        100 * time.Millisecond,
	})
	if err == nil {
		t.Error("expected an error since `claude` CLI isn't installed in tests")
	}
}

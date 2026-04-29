package code

import (
	"strings"
	"testing"
)

func TestRedactAndTruncate(t *testing.T) {
	in := "ghp_" + strings.Repeat("X", 30) + " glpat-" + strings.Repeat("Y", 20) + " token=abc&q=1 ok"
	got := redactAndTruncate(in)
	if strings.Contains(got, "ghp_") || strings.Contains(got, "glpat-") || strings.Contains(got, "token=abc") {
		t.Errorf("not redacted: %q", got)
	}
	if !strings.Contains(got, "ok") {
		t.Errorf("non-secret content lost: %q", got)
	}

	long := strings.Repeat("y", maxErrChars+5)
	got = redactAndTruncate(long)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis: %q", got[len(got)-10:])
	}
}

func TestRenderFailureCommentFallback(t *testing.T) {
	got := renderFailureComment("code", "stage1", "boom")
	if !strings.Contains(got, "code") || !strings.Contains(got, "stage1") || !strings.Contains(got, "boom") {
		t.Errorf("comment = %q", got)
	}
}

func TestClaimRelease(t *testing.T) {
	if !claim("X-1") {
		t.Error("first claim should succeed")
	}
	if claim("X-1") {
		t.Error("second claim should fail")
	}
	release("X-1")
	if !claim("X-1") {
		t.Error("post-release claim should succeed")
	}
	release("X-1")
}

func TestCancelHelpers(t *testing.T) {
	called := 0
	cancel := func() { called++ }
	registerCancel("X-1", cancel)
	registerCancel("X-1", cancel)
	if called != 1 {
		t.Errorf("expected previous cancel called: %d", called)
	}
	cancelIfRunning("X-1")
	if called != 2 {
		t.Errorf("expected cancel called: %d", called)
	}
	cancelIfRunning("X-1")
	if called != 2 {
		t.Errorf("expected no extra: %d", called)
	}
	registerCancel("X-2", cancel)
	unregisterCancel("X-2")
	cancelIfRunning("X-2")
	if called != 2 {
		t.Errorf("expected no extra: %d", called)
	}
}

func TestBuildCodePrompt(t *testing.T) {
	loadFixturePrompts(t)
	got, err := buildCodePrompt("PROJ-1", "title", "desc")
	if err != nil {
		t.Fatalf("buildCodePrompt: %v", err)
	}
	for _, want := range []string{"PROJ-1", "title", "desc"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q: %s", want, got)
		}
	}
}

func TestBuildPRBody(t *testing.T) {
	got := BuildPRBody("title", "desc", "PROJ-1", "https://x")
	if !strings.Contains(got, "title") || !strings.Contains(got, "desc") || !strings.Contains(got, "PROJ-1") || !strings.Contains(got, "https://x") {
		t.Errorf("body missing fields: %q", got)
	}
}

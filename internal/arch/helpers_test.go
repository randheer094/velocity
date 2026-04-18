package arch

import (
	"context"
	"strings"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/status"
)

func TestExtractPlanWithMarkers(t *testing.T) {
	raw := `prelude
<<<PLAN_BEGIN>>>
{"task_list":[{"id":"t1","title":"a"}],"waves":[{"tasks":[{"id":"t1"}]}]}
<<<PLAN_END>>>
trailing`
	p, err := extractPlan(raw)
	if err != nil {
		t.Fatalf("extractPlan: %v", err)
	}
	if len(p.TaskList) != 1 || p.TaskList[0].ID != "t1" {
		t.Errorf("task list: %+v", p)
	}
	if len(p.Waves) != 1 {
		t.Errorf("waves: %+v", p)
	}
}

func TestExtractPlanFallbackJSON(t *testing.T) {
	raw := `some chatter {"task_list":[{"id":"t1","title":"a"}],"waves":[{"tasks":[{"id":"t1"}]}]} end`
	p, err := extractPlan(raw)
	if err != nil {
		t.Fatalf("extractPlan: %v", err)
	}
	if len(p.TaskList) != 1 {
		t.Errorf("task list: %+v", p)
	}
}

func TestExtractPlanInvalid(t *testing.T) {
	if _, err := extractPlan("no json here"); err == nil {
		t.Error("expected error")
	}
	if _, err := extractPlan("<<<PLAN_BEGIN>>> not json <<<PLAN_END>>>"); err == nil {
		t.Error("expected error on invalid plan body")
	}
}

func TestLastJSONObject(t *testing.T) {
	if got := lastJSONObject(""); got != "" {
		t.Errorf("empty: %q", got)
	}
	if got := lastJSONObject("noobject"); got != "" {
		t.Errorf("no braces: %q", got)
	}
	got := lastJSONObject(`pre {"a":1} mid {"b":2} end`)
	if got != `{"b":2}` {
		t.Errorf("last = %q", got)
	}
	got = lastJSONObject(`prefix {"a":{"b":1}} suffix`)
	if got != `{"a":{"b":1}}` {
		t.Errorf("nested = %q", got)
	}
}

func TestTrunc(t *testing.T) {
	if got := trunc("hello", 100); got != "hello" {
		t.Errorf("short: %q", got)
	}
	if got := trunc("helloworld", 5); got != "hello..." {
		t.Errorf("long: %q", got)
	}
}

func TestKeysOf(t *testing.T) {
	w := data.Wave{Tasks: []data.WaveRef{
		{ID: "a", JiraKey: "PROJ-1"},
		{ID: "b"},
		{ID: "c", JiraKey: "PROJ-2"},
	}}
	got := keysOf(w)
	want := []string{"PROJ-1", "PROJ-2"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("keysOf = %v", got)
	}
}

func TestAllDone(t *testing.T) {
	keys := []string{"A-1", "A-2"}
	statusName := func(c status.Canonical) string {
		switch c {
		case status.Done:
			return "Done"
		case status.Dismissed:
			return "Dismissed"
		}
		return "Other"
	}
	cfg := setupConfig(t)
	defer cfg()

	good := map[string]status.IssueInfo{
		"A-1": {Status: statusName(status.Done)},
		"A-2": {Status: statusName(status.Dismissed)},
	}
	if !allDone(good, keys) {
		t.Error("expected allDone true")
	}
	bad := map[string]status.IssueInfo{
		"A-1": {Status: "In Progress"},
		"A-2": {Status: "Done"},
	}
	if allDone(bad, keys) {
		t.Error("expected allDone false (in progress)")
	}
	missing := map[string]status.IssueInfo{
		"A-1": {Status: "Done"},
	}
	if allDone(missing, keys) {
		t.Error("expected allDone false (missing)")
	}
}

func TestRedactAndTruncate(t *testing.T) {
	in := "ghp_" + strings.Repeat("A", 30) + " glpat-" + strings.Repeat("A", 20) + " token=abc&q=1"
	got := redactAndTruncate(in)
	if strings.Contains(got, "ghp_") || strings.Contains(got, "glpat-") || strings.Contains(got, "token=abc") {
		t.Errorf("not redacted: %q", got)
	}

	long := strings.Repeat("x", maxErrChars+50)
	got = redactAndTruncate(long)
	if len(got) <= maxErrChars {
		t.Errorf("truncate failed, len=%d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis suffix")
	}
}

func TestFormatFailureComment(t *testing.T) {
	got := formatFailureComment("arch", "stage1", "boom")
	if !strings.Contains(got, "arch") || !strings.Contains(got, "stage1") || !strings.Contains(got, "boom") {
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
	registerCancel("X-1", cancel) // replaces; first cancel called
	if called != 1 {
		t.Errorf("expected 1 prev cancel, got %d", called)
	}
	cancelIfRunning("X-1")
	if called != 2 {
		t.Errorf("expected cancel called: %d", called)
	}
	cancelIfRunning("X-1") // no-op
	if called != 2 {
		t.Errorf("expected no extra: %d", called)
	}
	registerCancel("X-2", cancel)
	unregisterCancel("X-2")
	cancelIfRunning("X-2") // no cancel after unregister
	if called != 2 {
		t.Errorf("expected no extra: %d", called)
	}
}

func TestBuildArchPrompt(t *testing.T) {
	got := buildArchPrompt("PROJ-1", "do thing")
	if !strings.Contains(got, "PROJ-1") || !strings.Contains(got, "do thing") {
		t.Errorf("prompt missing fields: %q", got)
	}
	if !strings.Contains(got, planBegin) || !strings.Contains(got, planEnd) {
		t.Errorf("prompt missing markers")
	}
}

// dummy context to allow cancel funcs to compile
var _ = context.Background

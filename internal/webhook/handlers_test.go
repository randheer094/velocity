package webhook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/randheer094/velocity/internal/config"
)

// Unset webhook secret env vars so tests default to HMAC-disabled.
// Individual tests that exercise signature paths opt in via t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv(config.JiraWebhookSecretEnv)
	os.Unsetenv(config.GithubWebhookSecretEnv)
	os.Exit(m.Run())
}

const goodConfig = `{
  "jira": {
    "base_url": "https://example.atlassian.net",
    "email": "a@b.c",
    "architect_jira_id": "arch-id",
    "developer_jira_id": "dev-id",
    "repo_url_field": "customfield_repo",
    "task_status_map": {
      "new": {"default": "To Do"},
      "planning": {"default": "Planning"},
      "planning_failed": {"default": "Planning Failed"},
      "coding": {"default": "In Progress"},
      "done": {"default": "Done", "aliases": ["Dismissed"]}
    },
    "subtask_status_map": {
      "new": {"default": "To Do"},
      "coding": {"default": "Dev In Progress"},
      "coding_failed": {"default": "Dev Failed"},
      "in_review": {"default": "In Review"},
      "done": {"default": "Done", "aliases": ["Dismissed"]}
    }
  }
}`

func setupConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(goodConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir)
	if config.Get() == nil {
		t.Fatalf("config not loaded: %s", config.LoadError())
	}
}

func teardownConfig(t *testing.T) {
	t.Helper()
	config.SetDir(t.TempDir())
}

// startCapturingQueue stubs the DB-backed Enqueue so tests can assert
// against recorded inserts without needing Postgres. Returns the
// recorder whose Names() matches the pre-DB helper's API.
func startCapturingQueue(t *testing.T) *fakeInsert { return installCapture(t) }

// startRunningQueue is the "also exercise closures" variant: every
// recorded insert is dispatched through a stubbed dispatcher so we
// keep coverage for the payload round-trip.
func startRunningQueue(t *testing.T) *fakeInsert { return installRunning(t) }

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	waitForCond(t, 2*time.Second, cond)
}

func TestJiraHandlerWrongMethod(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/webhook/jira", nil)
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestJiraHandlerNoConfig(t *testing.T) {
	teardownConfig(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader([]byte("{}")))
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestJiraHandlerInvalidJSON(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader([]byte("not json")))
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestJiraHandlerMissingKey(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira",
		bytes.NewReader([]byte(`{"webhookEvent":"x","issue":{"fields":{}}}`)))
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestJiraHandlerIgnoredEvent(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira",
		bytes.NewReader([]byte(`{"webhookEvent":"comment_created","issue":{"key":"PROJ-1","fields":{}}}`)))
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
}

func TestJiraHandlerSubtaskAssignedDeveloper(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "webhookEvent": "x",
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "summary": "do thing",
          "description": "details",
          "issuetype": {"name": "Subtask", "subtask": true},
          "parent": {"key": "PROJ-1"},
          "assignee": {"accountId": "dev-id"},
          "customfield_repo": "https://github.com/o/r.git"
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.Run:PROJ-2") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerSubtaskAssignedNotDeveloper(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "parent": {"key": "PROJ-1"},
          "assignee": {"accountId": "someone-else"},
          "customfield_repo": "https://github.com/o/r.git"
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestJiraHandlerSubtaskMissingParent(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "assignee": {"accountId": "dev-id"},
          "customfield_repo": "https://github.com/o/r.git"
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestJiraHandlerParentAssignedArchitect(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-1",
        "fields": {
          "summary": "task",
          "description": {"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]},
          "issuetype": {"name": "Task", "subtask": false},
          "assignee": {"accountId": "arch-id"},
          "customfield_repo": "https://github.com/o/r.git"
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "arch.Run:PROJ-1") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerParentMissingRepo(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-1",
        "fields": {
          "issuetype": {"subtask": false},
          "assignee": {"accountId": "arch-id"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestJiraHandlerParentMissingDescription(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-1",
        "fields": {
          "summary": "x",
          "issuetype": {"subtask": false},
          "assignee": {"accountId": "arch-id"},
          "customfield_repo": "https://github.com/o/r.git"
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestJiraHandlerSubtaskUpdatedDone(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_updated",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "parent": {"key": "PROJ-1"},
          "status": {"name": "Done"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "arch.AdvanceWave:PROJ-1") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerSubtaskUpdatedDismissed(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_updated",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "parent": {"key": "PROJ-1"},
          "status": {"name": "Dismissed"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.OnDismissed:PROJ-2") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerSubtaskDoneNoParent(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	// isSubtask=true via issuetype.subtask, but no parent.field — early return.
	body := []byte(`{
      "issue_event_type_name": "issue_updated",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "status": {"name": "Done"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestJiraHandlerSubtaskUnknownStatus(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_updated",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "parent": {"key": "PROJ-1"},
          "status": {"name": "Some Random Status"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestJiraHandlerParentUpdatedDismissed(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_updated",
      "issue": {
        "key": "PROJ-1",
        "fields": {
          "issuetype": {"subtask": false},
          "status": {"name": "Dismissed"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "arch.OnDismissed:PROJ-1") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerSubtaskDoneRunsClosure(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startRunningQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_updated",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "parent": {"key": "PROJ-1"},
          "status": {"name": "Done"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "arch.AdvanceWave:PROJ-1") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerSubtaskDismissedRunsClosure(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startRunningQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_updated",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "parent": {"key": "PROJ-1"},
          "status": {"name": "Dismissed"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.OnDismissed:PROJ-2") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerParentDismissedRunsClosure(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startRunningQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_updated",
      "issue": {
        "key": "PROJ-1",
        "fields": {
          "issuetype": {"subtask": false},
          "status": {"name": "Dismissed"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "arch.OnDismissed:PROJ-1") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerSubtaskAssignedNoRepoFallsThrough(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startCapturingQueue(t)

	// No customfield_repo on subtask; jira.Shared() is unset/can't fetch
	// → lookupParentRepo returns "", handler logs warn and skips Enqueue.
	body := []byte(`{
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "issuetype": {"subtask": true},
          "parent": {"key": "PROJ-1"},
          "assignee": {"accountId": "dev-id"}
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestJiraHandlerSubtaskAssignedRunsClosure(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startRunningQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-2",
        "fields": {
          "summary": "do",
          "issuetype": {"subtask": true},
          "parent": {"key": "PROJ-1"},
          "assignee": {"accountId": "dev-id"},
          "customfield_repo": "https://github.com/o/r.git"
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.Run:PROJ-2") {
				return true
			}
		}
		return false
	})
}

func TestJiraHandlerParentAssignedRunsClosure(t *testing.T) {
	setupConfig(t)
	defer teardownConfig(t)
	cap := startRunningQueue(t)

	body := []byte(`{
      "issue_event_type_name": "issue_assigned",
      "issue": {
        "key": "PROJ-1",
        "fields": {
          "summary": "task",
          "description": {"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]},
          "issuetype": {"name": "Task", "subtask": false},
          "assignee": {"accountId": "arch-id"},
          "customfield_repo": "https://github.com/o/r.git"
        }
      }
    }`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/jira", bytes.NewReader(body))
	JiraHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "arch.Run:PROJ-1") {
				return true
			}
		}
		return false
	})
}

func TestGithubHandlerMergedRunsClosure(t *testing.T) {
	cap := startRunningQueue(t)
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"closed","pull_request":{"merged":true,"html_url":"https://gh/pr/1","head":{"ref":"PROJ-2"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	GithubHandler{}.ServeHTTP(rec, req)
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.MarkMerged:PROJ-2") {
				return true
			}
		}
		return false
	})
}

func TestExtractRepoURL(t *testing.T) {
	cases := []struct {
		fields map[string]any
		field  string
		want   string
	}{
		{map[string]any{"f": "https://x"}, "f", "https://x"},
		{map[string]any{"f": map[string]any{"value": "https://y"}}, "f", "https://y"},
		{map[string]any{"f": 42}, "f", ""},
		{map[string]any{}, "", ""},
		{map[string]any{}, "f", ""},
	}
	for _, c := range cases {
		if got := extractRepoURL(c.fields, c.field); got != c.want {
			t.Errorf("extractRepoURL(%v,%q) = %q, want %q", c.fields, c.field, got, c.want)
		}
	}
}

func TestGithubHandlerWrongMethod(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/webhook/github", nil)
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestGithubHandlerIgnoredEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader([]byte("{}")))
	req.Header.Set("X-GitHub-Event", "ping")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGithubHandlerInvalidJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader([]byte("not json")))
	req.Header.Set("X-GitHub-Event", "pull_request")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGithubHandlerNonMerged(t *testing.T) {
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"closed","pull_request":{"merged":false,"head":{"ref":"feat"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGithubHandlerNonJiraBranch(t *testing.T) {
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"closed","pull_request":{"merged":true,"html_url":"u","head":{"ref":"feature/x"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGithubHandlerMergedJiraBranch(t *testing.T) {
	cap := startCapturingQueue(t)
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"closed","pull_request":{"merged":true,"html_url":"https://gh/pr/1","head":{"ref":"PROJ-2"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.MarkMerged:PROJ-2") {
				return true
			}
		}
		return false
	})
}

func TestGithubHandlerWorkflowRunIgnoredConclusion(t *testing.T) {
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"completed","workflow_run":{"conclusion":"success","head_branch":"PROJ-2"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGithubHandlerWorkflowRunInvalidJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader([]byte("bad")))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGithubHandlerWorkflowRunNoPullRequests(t *testing.T) {
	// PR-only gate: workflow_run without an associated PR is ignored
	// regardless of branch shape.
	cap := startCapturingQueue(t)
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"completed","workflow_run":{"conclusion":"failure","head_branch":"PROJ-99","pull_requests":[]}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestGithubHandlerIssueCommentInvalidJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader([]byte("bad")))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGithubHandlerIssueCommentNonPR(t *testing.T) {
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"created","issue":{"number":1},"comment":{"body":"/velocity do"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestGithubHandlerIssueCommentNoPrefix(t *testing.T) {
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"created","issue":{"number":1,"pull_request":{}},"comment":{"body":"just chatting"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestBuildWorkflowRunInstructionWithSummary(t *testing.T) {
	got := buildWorkflowRunInstruction("CI", "https://gh/run/1", "LOG TAIL")
	for _, want := range []string{"CI", "https://gh/run/1", "LOG TAIL", "Failure logs:"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestBuildWorkflowRunInstructionNoSummary(t *testing.T) {
	got := buildWorkflowRunInstruction("CI", "https://gh/run/1", "")
	if !strings.Contains(got, "Log fetch failed") {
		t.Errorf("missing fallback note: %q", got)
	}
}

func TestDeriveCICommitHintFromErrorLine(t *testing.T) {
	summary := "=== job: test (failed) ===\n" +
		"2026-04-19T10:11:12.000Z some info\n" +
		"2026-04-19T10:11:13.000Z error: undefined symbol foo\n" +
		"panic stack below...\n"
	got := deriveCICommitHint("tests", summary)
	if !strings.HasPrefix(got, "fix CI: ") {
		t.Errorf("hint must prefix: %q", got)
	}
	if !strings.Contains(got, "error: undefined symbol foo") {
		t.Errorf("hint must carry error line: %q", got)
	}
}

func TestDeriveCICommitHintFallback(t *testing.T) {
	got := deriveCICommitHint("lint", "")
	if got != "fix CI: lint" {
		t.Errorf("got %q", got)
	}
}

func TestDeriveCICommitHintNoErrorLineFallsBackToWorkflow(t *testing.T) {
	got := deriveCICommitHint("ci", "just some benign log\nnothing fishy\n")
	if got != "fix CI: ci" {
		t.Errorf("got %q", got)
	}
}

func TestDeriveCICommitHintTruncates(t *testing.T) {
	long := "error: " + strings.Repeat("x", 200)
	got := deriveCICommitHint("w", long)
	if len(got) > 70 {
		t.Errorf("hint too long: %d (%q)", len(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis: %q", got)
	}
}

func TestTruncateHint(t *testing.T) {
	if got := truncateHint("abc", 10); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := truncateHint("abcdefghij", 5); got != "abcd…" {
		t.Errorf("got %q", got)
	}
}

func TestStripLogTimestamp(t *testing.T) {
	if got := stripLogTimestamp("2026-04-19T10:11:12.000Z hello"); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := stripLogTimestamp("plain line"); got != "plain line" {
		t.Errorf("got %q", got)
	}
}

func TestGithubHandlerWorkflowRunHappyPath(t *testing.T) {
	cap := startCapturingQueue(t)
	oldFetch := fetchWorkflowFailureSummary
	fetchWorkflowFailureSummary = func(string, int64) string { return "error: boom" }
	defer func() { fetchWorkflowFailureSummary = oldFetch }()

	rec := httptest.NewRecorder()
	body := []byte(`{"action":"completed","workflow_run":{"id":77,"name":"ci","conclusion":"failure","head_branch":"feature/x","html_url":"u","pull_requests":[{"number":42}]},"repository":{"full_name":"o/r","clone_url":"https://github.com/o/r.git"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.Iterate:ci:feature/x") {
				return true
			}
		}
		return false
	})
}

func TestGithubHandlerIssueCommentHappyPath(t *testing.T) {
	cap := startCapturingQueue(t)
	oldLookup := lookupBranchForPR
	lookupBranchForPR = func(string, int) string { return "feature/x" }
	defer func() { lookupBranchForPR = oldLookup }()

	rec := httptest.NewRecorder()
	body := []byte(`{"action":"created","issue":{"number":5,"pull_request":{}},"repository":{"full_name":"o/r","clone_url":"https://github.com/o/r.git"},"comment":{"body":"/velocity tweak the docs"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.Iterate:cmd:feature/x") {
				return true
			}
		}
		return false
	})
}

func TestGithubHandlerIssueCommentEmptyAfterPrefix(t *testing.T) {
	cap := startCapturingQueue(t)
	rec := httptest.NewRecorder()
	body := []byte(`{"action":"created","issue":{"number":5,"pull_request":{}},"repository":{"full_name":"o/r"},"comment":{"body":"/velocity   "}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

func TestGithubHandlerWorkflowRunStubbedSummary(t *testing.T) {
	cap := startCapturingQueue(t)
	old := fetchWorkflowFailureSummary
	fetchWorkflowFailureSummary = func(string, int64) string { return "error: sentinel" }
	defer func() { fetchWorkflowFailureSummary = old }()

	rec := httptest.NewRecorder()
	body := []byte(`{"action":"completed","workflow_run":{"id":77,"name":"ci","conclusion":"failure","head_branch":"PROJ-99","html_url":"u","pull_requests":[{"number":3}]},"repository":{"full_name":"o/r"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "workflow_run")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	waitFor(t, func() bool {
		for _, n := range cap.Names() {
			if strings.HasPrefix(n, "code.Iterate:ci:PROJ-99") {
				return true
			}
		}
		return false
	})
}

func TestFetchWorkflowFailureSummaryEmptyInputs(t *testing.T) {
	if got := fetchWorkflowFailureSummary("", 1); got != "" {
		t.Errorf("empty repo should return empty: %q", got)
	}
	if got := fetchWorkflowFailureSummary("o/r", 0); got != "" {
		t.Errorf("zero run id should return empty: %q", got)
	}
}

func TestGithubHandlerIssueCommentNoBranch(t *testing.T) {
	// If GitHub returns no head branch for the PR (e.g. auth failure or
	// deleted PR), the handler ignores the comment rather than
	// enqueuing an iterate with an empty branch.
	oldLookup := lookupBranchForPR
	lookupBranchForPR = func(string, int) string { return "" }
	defer func() { lookupBranchForPR = oldLookup }()
	cap := startCapturingQueue(t)

	rec := httptest.NewRecorder()
	body := []byte(`{"action":"created","issue":{"number":5,"pull_request":{}},"repository":{"full_name":"o/r"},"comment":{"body":"/velocity help"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	GithubHandler{}.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	time.Sleep(20 * time.Millisecond)
	if len(cap.Names()) != 0 {
		t.Errorf("expected no jobs, got %v", cap.Names())
	}
}

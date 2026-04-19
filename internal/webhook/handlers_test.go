package webhook

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
    "project_keys": ["PROJ"],
    "task_status_map": {
      "new": {"default": "To Do"},
      "planning": {"default": "Planning"},
      "planning_failed": {"default": "Planning Failed"},
      "subtask_in_progress": {"default": "In Progress"},
      "done": {"default": "Done"},
      "dismissed": {"default": "Dismissed"}
    },
    "subtask_status_map": {
      "new": {"default": "To Do"},
      "in_progress": {"default": "In Progress"},
      "pr_open": {"default": "In Review"},
      "code_failed": {"default": "Dev Failed"},
      "done": {"default": "Done"},
      "dismissed": {"default": "Dismissed"}
    }
  }
}`

func setupConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(goodConfig), 0o644); err != nil {
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

// drainEnqueued runs queued jobs synchronously by replacing the queue.
// Tests that don't care about job execution can ignore this.
func startCapturingQueue(t *testing.T) *capturedQueue {
	return startCapturingQueueOpts(t, false)
}

// startRunningQueue: like startCapturingQueue but also calls each job's Fn.
// Lets tests cover closure bodies built by webhook handlers.
func startRunningQueue(t *testing.T) *capturedQueue {
	return startCapturingQueueOpts(t, true)
}

func startCapturingQueueOpts(t *testing.T, run bool) *capturedQueue {
	t.Helper()
	cap := &capturedQueue{}
	resetQueue()
	queueMu.Lock()
	queue = make(chan Job, 32)
	queueCap = 32
	rootCtx = context.Background()
	q := queue
	queueMu.Unlock()

	cap.q = q
	go func() {
		for j := range q {
			if run && j.Fn != nil {
				func() {
					defer func() { _ = recover() }()
					j.Fn(context.Background())
				}()
			}
			cap.mu.Lock()
			cap.jobs = append(cap.jobs, j.Name)
			cap.mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		resetQueue()
	})
	return cap
}

type capturedQueue struct {
	mu   sync.Mutex
	jobs []string
	q    chan Job
}

func (c *capturedQueue) Names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.jobs))
	copy(out, c.jobs)
	return out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
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

package code

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/github"
	"github.com/randheer094/velocity/internal/jira"
)

func osExec(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

// Mirror arch's cfgJSON; using our own copy to avoid cross-package imports.
const cfgJSON = `{
  "jira": {
    "base_url": "BASEURL",
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

var (
	fakeJira       *httptest.Server
	fakeGithub     *httptest.Server
	dbReady        bool
	transitionsLog sync.Map
	createdPRs     sync.Map // repoFullName -> count
)

func fakeJiraHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/comment") && r.Method == http.MethodPost:
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	case strings.HasSuffix(r.URL.Path, "/transitions") && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"transitions":[{"id":"11","to":{"name":"Done"}},{"id":"12","to":{"name":"Dismissed"}},{"id":"13","to":{"name":"Dev Failed"}},{"id":"14","to":{"name":"In Review"}},{"id":"15","to":{"name":"In Progress"}}]}`))
	case strings.HasSuffix(r.URL.Path, "/transitions") && r.Method == http.MethodPost:
		var body struct {
			Transition struct{ ID string } `json:"transition"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
		key = strings.TrimSuffix(key, "/transitions")
		transitionsLog.Store(key, body.Transition.ID)
		w.WriteHeader(204)
	case strings.HasSuffix(r.URL.Path, "/assignee") && r.Method == http.MethodPut:
		w.WriteHeader(204)
	default:
		_, _ = fmt.Fprint(w, `{}`)
	}
}

func fakeGithubHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/pulls") && r.Method == http.MethodGet:
		// FindOpenPR list query: return empty array (no existing PR).
		_, _ = w.Write([]byte(`[]`))
	case strings.HasSuffix(r.URL.Path, "/pulls") && r.Method == http.MethodPost:
		// CreatePR: count and respond.
		path := strings.TrimPrefix(r.URL.Path, "/repos/")
		repo := strings.TrimSuffix(path, "/pulls")
		n, _ := createdPRs.LoadOrStore(repo, 0)
		createdPRs.Store(repo, n.(int)+1)
		_, _ = fmt.Fprintf(w, `{"html_url":"https://github.com/%s/pull/1","number":1}`, repo)
	default:
		w.WriteHeader(404)
	}
}

func setupBareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")
	for _, args := range [][]string{
		{"init", "--bare", "--initial-branch=main", remote},
		{"init", "--initial-branch=main", work},
	} {
		c := osExec("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-C", work, "config", "user.email", "t@t"},
		{"-C", work, "config", "user.name", "t"},
		{"-C", work, "add", "."},
		{"-C", work, "commit", "-m", "init"},
		{"-C", work, "remote", "add", "origin", remote},
		{"-C", work, "push", "-u", "origin", "main"},
	} {
		c := osExec("git", args...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Set HEAD on the bare so DefaultBranch can resolve it after clone.
	c := osExec("git", "-C", remote, "symbolic-ref", "HEAD", "refs/heads/main")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("symbolic-ref: %v\n%s", err, out)
	}
	return remote
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "code-test-")
	if err != nil {
		panic(err)
	}

	// Fake claude binary on PATH that writes a small file in $PWD when
	// invoked as the code agent (any prompt that's not the arch PLAN one).
	binDir := filepath.Join(dir, "bin")
	_ = os.MkdirAll(binDir, 0o755)
	script := `#!/bin/sh
[ -z "$CODE_TEST_NO_WRITE" ] && echo "implementation" > implementation.go
echo "ok"
`
	_ = os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755)
	_ = os.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	_ = os.Setenv("GIT_AUTHOR_NAME", "t")
	_ = os.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	_ = os.Setenv("GIT_COMMITTER_NAME", "t")
	_ = os.Setenv("GIT_COMMITTER_EMAIL", "t@t")

	fakeJira = httptest.NewServer(http.HandlerFunc(fakeJiraHandler))
	fakeGithub = httptest.NewServer(http.HandlerFunc(fakeGithubHandler))
	github.SetAPIBaseForTest(fakeGithub.URL)

	// Override test seams: skip the github auth-remote rewrite so push
	// continues against the local bare remote configured by clone.
	configureAuthRemote = func(string, string) error { return nil }

	cfg := strings.ReplaceAll(cfgJSON, "BASEURL", fakeJira.URL)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		panic(err)
	}
	config.SetDir(dir)
	jira.Reinit()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := db.Start(ctx, filepath.Join(dir, "db")); err != nil {
		os.Stderr.WriteString("code tests: db skipped: " + err.Error() + "\n")
	} else {
		dbReady = true
	}

	code := m.Run()

	if dbReady {
		_ = db.Stop()
	}
	fakeJira.Close()
	fakeGithub.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func requireDB(t *testing.T) {
	t.Helper()
	if !dbReady {
		t.Skip("db not available")
	}
}

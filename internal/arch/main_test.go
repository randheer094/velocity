package arch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/prompts"
)

var (
	fakeJira       *httptest.Server
	dbReady        bool
	dbDir          string
	transitionsLog sync.Map // issueKey -> last requested status name
	statusOverride sync.Map // issueKey -> status name returned by GetTasksStatus
)

// fakeJiraHandler responds plausibly for the endpoints used by recordFailure,
// OnDismissed, AdvanceWave, assignWave, archiveDone.
func fakeJiraHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/search/jql") && r.Method == http.MethodGet:
		// Parse JQL "issueKey in (A,B,C)"
		jql := r.URL.Query().Get("jql")
		jql = strings.TrimPrefix(jql, "issueKey in (")
		jql = strings.TrimSuffix(jql, ")")
		keys := strings.Split(jql, ",")
		var issues []string
		for _, k := range keys {
			k = strings.TrimSpace(k)
			st := "To Do"
			if v, ok := statusOverride.Load(k); ok {
				st = v.(string)
			}
			issues = append(issues, fmt.Sprintf(`{"key":%q,"fields":{"status":{"name":%q},"assignee":null}}`, k, st))
		}
		_, _ = fmt.Fprintf(w, `{"issues":[%s]}`, strings.Join(issues, ","))
	case strings.HasSuffix(r.URL.Path, "/comment") && r.Method == http.MethodPost:
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"1"}`))
	case strings.HasSuffix(r.URL.Path, "/transitions") && r.Method == http.MethodGet:
		_, _ = w.Write([]byte(`{"transitions":[{"id":"11","to":{"name":"Done"}},{"id":"12","to":{"name":"Dismissed"}},{"id":"13","to":{"name":"Planning Failed"}},{"id":"14","to":{"name":"Dev Failed"}}]}`))
	case strings.HasSuffix(r.URL.Path, "/transitions") && r.Method == http.MethodPost:
		// Capture target status
		var body struct {
			Transition struct{ ID string } `json:"transition"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// echo by id
		key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
		key = strings.TrimSuffix(key, "/transitions")
		transitionsLog.Store(key, body.Transition.ID)
		w.WriteHeader(204)
	case strings.HasSuffix(r.URL.Path, "/assignee") && r.Method == http.MethodPut:
		w.WriteHeader(204)
	case strings.HasSuffix(r.URL.Path, "/rest/api/3/issue") && r.Method == http.MethodPost:
		// CreateSubtask: return a fake key based on the summary
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		fields, _ := body["fields"].(map[string]any)
		summary, _ := fields["summary"].(string)
		key := fmt.Sprintf("PROJ-%d", len(summary)+100)
		_, _ = fmt.Fprintf(w, `{"key":%q}`, key)
	case strings.Contains(r.URL.Path, "/issue/") && r.Method == http.MethodGet:
		// Used by GetTasksStatus path: GET /issue/{key}?fields=status,assignee
		key := strings.TrimPrefix(r.URL.Path, "/rest/api/3/issue/")
		key = strings.SplitN(key, "/", 2)[0]
		st := "Done"
		if v, ok := statusOverride.Load(key); ok {
			st = v.(string)
		}
		_, _ = fmt.Fprintf(w, `{"key":%q,"fields":{"status":{"name":%q},"assignee":{"accountId":"u"}}}`, key, st)
	default:
		w.WriteHeader(404)
	}
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "arch-test-")
	if err != nil {
		panic(err)
	}
	dbDir = dir

	// Fake claude binary on PATH that emits a valid plan when prompted.
	binDir := filepath.Join(dir, "bin")
	_ = os.MkdirAll(binDir, 0o755)
	script := `#!/bin/sh
last=""
for a in "$@"; do last="$a"; done
case "${ARCH_TEST_MODE:-}" in
  fail)
    echo "synthetic claude failure" >&2
    exit 1
    ;;
  bad-json)
    echo "<<<PLAN_BEGIN>>>this is not json<<<PLAN_END>>>"
    exit 0
    ;;
  empty-tasks)
    echo '<<<PLAN_BEGIN>>>{"waves":[{"tasks":[]}]}<<<PLAN_END>>>'
    exit 0
    ;;
  empty-waves)
    echo '<<<PLAN_BEGIN>>>{"waves":[]}<<<PLAN_END>>>'
    exit 0
    ;;
esac
case "$last" in
  *PLAN_BEGIN*)
    cat <<EOF
<<<PLAN_BEGIN>>>
{"waves":[{"tasks":[{"title":"first"}]},{"tasks":[{"title":"second"}]}]}
<<<PLAN_END>>>
EOF
    ;;
  *)
    echo "ok"
    ;;
esac
`
	_ = os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755)
	_ = os.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	fakeJira = httptest.NewServer(http.HandlerFunc(fakeJiraHandler))
	cfg := strings.ReplaceAll(cfgJSON, "https://example.atlassian.net", fakeJira.URL)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		panic(err)
	}
	config.SetDir(dir)
	jira.Reinit()

	if err := seedFixturePromptsForMain(dir); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := db.Start(ctx); err != nil {
		os.Stderr.WriteString("arch tests: db skipped: " + err.Error() + "\n")
	} else {
		dbReady = true
	}

	code := m.Run()

	if dbReady {
		_ = db.Stop()
	}
	fakeJira.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func requireDB(t *testing.T) {
	t.Helper()
	if !dbReady {
		t.Skip("db not available")
	}
}

// seedFixturePromptsForMain installs a small resource cache + loads it
// so TestMain's environment matches what a real `velocity setup` would
// produce. Used by TestMain only; per-test installations use
// loadFixturePrompts which goes through prompts.SetForTest.
func seedFixturePromptsForMain(dir string) error {
	resDir := filepath.Join(dir, "resources")
	files := map[string]string{
		"prompts/manifest.yaml": `version: 0
prompts:
  - id: arch_plan
    path: arch/plan.md
    placeholders: [PlanBegin, PlanEnd, ParentKey, Requirement]
  - id: failure_jira
    path: failure/jira.md
    placeholders: [Role, Stage, Message]
`,
		"prompts/arch/plan.md":    "{{.PlanBegin}} parent={{.ParentKey}} req={{.Requirement}} {{.PlanEnd}}",
		"prompts/failure/jira.md": "Velocity {{.Role}} failed at stage {{.Stage}}: {{.Message}}",
		"VERSION":                 "v0.0.0",
	}
	for rel, body := range files {
		full := filepath.Join(resDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return prompts.Load(resDir)
}

// cleanPlan removes any residual plan rows (and children) for parentKey so
// tests exercising the full fresh-plan path aren't short-circuited by the
// retry guard on a stale DB.
func cleanPlan(t *testing.T, parentKey string) {
	t.Helper()
	if !dbReady {
		return
	}
	ctx := context.Background()
	_ = db.WipePlanChildren(ctx, parentKey)
	_, _ = db.Shared().Exec(ctx, "DELETE FROM plans WHERE parent_jira_key=$1", parentKey)
}

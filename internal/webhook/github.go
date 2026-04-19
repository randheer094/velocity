package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/randheer094/velocity/internal/code"
	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/github"
)

const githubSignatureHeader = "X-Hub-Signature-256"

// GithubHandler dispatches pull_request, workflow_run, and
// issue_comment events. The head branch (= sub-task key) is the
// lookup key for an existing velocity code task.
type GithubHandler struct{}

func (h GithubHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	secret := os.Getenv(config.GithubWebhookSecretEnv)
	if !verifyHMACSHA256(secret, r.Header.Get(githubSignatureHeader), body) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	switch event {
	case "pull_request":
		handlePullRequest(w, body)
	case "workflow_run":
		handleWorkflowRun(w, body)
	case "issue_comment":
		handleIssueComment(w, body)
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "event": event})
	}
}

func handlePullRequest(w http.ResponseWriter, body []byte) {
	var payload struct {
		Action      string `json:"action"`
		PullRequest struct {
			Merged  bool   `json:"merged"`
			HTMLURL string `json:"html_url"`
			Head    struct {
				Ref string `json:"ref"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if payload.Action != "closed" || !payload.PullRequest.Merged {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "action": payload.Action})
		return
	}

	branch := payload.PullRequest.Head.Ref
	if !data.ValidJiraKey(branch) {
		slog.Info("github webhook: branch is not a jira key, ignoring", "branch", branch)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "branch": branch})
		return
	}

	slog.Info("github webhook: PR merged", "branch", branch, "url", payload.PullRequest.HTMLURL)
	prURL := payload.PullRequest.HTMLURL
	Enqueue(Job{
		Name: "code.MarkMerged:" + branch,
		Fn: func(ctx context.Context) {
			if err := code.MarkMerged(ctx, branch, prURL); err != nil {
				slog.Error("code: mark merged failed", "key", branch, "err", err)
			}
		},
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "key": branch})
}

func handleWorkflowRun(w http.ResponseWriter, body []byte) {
	var payload struct {
		Action      string `json:"action"`
		WorkflowRun struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			HTMLURL    string `json:"html_url"`
			Conclusion string `json:"conclusion"`
			HeadBranch string `json:"head_branch"`
		} `json:"workflow_run"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if payload.Action != "completed" || payload.WorkflowRun.Conclusion != "failure" {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "conclusion": payload.WorkflowRun.Conclusion})
		return
	}

	branch := payload.WorkflowRun.HeadBranch
	if !data.ValidJiraKey(branch) {
		slog.Info("github webhook: workflow_run head branch is not a jira key", "branch", branch)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "branch": branch})
		return
	}

	task := getCodeTaskByKey(branch)
	if task == nil {
		slog.Info("github webhook: workflow_run for unknown branch, ignoring", "branch", branch)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "unknown"})
		return
	}
	if task.Status != data.CodeInReview {
		slog.Info("github webhook: workflow_run but task not in review, ignoring",
			"branch", branch, "status", task.Status)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "not-in-review"})
		return
	}

	slog.Info("github webhook: CI failure, iterating", "branch", branch, "workflow", payload.WorkflowRun.Name)
	summary := fetchWorkflowFailureSummary(payload.Repository.FullName, payload.WorkflowRun.ID)
	reason := buildWorkflowRunInstruction(payload.WorkflowRun.Name, payload.WorkflowRun.HTMLURL, summary)
	hint := deriveCICommitHint(payload.WorkflowRun.Name, summary)
	Enqueue(Job{
		Name: "code.Iterate:ci:" + branch,
		Fn: func(ctx context.Context) {
			code.Iterate(ctx, branch, code.IterateCI, reason, hint)
		},
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "key": branch})
}

// fetchWorkflowFailureSummary is a package-level var so tests can stub
// the network call out.
var fetchWorkflowFailureSummary = func(repoFullName string, runID int64) string {
	if repoFullName == "" || runID == 0 {
		return ""
	}
	return github.New().WorkflowRunFailureSummary(repoFullName, runID)
}

// buildWorkflowRunInstruction composes the /velocity-free iterate
// prompt that carries the failing CI context to the coder.
func buildWorkflowRunInstruction(name, htmlURL, summary string) string {
	if summary != "" {
		return fmt.Sprintf(
			"CI check %q failed on this PR. Run: %s\n\nFailure logs:\n%s\n\nDiagnose the failure and push a fix that turns the checks green.",
			name, htmlURL, summary)
	}
	return fmt.Sprintf(
		"CI check %q failed on this PR. Run: %s\n\n(Log fetch failed — inspect the run manually.) Diagnose the failure and push a fix that turns the checks green.",
		name, htmlURL)
}

// deriveCICommitHint picks a short commit subject from the failure
// log: the first error-looking line, trimmed of noise and capped. On
// empty summary, returns "fix CI: <workflow>".
func deriveCICommitHint(workflow, summary string) string {
	const cap = 60
	if summary == "" {
		return "fix CI: " + truncateHint(workflow, cap)
	}
	for _, raw := range strings.Split(summary, "\n") {
		l := strings.TrimSpace(stripLogTimestamp(raw))
		if l == "" {
			continue
		}
		low := strings.ToLower(l)
		if strings.Contains(low, "error:") ||
			strings.HasPrefix(low, "error ") ||
			strings.HasPrefix(low, "fail") ||
			strings.HasPrefix(low, "--- fail") ||
			strings.Contains(low, " failed") {
			return "fix CI: " + truncateHint(l, cap)
		}
	}
	return "fix CI: " + truncateHint(workflow, cap)
}

// stripLogTimestamp drops the "2026-01-02T15:04:05.000Z " prefix that
// GitHub puts on every log line.
func stripLogTimestamp(s string) string {
	if len(s) > 20 && s[4] == '-' && s[7] == '-' && s[10] == 'T' {
		if idx := strings.Index(s, " "); idx > 0 && idx < len(s)-1 {
			return s[idx+1:]
		}
	}
	return s
}

func truncateHint(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func handleIssueComment(w http.ResponseWriter, body []byte) {
	var payload struct {
		Action string `json:"action"`
		Issue  struct {
			Number      int `json:"number"`
			PullRequest *struct {
				URL string `json:"url"`
			} `json:"pull_request"`
		} `json:"issue"`
		Comment struct {
			Body string `json:"body"`
		} `json:"comment"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if payload.Action != "created" || payload.Issue.PullRequest == nil {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "not-pr-comment"})
		return
	}
	instruction := strings.TrimSpace(payload.Comment.Body)
	if !strings.HasPrefix(instruction, "/velocity") {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "no-prefix"})
		return
	}
	instruction = strings.TrimSpace(strings.TrimPrefix(instruction, "/velocity"))

	repo := payload.Repository.FullName
	prNumber := payload.Issue.Number

	branch := lookupBranchForPR(repo, prNumber)
	task := getCodeTaskByKey(branch)
	if task == nil || task.Status != data.CodeInReview {
		slog.Info("github webhook: /velocity on out-of-scope PR", "repo", repo, "pr", prNumber, "branch", branch)
		if repo != "" && prNumber > 0 {
			github.New().AddPRComment(repo, prNumber, "Cannot perform any action on this")
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "out-of-scope"})
		return
	}
	if instruction == "" {
		github.New().AddPRComment(repo, prNumber, "Usage: `/velocity <instruction>` — describe the change you want.")
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "reason": "empty"})
		return
	}

	slog.Info("github webhook: /velocity command", "branch", branch, "instruction", instruction)
	extra := "User command posted on the open PR: " + instruction
	hint := truncateHint(instruction, 60)
	Enqueue(Job{
		Name: "code.Iterate:cmd:" + branch,
		Fn: func(ctx context.Context) {
			code.Iterate(ctx, branch, code.IterateCommand, extra, hint)
		},
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "key": branch})
}

// getCodeTaskByKey is a var so tests can stub the DB lookup without
// needing a live Postgres in the webhook package.
var getCodeTaskByKey = func(key string) *data.CodeTask {
	t, _ := db.GetCodeTask(context.Background(), key)
	return t
}

// lookupBranchForPR asks GitHub for a PR's head ref. Returning "" is
// safe — callers fall back to the "out of scope" path.
var lookupBranchForPR = func(repoFullName string, number int) string {
	if repoFullName == "" || number <= 0 {
		return ""
	}
	return github.New().PRHeadBranch(repoFullName, number)
}

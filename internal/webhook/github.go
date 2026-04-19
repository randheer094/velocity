package webhook

import (
	"context"
	"encoding/json"
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
			Name        string   `json:"name"`
			HTMLURL     string   `json:"html_url"`
			Conclusion  string   `json:"conclusion"`
			HeadBranch  string   `json:"head_branch"`
			PullRequests []struct {
				Number int `json:"number"`
			} `json:"pull_requests"`
		} `json:"workflow_run"`
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

	task, _ := db.GetCodeTask(context.Background(), branch)
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
	reason := "CI check \"" + payload.WorkflowRun.Name + "\" failed on this PR. Run details: " + payload.WorkflowRun.HTMLURL +
		".\n\nPull the latest branch, diagnose the failing workflow, and push a fix that turns the checks green."
	Enqueue(Job{
		Name: "code.Iterate:ci:" + branch,
		Fn: func(ctx context.Context) {
			code.Iterate(ctx, branch, code.IterateCI, reason)
		},
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "key": branch})
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
	task, _ := db.GetCodeTask(context.Background(), branch)
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
	Enqueue(Job{
		Name: "code.Iterate:cmd:" + branch,
		Fn: func(ctx context.Context) {
			code.Iterate(ctx, branch, code.IterateCommand, extra)
		},
	})
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "key": branch})
}

// lookupBranchForPR asks GitHub for a PR's head ref. Returning "" is
// safe — callers fall back to the "out of scope" path.
var lookupBranchForPR = func(repoFullName string, number int) string {
	if repoFullName == "" || number <= 0 {
		return ""
	}
	return github.New().PRHeadBranch(repoFullName, number)
}

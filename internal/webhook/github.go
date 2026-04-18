package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/randheer094/velocity/internal/code"
	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
)

const githubSignatureHeader = "X-Hub-Signature-256"

// GithubHandler handles pull_request closed+merged events.
// The head branch is the sub-task key (invariant maintained by code.Run).
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
	secret, _ := config.GetSecret(config.GithubWebhookSecretKey)
	if !verifyHMACSHA256(secret, r.Header.Get(githubSignatureHeader), body) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	if event != "pull_request" {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored", "event": event})
		return
	}

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

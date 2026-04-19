// Package webhook is the single HTTP entry point for Jira + GitHub events.
package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/randheer094/velocity/internal/arch"
	"github.com/randheer094/velocity/internal/code"
	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/status"
)

// Jira Cloud emits "sha256=<hex>" here, same format as GitHub's
// X-Hub-Signature-256 — only the header name differs.
const jiraSignatureHeader = "X-Hub-Signature"

type JiraHandler struct{}

func (h JiraHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := config.Get()
	if cfg == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "reason": "setup required"})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	secret := os.Getenv(config.JiraWebhookSecretEnv)
	if !verifyHMACSHA256(secret, r.Header.Get(jiraSignatureHeader), body) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	event, _ := payload["webhookEvent"].(string)
	eventType, _ := payload["issue_event_type_name"].(string)
	slog.Info("jira webhook", "event", event, "type", eventType)

	issue, _ := payload["issue"].(map[string]any)
	fields, _ := issue["fields"].(map[string]any)
	key, _ := issue["key"].(string)
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "reason": "missing issue.key"})
		return
	}

	switch eventType {
	case "issue_assigned":
		handleAssigned(key, fields, cfg)
	case "issue_generic", "issue_updated":
		handleUpdated(key, fields)
	default:
		slog.Info("jira webhook ignored", "type", eventType)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "key": key})
}

func handleAssigned(key string, fields map[string]any, cfg *config.Config) {
	assigneeID := ""
	if assignee, ok := fields["assignee"].(map[string]any); ok {
		assigneeID, _ = assignee["accountId"].(string)
	}

	issueType := ""
	isSubtask := false
	if it, ok := fields["issuetype"].(map[string]any); ok {
		issueType, _ = it["name"].(string)
		if sub, ok := it["subtask"].(bool); ok {
			isSubtask = sub
		}
	}
	parentKey := ""
	if parent, ok := fields["parent"].(map[string]any); ok {
		parentKey, _ = parent["key"].(string)
	}
	if parentKey != "" {
		isSubtask = true
	}

	summary, _ := fields["summary"].(string)
	description := jira.FlattenADF(fields["description"])
	repoURL := extractRepoURL(fields, cfg.Jira.RepoURLField)

	if isSubtask {
		if assigneeID != cfg.Jira.DeveloperJiraID {
			slog.Info("jira webhook: subtask not assigned to developer, ignoring", "key", key, "assignee", assigneeID)
			return
		}
		if parentKey == "" {
			slog.Warn("developer-assigned subtask has no parent — ignoring", "key", key, "type", issueType)
			return
		}
		if repoURL == "" {
			repoURL = lookupParentRepo(parentKey, cfg.Jira.RepoURLField)
		}
		if repoURL == "" {
			slog.Warn("cannot resolve repo_url for developer subtask", "key", key, "parent", parentKey)
			return
		}
		Enqueue(Job{
			Name: "code.Run:" + key,
			Fn: func(ctx context.Context) {
				code.Run(ctx, key, parentKey, repoURL, summary, description)
			},
		})
		return
	}

	if assigneeID != cfg.Jira.ArchitectJiraID {
		slog.Info("jira webhook: parent task not assigned to architect, ignoring", "key", key, "assignee", assigneeID)
		return
	}
	if repoURL == "" {
		slog.Warn("jira webhook missing repository_url for architect", "key", key)
		return
	}
	if description == "" {
		slog.Warn("jira webhook missing description for architect", "key", key)
		return
	}
	requirement := summary + "\n\n" + description
	Enqueue(Job{
		Name: "arch.Run:" + key,
		Fn: func(ctx context.Context) {
			arch.Run(ctx, key, repoURL, summary, requirement)
		},
	})
}

func handleUpdated(key string, fields map[string]any) {
	st := ""
	if s, ok := fields["status"].(map[string]any); ok {
		st, _ = s["name"].(string)
	}
	parentKey := ""
	if parent, ok := fields["parent"].(map[string]any); ok {
		parentKey, _ = parent["key"].(string)
	}
	isSubtask := parentKey != ""
	if !isSubtask {
		if it, ok := fields["issuetype"].(map[string]any); ok {
			if sub, ok := it["subtask"].(bool); ok {
				isSubtask = sub
			}
		}
	}

	if isSubtask {
		if status.SubtaskCanonical(st) != status.Done {
			return
		}
		if status.IsSubtaskDismissAlias(st) {
			slog.Info("jira webhook: subtask DISMISSED", "key", key, "parent", parentKey)
			Enqueue(Job{
				Name: "code.OnDismissed:" + key,
				Fn: func(ctx context.Context) {
					if err := code.OnDismissed(ctx, key, st); err != nil {
						slog.Error("code: dismiss failed", "key", key, "err", err)
					}
					if parentKey != "" {
						if err := arch.AdvanceWave(ctx, parentKey); err != nil {
							slog.Error("arch: advance after dismiss failed", "parent", parentKey, "err", err)
						}
					}
				},
			})
			return
		}
		if parentKey == "" {
			return
		}
		slog.Info("jira webhook: subtask DONE, advancing parent", "key", key, "parent", parentKey)
		Enqueue(Job{
			Name: "arch.AdvanceWave:" + parentKey,
			Fn: func(ctx context.Context) {
				if err := arch.AdvanceWave(ctx, parentKey); err != nil {
					slog.Error("arch: advance failed", "parent", parentKey, "err", err)
				}
			},
		})
		return
	}

	if status.IsTaskDismissAlias(st) {
		slog.Info("jira webhook: parent DISMISSED", "key", key)
		Enqueue(Job{
			Name: "arch.OnDismissed:" + key,
			Fn: func(ctx context.Context) {
				if err := arch.OnDismissed(ctx, key, st); err != nil {
					slog.Error("arch: dismiss failed", "key", key, "err", err)
				}
			},
		})
	}
}

// extractRepoURL reads the operator-configured custom field. Accepts
// plain strings and {"value": "..."} shaped payloads.
func extractRepoURL(fields map[string]any, fieldName string) string {
	if fieldName == "" {
		return ""
	}
	switch v := fields[fieldName].(type) {
	case string:
		return v
	case map[string]any:
		if s, ok := v["value"].(string); ok {
			return s
		}
	}
	return ""
}

func lookupParentRepo(parentKey, fieldName string) string {
	client := jira.Shared()
	if client == nil {
		return ""
	}
	parent := client.GetIssue(parentKey)
	if parent == nil {
		return ""
	}
	fields, _ := parent["fields"].(map[string]any)
	return extractRepoURL(fields, fieldName)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

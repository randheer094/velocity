// Package webhook is the single HTTP entry point for Jira + GitHub events.
package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/status"
)

// Jira Cloud emits "sha256=<hex>" here, same format as GitHub's
// X-Hub-Signature-256 — only the header name differs.
const jiraSignatureHeader = "X-Hub-Signature"

// MaxWebhookBody caps the request body the handlers will read. Real
// Jira / GitHub webhook payloads sit well under 1 MB; the cap is
// generous enough to cover oddball ADF descriptions while preventing
// memory exhaustion from a malicious or misconfigured sender.
const MaxWebhookBody = 5 << 20

// MaxRequirementChars caps the summary+description text handed to the
// architect LLM. Sized to stay well within the Claude Code 250K-token
// context window after reserving room for the system prompt, tool
// schemas, and tool-call rounds that read the codebase during planning.
// The Jira ticket itself is untouched; only the text sent to the LLM
// is truncated.
const MaxRequirementChars = 150_000

const requirementTruncationMarker = "\n\n[…truncated to fit 250K context window]"

// capRequirement trims s to MaxRequirementChars, appending a marker
// on truncation so the architect knows the tail was dropped.
func capRequirement(s string) string {
	if len(s) <= MaxRequirementChars {
		return s
	}
	keep := MaxRequirementChars - len(requirementTruncationMarker)
	if keep < 0 {
		keep = 0
	}
	return s[:keep] + requirementTruncationMarker
}

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
	if !Started() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "reason": "queue not started"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxWebhookBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusRequestEntityTooLarge)
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
		handleAssigned(r.Context(), key, fields, cfg)
	case "issue_generic", "issue_updated":
		handleUpdated(key, fields)
	default:
		slog.Info("jira webhook ignored", "type", eventType)
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "key": key})
}

func handleAssigned(ctx context.Context, key string, fields map[string]any, cfg *config.Config) {
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
			repoURL = lookupParentRepo(ctx, parentKey, cfg.Jira.RepoURLField)
		}
		if repoURL == "" {
			slog.Warn("cannot resolve repo_url for developer subtask", "key", key, "parent", parentKey)
			return
		}
		Enqueue(KindCodeRun, "code.Run:"+key, codeRunPayload{
			Key:         key,
			ParentKey:   parentKey,
			RepoURL:     repoURL,
			Summary:     summary,
			Description: description,
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
	if len(requirement) > MaxRequirementChars {
		slog.Warn("jira webhook: requirement exceeds cap, truncating",
			"key", key, "length", len(requirement), "cap", MaxRequirementChars)
		requirement = capRequirement(requirement)
	}
	Enqueue(KindArchRun, "arch.Run:"+key, archRunPayload{
		Key:         key,
		RepoURL:     repoURL,
		Summary:     summary,
		Requirement: requirement,
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
			Enqueue(KindCodeOnDismissed, "code.OnDismissed:"+key, codeOnDismissedPayload{
				Key:        key,
				JiraStatus: st,
				ParentKey:  parentKey,
			})
			return
		}
		if parentKey == "" {
			return
		}
		slog.Info("jira webhook: subtask DONE, advancing parent", "key", key, "parent", parentKey)
		Enqueue(KindArchAdvanceWave, "arch.AdvanceWave:"+parentKey, archAdvanceWavePayload{
			ParentKey: parentKey,
		})
		return
	}

	if status.IsTaskDismissAlias(st) {
		slog.Info("jira webhook: parent DISMISSED", "key", key)
		Enqueue(KindArchOnDismissed, "arch.OnDismissed:"+key, archOnDismissedPayload{
			Key:        key,
			JiraStatus: st,
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

func lookupParentRepo(ctx context.Context, parentKey, fieldName string) string {
	client := jira.Shared()
	if client == nil {
		return ""
	}
	parent := client.GetIssue(ctx, parentKey)
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

package code

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/prompts"
	"github.com/randheer094/velocity/internal/status"
)

// renderFailureComment falls back to a hardcoded one-liner when the
// template is unavailable so a template bug never silently swallows a
// real failure record.
func renderFailureComment(role, stage, msg string) string {
	out, err := prompts.Render("failure_jira", failureData{Role: role, Stage: stage, Message: msg})
	if err != nil {
		slog.Warn("code: render failure_jira fallback", "err", err)
		return fmt.Sprintf("Velocity %s failed at stage %s: %s", role, stage, msg)
	}
	return out
}

func renderIterateJiraComment(reason, stage, msg string) string {
	out, err := prompts.Render("failure_iterate_jira", iterateJiraData{Reason: reason, Stage: stage, Message: msg})
	if err != nil {
		slog.Warn("code: render failure_iterate_jira fallback", "err", err)
		return fmt.Sprintf("Velocity iterate (%s) failed at stage %s: %s", reason, stage, msg)
	}
	return out
}

func renderIteratePRComment(stage, msg string) string {
	out, err := prompts.Render("failure_iterate_pr", iteratePRData{Stage: stage, Message: msg})
	if err != nil {
		slog.Warn("code: render failure_iterate_pr fallback", "err", err)
		return fmt.Sprintf("Velocity iterate failed at stage %s: %s", stage, msg)
	}
	return out
}

// Best-effort scrub — the unredacted error stays in daemon.log.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_\-]{10,}`),
	regexp.MustCompile(`(?i)token=[^\s&]+`),
}

const maxErrChars = 1000

func redactAndTruncate(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	if len(s) > maxErrChars {
		s = s[:maxErrChars] + "..."
	}
	return s
}

// recordFailure is the single failure sink for code.Run. Best-effort:
// sub-step errors are logged and skipped.
func recordFailure(ctx context.Context, issueKey, parentKey, repoURL, title, stage string, err error) {
	msg := redactAndTruncate(err.Error())
	slog.Error("code: stage failed", "key", issueKey, "stage", stage, "err", err)
	failedName := status.SubtaskJiraName(status.CodingFailed)
	if client := jira.Shared(); client != nil {
		_ = client.CommentIssue(ctx, issueKey, renderFailureComment("code", stage, msg))
		if failedName != "" {
			_ = client.Transition(ctx, issueKey, failedName)
		}
	}
	if err := db.MarkCodeFailed(ctx, issueKey, parentKey, repoURL, title, issueKey, failedName, stage, msg); err != nil {
		slog.Warn("code: mark failed (db)", "key", issueKey, "err", err)
	}
	_ = os.RemoveAll(config.WorkspacePath(issueKey))
}

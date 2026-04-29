package arch

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

type failureCommentData struct {
	Role    string
	Stage   string
	Message string
}

// renderFailureComment falls back to a hardcoded one-liner when the
// template is unavailable (e.g. setup never ran, or template parse
// error during an in-flight reload). A template bug must never silently
// swallow a real failure record.
func renderFailureComment(role, stage, msg string) string {
	out, err := prompts.Render("failure_jira", failureCommentData{Role: role, Stage: stage, Message: msg})
	if err != nil {
		slog.Warn("arch: render failure_jira fallback", "err", err)
		return fmt.Sprintf("Velocity %s failed at stage %s: %s", role, stage, msg)
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

// recordFailure is the single failure sink for arch.Run. Best-effort:
// sub-step errors are logged and skipped.
func recordFailure(ctx context.Context, parentKey, stage string, err error) {
	msg := redactAndTruncate(err.Error())
	slog.Error("arch: stage failed", "key", parentKey, "stage", stage, "err", err)
	failedName := status.TaskJiraName(status.PlanningFailed)
	if client := jira.Shared(); client != nil {
		_ = client.CommentIssue(parentKey, renderFailureComment("arch", stage, msg))
		if failedName != "" {
			_ = client.Transition(parentKey, failedName)
		}
	}
	if err := db.MarkPlanFailed(ctx, parentKey, failedName, stage, msg); err != nil {
		slog.Warn("arch: mark plan failed (db)", "key", parentKey, "err", err)
	}
	_ = os.RemoveAll(config.WorkspacePath(parentKey))
}

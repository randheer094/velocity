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
	"github.com/randheer094/velocity/internal/status"
)

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

func formatFailureComment(role, stage, msg string) string {
	return fmt.Sprintf(
		"Velocity %s failed at stage *%s*.\n\n```\n%s\n```\n\nSee daemon.log for full details.",
		role, stage, msg,
	)
}

// recordFailure is the single failure sink for arch.Run. Best-effort:
// sub-step errors are logged and skipped.
func recordFailure(ctx context.Context, parentKey, stage string, err error) {
	msg := redactAndTruncate(err.Error())
	slog.Error("arch: stage failed", "key", parentKey, "stage", stage, "err", err)
	if client := jira.Shared(); client != nil {
		_ = client.CommentIssue(parentKey, formatFailureComment("arch", stage, msg))
		if name := status.TaskJiraName(status.PlanningFailed); name != "" {
			_ = client.Transition(parentKey, name)
		}
	}
	if err := db.MarkPlanFailed(ctx, parentKey, stage, msg); err != nil {
		slog.Warn("arch: mark plan failed (db)", "key", parentKey, "err", err)
	}
	_ = os.RemoveAll(config.WorkspacePath(parentKey))
}

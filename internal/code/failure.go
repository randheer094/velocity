package code

import (
	"context"
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

// recordFailure is the single failure sink for code.Run. Best-effort:
// sub-step errors are logged and skipped.
func recordFailure(ctx context.Context, issueKey, parentKey, repoURL, title, stage string, err error) {
	msg := redactAndTruncate(err.Error())
	slog.Error("code: stage failed", "key", issueKey, "stage", stage, "err", err)
	failedName := status.SubtaskJiraName(status.CodingFailed)
	if client := jira.Shared(); client != nil {
		_ = client.CommentIssue(issueKey, formatFailureComment("code", stage, msg))
		if failedName != "" {
			_ = client.Transition(issueKey, failedName)
		}
	}
	if err := db.MarkCodeFailed(ctx, issueKey, parentKey, repoURL, title, issueKey, failedName, stage, msg); err != nil {
		slog.Warn("code: mark failed (db)", "key", issueKey, "err", err)
	}
	_ = os.RemoveAll(config.WorkspacePath(issueKey))
}

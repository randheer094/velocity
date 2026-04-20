package arch

import (
	"context"
	"log/slog"
	"os"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
	"github.com/randheer094/velocity/internal/jira"
	"github.com/randheer094/velocity/internal/status"
)

// OnDismissed cascades DISMISSED to every still-open sub-task.
// Best-effort: per-step failures are logged but do not abort.
// jiraStatus is the parent's Jira status name from the dismiss webhook.
func OnDismissed(ctx context.Context, parentKey, jiraStatus string) error {
	cancelIfRunning(parentKey)

	p, err := db.GetPlan(ctx, parentKey)
	if err != nil {
		return err
	}
	if p == nil {
		slog.Info("arch: dismiss for parent without plan, nothing to cascade", "key", parentKey)
		return nil
	}
	if p.Status == data.PlanDone {
		slog.Info("arch: plan already terminal, ignoring dismiss", "key", parentKey, "status", p.Status)
		return nil
	}

	client := jira.Shared()
	dismissedName := status.SubtaskDismissJiraName()
	if client != nil && dismissedName != "" {
		var subKeys []string
		for _, w := range p.Waves {
			for _, t := range w.Tasks {
				if t.JiraKey != "" {
					subKeys = append(subKeys, t.JiraKey)
				}
			}
		}
		infos := client.GetTasksStatus(subKeys)
		for _, key := range subKeys {
			info, ok := infos[key]
			if !ok {
				continue
			}
			switch status.SubtaskCanonical(info.Status) {
			case status.Done, status.CodingFailed:
				continue
			}
			if !client.Transition(key, dismissedName) {
				slog.Warn("arch: cascade dismiss failed", "parent", parentKey, "sub", key)
			}
		}
	}

	if err := db.MarkPlanDismissed(ctx, parentKey, jiraStatus); err != nil {
		slog.Warn("arch: mark plan dismissed", "key", parentKey, "err", err)
	}
	_ = os.RemoveAll(config.WorkspacePath(parentKey))
	return nil
}

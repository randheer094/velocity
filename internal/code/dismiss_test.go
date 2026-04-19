package code

import (
	"context"
	"testing"

	"github.com/randheer094/velocity/internal/data"
	"github.com/randheer094/velocity/internal/db"
)

func TestOnDismissedNoTask(t *testing.T) {
	requireDB(t)
	if err := OnDismissed(context.Background(), "CODE-NOPE", "Dismissed"); err != nil {
		t.Errorf("OnDismissed: %v", err)
	}
}

func TestOnDismissedExisting(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "CODE-DM",
		ParentJiraKey: "CODE-1",
		RepoURL:       "r",
		Title:         "x",
		Branch:        "CODE-DM",
		Status:        data.CodeCoding,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := OnDismissed(ctx, "CODE-DM", "Dismissed"); err != nil {
		t.Fatalf("OnDismissed: %v", err)
	}
	got, _ := db.GetCodeTask(ctx, "CODE-DM")
	if got.Status != data.CodeDone || got.JiraStatus != "Dismissed" {
		t.Errorf("status = %q jira = %q", got.Status, got.JiraStatus)
	}
}

func TestMarkMergedFull(t *testing.T) {
	requireDB(t)
	ctx := context.Background()
	task := &data.CodeTask{
		IssueKey:      "CODE-M",
		ParentJiraKey: "CODE-1",
		RepoURL:       "r",
		Title:         "x",
		Branch:        "CODE-M",
		Status:        data.CodeInReview,
	}
	if err := db.SaveCodeTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := MarkMerged(ctx, "CODE-M", "https://pr"); err != nil {
		t.Fatalf("MarkMerged: %v", err)
	}
	got, _ := db.GetCodeTask(ctx, "CODE-M")
	if got.Status != data.CodeDone || got.PRURL != "https://pr" {
		t.Errorf("got = %+v", got)
	}
}

func TestMarkMergedNoTask(t *testing.T) {
	requireDB(t)
	if err := MarkMerged(context.Background(), "CODE-MX", ""); err != nil {
		t.Errorf("MarkMerged: %v", err)
	}
}

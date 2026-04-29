package cli

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/randheer094/velocity/internal/config"
)

func TestUpdatePromptsExplicitTag(t *testing.T) {
	repoSlug := "owner/velocity-resources"
	tag := "v0.6.1"
	startReleaseServer(t, repoSlug, tag, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts: []\n",
		"VERSION":               tag,
	}, "")

	seedConfigWithResources(t, repoSlug, "v0.5.0")

	cmd := newUpdatePromptsCmd()
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{tag}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if err := config.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Resources.Version; got != tag {
		t.Errorf("version = %q, want %q", got, tag)
	}
	versionFile, err := os.ReadFile(filepath.Join(config.ResourcesDir(), "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(versionFile)) != tag {
		t.Errorf("VERSION file = %q, want %q", versionFile, tag)
	}
	if !strings.Contains(out.String(), "daemon not running") {
		t.Errorf("expected 'daemon not running' message, got:\n%s", out.String())
	}
}

func TestUpdatePromptsLatestTag(t *testing.T) {
	repoSlug := "owner/velocity-resources"
	tag := "v0.7.0"
	releasesJSON := `[
		{"tag_name":"v0.6.0","draft":false,"prerelease":false,"published_at":"2025-01-01T00:00:00Z"},
		{"tag_name":"v0.7.0","draft":false,"prerelease":false,"published_at":"2025-02-01T00:00:00Z"},
		{"tag_name":"v1.0.0","draft":false,"prerelease":false,"published_at":"2026-01-01T00:00:00Z"}
	]`
	startReleaseServer(t, repoSlug, tag, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts: []\n",
		"VERSION":               tag,
	}, releasesJSON)

	seedConfigWithResources(t, repoSlug, "v0.6.0")

	cmd := newUpdatePromptsCmd()
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if err := config.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := config.Get().Resources.Version; got != tag {
		t.Errorf("version = %q, want %q", got, tag)
	}
}

func TestUpdatePromptsRejectsMajorMismatch(t *testing.T) {
	seedConfigWithResources(t, "owner/repo", "v0.6.0")

	cmd := newUpdatePromptsCmd()
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.RunE(cmd, []string{"v9.0.0"})
	if err == nil || !strings.Contains(err.Error(), "major mismatch") {
		t.Fatalf("expected major mismatch, got %v", err)
	}
}

func TestUpdatePromptsNoRepoConfigured(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	t.Cleanup(func() { config.SetDir(t.TempDir()) })
	// Save a config without resources block.
	cfg := &config.Config{
		Jira: config.JiraConfig{
			BaseURL:         "https://x.atlassian.net",
			Email:           "a@b.c",
			ArchitectJiraID: "arch",
			DeveloperJiraID: "dev",
			RepoURLField:    "customfield_repo",
			TaskStatusMap: config.TaskStatusMap{
				New:            config.StatusBucket{Default: "To Do"},
				Planning:       config.StatusBucket{Default: "Planning"},
				PlanningFailed: config.StatusBucket{Default: "Planning Failed"},
				Coding:         config.StatusBucket{Default: "In Progress"},
				Done:           config.StatusBucket{Default: "Done", Aliases: []string{"Dismissed"}},
			},
			SubtaskStatusMap: config.SubtaskStatusMap{
				New:          config.StatusBucket{Default: "To Do"},
				Coding:       config.StatusBucket{Default: "Dev In Progress"},
				CodingFailed: config.StatusBucket{Default: "Dev Failed"},
				InReview:     config.StatusBucket{Default: "In Review"},
				Done:         config.StatusBucket{Default: "Done", Aliases: []string{"Dismissed"}},
			},
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	cmd := newUpdatePromptsCmd()
	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, []string{"v0.6.0"})
	if err == nil || !strings.Contains(err.Error(), "velocity setup") {
		t.Fatalf("expected setup hint, got %v", err)
	}
}

func TestUpdatePromptsSendsSIGHUP(t *testing.T) {
	repoSlug := "owner/velocity-resources"
	tag := "v0.6.0"
	startReleaseServer(t, repoSlug, tag, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts: []\n",
		"VERSION":               tag,
	}, "")

	seedConfigWithResources(t, repoSlug, "v0.5.0")

	// Install a SIGHUP handler so the kill below doesn't terminate the
	// test process. The handler also confirms the signal arrived.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP)
	defer signal.Stop(sigCh)

	if err := writePid(syscall.Getpid()); err != nil {
		t.Fatal(err)
	}

	cmd := newUpdatePromptsCmd()
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{tag}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "daemon SIGHUP sent") {
		t.Errorf("expected SIGHUP message, got:\n%s", out.String())
	}
	select {
	case <-sigCh:
		// Got it.
	case <-time.After(2 * time.Second):
		t.Fatal("SIGHUP not delivered to current process")
	}
}

func seedConfigWithResources(t *testing.T, repoSlug, version string) string {
	t.Helper()
	dir := t.TempDir()
	config.SetDir(dir)
	t.Cleanup(func() { config.SetDir(t.TempDir()) })
	cfg := &config.Config{
		Jira: config.JiraConfig{
			BaseURL:         "https://x.atlassian.net",
			Email:           "a@b.c",
			ArchitectJiraID: "arch",
			DeveloperJiraID: "dev",
			RepoURLField:    "customfield_repo",
			TaskStatusMap: config.TaskStatusMap{
				New:            config.StatusBucket{Default: "To Do"},
				Planning:       config.StatusBucket{Default: "Planning"},
				PlanningFailed: config.StatusBucket{Default: "Planning Failed"},
				Coding:         config.StatusBucket{Default: "In Progress"},
				Done:           config.StatusBucket{Default: "Done", Aliases: []string{"Dismissed"}},
			},
			SubtaskStatusMap: config.SubtaskStatusMap{
				New:          config.StatusBucket{Default: "To Do"},
				Coding:       config.StatusBucket{Default: "Dev In Progress"},
				CodingFailed: config.StatusBucket{Default: "Dev Failed"},
				InReview:     config.StatusBucket{Default: "In Review"},
				Done:         config.StatusBucket{Default: "Done", Aliases: []string{"Dismissed"}},
			},
		},
		Resources: config.ResourcesConfig{
			RepoSlug: repoSlug,
			Version:  version,
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return dir
}

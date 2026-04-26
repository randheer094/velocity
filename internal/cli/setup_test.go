package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/resources"
)

// startReleaseServer mimics github.com/<slug>/releases/download/<tag>/...
// and api.github.com/repos/<slug>/releases endpoints. Returns the
// server (so the caller can Close manually if needed; t.Cleanup also
// closes).
func startReleaseServer(t *testing.T, repoSlug, tag string, files map[string]string, releasesJSON string) {
	t.Helper()
	tarball := buildReleaseTarball(t, files)
	tarballName := "velocity-resources-" + tag + ".tar.gz"
	hash := sha256.Sum256(tarball)
	sums := hex.EncodeToString(hash[:]) + "  " + tarballName + "\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+repoSlug+"/releases":
			if releasesJSON == "" {
				http.Error(w, "no releases", http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(releasesJSON))
		case r.URL.Path == "/"+repoSlug+"/releases/download/"+tag+"/"+tarballName:
			_, _ = w.Write(tarball)
		case r.URL.Path == "/"+repoSlug+"/releases/download/"+tag+"/SHA256SUMS":
			_, _ = w.Write([]byte(sums))
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	prevAPI, prevDL := resources.APIBase, resources.DownloadBase
	resources.SetAPIBase(srv.URL)
	t.Cleanup(func() {
		resources.APIBase = prevAPI
		resources.DownloadBase = prevDL
	})
}

func buildReleaseTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for path, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     path,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// seedConfigForSetup writes a valid Jira config (no resources block)
// so velocity setup's "config loaded" gate passes.
func seedConfigForSetup(t *testing.T) string {
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
	}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNormalizeRepoSlug(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"randheer094/velocity-resources":                       {"randheer094/velocity-resources", true},
		"https://github.com/randheer094/velocity-resources":    {"randheer094/velocity-resources", true},
		"http://github.com/randheer094/velocity-resources":     {"randheer094/velocity-resources", true},
		"github.com/randheer094/velocity-resources":            {"randheer094/velocity-resources", true},
		"randheer094/velocity-resources.git":                   {"randheer094/velocity-resources", true},
		"randheer094/velocity-resources/":                      {"randheer094/velocity-resources", true},
		"":                                                     {"", false},
		"only-one-slash":                                       {"", false},
		"a/b/c":                                                {"", false},
		"   randheer094/velocity-resources   ":                 {"randheer094/velocity-resources", true},
	}
	for in, tc := range cases {
		got, err := normalizeRepoSlug(in)
		if tc.ok && err != nil {
			t.Errorf("normalizeRepoSlug(%q) err: %v", in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("normalizeRepoSlug(%q) should have errored", in)
		}
		if tc.ok && got != tc.want {
			t.Errorf("normalizeRepoSlug(%q) = %q, want %q", in, got, tc.want)
		}
	}
}

func TestSetupHappy(t *testing.T) {
	repoSlug := "owner/velocity-resources"
	tag := "v0.6.0"
	startReleaseServer(t, repoSlug, tag, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: []\n",
		"prompts/a.md":          "x",
	}, "")

	seedConfigForSetup(t)

	cmd := newSetupCmd()
	if err := cmd.Flags().Set("repo", repoSlug); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("version", tag); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	// Resources extracted on disk?
	if _, err := os.Stat(filepath.Join(config.ResourcesDir(), "VERSION")); err != nil {
		t.Errorf("VERSION not written: %v", err)
	}
	// Config persisted?
	if err := config.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	cfg := config.Get()
	if cfg.Resources.RepoSlug != repoSlug {
		t.Errorf("RepoSlug = %q, want %q", cfg.Resources.RepoSlug, repoSlug)
	}
	if cfg.Resources.Version != tag {
		t.Errorf("Version = %q, want %q", cfg.Resources.Version, tag)
	}
}

func TestSetupRejectsURLPrefixedRepoSlug(t *testing.T) {
	// flag normalization happens for us, so verify normalizeRepoSlug
	// alone via cmd run with URL-style flag.
	seedConfigForSetup(t)
	cmd := newSetupCmd()
	_ = cmd.Flags().Set("repo", "https://github.com/owner/repo")
	_ = cmd.Flags().Set("version", "v0.0.1")

	// This will fail at download (no server), but the slug will have
	// been normalized cleanly. Run it and confirm we don't get a
	// "invalid repo slug" error.
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, nil)
	if err != nil && strings.Contains(err.Error(), "invalid repo slug") {
		t.Errorf("URL-prefixed slug should have normalized: %v", err)
	}
}

func TestSetupRejectsMajorMismatch(t *testing.T) {
	seedConfigForSetup(t)
	cmd := newSetupCmd()
	_ = cmd.Flags().Set("repo", "owner/repo")
	_ = cmd.Flags().Set("version", "v9.0.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "major mismatch") {
		t.Fatalf("expected major mismatch, got %v", err)
	}
}

func TestSetupNoConfig(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	t.Cleanup(func() { config.SetDir(t.TempDir()) })
	cmd := newSetupCmd()
	_ = cmd.Flags().Set("repo", "owner/repo")
	_ = cmd.Flags().Set("version", "v0.0.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("expected config-required error, got %v", err)
	}
}

func TestSetupInvalidRepoSlug(t *testing.T) {
	seedConfigForSetup(t)
	cmd := newSetupCmd()
	_ = cmd.Flags().Set("repo", "no-slash")
	_ = cmd.Flags().Set("version", "v0.0.0")
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid repo slug") {
		t.Fatalf("expected invalid-slug error, got %v", err)
	}
}

func TestSetupBadConfigError(t *testing.T) {
	dir := t.TempDir()
	config.SetDir(dir)
	t.Cleanup(func() { config.SetDir(t.TempDir()) })
	if err := os.WriteFile(config.ConfigPath(), []byte("not yaml: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir) // reload picks up the load error
	cmd := newSetupCmd()
	_ = cmd.Flags().Set("repo", "owner/repo")
	_ = cmd.Flags().Set("version", "v0.0.0")
	cmd.SetContext(context.Background())
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("expected config-error, got %v", err)
	}
}

func TestSetupEmptyVersionAfterPrompt(t *testing.T) {
	seedConfigForSetup(t)
	cmd := newSetupCmd()
	_ = cmd.Flags().Set("repo", "owner/repo")
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("\n")) // empty version, no default → required error
	err := cmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "version is required") {
		t.Fatalf("expected version-required, got %v", err)
	}
}

func TestSetupInteractivePromptUsesDefaults(t *testing.T) {
	repoSlug := "owner/velocity-resources"
	tag := "v0.5.0"
	startReleaseServer(t, repoSlug, tag, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: []\n",
		"prompts/a.md":          "x",
	}, "")

	dir := seedConfigForSetup(t)
	// Pre-populate cfg.Resources so the prompt-defaults path activates.
	cfg := config.Get()
	cfg.Resources.RepoSlug = repoSlug
	cfg.Resources.Version = tag
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	_ = dir

	cmd := newSetupCmd()
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	// Two empty lines accept the persisted defaults.
	cmd.SetIn(strings.NewReader("\n\n"))
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
}

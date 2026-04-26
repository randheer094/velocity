package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goFiles is the canonical fake-resources layout for a Go project.
// Tests only check that these paths get materialised; their byte
// contents are arbitrary fixture data.
var goFiles = map[string]string{
	"go/.claude/CLAUDE.md":                      "# go index\n",
	"go/.claude/rules/build.md":                 "# build\n",
	"go/.claude/rules/errors.md":                "# errors\n",
	"go/.claude/skills/prepare-for-pr/SKILL.md": "---\nname: prepare-for-pr\n---\n",
}

var androidFiles = map[string]string{
	"android/.claude/CLAUDE.md":                      "# android index\n",
	"android/.claude/rules/architecture.md":          "# arch\n",
	"android/.claude/skills/prepare-for-pr/SKILL.md": "---\nname: prepare-for-pr\n---\n",
}

// startResourcesServer stands up an httptest server that returns a
// tarball mirroring the velocity-resources layout, and rewires
// resourcesURL to point at it for the duration of the test.
func startResourcesServer(t *testing.T, files map[string]string) {
	t.Helper()
	body := buildResourcesTarball(t, "velocity-resources-main", files)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	saved := resourcesURL
	resourcesURL = func(ref string) string { return srv.URL + "/" + ref }
	t.Cleanup(func() { resourcesURL = saved })
}

func buildResourcesTarball(t *testing.T, topDir string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for path, body := range files {
		hdr := &tar.Header{
			Name:     topDir + "/" + path,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
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

func TestDetectProjectType(t *testing.T) {
	cases := []struct {
		name string
		seed map[string]string
		want projectType
	}{
		{"go", map[string]string{"go.mod": "module x\n"}, projectGo},
		{"android groovy", map[string]string{"build.gradle": ""}, projectAndroid},
		{"android kts", map[string]string{"build.gradle.kts": ""}, projectAndroid},
		{"android settings", map[string]string{"settings.gradle": ""}, projectAndroid},
		{"android settings kts", map[string]string{"settings.gradle.kts": ""}, projectAndroid},
		{"unknown", map[string]string{"readme.txt": "x"}, projectUnknown},
		{"prefers go over android", map[string]string{"go.mod": "module x\n", "build.gradle": ""}, projectGo},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range tc.seed {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := detectProjectType(dir); got != tc.want {
				t.Errorf("detectProjectType = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFileAndDirExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(file) {
		t.Error("fileExists(file) = false")
	}
	if fileExists(dir) {
		t.Error("fileExists(dir) = true")
	}
	if !dirExists(dir) {
		t.Error("dirExists(dir) = false")
	}
	if dirExists(file) {
		t.Error("dirExists(file) = true")
	}
	missing := filepath.Join(dir, "nope")
	if fileExists(missing) {
		t.Error("fileExists(missing) = true")
	}
	if dirExists(missing) {
		t.Error("dirExists(missing) = true")
	}
}

func TestInspectReadinessAllMissing(t *testing.T) {
	dir := t.TempDir()
	r := inspectReadiness(dir)
	if r.ready() {
		t.Error("empty dir should not be ready")
	}
	if r.claudeMd.ok || r.claudeDir.ok || r.prepareSkill.ok {
		t.Errorf("expected all checks to fail, got %+v", r)
	}
}

// check only verifies presence; a directory at CLAUDE.md's path still
// fails because the expected artifact is a file.
func TestInspectReadinessCLAUDEIsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, claudeMdPath), 0o755); err != nil {
		t.Fatal(err)
	}
	r := inspectReadiness(dir)
	if r.claudeMd.ok {
		t.Error("CLAUDE.md-as-directory should not count as ok")
	}
}

// Presence-only semantics: empty CLAUDE.md / SKILL.md are OK. Content
// is the project's problem, not velocity's.
func TestInspectReadinessEmptyFilesAreOK(t *testing.T) {
	dir := t.TempDir()
	claudeMd := filepath.Join(dir, claudeMdPath)
	if err := os.MkdirAll(filepath.Dir(claudeMd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeMd, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, ".claude", "skills", "prepare-for-pr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r := inspectReadiness(dir)
	if !r.ready() {
		t.Errorf("empty-but-present files should pass presence check, got %+v", r)
	}
}

func TestInspectReadinessSkillIsDir(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".claude", "skills", "prepare-for-pr", "SKILL.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := inspectReadiness(dir)
	if r.prepareSkill.ok {
		t.Error("SKILL.md-as-dir should not count as ok")
	}
}

func TestInspectReadinessReady(t *testing.T) {
	dir := t.TempDir()
	claudeMd := filepath.Join(dir, claudeMdPath)
	if err := os.MkdirAll(filepath.Dir(claudeMd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeMd, []byte("# project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, ".claude", "skills", "prepare-for-pr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: prepare-for-pr\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := inspectReadiness(dir)
	if !r.ready() {
		t.Errorf("expected ready, got %+v", r)
	}
}

func TestReadinessReportWriteReady(t *testing.T) {
	r := readinessReport{
		root:         "/x",
		projectType:  projectGo,
		claudeMd:     checkResult{name: "a", ok: true},
		claudeDir:    checkResult{name: "b", ok: true},
		prepareSkill: checkResult{name: "c", ok: true},
	}
	var buf bytes.Buffer
	r.write(&buf)
	s := buf.String()
	if !strings.Contains(s, "READY") || strings.Contains(s, "NOT READY") {
		t.Errorf("want READY, got:\n%s", s)
	}
	if !strings.Contains(s, "[ OK ] a") {
		t.Errorf("missing OK marker:\n%s", s)
	}
}

func TestReadinessReportWriteNotReady(t *testing.T) {
	r := readinessReport{
		root:         "/x",
		projectType:  projectGo,
		claudeMd:     checkResult{name: "a", detail: "missing"},
		claudeDir:    checkResult{name: "b", ok: true},
		prepareSkill: checkResult{name: "c", ok: true},
	}
	var buf bytes.Buffer
	r.write(&buf)
	s := buf.String()
	if !strings.Contains(s, "NOT READY") {
		t.Errorf("want NOT READY, got:\n%s", s)
	}
	if !strings.Contains(s, "[FAIL] a") {
		t.Errorf("missing FAIL marker:\n%s", s)
	}
	if !strings.Contains(s, "missing") {
		t.Errorf("detail not rendered:\n%s", s)
	}
	if !strings.Contains(s, "velocity prepare /x") {
		t.Errorf("missing prepare hint:\n%s", s)
	}
}

func TestReadinessReportWriteUnknownType(t *testing.T) {
	r := readinessReport{
		root:         "/x",
		projectType:  projectUnknown,
		claudeMd:     checkResult{name: "a"},
		claudeDir:    checkResult{name: "b"},
		prepareSkill: checkResult{name: "c"},
	}
	var buf bytes.Buffer
	r.write(&buf)
	s := buf.String()
	if !strings.Contains(s, "unknown") {
		t.Errorf("want unknown project-type label:\n%s", s)
	}
	if !strings.Contains(s, "go.mod") || !strings.Contains(s, "build.gradle") {
		t.Errorf("unknown hint should mention marker files:\n%s", s)
	}
}

func TestNewCheckCmdReadyProject(t *testing.T) {
	dir := t.TempDir()
	seedReadyGoProject(t, dir)
	cmd := newCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	if !strings.Contains(out.String(), "READY") {
		t.Errorf("expected READY in output, got:\n%s", out.String())
	}
}

func TestNewCheckCmdNotReady(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newCheckCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.RunE(cmd, []string{dir})
	if err == nil {
		t.Fatal("expected error when project is not ready")
	}
	if !strings.Contains(out.String(), "NOT READY") {
		t.Errorf("expected NOT READY in output, got:\n%s", out.String())
	}
}

func TestNewCheckCmdMissingPath(t *testing.T) {
	cmd := newCheckCmd()
	err := cmd.RunE(cmd, []string{filepath.Join(t.TempDir(), "does-not-exist")})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestNewCheckCmdPathIsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newCheckCmd()
	err := cmd.RunE(cmd, []string{file})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected not-a-directory error, got %v", err)
	}
}

func TestNewPrepareCmdGoInstalls(t *testing.T) {
	startResourcesServer(t, goFiles)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	for path, want := range map[string]string{
		".claude/CLAUDE.md":                      goFiles["go/.claude/CLAUDE.md"],
		".claude/rules/build.md":                 goFiles["go/.claude/rules/build.md"],
		".claude/rules/errors.md":                goFiles["go/.claude/rules/errors.md"],
		".claude/skills/prepare-for-pr/SKILL.md": goFiles["go/.claude/skills/prepare-for-pr/SKILL.md"],
	} {
		got, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Errorf("missing %s: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s contents = %q, want %q", path, got, want)
		}
	}
	if !strings.Contains(out.String(), "Detected go project") {
		t.Errorf("expected 'Detected go project' in output, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Fetching templates from "+resourcesRepo) {
		t.Errorf("expected fetch line in output, got:\n%s", out.String())
	}
}

func TestNewPrepareCmdAndroidInstalls(t *testing.T) {
	startResourcesServer(t, androidFiles)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "build.gradle.kts"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	for _, rel := range []string{
		".claude/CLAUDE.md",
		".claude/rules/architecture.md",
		".claude/skills/prepare-for-pr/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestNewPrepareCmdUnknownProjectErrors(t *testing.T) {
	dir := t.TempDir()
	cmd := newPrepareCmd()
	err := cmd.RunE(cmd, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "unsupported project") {
		t.Fatalf("expected unsupported-project error, got %v", err)
	}
}

func TestNewPrepareCmdSkipsExistingWithoutForce(t *testing.T) {
	startResourcesServer(t, goFiles)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pre := []byte("# keep me\n")
	claudeMd := filepath.Join(dir, claudeMdPath)
	if err := os.MkdirAll(filepath.Dir(claudeMd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeMd, pre, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pre) {
		t.Errorf(".claude/CLAUDE.md was overwritten without --force:\n%s", got)
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Errorf("expected 'skipped' in output, got:\n%s", out.String())
	}
}

func TestNewPrepareCmdForceOverwrites(t *testing.T) {
	startResourcesServer(t, goFiles)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeMd := filepath.Join(dir, claudeMdPath)
	if err := os.MkdirAll(filepath.Dir(claudeMd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeMd, []byte("# old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "# old") {
		t.Errorf(".claude/CLAUDE.md was not overwritten by --force:\n%s", got)
	}
	if !strings.Contains(out.String(), "wrote") {
		t.Errorf("expected 'wrote' in output, got:\n%s", out.String())
	}
}

func TestNewPrepareCmdMissingPath(t *testing.T) {
	cmd := newPrepareCmd()
	err := cmd.RunE(cmd, []string{filepath.Join(t.TempDir(), "does-not-exist")})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

// MkdirAll inside installTemplates returns ENOTDIR when a regular
// file blocks the destination path, and that bubbles out as an error.
func TestNewPrepareCmdMkdirAllFails(t *testing.T) {
	startResourcesServer(t, goFiles)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{dir}); err == nil {
		t.Fatal("expected MkdirAll error")
	}
}

// Under --force, installTemplates skips the pre-stat and runs
// WriteFile directly — which fails when the target path is itself a
// directory.
func TestNewPrepareCmdWriteFileFails(t *testing.T) {
	startResourcesServer(t, goFiles)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(dir, ".claude", "skills", "prepare-for-pr", "SKILL.md")
	if err := os.MkdirAll(skillPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	if err := cmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{dir}); err == nil {
		t.Fatal("expected WriteFile error")
	}
}

func TestNewPrepareCmdFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	saved := resourcesURL
	resourcesURL = func(ref string) string { return srv.URL + "/" + ref }
	t.Cleanup(func() { resourcesURL = saved })

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.RunE(cmd, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected status 500 error, got %v", err)
	}
}

func TestNewPrepareCmdFetchEmptyTarball(t *testing.T) {
	startResourcesServer(t, map[string]string{"unrelated/file.md": "x\n"})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := cmd.RunE(cmd, []string{dir})
	if err == nil || !strings.Contains(err.Error(), "no resources") {
		t.Fatalf("expected no-resources error, got %v", err)
	}
}

func TestResourcesRefUsesEnvOverride(t *testing.T) {
	t.Setenv(resourcesRefEnv, "")
	if got := resourcesRef(); got != defaultResourcesRef {
		t.Errorf("default ref = %q, want %q", got, defaultResourcesRef)
	}
	t.Setenv(resourcesRefEnv, "feature/x")
	if got := resourcesRef(); got != "feature/x" {
		t.Errorf("override ref = %q, want feature/x", got)
	}
}

func TestResolveProjectPathEmpty(t *testing.T) {
	if _, err := resolveProjectPath(""); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestRootCmdIncludesReadinessSubcommands(t *testing.T) {
	root := NewRootCmd()
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"check", "prepare"} {
		if !names[want] {
			t.Errorf("missing subcommand: %s", want)
		}
	}
}

func seedReadyGoProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeMd := filepath.Join(dir, claudeMdPath)
	if err := os.MkdirAll(filepath.Dir(claudeMd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeMd, []byte("# project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, ".claude", "skills", "prepare-for-pr")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: prepare-for-pr\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectProjectType(t *testing.T) {
	cases := []struct {
		name  string
		seed  map[string]string
		want  projectType
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
	if err := os.Mkdir(filepath.Join(dir, claudeMdPath), 0o755); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, claudeMdPath), nil, 0o644); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, claudeMdPath), []byte("# project\n"), 0o644); err != nil {
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
		root:        "/x",
		projectType: projectGo,
		claudeMd:    checkResult{name: "a", ok: true},
		claudeDir:   checkResult{name: "b", ok: true},
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
		root:        "/x",
		projectType: projectGo,
		claudeMd:    checkResult{name: "a", detail: "missing"},
		claudeDir:   checkResult{name: "b", ok: true},
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
		root:        "/x",
		projectType: projectUnknown,
		claudeMd:    checkResult{name: "a"},
		claudeDir:   checkResult{name: "b"},
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
	// Go project marker but missing everything else.
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
	claudeMd := filepath.Join(dir, "CLAUDE.md")
	if b, err := os.ReadFile(claudeMd); err != nil || len(b) == 0 {
		t.Errorf("CLAUDE.md not written: err=%v size=%d", err, len(b))
	}
	skill := filepath.Join(dir, ".claude", "skills", "prepare-for-pr", "SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"gofmt",
		"go vet",
		"go build",
		"go test",
		"-race",
		"go mod tidy",
		"mandatory",
		"conventions.md",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Go SKILL.md missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(out.String(), "Detected go project") {
		t.Errorf("expected 'Detected go project' in output, got:\n%s", out.String())
	}

	conventions := filepath.Join(dir, ".claude", "rules", "conventions.md")
	cb, err := os.ReadFile(conventions)
	if err != nil {
		t.Fatalf("Go conventions.md not written: %v", err)
	}
	cs := string(cb)
	for _, want := range []string{
		"Errors",
		"errors.Is",
		"Concurrency",
		"context.Context",
		"log/slog",
		"Testing",
		"Table-driven",
		"Security",
		"ConstantTimeCompare",
		"Layout",
		"cmd/",
		"internal/",
	} {
		if !strings.Contains(cs, want) {
			t.Errorf("Go conventions.md missing %q:\n%s", want, cs)
		}
	}
}

func TestNewPrepareCmdAndroidInstalls(t *testing.T) {
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

	skill := filepath.Join(dir, ".claude", "skills", "prepare-for-pr", "SKILL.md")
	b, err := os.ReadFile(skill)
	if err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		"gradlew",
		"connectedAndroidTest",
		"android analyze",
		"android avd",
		"android sdk install",
		"android emulator",
		"android skills",
		"developer.android.com/tools/agents/android-cli",
		"adb wait-for-device",
		"detekt",
		"lint",
		"./gradlew check connectedCheck",
		"mandatory",
		"E2E",
		"conventions.md",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Android SKILL.md missing %q:\n%s", want, s)
		}
	}

	claudeMd := filepath.Join(dir, "CLAUDE.md")
	cb, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatalf("CLAUDE.md not written: %v", err)
	}
	cs := string(cb)
	for _, want := range []string{
		"gradlew",
		"connectedAndroidTest",
		"conventions.md",
	} {
		if !strings.Contains(cs, want) {
			t.Errorf("Android CLAUDE.md missing %q:\n%s", want, cs)
		}
	}

	conventions := filepath.Join(dir, ".claude", "rules", "conventions.md")
	vb, err := os.ReadFile(conventions)
	if err != nil {
		t.Fatalf("Android conventions.md not written: %v", err)
	}
	vs := string(vb)
	for _, want := range []string{
		"MVI",
		"Intent",
		"Effect",
		"reduce",
		"Hilt",
		"@HiltAndroidApp",
		"@HiltViewModel",
		"@HiltAndroidTest",
		"src/androidTest/",
		"Testing",
		"mandatory",
	} {
		if !strings.Contains(vs, want) {
			t.Errorf("Android conventions.md missing %q:\n%s", want, vs)
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
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pre := []byte("# keep me\n")
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), pre, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newPrepareCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, []string{dir}); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pre) {
		t.Errorf("CLAUDE.md was overwritten without --force:\n%s", got)
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Errorf("expected 'skipped' in output, got:\n%s", out.String())
	}
}

func TestNewPrepareCmdForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# old\n"), 0o644); err != nil {
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
	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "# old") {
		t.Errorf("CLAUDE.md was not overwritten by --force:\n%s", got)
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

// A regular file where we need a directory makes MkdirAll inside
// installTemplates return ENOTDIR, which bubbles out as an error.
func TestNewPrepareCmdMkdirAllFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// `.claude` as a regular file — MkdirAll(<root>/.claude/skills/…) fails.
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

// WriteFile against a path that is itself a directory fails with
// "is a directory"; --force skips the pre-stat and hits WriteFile.
func TestNewPrepareCmdWriteFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Make SKILL.md a directory so os.WriteFile fails under --force.
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
	if err := os.WriteFile(filepath.Join(dir, claudeMdPath), []byte("# project\n"), 0o644); err != nil {
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

package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/randheer094/velocity/internal/version"
)

// velocityMajor returns version.Major behind a function so the test
// helper isn't a compile-time-substituted constant — guards against
// the test being a tautology if the linker ever optimised it out.
func velocityMajor() int { return version.Major }

const goodManifest = `version: 0
prompts:
  - id: arch_plan
    path: arch/plan.md
    placeholders: [PlanBegin, PlanEnd, ParentKey, Requirement]
  - id: failure_jira
    path: failure/jira.md
    placeholders: [Role, Stage, Message]
`

func writeFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadAndRender(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"VERSION":                 "v0.6.0\n",
		"prompts/manifest.yaml":   goodManifest,
		"prompts/arch/plan.md":    "{{.PlanBegin}}|{{.ParentKey}}|{{.Requirement}}|{{.PlanEnd}}",
		"prompts/failure/jira.md": "{{.Role}}: {{.Stage}}: {{.Message}}",
	})
	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := Shared().Count(); got != 2 {
		t.Errorf("count = %d, want 2", got)
	}
	if got := Shared().Tag(); got != "v0.6.0" {
		t.Errorf("tag = %q, want v0.6.0", got)
	}
	out, err := Render("arch_plan", struct {
		PlanBegin, PlanEnd, ParentKey, Requirement string
	}{"<<B>>", "<<E>>", "PROJ-1", "do thing"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if out != "<<B>>|PROJ-1|do thing|<<E>>" {
		t.Errorf("rendered = %q", out)
	}
}

func TestRenderUnknownID(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: []\n",
		"prompts/a.md":          "x",
	})
	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Render("nope", nil); err == nil || !strings.Contains(err.Error(), "unknown template") {
		t.Fatalf("expected unknown template id error, got %v", err)
	}
}

func TestRenderMissingKey(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: [Foo]\n",
		"prompts/a.md":          "{{.Foo}}",
	})
	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Render("a", struct{ Bar string }{"x"}); err == nil {
		t.Fatal("expected missingkey error")
	}
}

func TestLoadMajorMismatch(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 1\nprompts:\n  - id: a\n    path: a.md\n    placeholders: []\n",
		"prompts/a.md":          "x",
	})
	if err := Load(dir); err == nil || !strings.Contains(err.Error(), "major mismatch") {
		t.Fatalf("expected major mismatch error, got %v", err)
	}
}

func TestLoadMissingManifest(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	if err := Load(dir); err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

func TestLoadMissingTemplate(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: missing.md\n    placeholders: []\n",
	})
	if err := Load(dir); err == nil {
		t.Fatal("expected error for missing template file")
	}
}

func TestLoadEmptyManifest(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts: []\n",
	})
	if err := Load(dir); err == nil || !strings.Contains(err.Error(), "no prompts") {
		t.Fatalf("expected no-prompts error, got %v", err)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "::not yaml::",
	})
	if err := Load(dir); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestReloadKeepsPreviousOnFailure(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"VERSION":               "v0.1.0",
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: []\n",
		"prompts/a.md":          "ok",
	})
	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	bad := t.TempDir()
	if err := Reload(bad); err == nil {
		t.Fatal("expected reload to fail with empty dir")
	}
	if Shared() == nil || Shared().Count() != 1 {
		t.Errorf("previous store dropped after failed reload")
	}
}

func TestReloadSwaps(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"VERSION":               "v0.1.0",
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: []\n",
		"prompts/a.md":          "first",
	})
	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, _ := Render("a", nil)
	if got != "first" {
		t.Fatalf("first render = %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "a.md"), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Reload(dir); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	got, _ = Render("a", nil)
	if got != "second" {
		t.Errorf("after reload = %q, want second", got)
	}
}

func TestRenderWithoutLoad(t *testing.T) {
	resetForTest()
	if _, err := Render("a", nil); err == nil {
		t.Fatal("expected error before Load")
	}
}

func TestStoreRenderNil(t *testing.T) {
	var s *Store
	if _, err := s.Render("a", nil); err == nil {
		t.Fatal("expected nil-store error")
	}
}

func TestMajorVersionTracksVelocityMajor(t *testing.T) {
	// The manifest schema major is pinned to the velocity binary's
	// major version. If they ever drift, setup/update-prompts will
	// reject every release of velocity-resources for nonsensical
	// reasons. Catch the drift at unit-test time.
	if MajorVersion != velocityMajor() {
		t.Errorf("prompts.MajorVersion (%d) != version.Major (%d)", MajorVersion, velocityMajor())
	}
}

func TestSetForTestAndReset(t *testing.T) {
	defer resetForTest()
	SetForTest(t, map[string]string{
		"a": "hello {{.Name}}",
	})
	got, err := Render("a", struct{ Name string }{"world"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello world" {
		t.Errorf("got %q", got)
	}
	if Shared().Tag() != "test" {
		t.Errorf("tag = %q", Shared().Tag())
	}

	ResetForTest(t)
	if Shared() != nil {
		t.Errorf("expected nil after reset")
	}
}

func TestSetForTestRejectsBadTemplate(t *testing.T) {
	defer resetForTest()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from Fatalf")
		}
	}()
	SetForTest(panicT{}, map[string]string{
		"bad": "{{.Unclosed",
	})
}

// panicT mirrors enough of testing.T for SetForTest to fail loudly.
type panicT struct{}

func (panicT) Helper() {}
func (panicT) Fatalf(format string, args ...any) {
	panic("fatal")
}
func (panicT) Cleanup(func()) {}

func TestLoadEmptyDir(t *testing.T) {
	defer resetForTest()
	if err := Load(""); err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestLoadManifestEntryMissingFields(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: \"\"\n    path: x.md\n    placeholders: []\n",
		"prompts/x.md":          "x",
	})
	if err := Load(dir); err == nil {
		t.Fatal("expected error for empty id")
	}

	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: \"\"\n    placeholders: []\n",
	})
	if err := Load(dir); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestLoadBadTemplateSyntax(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: []\n",
		"prompts/a.md":          "{{.Unclosed",
	})
	if err := Load(dir); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadRejectsPlaceholderDrift(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: [Declared]\n",
		"prompts/a.md":          "{{.Used}}",
	})
	err := Load(dir)
	if err == nil {
		t.Fatal("expected drift error")
	}
	if !strings.Contains(err.Error(), "drift") {
		t.Errorf("expected drift error, got %v", err)
	}
}

func TestLoadRejectsMissingPlaceholderEntry(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: []\n",
		"prompts/a.md":          "{{.Used}}",
	})
	err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "missing entries") {
		t.Fatalf("expected missing-entries error, got %v", err)
	}
}

func TestLoadRejectsUnusedDeclaredPlaceholder(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: [Unused]\n",
		"prompts/a.md":          "static",
	})
	err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "unused") {
		t.Fatalf("expected unused-placeholders error, got %v", err)
	}
}

func TestLoadAcceptsControlStructures(t *testing.T) {
	defer resetForTest()
	dir := t.TempDir()
	writeFixture(t, dir, map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts:\n  - id: a\n    path: a.md\n    placeholders: [Items, Show]\n",
		"prompts/a.md":          "{{if .Show}}{{range .Items}}- {{.}}\n{{end}}{{end}}",
	})
	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

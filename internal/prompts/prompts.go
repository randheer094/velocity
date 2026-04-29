// Package prompts loads versioned prompt + Jira/PR comment templates
// from a local resources cache and renders them with text/template.
package prompts

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"text/template/parse"

	"gopkg.in/yaml.v3"
)

// Manifest is the on-disk schema for prompts/manifest.yaml inside the
// resources tarball. Version is the manifest major; this binary only
// accepts manifests whose Version equals MajorVersion.
type Manifest struct {
	Version int           `yaml:"version"`
	Prompts []PromptEntry `yaml:"prompts"`
}

// PromptEntry maps a logical id to a relative template path plus a
// declared placeholder list. Render data structs must expose fields
// whose names exactly match these placeholders.
type PromptEntry struct {
	ID           string   `yaml:"id"`
	Path         string   `yaml:"path"`
	Placeholders []string `yaml:"placeholders"`
}

// Store is an immutable snapshot of all parsed templates. Reload
// builds a new Store and swaps it in atomically.
type Store struct {
	tag       string
	templates map[string]*template.Template
}

var (
	mu     sync.RWMutex
	shared *Store
)

// Shared returns the package-level Store. Callers must not mutate it.
// Returns nil if Load has never succeeded.
func Shared() *Store {
	mu.RLock()
	defer mu.RUnlock()
	return shared
}

// Load reads manifest.yaml + every referenced template file from
// resourcesDir/prompts and installs the result as the shared store.
// resourcesDir/VERSION is read for tag reporting if present; a
// missing VERSION file is silently tolerated (the store reports an
// empty Tag()).
func Load(resourcesDir string) error {
	store, err := load(resourcesDir)
	if err != nil {
		return err
	}
	mu.Lock()
	shared = store
	mu.Unlock()
	slog.Info("prompts: loaded", "count", len(store.templates), "tag", store.tag)
	return nil
}

// Reload re-reads the resources cache and atomically swaps the shared
// store. On failure it logs and keeps the previous templates in place
// so the daemon never ends up with no prompts.
func Reload(resourcesDir string) error {
	store, err := load(resourcesDir)
	if err != nil {
		slog.Error("prompts: reload failed; keeping previous templates", "err", err)
		return err
	}
	mu.Lock()
	shared = store
	mu.Unlock()
	slog.Info("prompts: reloaded", "count", len(store.templates), "tag", store.tag)
	return nil
}

func load(resourcesDir string) (*Store, error) {
	if resourcesDir == "" {
		return nil, errors.New("resources dir is empty")
	}
	manifestPath := filepath.Join(resourcesDir, "prompts", "manifest.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version != MajorVersion {
		return nil, fmt.Errorf("major mismatch: binary expects %d, manifest declares %d", MajorVersion, m.Version)
	}
	if len(m.Prompts) == 0 {
		return nil, errors.New("manifest declares no prompts")
	}

	templates := make(map[string]*template.Template, len(m.Prompts))
	for _, p := range m.Prompts {
		if p.ID == "" {
			return nil, errors.New("manifest entry missing id")
		}
		if p.Path == "" {
			return nil, fmt.Errorf("manifest entry %s missing path", p.ID)
		}
		body, err := os.ReadFile(filepath.Join(resourcesDir, "prompts", filepath.FromSlash(p.Path)))
		if err != nil {
			return nil, fmt.Errorf("read template %s: %w", p.ID, err)
		}
		tpl, err := template.New(p.ID).Option("missingkey=error").Parse(string(body))
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", p.ID, err)
		}
		if err := checkPlaceholders(p, tpl); err != nil {
			return nil, err
		}
		templates[p.ID] = tpl
	}

	return &Store{
		tag:       strings.TrimSpace(readVersionFile(resourcesDir)),
		templates: templates,
	}, nil
}

// checkPlaceholders verifies the manifest's declared placeholder list
// matches the field references in the template body exactly. Mismatch
// is a manifest authoring bug — the data structs in arch/code declare
// fields keyed off these names, so a drift means rendering will fail
// at runtime with missingkey=error.
func checkPlaceholders(p PromptEntry, tpl *template.Template) error {
	declared := map[string]bool{}
	for _, name := range p.Placeholders {
		declared[name] = true
	}
	used := map[string]bool{}
	for _, t := range tpl.Templates() {
		if t.Tree == nil || t.Tree.Root == nil {
			continue
		}
		collectFieldNames(t.Tree.Root, used)
	}

	var missing, extra []string
	for name := range used {
		if !declared[name] {
			missing = append(missing, name)
		}
	}
	for name := range declared {
		if !used[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	switch {
	case len(missing) > 0 && len(extra) > 0:
		return fmt.Errorf("manifest %s placeholders drift: template uses %v not declared, declared %v not used by template",
			p.ID, missing, extra)
	case len(missing) > 0:
		return fmt.Errorf("manifest %s placeholders missing entries for: %v", p.ID, missing)
	case len(extra) > 0:
		return fmt.Errorf("manifest %s declares unused placeholders: %v", p.ID, extra)
	}
	return nil
}

func collectFieldNames(node parse.Node, out map[string]bool) {
	if node == nil {
		return
	}
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			collectFieldNames(c, out)
		}
	case *parse.ActionNode:
		collectFieldNames(n.Pipe, out)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, cmd := range n.Cmds {
			collectFieldNames(cmd, out)
		}
	case *parse.CommandNode:
		for _, arg := range n.Args {
			collectFieldNames(arg, out)
		}
	case *parse.FieldNode:
		// Only top-level field references (.Foo, not .Foo.Bar.Baz)
		// participate in the manifest contract — sub-fields are
		// internal to a placeholder's struct.
		if len(n.Ident) > 0 {
			out[n.Ident[0]] = true
		}
	case *parse.IfNode:
		collectFieldNames(n.Pipe, out)
		collectFieldNames(n.List, out)
		collectFieldNames(n.ElseList, out)
	case *parse.RangeNode:
		collectFieldNames(n.Pipe, out)
		collectFieldNames(n.List, out)
		collectFieldNames(n.ElseList, out)
	case *parse.WithNode:
		collectFieldNames(n.Pipe, out)
		collectFieldNames(n.List, out)
		collectFieldNames(n.ElseList, out)
	case *parse.TemplateNode:
		collectFieldNames(n.Pipe, out)
	}
}

func readVersionFile(resourcesDir string) string {
	data, err := os.ReadFile(filepath.Join(resourcesDir, "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Tag returns the release tag this store was loaded from, or "" if
// the VERSION file was missing.
func (s *Store) Tag() string { return s.tag }

// Count returns the number of templates installed.
func (s *Store) Count() int { return len(s.templates) }

// Render expands the named template with data. Returns an error if the
// id is unknown or the template references a key absent from data
// (text/template missingkey=error).
func (s *Store) Render(id string, data any) (string, error) {
	if s == nil {
		return "", errors.New("prompts: no store loaded")
	}
	tpl, ok := s.templates[id]
	if !ok {
		return "", fmt.Errorf("prompts: unknown template id %q", id)
	}
	var buf strings.Builder
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render %s: %w", id, err)
	}
	return buf.String(), nil
}

// Render is a package-level helper that calls Shared().Render.
func Render(id string, data any) (string, error) {
	s := Shared()
	if s == nil {
		return "", errors.New("prompts: not loaded; run `velocity setup`")
	}
	return s.Render(id, data)
}

// resetForTest clears the shared store. Tests only.
func resetForTest() {
	mu.Lock()
	shared = nil
	mu.Unlock()
}

// SetForTest installs a synthetic Store built from inline templates.
// External tests use this to avoid baking on-disk fixtures into every
// package that calls Render.
func SetForTest(t TestingT, templates map[string]string) {
	t.Helper()
	store := &Store{
		tag:       "test",
		templates: make(map[string]*template.Template, len(templates)),
	}
	for id, body := range templates {
		tpl, err := template.New(id).Option("missingkey=error").Parse(body)
		if err != nil {
			t.Fatalf("parse fixture %s: %v", id, err)
		}
		store.templates[id] = tpl
	}
	mu.Lock()
	prev := shared
	shared = store
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		shared = prev
		mu.Unlock()
	})
}

// ResetForTest clears the shared store. External tests use this to
// exercise the no-prompts-loaded fallback path.
func ResetForTest(t TestingT) {
	t.Helper()
	mu.Lock()
	prev := shared
	shared = nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		shared = prev
		mu.Unlock()
	})
}

// TestingT is the subset of *testing.T that prompts test helpers use.
type TestingT interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

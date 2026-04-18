package webhook

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/randheer094/velocity/internal/config"
	"github.com/randheer094/velocity/internal/jira"
)

func TestExtractRepoURLVariants(t *testing.T) {
	if got := extractRepoURL(map[string]any{"f": "u"}, ""); got != "" {
		t.Errorf("empty field name: %q", got)
	}
	if got := extractRepoURL(map[string]any{"f": "u"}, "f"); got != "u" {
		t.Errorf("string: %q", got)
	}
	if got := extractRepoURL(map[string]any{"f": map[string]any{"value": "u2"}}, "f"); got != "u2" {
		t.Errorf("object: %q", got)
	}
	if got := extractRepoURL(map[string]any{"f": 5}, "f"); got != "" {
		t.Errorf("unknown type: %q", got)
	}
}

func TestLookupParentRepoNoClient(t *testing.T) {
	if got := lookupParentRepo("PROJ-1", "f"); got != "" {
		t.Errorf("got = %q", got)
	}
}

func TestLookupParentRepoFull(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/issue/PROJ-1") {
			w.WriteHeader(404)
			return
		}
		_, _ = fmt.Fprint(w, `{"key":"PROJ-1","fields":{"customfield_repo":"https://github.com/o/r.git"}}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := strings.ReplaceAll(goodConfig, "https://example.atlassian.net", srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetDir(dir)
	jira.Reinit()
	t.Cleanup(func() {
		config.SetDir(t.TempDir())
		jira.Reinit()
	})

	got := lookupParentRepo("PROJ-1", "customfield_repo")
	if got != "https://github.com/o/r.git" {
		t.Errorf("got = %q", got)
	}

	// Server returns 404 for missing key → GetIssue returns nil → "" returned.
	if got := lookupParentRepo("PROJ-XX", "customfield_repo"); got != "" {
		t.Errorf("missing key: %q", got)
	}
}

func TestWriteJSONHelper(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusAccepted, map[string]string{"k": "v"})
	if rec.Code != http.StatusAccepted {
		t.Errorf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"k":"v"`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

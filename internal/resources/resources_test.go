package resources

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTarGz(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for path, body := range files {
		_ = tw.WriteHeader(&tar.Header{
			Name:     path,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// startReleaseServer stands up an httptest server that serves both the
// /repos/<slug>/releases listing and asset downloads under
// /<slug>/releases/download/<tag>/<asset>.
func startReleaseServer(t *testing.T, repoSlug, tag string, files map[string]string, releasesJSON string) *httptest.Server {
	t.Helper()
	tarball := buildTarGz(files)
	tarballName := "velocity-resources-" + tag + ".tar.gz"
	sumsBody := sha256Hex(tarball) + "  " + tarballName + "\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+repoSlug+"/releases":
			if releasesJSON == "" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(releasesJSON))
		case r.URL.Path == "/"+repoSlug+"/releases/download/"+tag+"/"+tarballName:
			_, _ = w.Write(tarball)
		case r.URL.Path == "/"+repoSlug+"/releases/download/"+tag+"/SHA256SUMS":
			_, _ = w.Write([]byte(sumsBody))
		default:
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	prevAPI, prevDL := APIBase, DownloadBase
	SetAPIBase(srv.URL)
	t.Cleanup(func() { APIBase, DownloadBase = prevAPI, prevDL })
	return srv
}

func TestMajorOf(t *testing.T) {
	cases := map[string]struct {
		want int
		ok   bool
	}{
		"v0.6.0":  {0, true},
		"0.6.0":   {0, true},
		"v1.0.0":  {1, true},
		"v10.2.3": {10, true},
		"":        {0, false},
		"abc":     {0, false},
		"v":       {0, false},
	}
	for in, tc := range cases {
		got, err := MajorOf(in)
		if tc.ok && err != nil {
			t.Errorf("MajorOf(%q) error: %v", in, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("MajorOf(%q) should have errored", in)
		}
		if tc.ok && got != tc.want {
			t.Errorf("MajorOf(%q) = %d, want %d", in, got, tc.want)
		}
	}
}

func TestCheckMajor(t *testing.T) {
	if err := CheckMajor("v0.6.0", 0); err != nil {
		t.Errorf("v0.6.0 vs 0: %v", err)
	}
	if err := CheckMajor("v1.0.0", 0); err == nil || !strings.Contains(err.Error(), "major mismatch") {
		t.Errorf("v1.0.0 vs 0: %v", err)
	}
}

func TestParseSHASums(t *testing.T) {
	body := []byte("# comment\n" +
		"abc123  velocity-resources-v0.6.0.tar.gz\n" +
		"deadbeef *velocity-resources-v0.6.0.zip\n" +
		"\n")
	m, err := parseSHASums(body)
	if err != nil {
		t.Fatal(err)
	}
	if m["velocity-resources-v0.6.0.tar.gz"] != "abc123" {
		t.Errorf("tar.gz hash = %q", m["velocity-resources-v0.6.0.tar.gz"])
	}
	if m["velocity-resources-v0.6.0.zip"] != "deadbeef" {
		t.Errorf("zip hash = %q", m["velocity-resources-v0.6.0.zip"])
	}
	if _, err := parseSHASums([]byte("\n#x\n")); err == nil {
		t.Error("empty body should error")
	}
}

func TestInstallHappy(t *testing.T) {
	repo := "owner/repo"
	tag := "v0.6.0"
	files := map[string]string{
		"prompts/manifest.yaml": "version: 0\nprompts: []\n",
		"prompts/arch/plan.md":  "{{.X}}",
		"go/.claude/CLAUDE.md":  "go!",
	}
	startReleaseServer(t, repo, tag, files, "")

	dst := filepath.Join(t.TempDir(), "resources")
	if err := Install(context.Background(), Release{RepoSlug: repo, Tag: tag}, dst, 0); err != nil {
		t.Fatalf("Install: %v", err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("missing %s: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	versionData, err := os.ReadFile(filepath.Join(dst, "VERSION"))
	if err != nil || strings.TrimSpace(string(versionData)) != tag {
		t.Errorf("VERSION = %q, err=%v", versionData, err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SHA256SUMS")); err != nil {
		t.Errorf("SHA256SUMS not written: %v", err)
	}
}

func TestInstallSHAMismatch(t *testing.T) {
	repo := "owner/repo"
	tag := "v0.6.0"
	files := map[string]string{"prompts/manifest.yaml": "version: 0\n"}

	tarball := buildTarGz(files)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			_, _ = fmt.Fprintf(w, "deadbeefdeadbeef  velocity-resources-%s.tar.gz\n", tag)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			_, _ = w.Write(tarball)
		default:
			http.Error(w, "x", 404)
		}
	}))
	t.Cleanup(srv.Close)
	prev := DownloadBase
	DownloadBase = srv.URL
	t.Cleanup(func() { DownloadBase = prev })

	dst := filepath.Join(t.TempDir(), "resources")
	err := Install(context.Background(), Release{RepoSlug: repo, Tag: tag}, dst, 0)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch, got %v", err)
	}
}

func TestInstallMajorMismatch(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "resources")
	err := Install(context.Background(), Release{RepoSlug: "x/y", Tag: "v1.0.0"}, dst, 0)
	if err == nil || !strings.Contains(err.Error(), "major mismatch") {
		t.Fatalf("expected major mismatch, got %v", err)
	}
}

func TestInstallPreservesExistingOnFailure(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "resources")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "sentinel"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Major mismatch fails before any swap.
	_ = Install(context.Background(), Release{RepoSlug: "x/y", Tag: "v9.0.0"}, dst, 0)
	if _, err := os.Stat(filepath.Join(dst, "sentinel")); err != nil {
		t.Errorf("sentinel removed despite failure: %v", err)
	}
}

// TestInstallPreservesExistingWhenDownloadFails locks down the more
// interesting case: an existing resources cache must survive a failed
// install that gets past the major-version check.
func TestInstallPreservesExistingWhenDownloadFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	prev := DownloadBase
	DownloadBase = srv.URL
	t.Cleanup(func() { DownloadBase = prev })

	dst := filepath.Join(t.TempDir(), "resources")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "sentinel"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Install(context.Background(), Release{RepoSlug: "x/y", Tag: "v0.0.0"}, dst, 0)
	if err == nil {
		t.Fatal("expected download to fail")
	}
	if _, err := os.Stat(filepath.Join(dst, "sentinel")); err != nil {
		t.Errorf("sentinel removed despite download failure: %v", err)
	}
}

// TestInstallPreservesExistingWhenExtractFails covers the case where
// download + sha verify succeed but extract fails (corrupt tarball or
// unsafe path inside).
func TestInstallRejectsTarballWithoutManifest(t *testing.T) {
	// A tarball that extracts cleanly but doesn't carry
	// prompts/manifest.yaml must not replace an existing cache.
	repo := "owner/repo"
	tag := "v0.6.0"
	files := map[string]string{
		"go/.claude/CLAUDE.md": "go!", // no prompts/manifest.yaml
	}
	startReleaseServer(t, repo, tag, files, "")

	dst := filepath.Join(t.TempDir(), "resources")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "sentinel"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Install(context.Background(), Release{RepoSlug: repo, Tag: tag}, dst, 0)
	if err == nil || !strings.Contains(err.Error(), "missing prompts/manifest.yaml") {
		t.Fatalf("expected manifest-missing error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "sentinel")); err != nil {
		t.Errorf("sentinel removed despite sanity-check failure: %v", err)
	}
}

func TestInstallPreservesExistingWhenExtractFails(t *testing.T) {
	repo := "owner/repo"
	tag := "v0.0.0"
	tarball := buildTarGz(map[string]string{
		"../escape": "bad",
	})
	hash := sha256.Sum256(tarball)
	sums := hex.EncodeToString(hash[:]) + "  velocity-resources-" + tag + ".tar.gz\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			_, _ = w.Write([]byte(sums))
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			_, _ = w.Write(tarball)
		default:
			http.Error(w, "x", 404)
		}
	}))
	t.Cleanup(srv.Close)
	prev := DownloadBase
	DownloadBase = srv.URL
	t.Cleanup(func() { DownloadBase = prev })

	dst := filepath.Join(t.TempDir(), "resources")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "sentinel"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Install(context.Background(), Release{RepoSlug: repo, Tag: tag}, dst, 0)
	if err == nil {
		t.Fatal("expected extract to fail")
	}
	if _, err := os.Stat(filepath.Join(dst, "sentinel")); err != nil {
		t.Errorf("sentinel removed despite extract failure: %v", err)
	}
}

func TestExtractTarGzPathTraversal(t *testing.T) {
	// archive contains "../escape" — must be rejected.
	tarball := buildTarGz(map[string]string{
		"../escape": "bad",
	})
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "evil.tar.gz")
	if err := os.WriteFile(tarPath, tarball, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out")
	err := extractTarGz(tarPath, dst)
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("expected unsafe-path error, got %v", err)
	}
}

func TestExtractTarGzAbsolutePath(t *testing.T) {
	tarball := buildTarGz(map[string]string{
		"/etc/evil": "bad",
	})
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "evil.tar.gz")
	if err := os.WriteFile(tarPath, tarball, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out")
	err := extractTarGz(tarPath, dst)
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("expected unsafe-path error, got %v", err)
	}
}

func TestExtractTarGzFlatLayoutNoStrip(t *testing.T) {
	// extractTarGz takes entries at face value — sibling top-levels
	// land where they are declared, regardless of allowlist.
	tarball := buildTarGz(map[string]string{
		"prompts/manifest.yaml":     "m",
		"go/.claude/CLAUDE.md":      "g",
		"android/.claude/CLAUDE.md": "a",
		"kotlin/.claude/CLAUDE.md":  "k",
	})
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "flat.tar.gz")
	if err := os.WriteFile(tarPath, tarball, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out")
	if err := extractTarGz(tarPath, dst); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"prompts/manifest.yaml",
		"go/.claude/CLAUDE.md",
		"android/.claude/CLAUDE.md",
		"kotlin/.claude/CLAUDE.md",
	} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
}

func TestSafeEntryPath(t *testing.T) {
	cases := []struct {
		name, wantRel string
		wantOK        bool
	}{
		{"prompts/m.yaml", "prompts/m.yaml", true},
		{"./prompts/m.yaml", "prompts/m.yaml", true},
		{"", "", true},           // empty
		{"/etc/evil", "", false}, // absolute
		{"../escape", "", false}, // traversal
	}
	for _, c := range cases {
		got, ok := safeEntryPath(c.name)
		if ok != c.wantOK || got != c.wantRel {
			t.Errorf("safeEntryPath(%q) = (%q, %v), want (%q, %v)",
				c.name, got, ok, c.wantRel, c.wantOK)
		}
	}
}

func TestLatestForMajor(t *testing.T) {
	repo := "owner/repo"
	releasesJSON := `[
		{"tag_name":"v0.5.0","draft":false,"prerelease":false,"published_at":"2025-01-01T00:00:00Z"},
		{"tag_name":"v0.6.0","draft":false,"prerelease":false,"published_at":"2025-02-01T00:00:00Z"},
		{"tag_name":"v1.0.0","draft":false,"prerelease":false,"published_at":"2026-01-01T00:00:00Z"},
		{"tag_name":"v0.7.0-rc1","draft":false,"prerelease":true,"published_at":"2025-03-01T00:00:00Z"}
	]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(releasesJSON))
	}))
	t.Cleanup(srv.Close)
	prev := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = prev })

	got, err := LatestForMajor(context.Background(), repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.6.0" {
		t.Errorf("got %q, want v0.6.0", got)
	}
	got, err = LatestForMajor(context.Background(), repo, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != "v1.0.0" {
		t.Errorf("major 1: got %q", got)
	}
	if _, err := LatestForMajor(context.Background(), repo, 9); err == nil {
		t.Error("expected no-releases error for major=9")
	}
}

func TestReleaseURLs(t *testing.T) {
	r := Release{RepoSlug: "a/b", Tag: "v0.6.0"}
	if got := r.AssetName(); got != "velocity-resources-v0.6.0.tar.gz" {
		t.Errorf("AssetName = %q", got)
	}
	if SHASumName != "SHA256SUMS" {
		t.Errorf("SHASumName const = %q", SHASumName)
	}
	prev := DownloadBase
	DownloadBase = "https://example"
	t.Cleanup(func() { DownloadBase = prev })
	got := r.DownloadURL("foo.tar.gz")
	want := "https://example/a/b/releases/download/v0.6.0/foo.tar.gz"
	if got != want {
		t.Errorf("DownloadURL = %q, want %q", got, want)
	}
}

func TestLatestForMajorEmptyRepo(t *testing.T) {
	if _, err := LatestForMajor(context.Background(), "", 0); err == nil {
		t.Error("expected error for empty repo")
	}
}

func TestLatestForMajorBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	prev := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = prev })
	if _, err := LatestForMajor(context.Background(), "x/y", 0); err == nil {
		t.Error("expected error on 500")
	}
}

func TestInstallDownloadFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "x", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	prev := DownloadBase
	DownloadBase = srv.URL
	t.Cleanup(func() { DownloadBase = prev })

	dst := filepath.Join(t.TempDir(), "resources")
	err := Install(context.Background(), Release{RepoSlug: "a/b", Tag: "v0.0.0"}, dst, 0)
	if err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}

func TestInstallMissingShaEntry(t *testing.T) {
	repo := "owner/repo"
	tag := "v0.6.0"
	files := map[string]string{"prompts/manifest.yaml": "version: 0\n"}

	tarball := buildTarGz(files)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			_, _ = w.Write([]byte("abc  some-other-asset.tar.gz\n"))
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			_, _ = w.Write(tarball)
		default:
			http.Error(w, "x", 404)
		}
	}))
	t.Cleanup(srv.Close)
	prev := DownloadBase
	DownloadBase = srv.URL
	t.Cleanup(func() { DownloadBase = prev })

	dst := filepath.Join(t.TempDir(), "resources")
	err := Install(context.Background(), Release{RepoSlug: repo, Tag: tag}, dst, 0)
	if err == nil || !strings.Contains(err.Error(), "SHA256SUMS missing entry") {
		t.Fatalf("expected missing-entry error, got %v", err)
	}
}

func TestExtractTarGzNotGzip(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bad.tar.gz")
	if err := os.WriteFile(tarPath, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out")
	if err := extractTarGz(tarPath, dst); err == nil {
		t.Error("expected gunzip error")
	}
}

func TestExtractTarGzNoSuchFile(t *testing.T) {
	if err := extractTarGz("/nonexistent/tarball.gz", "/tmp/x"); err == nil {
		t.Error("expected open error")
	}
}

func TestSha256FileMissing(t *testing.T) {
	if _, err := sha256File("/nonexistent/path"); err == nil {
		t.Error("expected open error")
	}
}

func TestExtractTarGzWithDirectoryEntry(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{
		Name:     "prompts/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
	})
	_ = tw.WriteHeader(&tar.Header{
		Name:     "prompts/file.md",
		Mode:     0o644,
		Size:     3,
		Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write([]byte("hi\n"))
	// A symlink should be silently skipped.
	_ = tw.WriteHeader(&tar.Header{
		Name:     "prompts/link",
		Linkname: "file.md",
		Typeflag: tar.TypeSymlink,
	})
	_ = tw.Close()
	_ = gz.Close()

	dir := t.TempDir()
	tarPath := filepath.Join(dir, "ok.tar.gz")
	if err := os.WriteFile(tarPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out")
	if err := extractTarGz(tarPath, dst); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "prompts")); err != nil {
		t.Errorf("dir entry not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "prompts", "file.md")); err != nil {
		t.Errorf("file entry not created: %v", err)
	}
}

func TestInstallStagingExtractFails(t *testing.T) {
	repo := "owner/repo"
	tag := "v0.0.0"

	// Tarball that contains an unsafe path → extractTarGz errors.
	tarball := buildTarGz(map[string]string{
		"../escape": "bad",
	})
	hash := sha256.Sum256(tarball)
	sums := hex.EncodeToString(hash[:]) + "  velocity-resources-" + tag + ".tar.gz\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			_, _ = w.Write([]byte(sums))
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			_, _ = w.Write(tarball)
		default:
			http.Error(w, "x", 404)
		}
	}))
	t.Cleanup(srv.Close)
	prev := DownloadBase
	DownloadBase = srv.URL
	t.Cleanup(func() { DownloadBase = prev })

	dst := filepath.Join(t.TempDir(), "resources")
	err := Install(context.Background(), Release{RepoSlug: repo, Tag: tag}, dst, 0)
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("expected unsafe-path error, got %v", err)
	}
}

func TestDownloadBadURL(t *testing.T) {
	if err := download(context.Background(), "://bad-url", "/tmp/x"); err == nil {
		t.Error("expected error from malformed URL")
	}
}

func TestCheckMajorParseError(t *testing.T) {
	if err := CheckMajor("not-a-tag", 0); err == nil {
		t.Error("expected parse error")
	}
}

func TestDownloadDestinationInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("body"))
	}))
	t.Cleanup(srv.Close)
	// Pass a destination that points into a non-existent directory →
	// os.Create fails.
	if err := download(context.Background(), srv.URL, "/no-such-dir/file"); err == nil {
		t.Error("expected create-error")
	}
}

func TestInstallExtractFailsWhenDstParentBlocked(t *testing.T) {
	repo := "owner/repo"
	tag := "v0.0.0"
	files := map[string]string{"prompts/manifest.yaml": "version: 0\n"}
	startReleaseServer(t, repo, tag, files, "")

	parent := t.TempDir()
	// Make parent a regular file so MkdirAll(parent) fails.
	blocker := filepath.Join(parent, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(blocker, "resources")

	err := Install(context.Background(), Release{RepoSlug: repo, Tag: tag}, dst, 0)
	if err == nil {
		t.Error("expected MkdirAll error")
	}
}

func TestParseSHASumsSkipsShortLines(t *testing.T) {
	body := []byte("abc\nabc123  velocity-resources-v0.6.0.tar.gz\n")
	m, err := parseSHASums(body)
	if err != nil {
		t.Fatal(err)
	}
	if m["velocity-resources-v0.6.0.tar.gz"] != "abc123" {
		t.Errorf("hash mismatch: %v", m)
	}
}

func TestExtractTarGzMkdirAllFailure(t *testing.T) {
	// Use "prompts/" which is a known top-level so stripTopLevel keeps it.
	tarball := buildTarGz(map[string]string{"prompts/sub/file.txt": "x"})
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "ok.tar.gz")
	if err := os.WriteFile(tarPath, tarball, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	// Block dst/prompts as a regular file so MkdirAll(dst/prompts/sub) fails.
	if err := os.WriteFile(filepath.Join(dst, "prompts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(tarPath, dst); err == nil {
		t.Error("expected MkdirAll error inside extract")
	}
}

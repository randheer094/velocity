// Package resources downloads, verifies, and extracts the
// velocity-resources release tarball into the local cache used by the
// prompts package and `velocity prepare`.
package resources

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MajorVersionFn returns the prompt-manifest major version this binary
// accepts. Wired to prompts.MajorVersion at the CLI seam to avoid an
// import cycle (resources is imported by prompts.Reload? — no, but
// keeping this package agent-agnostic keeps future imports clean).
var (
	// HTTPClient is the default client; overridable in tests.
	HTTPClient = &http.Client{Timeout: 60 * time.Second}
	// APIBase is the base URL for the GitHub REST API. Overridable in
	// tests via SetAPIBase.
	APIBase = "https://api.github.com"
	// DownloadBase is the base URL for release-asset downloads.
	DownloadBase = "https://github.com"
)

// SetAPIBase rewires the API + download base URLs to a single host
// (typically an httptest server). Tests only.
func SetAPIBase(url string) {
	APIBase = url
	DownloadBase = url
}

// Release identifies a velocity-resources tarball.
type Release struct {
	RepoSlug string
	Tag      string
}

// AssetName is the tarball asset name, e.g.
// "velocity-resources-v0.6.0.tar.gz".
func (r Release) AssetName() string {
	return "velocity-resources-" + r.Tag + ".tar.gz"
}

// SHASumName is the sibling SHA256SUMS asset.
func (r Release) SHASumName() string {
	return "SHA256SUMS"
}

// DownloadURL returns the absolute URL for an asset on the release
// page.
func (r Release) DownloadURL(asset string) string {
	return fmt.Sprintf("%s/%s/releases/download/%s/%s", DownloadBase, r.RepoSlug, r.Tag, asset)
}

// MajorOf parses the leading numeric component of a tag like "v1.2.3"
// or "1.2.3". Returns an error for unparseable tags.
func MajorOf(tag string) (int, error) {
	t := strings.TrimSpace(tag)
	t = strings.TrimPrefix(t, "v")
	if t == "" {
		return 0, fmt.Errorf("empty tag")
	}
	dot := strings.IndexByte(t, '.')
	if dot >= 0 {
		t = t[:dot]
	}
	n, err := strconv.Atoi(t)
	if err != nil {
		return 0, fmt.Errorf("parse major from %q: %w", tag, err)
	}
	return n, nil
}

// CheckMajor errors if the tag's major does not equal expected.
func CheckMajor(tag string, expected int) error {
	got, err := MajorOf(tag)
	if err != nil {
		return err
	}
	if got != expected {
		return fmt.Errorf("major mismatch: binary expects %d, requested %d", expected, got)
	}
	return nil
}

// LatestForMajor queries the GitHub releases API and returns the
// newest release whose tag's major equals the given value.
func LatestForMajor(ctx context.Context, repoSlug string, major int) (string, error) {
	if repoSlug == "" {
		return "", errors.New("empty repo slug")
	}
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=100", APIBase, repoSlug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("list releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list releases: status %d", resp.StatusCode)
	}
	var releases []struct {
		TagName    string    `json:"tag_name"`
		Draft      bool      `json:"draft"`
		Prerelease bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", fmt.Errorf("decode releases: %w", err)
	}
	type cand struct {
		tag string
		at  time.Time
	}
	var matches []cand
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		m, err := MajorOf(r.TagName)
		if err != nil || m != major {
			continue
		}
		matches = append(matches, cand{tag: r.TagName, at: r.PublishedAt})
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no releases with major=%d for %s", major, repoSlug)
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].at.After(matches[j].at) })
	return matches[0].tag, nil
}

// download streams an asset to dst. dst is overwritten.
func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: status %d", url, resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// parseSHASums parses the SHA256SUMS asset format produced by
// `sha256sum`: "<hex>  <filename>" per line.
func parseSHASums(body []byte) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		hash := strings.ToLower(fields[0])
		// `sha256sum` emits "<hash>  <path>"; the path may have a
		// leading "*" for binary mode.
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		out[name] = hash
	}
	if len(out) == 0 {
		return nil, errors.New("SHA256SUMS contained no entries")
	}
	return out, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractTarGz extracts archivePath into dst. Entries that escape dst
// (path traversal, absolute paths) are rejected. Existing entries are
// overwritten.
func extractTarGz(archivePath, dst string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Reject absolute paths and parent-traversal in the raw header.
		// We do this before any wrapper-stripping to prevent a
		// "../escape" or "/etc/x" entry from being normalised into a
		// safe-looking local path.
		rawClean := strings.TrimPrefix(hdr.Name, "./")
		rawClean = strings.TrimPrefix(rawClean, "/")
		if rawClean == "" {
			continue
		}
		if filepath.IsAbs(hdr.Name) || strings.HasPrefix(hdr.Name, "/") || !filepath.IsLocal(filepath.FromSlash(rawClean)) {
			return fmt.Errorf("tar: rejecting unsafe path %q", hdr.Name)
		}
		rel := stripTopLevel(hdr.Name)
		if rel == "" {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		// Defence in depth: confirm target is still under dst.
		dstAbs, _ := filepath.Abs(dst)
		tgtAbs, _ := filepath.Abs(target)
		if !strings.HasPrefix(tgtAbs+string(os.PathSeparator), dstAbs+string(os.PathSeparator)) && tgtAbs != dstAbs {
			return fmt.Errorf("tar: rejecting unsafe path %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			// Symlinks etc. silently skipped: we don't need them.
		}
	}
	return nil
}

// stripTopLevel removes the first path component when the archive
// is wrapped in a single top-level directory (e.g.
// "velocity-resources-v0.6.0/prompts/manifest.yaml" →
// "prompts/manifest.yaml"). We only strip when the first component
// looks like a single wrapper: presence of ≥1 slash means it has a
// real path inside.
func stripTopLevel(name string) string {
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return ""
	}
	parts := strings.SplitN(name, "/", 2)
	if len(parts) < 2 {
		// Top-level file (no subdir) — keep as-is.
		return name
	}
	first := parts[0]
	// Only strip wrappers that look like the GitHub-archive prefix
	// (contains "-" and matches the resources tarball naming).
	// Heuristic: strip any top-level directory whose name does not
	// match an expected sibling layout slot.
	known := map[string]bool{"prompts": true, "go": true, "android": true}
	if known[first] {
		return name
	}
	return parts[1]
}

// Install downloads, verifies, and atomically replaces destDir with
// the contents of the release tarball. expectedMajor pins the major
// version of the requested tag. The verified SHA256SUMS file is
// written into destDir as well.
func Install(ctx context.Context, rel Release, destDir string, expectedMajor int) error {
	if err := CheckMajor(rel.Tag, expectedMajor); err != nil {
		return err
	}
	parent := filepath.Dir(destDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp(parent, ".velocity-resources-*")
	if err != nil {
		return err
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	tarPath := filepath.Join(tmpDir, rel.AssetName())
	shaPath := filepath.Join(tmpDir, rel.SHASumName())

	if err := download(ctx, rel.DownloadURL(rel.AssetName()), tarPath); err != nil {
		return err
	}
	if err := download(ctx, rel.DownloadURL(rel.SHASumName()), shaPath); err != nil {
		return err
	}

	shaBytes, err := os.ReadFile(shaPath)
	if err != nil {
		return err
	}
	sums, err := parseSHASums(shaBytes)
	if err != nil {
		return err
	}
	expected, ok := sums[rel.AssetName()]
	if !ok {
		return fmt.Errorf("SHA256SUMS missing entry for %s", rel.AssetName())
	}
	got, err := sha256File(tarPath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("sha256 mismatch for %s: got %s, expected %s", rel.AssetName(), got, expected)
	}

	stagingDir := filepath.Join(tmpDir, "staging")
	if err := extractTarGz(tarPath, stagingDir); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(stagingDir, "VERSION"), []byte(rel.Tag+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "SHA256SUMS"), shaBytes, 0o644); err != nil {
		return err
	}

	// Atomic-ish swap: remove the existing dir, then rename. Rename
	// over a non-empty dir is not portable.
	_ = os.RemoveAll(destDir)
	if err := os.Rename(stagingDir, destDir); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	cleanupTmp = true
	return nil
}

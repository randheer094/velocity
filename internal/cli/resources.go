package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// templateEntry is a single file inside the cached resources tree
// (relative path + body) ready to be installed under a target project.
type templateEntry struct {
	relPath string
	data    []byte
}

// loadCachedTemplates walks resourcesDir/<projectType>/ and returns the
// flattened list of files that prepare should install. Returns an
// error when the cache is missing or empty for the project type.
func loadCachedTemplates(resourcesDir string, pt projectType) ([]templateEntry, error) {
	root := filepath.Join(resourcesDir, string(pt))
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("resources not installed; run `velocity setup` first")
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("resources path %s is not a directory", root)
	}
	var entries []templateEntry
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, templateEntry{relPath: filepath.ToSlash(rel), data: body})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no resources for project type %q in %s", pt, root)
	}
	return entries, nil
}

// installTemplates writes entries into root. Pre-existing files are
// skipped unless force is set.
func installTemplates(root string, entries []templateEntry, force bool) (written, skipped []string, err error) {
	for _, e := range entries {
		dst := filepath.Join(root, filepath.FromSlash(e.relPath))
		if _, statErr := os.Stat(dst); statErr == nil && !force {
			skipped = append(skipped, e.relPath)
			continue
		}
		if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
			return written, skipped, mkErr
		}
		if wErr := os.WriteFile(dst, e.data, 0o644); wErr != nil {
			return written, skipped, wErr
		}
		written = append(written, e.relPath)
	}
	return written, skipped, nil
}


package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	resourcesRepo       = "randheer094/velocity-resources"
	defaultResourcesRef = "main"
	resourcesRefEnv     = "VELOCITY_RESOURCES_REF"
)

var resourcesURL = func(ref string) string {
	return "https://codeload.github.com/" + resourcesRepo + "/tar.gz/refs/heads/" + ref
}

type templateEntry struct {
	relPath string
	data    []byte
}

func resourcesRef() string {
	if v := os.Getenv(resourcesRefEnv); v != "" {
		return v
	}
	return defaultResourcesRef
}

func fetchTemplates(ctx context.Context, pt projectType, ref string) ([]templateEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourcesURL(ref), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s@%s: %w", resourcesRepo, ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s@%s: status %d", resourcesRepo, ref, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gunzip %s@%s: %w", resourcesRepo, ref, err)
	}
	defer gz.Close()

	needle := "/" + string(pt) + "/"
	var entries []templateEntry
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		idx := strings.Index(hdr.Name, needle)
		if idx < 0 {
			continue
		}
		rel := hdr.Name[idx+len(needle):]
		if rel == "" || !filepath.IsLocal(filepath.FromSlash(rel)) {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read tar entry %s: %w", hdr.Name, err)
		}
		entries = append(entries, templateEntry{relPath: rel, data: data})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no resources for project type %q at %s@%s", pt, resourcesRepo, ref)
	}
	return entries, nil
}

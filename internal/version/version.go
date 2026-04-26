// Package version exposes the velocity binary's compile-time version.
// Major is the source-of-truth for the manifest-schema major (the
// prompts package re-exports it) — bump them together when shipping
// breaking changes to the prompt-template contract.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Major is the velocity binary's major version. Bump only when this
// binary ships matching changes to the prompt-template placeholder
// schema or another contract that resources tarballs encode.
const Major = 0

// Tag is the human-readable build tag, e.g. "v0.6.0". It is set via
// -ldflags at release time; dev builds report "dev".
//
//	go build -ldflags="-X github.com/randheer094/velocity/internal/version.Tag=v0.6.0" ./cmd/velocity
var Tag = "dev"

// String returns the version string printed by `velocity version`.
func String() string {
	return Tag
}

// MajorOf parses the leading numeric component of a tag like "v1.2.3"
// or "1.2.3". Used by CI to confirm a release tag's major matches
// Major. Returns an error for unparseable tags (e.g. "dev").
func MajorOf(tag string) (int, error) {
	t := strings.TrimSpace(tag)
	t = strings.TrimPrefix(t, "v")
	if t == "" {
		return 0, fmt.Errorf("empty tag")
	}
	if dot := strings.IndexByte(t, '.'); dot >= 0 {
		t = t[:dot]
	}
	n, err := strconv.Atoi(t)
	if err != nil {
		return 0, fmt.Errorf("parse major from %q: %w", tag, err)
	}
	return n, nil
}

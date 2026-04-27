// Package version exposes the velocity binary's compile-time version.
//
// The source of truth is internal/version/VERSION, embedded into the
// binary via //go:embed. Tag is its trimmed contents; Major is
// declared as a const and cross-checked against the parsed major of
// Tag at init time. Bumping a major requires bumping both — drift is
// caught at startup as a panic, not a silent misreport.
//
// The file lives next to this package (rather than at the repo root)
// because //go:embed only accepts patterns within the package
// subtree. scripts/check-major.sh and tests reach for it via
// internal/version/VERSION.
//
// There is no -ldflags wiring: every build path (`go build`,
// `make build`, `go install`, CI release) reads the same value from
// the embedded file, so a developer who runs `go build ./cmd/velocity`
// directly gets the same version a release binary would.
package version

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed VERSION
var versionFile string

// Major is the velocity binary's major version. Bump only when this
// binary ships matching changes to the prompt-template placeholder
// schema or another contract that resources tarballs encode. Must
// equal the leading numeric component of the VERSION file.
const Major = 0

// Tag is the human-readable build tag, e.g. "v0.6.0", read from the
// embedded VERSION file with surrounding whitespace stripped.
var Tag = strings.TrimSpace(versionFile)

func init() {
	parsed, err := parseMajor(Tag)
	if err != nil {
		panic(fmt.Sprintf("version: VERSION file %q is unparseable: %v", Tag, err))
	}
	if parsed != Major {
		panic(fmt.Sprintf("version: VERSION file declares major %d but const Major = %d; bump them together", parsed, Major))
	}
}

// String returns the version string printed by `velocity version`.
func String() string {
	return Tag
}

// parseMajor extracts the leading numeric component of a tag like
// "v1.2.3" or "1.2.3".
func parseMajor(tag string) (int, error) {
	t := strings.TrimSpace(tag)
	t = strings.TrimPrefix(t, "v")
	if t == "" {
		return 0, fmt.Errorf("empty tag")
	}
	if dot := strings.IndexByte(t, '.'); dot >= 0 {
		t = t[:dot]
	}
	return strconv.Atoi(t)
}

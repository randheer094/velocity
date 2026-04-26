// Package version exposes the velocity binary's compile-time version.
// Major is the source-of-truth for the manifest-schema major (the
// prompts package re-exports it) — bump them together when shipping
// breaking changes to the prompt-template contract.
package version

// Major is the velocity binary's major version. Bump only when this
// binary ships matching changes to the prompt-template placeholder
// schema or another contract that resources tarballs encode.
const Major = 0

// Tag is the human-readable build tag, e.g. "v0.6.0". It is set via
// -ldflags at release time; the default below is the current release
// so a binary built without ldflags still reports a sensible value.
//
//	go build -ldflags="-X github.com/randheer094/velocity/internal/version.Tag=v0.6.0" ./cmd/velocity
var Tag = "v0.6.0"

// String returns the version string printed by `velocity version`.
func String() string {
	return Tag
}

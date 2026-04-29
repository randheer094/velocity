package prompts

import "github.com/randheer094/velocity/internal/version"

// MajorVersion is the only resources-manifest major version this
// binary accepts. It tracks the velocity binary's major version —
// bump version.Major and they advance together.
const MajorVersion = version.Major

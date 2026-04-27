#!/usr/bin/env bash
# check-major.sh — fail if the supplied release tag's major doesn't
# match the velocity binary's compile-time major (version.Major).
#
# Used by .github/workflows/release.yml to block a release whose tag
# leads with a major that the source code hasn't been bumped to.
#
# Usage: scripts/check-major.sh v0.6.1
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <release-tag>" >&2
  exit 2
fi
TAG="$1"

# Canonical release tag shape: vMAJOR.MINOR.PATCH, e.g. v0.6.0.
# Pre-release / build metadata suffixes are allowed
# (e.g. v0.6.0-rc1, v0.6.0+build.1).
if ! [[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+].*)?$ ]]; then
  echo "check-major: tag $TAG does not match the canonical vMAJOR.MINOR.PATCH format" >&2
  echo "             expected e.g. v0.6.0, v0.6.0-rc1, v1.2.3+build.4" >&2
  exit 1
fi

# Extract version.Major by running a tiny Go program that imports the
# version package and prints the constant. This is robust to any
# reformatting of internal/version/version.go that a regex grep would
# silently break on.
SRC_MAJOR=$(go run ./cmd/print-version-major)
if ! [[ "$SRC_MAJOR" =~ ^[0-9]+$ ]]; then
  echo "check-major: could not extract version.Major (got: '$SRC_MAJOR')" >&2
  exit 1
fi

# Take the leading numeric component after stripping the "v".
TAG_TRIM="${TAG#v}"
TAG_MAJOR="${TAG_TRIM%%.*}"

if [ "$TAG_MAJOR" != "$SRC_MAJOR" ]; then
  echo "check-major: release tag $TAG (major $TAG_MAJOR) does not match version.Major=$SRC_MAJOR" >&2
  echo "             bump internal/version/version.go before tagging." >&2
  exit 1
fi

echo "check-major: ok — tag $TAG matches version.Major=$SRC_MAJOR"

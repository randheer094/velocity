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

# Extract version.Major from internal/version/version.go. The constant
# is on a single line: `const Major = N`.
SRC_MAJOR=$(grep -E '^const Major = [0-9]+$' internal/version/version.go | awk '{print $4}')
if [ -z "${SRC_MAJOR:-}" ]; then
  echo "check-major: could not parse version.Major from internal/version/version.go" >&2
  exit 1
fi

# Strip optional leading "v" then take the leading numeric component.
TAG_TRIM="${TAG#v}"
TAG_MAJOR="${TAG_TRIM%%.*}"
if ! [[ "$TAG_MAJOR" =~ ^[0-9]+$ ]]; then
  echo "check-major: tag $TAG does not start with a numeric major" >&2
  exit 1
fi

if [ "$TAG_MAJOR" != "$SRC_MAJOR" ]; then
  echo "check-major: release tag $TAG (major $TAG_MAJOR) does not match version.Major=$SRC_MAJOR" >&2
  echo "             bump internal/version/version.go before tagging." >&2
  exit 1
fi

echo "check-major: ok — tag $TAG matches version.Major=$SRC_MAJOR"

#!/usr/bin/env bash
# check-major.sh — fail the release CI gate if the supplied tag is
# inconsistent with internal/version. Three layered assertions:
#
#   1. Tag follows the canonical vMAJOR.MINOR.PATCH shape.
#   2. Tag matches `cat internal/version/VERSION` exactly. The
#      VERSION file is the source of truth for the binary's version
#      (embedded via //go:embed); a release whose tag drifts from
#      VERSION ships a binary that under-reports its own version.
#   3. The leading numeric component matches the `const Major`
#      compiled into internal/version. Bumping a major requires
#      bumping both the file AND the const.
#
# Usage: scripts/check-major.sh v0.6.1
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <release-tag>" >&2
  exit 2
fi
TAG="$1"
VERSION_FILE="internal/version/VERSION"

# 1. Shape.
if ! [[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+].*)?$ ]]; then
  echo "check-major: tag $TAG does not match the canonical vMAJOR.MINOR.PATCH format" >&2
  echo "             expected e.g. v0.6.0, v0.6.0-rc1, v1.2.3+build.4" >&2
  exit 1
fi

# 2. Tag vs VERSION file.
if [ ! -f "$VERSION_FILE" ]; then
  echo "check-major: $VERSION_FILE missing — cannot verify tag" >&2
  exit 1
fi
FILE_TAG=$(tr -d '[:space:]' < "$VERSION_FILE")
if [ "$TAG" != "$FILE_TAG" ]; then
  echo "check-major: release tag $TAG does not match $VERSION_FILE ($FILE_TAG)" >&2
  echo "             update $VERSION_FILE before tagging." >&2
  exit 1
fi

# 3. Tag major vs version.Major. Extract by running a tiny Go program
# that imports the version package; robust to any reformatting of
# the source that a regex grep would silently break on.
SRC_MAJOR=$(go run ./cmd/print-version-major)
if ! [[ "$SRC_MAJOR" =~ ^[0-9]+$ ]]; then
  echo "check-major: could not extract version.Major (got: '$SRC_MAJOR')" >&2
  exit 1
fi

TAG_TRIM="${TAG#v}"
TAG_MAJOR="${TAG_TRIM%%.*}"

if [ "$TAG_MAJOR" != "$SRC_MAJOR" ]; then
  echo "check-major: release tag $TAG (major $TAG_MAJOR) does not match version.Major=$SRC_MAJOR" >&2
  echo "             bump const Major in internal/version/version.go before tagging." >&2
  exit 1
fi

echo "check-major: ok — tag $TAG matches $VERSION_FILE and version.Major=$SRC_MAJOR"

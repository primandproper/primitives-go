#!/usr/bin/env bash
set -euo pipefail

# Print the directory of every Go module in this repository other than the root,
# one per line, relative to the root.
# Usage: nested_modules.sh
#
# `./...` stops at a go.mod, so a nested module is outside every root command:
# its tests, build and lint have to be run from inside it. Discovered rather
# than listed, so adding one cannot leave it out of the suite. testdata is
# skipped: the go.mod files there are fixtures, not modules of this repository.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${PROJECT_ROOT}"
find . -name go.mod -not -path ./go.mod -not -path '*/testdata/*' -not -path './artifacts/*' -not -path './.*/*' -exec dirname {} \; | sed 's|^\./||' | sort

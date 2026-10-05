#!/usr/bin/env bash
set -euo pipefail

# Run tests with coverage
# Usage: coverage.sh [output_file]
#
# A nested module writes its profile to output_file inside its own directory,
# since `./...` stops at its go.mod.

OUTPUT_FILE="${1:-coverage.out}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUN_CONTAINER_TESTS="${RUN_CONTAINER_TESTS:-true}" "${SCRIPT_DIR}/pull_test_containers.sh"

# shellcheck disable=SC2086,SC2046
CGO_ENABLED=1 go test -shuffle=on -race -vet=all -failfast -covermode=atomic -coverprofile="${OUTPUT_FILE}" $(go list ./... | grep -Ev '(mock|testutils)')

while IFS= read -r module; do
	# shellcheck disable=SC2086,SC2046
	(cd "${SCRIPT_DIR}/../${module}" && CGO_ENABLED=1 go test -shuffle=on -race -vet=all -failfast -covermode=atomic -coverprofile="${OUTPUT_FILE}" $(go list ./... | grep -Ev '(mock|testutils)'))
done < <("${SCRIPT_DIR}/nested_modules.sh")

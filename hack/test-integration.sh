#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

# shellcheck disable=SC1091
source "$(dirname "$0")/common.sh"

echo "> Integration Tests"

packages=("$@")
if [ "${#packages[@]}" -eq 0 ]; then
  packages=(./test/integration)
fi

default_procs="$(rg -n '^[[:space:]]*It\(' "${packages[@]}" --glob '*.go' | wc -l | tr -d '[:space:]')"
if [ -z "${default_procs}" ] || [ "${default_procs}" = "0" ]; then
  default_procs=2
fi

test_flags=(
  -r
  -p
  --procs="${GINKGO_PROCS:-$default_procs}"
  --timeout=45m
)

if [ -n "${GINKGO_LABEL_FILTER:-}" ]; then
  test_flags+=(--label-filter="${GINKGO_LABEL_FILTER}")
fi

if [ -n "${CI:-}" ] && [ -n "${ARTIFACTS:-}" ]; then
  mkdir -p "$ARTIFACTS/junit"
  # shellcheck disable=SC2064
  trap "collect_reports \"$ARTIFACTS/junit\"" EXIT
  test_flags+=(--junit-report=junit.xml)
fi

ginkgo "${test_flags[@]}" "${packages[@]}"

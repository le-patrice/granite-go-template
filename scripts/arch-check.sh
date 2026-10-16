#!/usr/bin/env bash
# Fails if any internal/platform package imports an internal package that is
# outside internal/platform. Test imports are checked as well.
set -euo pipefail

module="$(go list -m)"
internal_prefix="${module}/internal/"
platform_root="${module}/internal/platform"

listing="$(go list -e -f '{{.ImportPath}}|{{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./internal/platform/... 2>/dev/null || true)"

if [ -z "${listing}" ]; then
  echo "arch-check: no platform packages found, nothing to check"
  exit 0
fi

status=0
while IFS='|' read -r pkg imports; do
  for imp in ${imports}; do
    case "${imp}" in
      "${platform_root}" | "${platform_root}"/*)
        ;;
      "${internal_prefix}"*)
        echo "arch-check: ${pkg} imports ${imp}, which is outside internal/platform" >&2
        status=1
        ;;
    esac
  done
done <<< "${listing}"

if [ "${status}" -eq 0 ]; then
  echo "arch-check: ok"
fi
exit "${status}"

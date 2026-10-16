#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export PATH="$PATH:$(go env GOPATH)/bin"

# Ensure tools exist
if ! command -v golangci-lint >/dev/null 2>&1; then
    echo "golangci-lint required but not found in PATH" >&2
    exit 1
fi
if ! command -v sqlc >/dev/null 2>&1; then
    echo "sqlc required but not found in PATH" >&2
    exit 1
fi

TEMP_ROOT="$(mktemp -d /tmp/scaffold-smoke-XXXXXX)"
trap 'rm -rf "$TEMP_ROOT"' EXIT

echo "=== Running 4-way Scaffold Smoke Test Matrix ==="

run_matrix_cell() {
    local cell_name="$1"
    local mode="$2"       # fresh | gonew
    local examples="$3"   # keep | strip
    local target_dir="$TEMP_ROOT/$cell_name"

    echo ""
    echo "--> Testing [$cell_name]: mode=$mode examples=$examples"
    mkdir -p "$target_dir"

    # Copy files excluding git history and temp dirs
    rsync -a --exclude=".git" --exclude=".template/answers.json" "$REPO_DIR/" "$target_dir/"
    cd "$target_dir"

    if [ "$mode" = "gonew" ]; then
        # Simulate gonew modifying module statement in go.mod
        sed -i 's|module example.com/app|module example.com/gonewmod|g' go.mod
        go run ./cmd/scaffold init --name="$cell_name" --examples="$examples"
    else
        go run ./cmd/scaffold init --name="$cell_name" --module="example.com/$cell_name" --examples="$examples"
    fi

    # Verify answers.json exists
    if [ ! -f .template/answers.json ]; then
        echo "FAIL [$cell_name]: .template/answers.json not created" >&2
        return 1
    fi

    # Verification checks
    go test -count=1 ./...
    golangci-lint run ./...
    bash scripts/arch-check.sh

    echo "PASS [$cell_name]"
}

run_matrix_cell "fresh-keep"  "fresh" "keep"
run_matrix_cell "fresh-strip" "fresh" "strip"
run_matrix_cell "gonew-keep"  "gonew" "keep"
run_matrix_cell "gonew-strip" "gonew" "strip"

echo ""
echo "=== All 4 scaffold smoke matrix tests passed! ==="

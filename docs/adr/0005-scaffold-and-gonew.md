# 0005. Scaffolding Engine and gonew Compatibility

Date: 2026-10-16
Status: Accepted

## Context

A production service template must support two user workflows:
1. Conventional Git cloning followed by parameterized substitution (`make init NAME=<x> MODULE=<y>`).
2. Go's official `gonew` tool (`gonew github.com/owner/granite-go myorg/myservice`), which rewrites `go.mod` automatically during initial checkout.

Shell-script scaffolding (`scripts/init.sh`, `scripts/strip-examples.sh`) suffered from portability limitations across BSD/GNU `sed`, fragility with complex marker stripping, and lacked structured answers tracking.

## Decision

We implement a pure-Go CLI (`cmd/scaffold`) using standard library packages only:
1. **gonew Detection**: Inspects `go.mod` on `init`. If already rewritten by `gonew`, adopts the existing module path while substituting secondary tokens across non-Go files.
2. **Copier-like Answers**: Writes `.template/answers.json` on initialization to record project metadata and prevent accidental re-initialization without `--force`.
3. **Robust Example Stripping**: Replaces shell regex with a parser that validates inline markers (`// template:example:begin <tag>` ... `// template:example:end <tag>`), reporting syntax errors on unbalanced or nested tags, deletes marked example files (`template:example:file`), and runs `gofmt`, `sqlc`, and `go mod tidy`.
4. **Ownership-Aware Diffing**: Categorizes upstream changes into `template`, `shared`, and `project` tiers based on `.template/OWNERSHIP`.
5. **4-Way Smoke Matrix**: `scripts/scaffold-smoke.sh` exercises both fresh clones and simulated post-`gonew` projects with `EXAMPLES=keep` and `EXAMPLES=strip`, asserting full test, lint, and architecture compliance.

## Consequences

- Zero external dependencies introduced.
- Cross-platform consistency across Linux, macOS, and Windows.
- Full compatibility with both `gonew` and Git clone bootstrapping.

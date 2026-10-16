# Module: scaffold

A pure-Go templating and scaffolding engine compatible with Go's official `gonew` tool, offering Copier-like project initialization, marker-driven example removal, layered feature generation, and ownership-based template diffing.

## When to adopt

- **Already included** in the granite-go boilerplate baseline as the default scaffolding toolchain.
- Adopt if you need single-command project bootstrapping (`make init`), safe example pruning (`make strip-examples`), architectural feature generator (`make new-feature`), and clean upstream upgrades (`make template-diff`).

## What it adds

- `cmd/scaffold/main.go`: Pure-Go CLI with zero external dependencies.
- `.template/origin` and `.template/OWNERSHIP`: Upstream tracking metadata and path ownership classification.
- `.template/answers.json`: Recorded initialization configuration (name, module, template version).
- `scripts/scaffold-smoke.sh`: 4-way test matrix verifying fresh-clone and post-`gonew` projects with keep/strip examples.
- `docs/SCAFFOLDING.md`: User-facing documentation for template lifecycle.

## Uninstallation / Removal

If a downstream project has completed scaffolding and wishes to eliminate the scaffolding CLI entirely:

1. Remove the CLI tool and helper scripts:
   ```bash
   rm -rf cmd/scaffold scripts/scaffold-smoke.sh docs/SCAFFOLDING.md
   ```

2. Remove scaffolding targets from `Makefile` (`init`, `strip-examples`, `new-feature`, `template-diff`, `scaffold-smoke`).

3. Remove the `scaffold-smoke` job from `.github/workflows/ci.yml`.

4. Update `.template/OWNERSHIP` and `docs/ADDONS.md`.

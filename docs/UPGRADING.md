# Upgrading from the Template

Projects scaffolded from `granite-go` record their template origin in `.template/origin` and path ownership in `.template/OWNERSHIP`.

## Ownership Categories

- **`template`:** Infrastructure owned by the template (`internal/platform/**`, `scripts/**`, `.github/workflows/**`, `.golangci.yml`, `deployments/**`, `docs/ARCHITECTURE.md`, `AGENTS.md`). Avoid project-specific edits here so upgrades apply cleanly.
- **`shared`:** Composition roots and configuration (`internal/app/**`, `internal/config/**`, `cmd/**`, `Makefile`, `sqlc.yaml`). Projects extend these and merge upstream template changes by hand.
- **`project`:** Business features, integrations, migrations, queries, OpenAPI spec, and project docs. Never touched by template upgrades.

## Upgrade Workflow

1. **Add the template remote (once):**
   ```bash
   git remote add template <template-git-url>
   git fetch template --tags
   ```

2. **Inspect upstream changes grouped by ownership:**
   ```bash
   make template-diff TAG=v0.2.0
   ```
   `template-diff` compares the version recorded in `.template/origin` against `TAG`, filtering out `project`-owned paths and grouping diffs into `template` and `shared` sections.

3. **Read `CHANGELOG.md` "Upgrade notes":**
   Any template release that requires edits to `shared` or `project` files includes explicit instructions under `### Upgrade notes`.

4. **Apply `template`-owned changes and hand-merge `shared` files:**
   ```bash
   git checkout v0.2.0 -- internal/platform
   ```
   Rewrite the stand-in module path (`example.com/app`) to your project's module path if needed, hand-merge `shared` changes, update `.template/origin`, and run the full verification suite:
   ```bash
   make lint arch-check test test-integration migrate-verify
   ```

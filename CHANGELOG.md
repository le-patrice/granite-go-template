# Changelog

All notable changes to this template are recorded here. The template uses
semantic versioning, recorded in `TEMPLATE_VERSION`. Any change that needs
edits in downstream projects carries an "Upgrade notes" entry.

## [Unreleased]

## [0.2.0]

### Added

- `cmd/scaffold`: Pure-Go CLI replacing shell scripts for `init`, `strip-examples`, `new-feature`, and `template-diff`.
- `gonew` support: Automatic detection and parameter inheritance when initialized via `gonew`.
- Answers tracking: `.template/answers.json` records initial scaffolding configuration and prevents accidental re-runs.
- Path ownership definitions in `.template/OWNERSHIP` and origin tracking in `.template/origin`.
- 4-way `scaffold-smoke` test matrix (`scripts/scaffold-smoke.sh`) and CI workflow integration.
- `docs/SCAFFOLDING.md` guide and ADR 0005.

### Upgrade notes

- Downstream projects can adopt `cmd/scaffold` by copying `cmd/scaffold/`, `.template/`, and updating `Makefile` targets.

### Added

- Repository skeleton: Go module, Makefile, golangci-lint v2 configuration
  with architecture rules, CI workflow, ignore files, and the architecture
  check script.

### Upgrade notes

None. This is the initial release.

# Domain Migration Ledger

Tracks per-domain progress in `MODE=migrate` (Phases M0–M7). Tag each completed domain step as `migrate-<domain>-M<k>`.

## Domains

| Domain | Status (`inventoried`/`baseline`/`characterized`/`ported`/`strangled`/`cut-over`/`decommissioned`) | Tag | Date | Notes |
|---|---|---|---|---|
| | | | | |

## Post-Port Improvement Backlog

Record behavior improvements or refactors discovered during porting here so they are applied in separate commits after M6 cutover.

- [ ] Example: tighten validation or pagination limit after legacy clients migrate.

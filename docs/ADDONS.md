# Add-ons Registry

This repository supports self-contained, opt-in add-ons that enhance developer experience and API capabilities while preserving modularity and the ability to cleanly uninstall.

| Add-on | Status | Version | Installed At | Description |
|---|---|---|---|---|
| `scaffold` | installed | 0.2.0 | 2026-10-16 | gonew-ready template, Copier-like init, template-diff, new-feature generator |
| `huma` | installed | 0.3.0 | 2026-10-16 | Typed handlers, automatic OpenAPI 3.1, interactive docs UI, unified RFC 9457 errors |

## Management

Add-ons follow the add-on protocol:
1. Each add-on is developed in an isolated branch (`addon/<name>`).
2. Verification gates are executed at every step.
3. Upon acceptance, the add-on is merged to `main`, tagged `addon-<name>`, and marked `installed` in this registry.
4. Each add-on includes an uninstallation guide in `templates/modules/<name>/README.md`.

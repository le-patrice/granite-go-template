# Module: contract

## When to adopt

Adopt this module when downstream consumers or contract tests want generated Go or TypeScript clients from `api/openapi.yaml`.

## What it adds

- Code generation config and Makefile target generating a typed client package from `api/openapi.yaml`.

## How to remove

1. Delete the generated client directory and config file.
2. Remove the generation target from `Makefile`.

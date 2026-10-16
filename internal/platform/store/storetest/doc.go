// Package storetest starts a throwaway Postgres container, applies the goose
// migrations, and hands tests a clean database. Its implementation is built
// only with the "integration" tag, so normal builds do not pull in
// testcontainers. Phase 5 adds River's migrations through WithMigration.
package storetest

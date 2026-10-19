#!/usr/bin/env bash
set -euo pipefail

TABLE="${TABLE:-your_tenant_table}"
TIMESTAMP="$(date -u +%Y%m%d%H%M%S)"
TARGET="db/migrations/${TIMESTAMP}_enable_${TABLE}_rls.sql"

mkdir -p db/migrations

cat > "${TARGET}" <<EOF
-- +goose Up
-- Enable and force PostgreSQL Row-Level Security (RLS) on ${TABLE}.
-- Prerequisite: ${TABLE} must have a "tenant_id uuid NOT NULL" column and the
-- application database role must be NOSUPERUSER NOBYPASSRLS.
ALTER TABLE ${TABLE} ENABLE ROW LEVEL SECURITY;
ALTER TABLE ${TABLE} FORCE ROW LEVEL SECURITY;

CREATE POLICY ${TABLE}_tenant_isolation ON ${TABLE}
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- +goose Down
DROP POLICY IF EXISTS ${TABLE}_tenant_isolation ON ${TABLE};
ALTER TABLE ${TABLE} NO FORCE ROW LEVEL SECURITY;
ALTER TABLE ${TABLE} DISABLE ROW LEVEL SECURITY;
EOF

echo "Created RLS migration template: ${TARGET}"
echo "Next steps:"
echo "  1. Edit ${TARGET} to target your tenant-scoped table(s)."
echo "  2. Wrap repository queries in store.InTenantTx(ctx, r.pool, tenantID, ...)."
echo "  3. Ensure the runtime Postgres role has NOSUPERUSER and NOBYPASSRLS."

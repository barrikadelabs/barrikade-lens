CREATE TABLE IF NOT EXISTS service_accounts (
    id uuid PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name text NOT NULL,
    token_hash bytea NOT NULL UNIQUE,
    scopes text[] NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at timestamptz,
    UNIQUE (organization_id, name)
);

CREATE INDEX IF NOT EXISTS service_accounts_active
    ON service_accounts (organization_id, id)
    WHERE revoked_at IS NULL;

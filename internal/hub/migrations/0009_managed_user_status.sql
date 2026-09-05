CREATE TABLE IF NOT EXISTS managed_users (
    user_id text PRIMARY KEY,
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked','deleted')),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS request_rate_limits (
    scope text NOT NULL,
    key_digest bytea NOT NULL,
    window_start timestamptz NOT NULL,
    request_count integer NOT NULL CHECK (request_count > 0),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (scope,key_digest,window_start)
);

CREATE INDEX IF NOT EXISTS request_rate_limits_expiry
    ON request_rate_limits(expires_at);

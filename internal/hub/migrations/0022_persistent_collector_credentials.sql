-- Managed collectors are long-lived installations. Their stored credential
-- remains valid until an administrator explicitly revokes the source or the
-- device is re-enrolled. Short-lived access tokens are still minted from this
-- credential, so normal API authorization remains bounded.
ALTER TABLE collector_refresh_tokens
    ALTER COLUMN expires_at DROP NOT NULL;

UPDATE collector_refresh_tokens
SET expires_at = NULL
WHERE expires_at IS NOT NULL;

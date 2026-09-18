-- Keep administrator-defined labels separate from the hostname reported by a
-- collector. A re-enrollment or hostname change may update observed_name, but
-- must never replace display_name or the persistent installation identity.
ALTER TABLE discovery_targets
    ADD COLUMN IF NOT EXISTS observed_name text,
    ADD COLUMN IF NOT EXISTS display_name text,
    ADD COLUMN IF NOT EXISTS revoked_at timestamptz,
    ADD COLUMN IF NOT EXISTS revoked_by text;

UPDATE discovery_targets SET observed_name=name WHERE observed_name IS NULL;

CREATE INDEX IF NOT EXISTS discovery_targets_device_fleet
    ON discovery_targets(organization_id,target_type,deployment_policy_id,created_at DESC,id)
    WHERE target_type='endpoint';

CREATE INDEX IF NOT EXISTS discovery_targets_device_lifecycle
    ON discovery_targets(organization_id,target_type,revoked_at,last_seen_at DESC)
    WHERE target_type='endpoint';

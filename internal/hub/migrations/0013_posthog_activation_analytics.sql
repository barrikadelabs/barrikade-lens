ALTER TABLE managed_users
    ADD COLUMN IF NOT EXISTS analytics_opted_out_at timestamptz;

ALTER TABLE product_events
    ADD COLUMN IF NOT EXISTS dedupe_key text,
    ADD COLUMN IF NOT EXISTS posthog_status text,
    ADD COLUMN IF NOT EXISTS posthog_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS posthog_next_attempt_at timestamptz,
    ADD COLUMN IF NOT EXISTS posthog_lease_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS posthog_delivered_at timestamptz,
    ADD COLUMN IF NOT EXISTS posthog_last_error text;

ALTER TABLE product_events DROP CONSTRAINT IF EXISTS product_events_posthog_status_check;
ALTER TABLE product_events ADD CONSTRAINT product_events_posthog_status_check
    CHECK (posthog_status IS NULL OR posthog_status IN ('pending','processing','retry','delivered','dead'));

CREATE INDEX IF NOT EXISTS product_events_posthog_ready
    ON product_events(posthog_next_attempt_at,created_at)
    WHERE posthog_status IN ('pending','retry','processing');

CREATE UNIQUE INDEX IF NOT EXISTS product_events_workspace_dedupe
    ON product_events(organization_id,event_type,dedupe_key)
    WHERE organization_id IS NOT NULL AND dedupe_key IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS product_events_signup_actor_once_v2
    ON product_events(actor_id,event_type)
    WHERE actor_id IS NOT NULL AND event_type='signup_completed';

DROP INDEX IF EXISTS product_events_first_result_once;
CREATE UNIQUE INDEX IF NOT EXISTS product_events_first_discovery_inspected_once
    ON product_events(organization_id,event_type)
    WHERE organization_id IS NOT NULL AND event_type='first_credible_discovery_inspected';

CREATE UNIQUE INDEX IF NOT EXISTS product_events_first_discovery_completed_once
    ON product_events(organization_id,event_type)
    WHERE organization_id IS NOT NULL AND event_type='first_credible_discovery_completed';


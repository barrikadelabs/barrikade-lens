CREATE UNIQUE INDEX IF NOT EXISTS product_events_signup_once
    ON product_events(actor_id,event_type)
    WHERE actor_id IS NOT NULL AND event_type='signup_completed';

CREATE UNIQUE INDEX IF NOT EXISTS product_events_first_result_once
    ON product_events(organization_id,actor_id,event_type)
    WHERE organization_id IS NOT NULL AND actor_id IS NOT NULL AND event_type='first_result_viewed';

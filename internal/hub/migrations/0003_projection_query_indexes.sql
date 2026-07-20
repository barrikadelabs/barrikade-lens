-- Purpose-built seek-pagination indexes keep executive and inventory views
-- bounded even when an organization has millions of current entities.
CREATE INDEX IF NOT EXISTS entity_posture_system_page
    ON entity_posture (organization_id,last_seen_at DESC,entity_id DESC)
    WHERE current=true AND system_role='system';

CREATE INDEX IF NOT EXISTS entity_posture_system_filter_page
    ON entity_posture (organization_id,system_type,discovery_state,last_seen_at DESC,entity_id DESC)
    WHERE current=true AND system_role='system';

CREATE INDEX IF NOT EXISTS entity_posture_system_target_page
    ON entity_posture (organization_id,target_id,surface,last_seen_at DESC,entity_id DESC)
    WHERE current=true AND system_role='system';

CREATE INDEX IF NOT EXISTS entities_current_page
    ON entities (organization_id,last_seen_at DESC,id DESC)
    WHERE current=true;

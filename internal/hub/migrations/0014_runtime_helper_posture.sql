-- Runtime-bundled markdown agent definitions are subordinate configuration,
-- not proof of independently deployed autonomous systems. New collectors emit
-- entity_role explicitly; this backfill immediately corrects existing records.
UPDATE entity_posture AS posture
SET system_role = 'component',
    system_type = NULL,
    material_digest = md5(posture.material_digest || ':runtime-helper-component')
FROM entities AS entity
WHERE entity.organization_id = posture.organization_id
  AND entity.id = posture.entity_id
  AND entity.kind = 'agent'
  AND posture.current = true
  AND posture.system_role = 'system'
  AND entity.attributes->>'definition_format' = 'agent_markdown'
  AND entity.attributes->>'source_surface' = 'endpoint'
  AND COALESCE((entity.attributes->>'defined')::boolean, false) = true;

-- Findings are only valid for root systems. Preserve their audit rows but stop
-- presenting findings previously derived from these helper definitions.
UPDATE exposure_findings AS finding
SET current = false
FROM entity_posture AS posture
JOIN entities AS entity
  ON entity.organization_id = posture.organization_id
 AND entity.id = posture.entity_id
WHERE posture.organization_id = finding.organization_id
  AND posture.entity_id = finding.root_entity_id
  AND posture.system_role = 'component'
  AND entity.kind = 'agent'
  AND entity.attributes->>'definition_format' = 'agent_markdown'
  AND entity.attributes->>'source_surface' = 'endpoint'
  AND COALESCE((entity.attributes->>'defined')::boolean, false) = true
  AND finding.current = true;

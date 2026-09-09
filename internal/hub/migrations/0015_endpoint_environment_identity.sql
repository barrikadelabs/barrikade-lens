-- Endpoint hostnames are descriptive labels, not stable identities. Different
-- machines can legitimately share a hostname, and reimaging can create a new
-- persistent installation identity for the same hostname. Endpoint identity is
-- enforced through discovery_targets.identity_fingerprint and the active
-- source bound to that target.
DROP INDEX IF EXISTS environment_connections_external_active;
CREATE UNIQUE INDEX environment_connections_external_active
    ON environment_connections(organization_id,kind,external_id)
    WHERE external_id IS NOT NULL
      AND connection_status <> 'disconnected'
      AND kind <> 'endpoint';

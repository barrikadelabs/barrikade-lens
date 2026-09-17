ALTER TABLE enrollment_codes
    ADD COLUMN IF NOT EXISTS enrollment_mode text NOT NULL DEFAULT 'continuous';
ALTER TABLE enrollment_codes DROP CONSTRAINT IF EXISTS enrollment_codes_enrollment_mode_check;
ALTER TABLE enrollment_codes ADD CONSTRAINT enrollment_codes_enrollment_mode_check
    CHECK (enrollment_mode IN ('quick_scan','continuous'));

ALTER TABLE sources
    ADD COLUMN IF NOT EXISTS reporting_mode text NOT NULL DEFAULT 'continuous',
    ADD COLUMN IF NOT EXISTS quick_scan_started_at timestamptz,
    ADD COLUMN IF NOT EXISTS evidence_expires_at timestamptz;
ALTER TABLE sources DROP CONSTRAINT IF EXISTS sources_reporting_mode_check;
ALTER TABLE sources ADD CONSTRAINT sources_reporting_mode_check
    CHECK (reporting_mode IN ('quick_scan','continuous'));

ALTER TABLE discovery_targets
    ADD COLUMN IF NOT EXISTS reporting_mode text NOT NULL DEFAULT 'continuous',
    ADD COLUMN IF NOT EXISTS evidence_expires_at timestamptz;
ALTER TABLE discovery_targets DROP CONSTRAINT IF EXISTS discovery_targets_reporting_mode_check;
ALTER TABLE discovery_targets ADD CONSTRAINT discovery_targets_reporting_mode_check
    CHECK (reporting_mode IN ('quick_scan','continuous'));

ALTER TABLE environment_connections
    ADD COLUMN IF NOT EXISTS monitoring_mode text NOT NULL DEFAULT 'continuous';
ALTER TABLE environment_connections DROP CONSTRAINT IF EXISTS environment_connections_monitoring_mode_check;
ALTER TABLE environment_connections ADD CONSTRAINT environment_connections_monitoring_mode_check
    CHECK (monitoring_mode IN ('quick_scan','continuous'));

CREATE INDEX IF NOT EXISTS discovery_targets_quick_scan_expiry
    ON discovery_targets(evidence_expires_at)
    WHERE reporting_mode='quick_scan' AND current=true;

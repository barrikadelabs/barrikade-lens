CREATE INDEX IF NOT EXISTS ingestion_jobs_active_source
    ON ingestion_jobs(organization_id,source_id)
    WHERE status IN ('pending','processing');

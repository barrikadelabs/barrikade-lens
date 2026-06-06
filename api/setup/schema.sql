-- Barrikade Lens — BigQuery Schema
-- Run this via: bq query --use_legacy_sql=false < setup/schema.sql
-- Project: barrikade

CREATE SCHEMA IF NOT EXISTS `barrikade.lens`
  OPTIONS (location = 'US');

CREATE TABLE IF NOT EXISTS `barrikade.lens.telemetry_events` (
  unique_id            STRING     NOT NULL,
  ingested_at          TIMESTAMP  NOT NULL,
  client_timestamp     TIMESTAMP,
  platform             STRING,
  arch                 STRING,
  node_version         STRING,
  scanner_version      STRING,
  agents_count         INT64,
  agents_active        INT64,
  agents_installed     INT64,
  configs_scanned      INT64,
  mcp_servers_found    INT64,
  ports_scanned        INT64,
  ports_open           INT64,
  ports_exposed        INT64,
  secrets_found        INT64,
  critical_findings    INT64,
  high_findings        INT64,
  medium_findings      INT64,
  tool_execution       STRING,
  local_inference      STRING,
  workspace_presence   STRING,
  credential_exposure  STRING
)
PARTITION BY DATE(ingested_at)
CLUSTER BY platform, scanner_version;

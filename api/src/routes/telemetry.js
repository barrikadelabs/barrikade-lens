import { Router } from 'express';
import { validateTelemetryPayload } from '../middleware/validator.js';
import { insertTelemetryRow } from '../services/bigquery.js';

export const telemetryRouter = Router();

/**
 * POST /lens/telemetry
 *
 * Receives an anonymous telemetry payload from the barrikade-lens CLI,
 * validates it, and streams it into BigQuery.
 */
telemetryRouter.post('/telemetry', async (req, res) => {
  // ── Validate ────────────────────────────────────────────
  const validation = validateTelemetryPayload(req.body);
  if (!validation.ok) {
    return res.status(400).json({
      error: 'Invalid payload',
      details: validation.errors,
    });
  }

  const payload = req.body;

  // -- Build BigQuery row --
  const row = {
    unique_id: payload.uniqueId,
    ingested_at: new Date().toISOString(),
    client_timestamp: payload.timestamp || null,
    platform: payload.platform || null,
    arch: payload.arch || null,
    node_version: payload.nodeVersion || null,
    scanner_version: payload.scannerVersion || null,
    agents_count: payload.metrics?.agentsCount ?? 0,
    agents_active: payload.metrics?.agentsActive ?? 0,
    agents_installed: payload.metrics?.agentsInstalled ?? 0,
    configs_scanned: payload.metrics?.configsScanned ?? 0,
    mcp_servers_found: payload.metrics?.mcpServersFound ?? 0,
    ports_scanned: payload.metrics?.portsScanned ?? 0,
    ports_open: payload.metrics?.portsOpen ?? 0,
    ports_exposed: payload.metrics?.portsExposed ?? 0,
    secrets_found: payload.metrics?.secretsFound ?? 0,
    critical_findings: payload.metrics?.criticalFindings ?? 0,
    high_findings: payload.metrics?.highFindings ?? 0,
    medium_findings: payload.metrics?.mediumFindings ?? 0,
    tool_execution: payload.metrics?.toolExecutionStatus || 'UNKNOWN',
    local_inference: payload.metrics?.localInferenceStatus || 'UNKNOWN',
    workspace_presence: payload.metrics?.workspacePresenceStatus || 'UNKNOWN',
    credential_exposure: payload.metrics?.credentialExposureStatus || 'UNKNOWN',
  };

  // -- Insert --
  try {
    await insertTelemetryRow(row);
    return res.status(201).json({ status: 'created' });
  } catch (err) {
    console.error('BigQuery insert failed:', err.message);
    // Return 500 but don't leak internal details
    return res.status(500).json({ error: 'Internal server error' });
  }
});

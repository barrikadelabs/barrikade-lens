/**
 * Validates the incoming telemetry payload from barrikade-lens CLI.
 *
 * Checks that required top-level fields exist and that the `metrics`
 * object contains the expected numeric/string fields. Does NOT enforce
 * strict schemas - unknown fields are silently ignored.
 *
 * @param {any} body - The parsed JSON request body
 * @returns {{ ok: boolean, errors?: string[] }}
 */
export function validateTelemetryPayload(body) {
  const errors = [];

  if (!body || typeof body !== 'object') {
    return { ok: false, errors: ['Request body must be a JSON object'] };
  }

  // ── Required top-level fields ────────────────────────────
  if (typeof body.uniqueId !== 'string' || body.uniqueId.length === 0) {
    errors.push('uniqueId: required, must be a non-empty string');
  }

  if (typeof body.uniqueId === 'string' && body.uniqueId.length > 128) {
    errors.push('uniqueId: must be <= 128 characters');
  }

  if (body.timestamp !== undefined && typeof body.timestamp !== 'string') {
    errors.push('timestamp: must be an ISO 8601 string if provided');
  }

  if (body.platform !== undefined && typeof body.platform !== 'string') {
    errors.push('platform: must be a string if provided');
  }

  // ── Metrics object ───────────────────────────────────────
  if (!body.metrics || typeof body.metrics !== 'object') {
    errors.push('metrics: required, must be an object');
    return { ok: false, errors };
  }

  const intFields = [
    'agentsCount', 'agentsActive', 'agentsInstalled',
    'configsScanned', 'mcpServersFound',
    'portsScanned', 'portsOpen', 'portsExposed',
    'secretsFound', 'criticalFindings', 'highFindings', 'mediumFindings'
  ];

  for (const field of intFields) {
    const val = body.metrics[field];
    if (val !== undefined && (typeof val !== 'number' || !Number.isInteger(val) || val < 0)) {
      errors.push(`metrics.${field}: must be a non-negative integer if provided`);
    }
  }

  const statusFields = [
    'toolExecutionStatus', 'localInferenceStatus',
    'workspacePresenceStatus', 'credentialExposureStatus'
  ];

  for (const field of statusFields) {
    const val = body.metrics[field];
    if (val !== undefined && typeof val !== 'string') {
      errors.push(`metrics.${field}: must be a string if provided`);
    }
  }

  return errors.length > 0 ? { ok: false, errors } : { ok: true };
}

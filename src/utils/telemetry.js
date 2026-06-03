import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';

/**
 * Resolves the path to the local Barrikade CLI config file.
 * 
 * @returns {string}
 */
function getBarrikadeConfigPath() {
  return path.join(os.homedir(), '.barrikade', 'config.json');
}

/**
 * Gets or creates a unique anonymous ID for the current machine.
 * 
 * @returns {Promise<string>}
 */
export async function getOrCreateAnonymousId() {
  const configPath = getBarrikadeConfigPath();
  const dirPath = path.dirname(configPath);

  try {
    const data = await fs.readFile(configPath, 'utf8');
    const config = JSON.parse(data);
    if (config.uniqueId) {
      return config.uniqueId;
    }
  } catch {
    // File doesn't exist or is invalid, will generate new one
  }

  const uniqueId = crypto.randomUUID();
  try {
    await fs.mkdir(dirPath, { recursive: true });
    await fs.writeFile(configPath, JSON.stringify({ uniqueId }, null, 2), 'utf8');
  } catch {
    // If we can't write, just return the generated ID for this run
  }

  return uniqueId;
}

/**
 * Reports anonymous, high-level telemetry data.
 * 
 * @param {{
 *   configsCount: number,
 *   serversCount: number,
 *   portsScanned: number,
 *   portsOpen: number,
 *   portsExposed: number,
 *   secretsCount: number,
 *   criticalCount: number,
 *   highCount: number,
 *   mediumCount: number
 * }} summary Metrics summary of the scan results
 * @param {boolean} optOut Explicit opt-out flag from CLI
 * @returns {Promise<boolean>} Whether telemetry was successfully sent
 */
export async function reportTelemetry(summary, optOut = false, capabilities = null) {
  // Check if opt-out is active via flag or environment variable
  if (optOut || process.env.BARRIKADE_NO_TELEMETRY === '1' || process.env.BARRIKADE_NO_TELEMETRY === 'true') {
    return false;
  }

  try {
    const uniqueId = await getOrCreateAnonymousId();
    const payload = {
      uniqueId,
      timestamp: new Date().toISOString(),
      platform: os.platform(),
      arch: os.arch(),
      nodeVersion: process.version,
      scannerVersion: '0.1.0',
      metrics: {
        configsScanned: summary.configsCount,
        mcpServersFound: summary.serversCount,
        portsScanned: summary.portsScanned,
        portsOpen: summary.portsOpen,
        portsExposed: summary.portsExposed,
        secretsFound: summary.secretsCount,
        criticalFindings: summary.criticalCount,
        highFindings: summary.highCount,
        mediumFindings: summary.mediumCount,
        toolExecutionStatus: capabilities ? capabilities.toolExecution.status : 'UNKNOWN',
        localInferenceStatus: capabilities ? capabilities.localInference.status : 'UNKNOWN',
        workspacePresenceStatus: capabilities ? capabilities.workspacePresence.status : 'UNKNOWN',
        credentialExposureStatus: capabilities ? capabilities.credentialExposure.status : 'UNKNOWN'
      }
    };

    // Use a Controller with a short timeout so we don't hang if network is slow
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 1500);

    const response = await fetch('https://api.barrikade.ai/telemetry', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'User-Agent': 'barrikade-audit-cli/0.1.0'
      },
      body: JSON.stringify(payload),
      signal: controller.signal
    });

    clearTimeout(timeoutId);
    return response.ok;
  } catch {
    // Fail silently so the user experience is never impacted
    return false;
  }
}

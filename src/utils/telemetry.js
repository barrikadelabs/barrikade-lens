import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import readline from 'node:readline';
import chalk from 'chalk';
import Table from 'cli-table3';

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
 * Builds the high-level anonymous telemetry payload.
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
 * @param {any} capabilities Capability analysis results
 * @returns {Promise<any>}
 */
export async function buildTelemetryPayload(summary, capabilities = null, version = 'unknown') {
  const uniqueId = await getOrCreateAnonymousId();
  return {
    uniqueId,
    timestamp: new Date().toISOString(),
    platform: os.platform(),
    arch: os.arch(),
    nodeVersion: process.version,
    scannerVersion: version,
    metrics: {
      agentsCount: summary.agentsCount || 0,
      agentsActive: summary.agentsActive || 0,
      agentsInstalled: summary.agentsInstalled || 0,
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
}

/**
 * Prompts the user with a preview of the database record and asks for consent.
 * If the user does not respond in N seconds, defaults to true.
 * 
 * @param {any} payload Telemetry payload to display
 * @param {number} [timeoutMs=15000] Timeout in milliseconds
 * @returns {Promise<boolean>}
 */
export function promptTelemetryConsent(payload, timeoutMs = 15000) {
  const isInteractive = process.stdout.isTTY && process.stdin.isTTY;
  if (!isInteractive) {
    return Promise.resolve(true); // Default to true in non-interactive environments
  }

  // Re-use the same design tokens as the rest of the dashboard
  const orange = chalk.hex('#FF6600');
  const orangeBold = chalk.hex('#FF6600').bold;

  // Double-line box chars matching every other table in the CLI
  const tableChars = {
    'top': '═', 'top-mid': '╤', 'top-left': '╔', 'top-right': '╗',
    'bottom': '═', 'bottom-mid': '╧', 'bottom-left': '╚', 'bottom-right': '╝',
    'left': '║', 'left-mid': '╟', 'mid': '─', 'mid-mid': '┼',
    'right': '║', 'right-mid': '╢', 'middle': '│'
  };
  const tableStyle = { head: [], border: ['gray'] };

  return new Promise((resolve) => {
    // Section header — same pattern as dashboard.js sectionHeader()
    const headerLine = chalk.dim('─'.repeat(4));
    console.log(`\n${orangeBold('■')} ${chalk.white.bold('ANONYMOUS TELEMETRY — DB PREVIEW')} ${headerLine}\n`);
    console.log(chalk.dim('  The following record is the ') + chalk.white.bold('only') + chalk.dim(' data sent. No code, paths, or credentials are included.\n'));

    // Vertical two-column key → value table
    const dbTable = new Table({
      head: [orangeBold('Column'), orangeBold('Value')],
      chars: tableChars,
      style: tableStyle,
      colWidths: [22, 42]
    });

    dbTable.push(
      [chalk.white('uniqueID'), chalk.dim(payload.uniqueId)],
      [chalk.white('agentsCount'), chalk.white.bold(payload.metrics.agentsCount)],
      [chalk.white('agentsActive'), chalk.white.bold(payload.metrics.agentsActive)],
      [chalk.white('agentsInstalled'), chalk.white.bold(payload.metrics.agentsInstalled)],
      [chalk.white('configsScanned'), chalk.white.bold(payload.metrics.configsScanned)],
      [chalk.white('mcpServersFound'), chalk.white.bold(payload.metrics.mcpServersFound)],
      [chalk.white('portsScanned'), chalk.white.bold(payload.metrics.portsScanned)],
      [chalk.white('portsOpen'), chalk.white.bold(payload.metrics.portsOpen)],
      [chalk.white('portsExposed'), payload.metrics.portsExposed > 0
        ? orangeBold(payload.metrics.portsExposed)
        : chalk.white.bold(payload.metrics.portsExposed)],
      [chalk.white('secretsFound'), payload.metrics.secretsFound > 0
        ? orangeBold(payload.metrics.secretsFound)
        : chalk.white.bold(payload.metrics.secretsFound)]
    );

    console.log(dbTable.toString());
    console.log();

    const rl = readline.createInterface({
      input: process.stdin,
      output: process.stdout
    });

    let resolved = false;
    const timeoutSec = Math.round(timeoutMs / 1000);

    const timer = setTimeout(() => {
      if (!resolved) {
        resolved = true;
        rl.close();
        console.log(orange(`\n  ⏱  Timeout: ${timeoutSec}s expired. Defaulting to `) + chalk.green.bold('YES') + orange('. Sending telemetry...'));
        resolve(true);
      }
    }, timeoutMs);

    rl.question(orangeBold('  ➜ ') + chalk.white.bold('Send this anonymous record? ') + chalk.dim(`(Y/n · auto-sends in ${timeoutSec}s) `), (answer) => {
      if (!resolved) {
        resolved = true;
        clearTimeout(timer);
        rl.close();
        const cleaned = answer.trim().toLowerCase();
        if (cleaned === 'n' || cleaned === 'no') {
          console.log(chalk.yellow('\n  ✖ Telemetry denied. Record discarded.\n'));
          resolve(false);
        } else {
          console.log(chalk.green('\n  ✔ Sending telemetry...\n'));
          resolve(true);
        }
      }
    });
  });
}

/**
 * Sends telemetry payload to Barrikade servers.
 * 
 * @param {any} payload 
 * @returns {Promise<boolean>}
 */
export async function sendTelemetry(payload) {
  try {
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 1500);

    const response = await fetch('https://api.barrikade.ai/lens/telemetry', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'User-Agent': `barrikade-lens/${payload.scannerVersion}`
      },
      body: JSON.stringify(payload),
      signal: controller.signal
    });

    clearTimeout(timeoutId);
    return response.ok;
  } catch {
    return false;
  }
}

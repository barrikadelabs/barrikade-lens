import os from 'node:os';
import fs from 'node:fs/promises';
import ora from 'ora';
import chalk from 'chalk';
import { auditConfigs, auditWorkspaceArtifacts, auditDependencies } from './scanners/config-auditor.js';
import { scanPorts } from './scanners/port-scanner.js';
import { scanProcesses } from './scanners/process-scanner.js';
import { scanEnvFiles } from './scanners/env-scanner.js';
import { scanHistory } from './scanners/history-scanner.js';
import { scanConfigsForSecrets } from './scanners/secret-scanner.js';
import { analyzeCapabilities } from './utils/analyzer.js';
import { buildTelemetryPayload, promptTelemetryConsent, sendTelemetry } from './utils/telemetry.js';
import { displayDashboard } from './ui/dashboard.js';
import { exportJson } from './exporters/json-exporter.js';
import { exportHtml } from './exporters/html-exporter.js';

/**
 * Executes the full capability-based security audit workflow.
 * 
 * @param {{
 *   json?: boolean,
 *   report?: string,
 *   html?: string,
 *   telemetry?: boolean,
 *   verbose?: boolean
 * }} options Command-line flags
 * @returns {Promise<number>} Exit code (0 if clean, 1 if critical/high vulnerabilities found)
 */
export async function runAudit(options = {}) {
  const isQuiet = options.json === true;
  const isTelemetryOptOut = options.telemetry === false;

  let spinner;
  if (!isQuiet) {
    spinner = ora({
      text: 'Scanning workspace structures & active processes...',
      color: 'yellow'
    }).start();
  }

  try {
    // 1. Audit config files
    if (spinner) spinner.text = 'Auditing Shadow AI config files...';
    const configs = await auditConfigs(process.cwd());

    // 2. Audit workspace directories & rule files
    if (spinner) spinner.text = 'Sweeping agent workspace structures & rule files...';
    const workspaceArtifacts = await auditWorkspaceArtifacts(process.cwd());

    // 3. Audit project dependencies
    if (spinner) spinner.text = 'Analyzing project dependencies for agent frameworks...';
    const depFrameworks = await auditDependencies(process.cwd());

    // 4. Scan active processes
    if (spinner) spinner.text = 'Detecting active AI and model inference processes...';
    const processes = await scanProcesses();

    // 5. Scan local LLM ports
    if (spinner) spinner.text = 'Checking local model server ports...';
    const ports = await scanPorts(300);

    // 6. Scan environment files for secrets
    if (spinner) spinner.text = 'Scanning workspace .env files for hardcoded secrets...';
    const envSecrets = await scanEnvFiles(process.cwd());

    // 7. Scan shell history for secrets & commands
    if (spinner) spinner.text = 'Auditing shell history logs...';
    const history = await scanHistory();

    // 8. Scan configurations for secrets
    if (spinner) spinner.text = 'Analyzing configuration blocks for plaintext credentials...';
    const configSecrets = await scanConfigsForSecrets(configs);

    if (spinner) {
      spinner.stop();
    }

    // Strip rawContent to ensure absolute user privacy in reports
    for (const config of configs) {
      delete config.rawContent;
    }

    // Merge secrets findings from config files, env files, and shell history
    const mergedSecrets = [
      ...configSecrets,
      ...envSecrets,
      ...history.findings
    ];

    // 9. Run Capability Analysis
    const capabilityResult = analyzeCapabilities({
      configs,
      workspace: workspaceArtifacts,
      dependencies: depFrameworks,
      processes,
      ports,
      secrets: mergedSecrets,
      history
    });

    // 10. Calculate metrics summary
    const configsCount = configs.filter(c => c.exists).length;
    const serversCount = configs.reduce((acc, c) => acc + (c.exists ? c.servers.length : 0), 0);
    const portsScanned = ports.length;
    const portsOpen = ports.filter(p => p.open).length;
    const portsExposed = ports.filter(p => p.open && p.exposed).length;
    const secretsCount = mergedSecrets.length;

    const agentsCount = capabilityResult.agents.length;
    const agentsActive = capabilityResult.agents.filter(a => a.status === 'ACTIVE').length;
    const agentsInstalled = capabilityResult.agents.filter(a => a.status === 'INSTALLED').length;

    const criticalCount = mergedSecrets.filter(s => s.risk === 'CRITICAL').length + portsExposed;
    const highCount = mergedSecrets.filter(s => s.risk === 'HIGH').length;
    const mediumCount = mergedSecrets.filter(s => s.risk === 'MEDIUM').length + configs.filter(c => c.exists && c.malformed).length;

    const summary = {
      configsCount,
      serversCount,
      portsScanned,
      portsOpen,
      portsExposed,
      secretsCount,
      criticalCount,
      highCount,
      mediumCount,
      agentsCount,
      agentsActive,
      agentsInstalled
    };

    let version = '0.2.2';
    try {
      const pkgPath = new URL('../package.json', import.meta.url);
      const pkgContent = await fs.readFile(pkgPath, 'utf8');
      const pkg = JSON.parse(pkgContent);
      version = pkg.version;
    } catch {
      // Fallback
    }

    const aggregatedResults = {
      version,
      timestamp: new Date().toISOString(),
      platform: os.platform(),
      summary,
      capabilities: capabilityResult.capabilities,
      evidence: capabilityResult.evidence,
      agents: capabilityResult.agents,
      configs,
      ports,
      secrets: mergedSecrets
    };

    // 12. Handle output formats
    if (options.json) {
      await exportJson(aggregatedResults);
    } else {
      await displayDashboard(aggregatedResults);
    }

    // Optional exports to files
    if (options.report) {
      await exportJson(aggregatedResults, options.report);
      if (!isQuiet) {
        console.log(chalk.green(`\n✔ JSON report written to: `) + chalk.white(options.report));
      }
    }

    if (options.html) {
      await exportHtml(aggregatedResults, options.html);
      if (!isQuiet) {
        console.log(chalk.green(`✔ HTML CISO-Report written to: `) + chalk.white(options.html));
      }
    }

    // 13. Telemetry handling at the very end (prompt for consent or send silently in quiet mode)
    if (!isTelemetryOptOut && process.env.BARRIKADE_NO_TELEMETRY !== '1' && process.env.BARRIKADE_NO_TELEMETRY !== 'true') {
      const telemetryPayload = await buildTelemetryPayload(summary, capabilityResult.capabilities);
      if (options.json) {
        sendTelemetry(telemetryPayload).catch(() => { });
      } else {
        const consented = await promptTelemetryConsent(telemetryPayload, 15000);
        if (consented) {
          await sendTelemetry(telemetryPayload).catch(() => { });
        }
      }
    }

    // Exit with 1 if there are any CRITICAL or HIGH findings, else 0
    return (criticalCount > 0 || highCount > 0) ? 1 : 0;
  } catch (error) {
    if (spinner) spinner.stop();
    console.error(chalk.red('\n✖ Audit execution failed:'), error.message);
    if (options.verbose) {
      console.error(error);
    }
    return 1;
  }
}

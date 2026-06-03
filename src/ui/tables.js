import Table from 'cli-table3';
import chalk from 'chalk';
import { orange, orangeBold } from './banner.js';

// Reusable table styling configuration
const tableChars = {
  'top': '═', 'top-mid': '╤', 'top-left': '╔', 'top-right': '╗',
  'bottom': '═', 'bottom-mid': '╧', 'bottom-left': '╚', 'bottom-right': '╝',
  'left': '║', 'left-mid': '╟', 'mid': '─', 'mid-mid': '┼',
  'right': '║', 'right-mid': '╢', 'middle': '│'
};

const tableStyle = {
  head: [], 
  border: ['gray']
};

/**
 * Renders the Shadow Agents (MCP Servers) table.
 */
export function renderAgentTable(auditedConfigs) {
  const table = new Table({
    head: [orangeBold('Tool/Client'), orangeBold('Scope'), orangeBold('Server Name'), orangeBold('Type'), orangeBold('Command/URL')],
    chars: tableChars,
    style: tableStyle
  });

  let serversFound = 0;

  for (const config of auditedConfigs) {
    if (!config.exists) continue;
    
    if (config.servers.length === 0) {
      table.push([
        chalk.white(config.tool),
        chalk.dim(config.scope),
        chalk.dim('(none)'),
        chalk.dim('-'),
        chalk.dim('No MCP servers defined')
      ]);
    } else {
      for (const server of config.servers) {
        serversFound++;
        const serverName = server.disabled ? chalk.dim(`${server.name} (disabled)`) : chalk.white(server.name);
        const serverType = chalk.white(server.type.toUpperCase());
        const commandOrUrl = server.type === 'sse' 
          ? chalk.white(server.url || '-') 
          : chalk.white(server.command ? `${server.command} ${(server.args || []).join(' ')}` : '-');

        table.push([
          chalk.white(config.tool),
          chalk.dim(config.scope),
          serverName,
          serverType,
          commandOrUrl
        ]);
      }
    }
  }

  if (serversFound === 0) {
    return chalk.dim('  No active Model Context Protocol (MCP) servers discovered in system directories.\n');
  }

  return table.toString();
}

/**
 * Renders the Local LLM servers and binding exposure table.
 */
export function renderPortTable(portResults) {
  const table = new Table({
    head: [orangeBold('Port'), orangeBold('Service / Interface'), orangeBold('Status'), orangeBold('Network Binding'), orangeBold('Exposure Risk')],
    chars: tableChars,
    style: tableStyle
  });

  const activePorts = portResults.filter(r => r.open);

  if (activePorts.length === 0) {
    return chalk.green('  ✔ No local LLM or AI inference servers detected on common ports.\n');
  }

  for (const res of portResults) {
    if (!res.open) continue;

    const portStr = orange(res.port.toString());
    const serviceStr = chalk.white(res.service);
    const statusStr = chalk.green.bold('RUNNING');
    
    let bindingStr = '';
    let riskStr = '';

    if (res.exposed) {
      bindingStr = chalk.red.bold('0.0.0.0 (All Interfaces)');
      riskStr = chalk.red.bold('⚠ CRITICAL (Exposed to LAN)');
    } else {
      bindingStr = chalk.green('127.0.0.1 (Loopback)');
      riskStr = chalk.green('✔ SAFE (Local Only)');
    }

    table.push([portStr, serviceStr, statusStr, bindingStr, riskStr]);
  }

  return table.toString();
}

/**
 * Renders the detected plaintext secrets and risk configurations table.
 */
export function renderSecretTable(secretFindings) {
  if (secretFindings.length === 0) {
    return chalk.green('  ✔ No plaintext API keys or database credentials found in configuration files.\n');
  }

  const table = new Table({
    head: [orangeBold('Severity'), orangeBold('Source / Location'), orangeBold('Risk Type'), orangeBold('Detected Pattern'), orangeBold('Line')],
    chars: tableChars,
    style: tableStyle
  });

  for (const finding of secretFindings) {
    let severityStr = '';
    if (finding.risk === 'CRITICAL') {
      severityStr = chalk.red.bold('🔴 CRITICAL');
    } else if (finding.risk === 'HIGH') {
      severityStr = orangeBold('🟠 HIGH');
    } else {
      severityStr = chalk.yellow.bold('🟡 MEDIUM');
    }

    const fileBasename = finding.filePath.split('/').pop() || finding.filePath;
    const locationStr = chalk.white(`${finding.tool} (${fileBasename})`);
    const typeStr = chalk.white(finding.type);
    const patternStr = chalk.red(finding.matched);
    const lineStr = finding.line ? chalk.white(finding.line.toString()) : chalk.dim('N/A');

    table.push([severityStr, locationStr, typeStr, patternStr, lineStr]);
  }

  return table.toString();
}

/**
 * Renders the Autonomous AI Capabilities evaluation table.
 * 
 * @param {any} capabilities 
 * @returns {string}
 */
export function renderCapabilityTable(capabilities) {
  const table = new Table({
    head: [orangeBold('Capability Domain'), orangeBold('Risk Status'), orangeBold('Evaluation Detail')],
    chars: tableChars,
    style: tableStyle,
    colWidths: [28, 18, 62] // Wrap description neatly
  });

  const getStatusLabel = (domain, status) => {
    if (domain === 'toolExecution') {
      if (status === 'ACTIVE') return chalk.red.bold('🔴 ACTIVE');
      if (status === 'CAPABLE') return orangeBold('🟠 CAPABLE');
      return chalk.green('🟢 INACTIVE');
    }
    if (domain === 'localInference') {
      if (status === 'ACTIVE') return orangeBold('🟠 ACTIVE');
      if (status === 'CAPABLE') return chalk.green('🟢 CAPABLE');
      return chalk.green('🟢 INACTIVE');
    }
    if (domain === 'workspacePresence') {
      if (status === 'DETECTED') return orangeBold('🟠 DETECTED');
      return chalk.green('🟢 NOT FOUND');
    }
    if (domain === 'credentialExposure') {
      if (status === 'EXPOSED') return chalk.red.bold('🔴 EXPOSED');
      return chalk.green('🟢 SECURE');
    }
    return status;
  };

  table.push([
    chalk.white.bold('Tool & Code Execution'),
    getStatusLabel('toolExecution', capabilities.toolExecution.status),
    chalk.white(capabilities.toolExecution.detail)
  ]);

  table.push([
    chalk.white.bold('Local Model Inference'),
    getStatusLabel('localInference', capabilities.localInference.status),
    chalk.white(capabilities.localInference.detail)
  ]);

  table.push([
    chalk.white.bold('Agent Workspace Presence'),
    getStatusLabel('workspacePresence', capabilities.workspacePresence.status),
    chalk.white(capabilities.workspacePresence.detail)
  ]);

  table.push([
    chalk.white.bold('Credential Security'),
    getStatusLabel('credentialExposure', capabilities.credentialExposure.status),
    chalk.white(capabilities.credentialExposure.detail)
  ]);

  return table.toString();
}

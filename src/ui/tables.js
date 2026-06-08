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
 * Returns the usable terminal width, capped to prevent overshooting.
 * Falls back to 80 columns in non-TTY environments.
 * 
 * @returns {number}
 */
export function termWidth() {
  return Math.max(60, Math.min(process.stdout.columns || 80, 140));
}

/**
 * Mathematically allocates column widths so their sum is exactly the available table width.
 * This guarantees the table fits within w without spilling over.
 * 
 * @param {number} w Total terminal width
 * @param {number} overhead Borders & padding character overhead
 * @param {number[]} mins Minimum widths per column
 * @param {number[]} weights Proportional target weights (0.0 to 1.0)
 * @returns {number[]} Allocated widths
 */
function allocateWidths(w, overhead, mins, weights) {
  const available = Math.max(20, w - overhead);
  const widths = mins.map((min, i) => Math.max(min, Math.floor(available * weights[i])));
  
  let sum = widths.reduce((a, b) => a + b, 0);
  if (sum > available) {
    let diff = sum - available;
    // Step 1: shrink columns down to their min widths if they exceeded them
    for (let i = widths.length - 1; i >= 0 && diff > 0; i--) {
      const shrinkable = widths[i] - mins[i];
      if (shrinkable > 0) {
        const toShrink = Math.min(diff, shrinkable);
        widths[i] -= toShrink;
        diff -= toShrink;
      }
    }
    // Step 2: if still too wide, shrink columns past their mins, preserving at least 4 chars
    if (diff > 0) {
      for (let i = widths.length - 1; i >= 0 && diff > 0; i--) {
        const toShrink = Math.min(diff, widths[i] - 4);
        if (toShrink > 0) {
          widths[i] -= toShrink;
          diff -= toShrink;
        }
      }
    }
  } else if (sum < available) {
    // Distribute any rounding remainder to the last column
    widths[widths.length - 1] += (available - sum);
  }
  return widths;
}

/**
 * Word-wraps a string to fit within maxWidth characters.
 * Breaks on space boundaries.
 * 
 * @param {string} text 
 * @param {number} maxWidth 
 * @returns {string}
 */
function wrap(text, maxWidth) {
  if (!text || text.length <= maxWidth) return text;

  const words  = text.split(' ');
  const lines  = [];
  let current  = '';

  for (const word of words) {
    // Strip ANSI codes for length measurement
    const rawCurrent = current.replace(/\u001B\[[0-9;]*m/g, '');
    const rawWord    = word.replace(/\u001B\[[0-9;]*m/g, '');

    if (rawCurrent.length + rawWord.length + (current ? 1 : 0) <= maxWidth) {
      current = current ? `${current} ${word}` : word;
    } else {
      if (current) lines.push(current);
      current = word;
    }
  }
  if (current) lines.push(current);

  return lines.join('\n');
}

/**
 * Renders the Autonomous AI Capability Analysis table.
 * Column widths adapt to terminal width.
 * 
 * @param {any} capabilities 
 * @returns {string}
 */
export function renderCapabilityTable(capabilities) {
  const w = termWidth();
  const widths = allocateWidths(w, 10, [20, 12, 16], [0.32, 0.20, 0.48]);

  const table = new Table({
    head: [orangeBold('Capability Domain'), orangeBold('Risk Status'), orangeBold('Evaluation Detail')],
    chars: tableChars,
    style: tableStyle,
    colWidths: widths,
    wordWrap: true
  });

  const getStatusLabel = (domain, status) => {
    if (domain === 'toolExecution') {
      if (status === 'ACTIVE')  return chalk.red.bold('🔴 ACTIVE');
      if (status === 'CAPABLE') return orangeBold('🟠 CAPABLE');
      return chalk.green('🟢 INACTIVE');
    }
    if (domain === 'localInference') {
      if (status === 'ACTIVE')  return orangeBold('🟠 ACTIVE');
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

  const rows = [
    {
      domain: 'toolExecution',
      label: 'Autonomous System & Shell Execution',
      cap:   capabilities.toolExecution
    },
    {
      domain: 'localInference',
      label: 'Local Model Inference',
      cap:   capabilities.localInference
    },
    {
      domain: 'workspacePresence',
      label: 'Agent Configuration & Workspace Paths',
      cap:   capabilities.workspacePresence
    },
    {
      domain: 'credentialExposure',
      label: 'Credential Security',
      cap:   capabilities.credentialExposure
    }
  ];

  for (const row of rows) {
    table.push([
      chalk.white.bold(row.label),
      getStatusLabel(row.domain, row.cap.status),
      chalk.white(wrap(row.cap.detail, widths[2] - 4))
    ]);
  }

  return table.toString();
}

/**
 * Renders the Discovered AI Agents Inventory table.
 * Shows actual file paths / PIDs rather than generic labels.
 * 
 * @param {Array<{ name: string, status: 'ACTIVE' | 'INSTALLED', evidence: string[] }>} agents 
 * @returns {string}
 */
export function renderAgentInventoryTable(agents) {
  if (!agents || agents.length === 0) {
    return chalk.dim('  No AI agents or tools discovered on this workstation.\n');
  }

  const w = termWidth();
  const widths = allocateWidths(w, 10, [20, 12, 16], [0.32, 0.20, 0.48]);

  const table = new Table({
    head: [orangeBold('Agent / Tool Name'), orangeBold('Status'), orangeBold('Supporting Evidence')],
    chars: tableChars,
    style: tableStyle,
    colWidths: widths,
    wordWrap: true
  });

  // Sort: ACTIVE first, then alphabetical
  const sorted = [...agents].sort((a, b) => {
    if (a.status === 'ACTIVE' && b.status !== 'ACTIVE') return -1;
    if (a.status !== 'ACTIVE' && b.status === 'ACTIVE') return 1;
    return a.name.localeCompare(b.name);
  });

  for (const agent of sorted) {
    let statusStr = '';
    if (agent.status === 'ACTIVE') {
      statusStr = chalk.red.bold('🔴 ACTIVE');
    } else {
      statusStr = chalk.cyan.bold('🔵 INSTALLED');
    }

    // Show full evidence strings (paths, PIDs, port numbers)
    const evidenceLines = agent.evidence.join('\n');

    table.push([
      chalk.white.bold(agent.name),
      statusStr,
      chalk.white(wrap(evidenceLines, widths[2] - 4))
    ]);
  }

  return table.toString();
}

/**
 * Renders the Shadow Agents (MCP Servers) table.
 * When nothing is found, shows the list of config paths that were checked.
 * 
 * @param {Array<any>} auditedConfigs 
 * @returns {string}
 */
export function renderAgentTable(auditedConfigs) {
  const w = termWidth();
  const widths = allocateWidths(w, 16, [12, 8, 12, 8, 16], [0.20, 0.10, 0.18, 0.10, 0.42]);

  const table = new Table({
    head: [
      orangeBold('Tool/Client'),
      orangeBold('Scope'),
      orangeBold('Server Name'),
      orangeBold('Type'),
      orangeBold('Command/URL')
    ],
    chars: tableChars,
    style: tableStyle,
    colWidths: widths,
    wordWrap: true
  });

  let serversFound = 0;
  const checkedPaths = [];

  for (const config of auditedConfigs) {
    checkedPaths.push(config);

    if (!config.exists) continue;

    if (config.servers.length === 0) {
      table.push([
        chalk.white(config.tool),
        chalk.dim(config.scope),
        chalk.dim('—'),
        chalk.dim('—'),
        chalk.dim('No MCP servers defined')
      ]);
    } else {
      for (const server of config.servers) {
        serversFound++;
        const serverName   = server.disabled
          ? chalk.dim(`${server.name} (disabled)`)
          : chalk.white(server.name);
        const serverType   = chalk.white(server.type.toUpperCase());
        const commandOrUrl = server.type === 'sse'
          ? chalk.white(server.url || '—')
          : chalk.white(server.command
            ? wrap(`${server.command} ${(server.args || []).join(' ')}`, widths[4] - 4)
            : '—');

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

  const checkedCount  = checkedPaths.length;
  const existingCount = checkedPaths.filter(c => c.exists).length;

  if (serversFound === 0) {
    let output = '';
    output += chalk.dim(`  Checked ${checkedCount} known agent config location${checkedCount !== 1 ? 's' : ''} — ${existingCount} present on disk:\n\n`);
    for (const c of checkedPaths.filter(p => p.exists)) {
      output += `  ${chalk.green('✔')} ${chalk.dim(c.tool.padEnd(32))} ${chalk.white(c.filePath)}\n`;
    }
    output += `\n  ${chalk.green('✔ RESULT:')} ${chalk.white('0 active local MCP servers discovered.')}\n`;
    output += chalk.dim('\n  ⚠  If an unvetted server is registered, malicious prompts can hijack your shell\n');
    output += chalk.dim('     or exploit command-injection via poisoned tool arguments (MCP "Spawn Axis").\n');
    return output;
  }

  // Append tool-poisoning warning below the table when servers exist
  const warning = chalk.dim(
    '\n  ⚠  Verify each registered server above. Unvetted MCP servers can inject\n' +
    '     arbitrary shell commands into your agent\'s tool call pipeline.\n'
  );

  return table.toString() + warning;
}

/**
 * Renders the Local LLM servers and binding exposure table.
 * Adds a sub-line explaining the blast radius of 0.0.0.0 bindings.
 * 
 * @param {Array<any>} portResults 
 * @returns {string}
 */
export function renderPortTable(portResults) {
  const w = termWidth();
  const widths = allocateWidths(w, 16, [6, 12, 10, 18, 14], [0.08, 0.20, 0.12, 0.32, 0.28]);

  const table = new Table({
    head: [
      orangeBold('Port'),
      orangeBold('Service'),
      orangeBold('Status'),
      orangeBold('Network Reachability & Binding'),
      orangeBold('Blast Radius')
    ],
    chars: tableChars,
    style: tableStyle,
    colWidths: widths,
    wordWrap: true
  });

  const activePorts = portResults.filter(r => r.open);

  if (activePorts.length === 0) {
    return chalk.green('  ✔ No local LLM or AI inference servers detected on common ports.\n');
  }

  let hasExposed = false;

  for (const res of portResults) {
    if (!res.open) continue;

    const portStr    = orange(res.port.toString());
    const serviceStr = chalk.white(res.service);
    const statusStr  = chalk.green.bold('RUNNING');

    let bindingStr = '';
    let riskStr    = '';

    if (res.exposed) {
      hasExposed = true;
      bindingStr = chalk.red.bold('0.0.0.0  (All Interfaces)');
      riskStr    = chalk.red.bold('⚠ CRITICAL — LAN Exposed');
    } else {
      bindingStr = chalk.green('127.0.0.1  (Loopback Only)');
      riskStr    = chalk.green('✔ SAFE — Local Only');
    }

    table.push([portStr, serviceStr, statusStr, bindingStr, riskStr]);
  }

  let output = table.toString();

  if (hasExposed) {
    output += '\n' + chalk.red.bold('  ⚠ CRITICAL:') + chalk.white(
      ' Any device on your local Wi-Fi or corporate network can send\n' +
      '  queries and trigger tool-call pipelines on your host system via the\n' +
      '  exposed port. Bind immediately to 127.0.0.1 to close this attack surface.\n'
    );
  }

  return output;
}

/**
 * Renders the detected plaintext secrets and risk configurations table.
 * Secrets from active agents are elevated to CRITICAL.
 * 
 * @param {Array<any>} secretFindings 
 * @returns {string}
 */
export function renderSecretTable(secretFindings) {
  if (secretFindings.length === 0) {
    return chalk.green('  ✔ No plaintext API keys or database credentials found in configuration files.\n');
  }

  const w = termWidth();
  const widths = allocateWidths(w, 16, [12, 16, 16, 12, 5], [0.16, 0.28, 0.24, 0.24, 0.08]);

  const table = new Table({
    head: [
      orangeBold('Severity'),
      orangeBold('Source / Location'),
      orangeBold('AIBOM Exposure Type'),
      orangeBold('Detected Pattern'),
      orangeBold('Line')
    ],
    chars: tableChars,
    style: tableStyle,
    colWidths: widths,
    wordWrap: true
  });

  for (const finding of secretFindings) {
    let severityStr = '';
    // Hardcoded secrets in active agent configs are treated as CRITICAL
    if (finding.risk === 'CRITICAL' || finding.risk === 'HIGH') {
      severityStr = chalk.red.bold('🔴 CRITICAL');
    } else {
      severityStr = chalk.yellow.bold('🟡 MEDIUM');
    }

    const fileBasename = finding.filePath.split('/').pop() || finding.filePath;
    const locationStr  = chalk.white(wrap(`${finding.tool} (${fileBasename})`, widths[1] - 4));
    const typeStr      = chalk.white(finding.type);
    const patternStr   = chalk.red(finding.matched);
    const lineStr      = finding.line ? chalk.white(finding.line.toString()) : chalk.dim('N/A');

    table.push([severityStr, locationStr, typeStr, patternStr, lineStr]);
  }

  const warning = chalk.dim(
    '\n  ⚠  Hardcoded secrets used by active autonomous agents create an immediate\n' +
    '     backdoor. Revoke and replace with environment variable indirection now.\n'
  );

  return table.toString() + warning;
}

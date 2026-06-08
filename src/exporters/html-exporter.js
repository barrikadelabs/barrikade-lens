import fs from 'node:fs/promises';
import { calculateRiskScore } from '../ui/summary.js';

/**
 * Exports scan results as a self-contained, beautifully-styled HTML report.
 * 
 * @param {any} results Aggregated scan results
 * @param {string} outputPath File path to write the HTML report to
 */
export async function exportHtml(results, outputPath) {
  const summary = results.summary;
  const score = calculateRiskScore(summary.criticalCount, summary.highCount, summary.mediumCount);
  const capabilities = results.capabilities;

  let scoreClass = 'score-high';
  let ratingStr = 'SECURE / LOW RISK';
  if (score < 50) {
    scoreClass = 'score-low';
    ratingStr = 'CRITICAL SECURITY EXPOSURE';
  } else if (score < 80) {
    scoreClass = 'score-medium';
    ratingStr = 'MODERATE RISK';
  }

  const activePorts = results.ports.filter(p => p.open);
  const configsFound = results.configs.filter(c => c.exists);

  const getHtmlBadgeClass = (domain, status) => {
    if (domain === 'toolExecution') {
      if (status === 'ACTIVE') return 'status-critical';
      if (status === 'CAPABLE') return 'status-high';
      return 'status-safe';
    }
    if (domain === 'localInference') {
      if (status === 'ACTIVE') return 'status-high';
      if (status === 'CAPABLE') return 'status-safe';
      return 'status-safe';
    }
    if (domain === 'workspacePresence') {
      if (status === 'DETECTED') return 'status-high';
      return 'status-safe';
    }
    if (domain === 'credentialExposure') {
      if (status === 'EXPOSED') return 'status-critical';
      return 'status-safe';
    }
    return '';
  };

  const getHtmlBadgeLabel = (domain, status) => {
    if (domain === 'workspacePresence') {
      return status === 'DETECTED' ? 'DETECTED' : 'NOT FOUND';
    }
    if (domain === 'credentialExposure') {
      return status === 'EXPOSED' ? 'EXPOSED' : 'SECURE';
    }
    return status;
  };

  const html = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Barrikade Lens - Shadow AI Security Audit Report</title>
  <style>
    :root {
      --bg: #0A0A0B;
      --card-bg: #121214;
      --text: #F4F4F5;
      --text-muted: #A1A1AA;
      --orange: #FF6600;
      --orange-dim: #E05A00;
      --border: #27272A;
      --green: #10B981;
      --red: #EF4444;
      --yellow: #F59E0B;
    }

    body {
      background-color: var(--bg);
      color: var(--text);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
      margin: 0;
      padding: 0;
      line-height: 1.5;
    }

    .container {
      max-width: 1000px;
      margin: 0 auto;
      padding: 40px 20px;
    }

    header {
      border-bottom: 1px solid var(--border);
      padding-bottom: 20px;
      margin-bottom: 40px;
      display: flex;
      justify-content: space-between;
      align-items: center;
    }

    .logo-container h1 {
      margin: 0;
      font-size: 24px;
      letter-spacing: 2px;
      color: var(--text);
    }

    .logo-container h1 span {
      color: var(--orange);
    }

    .logo-container p {
      margin: 4px 0 0 0;
      font-size: 14px;
      color: var(--text-muted);
    }

    .badge {
      background-color: var(--border);
      color: var(--text);
      padding: 6px 12px;
      border-radius: 4px;
      font-size: 12px;
      font-weight: 600;
      border: 1px solid var(--border);
    }

    .grid {
      display: grid;
      grid-template-columns: 2fr 1fr;
      gap: 30px;
      margin-bottom: 30px;
    }

    .capability-grid {
      display: grid;
      grid-template-columns: repeat(2, 1fr);
      gap: 20px;
      margin-bottom: 30px;
    }

    @media (max-width: 768px) {
      .grid, .capability-grid {
        grid-template-columns: 1fr;
      }
    }

    .card {
      background-color: var(--card-bg);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 24px;
      box-shadow: 0 4px 20px rgba(0,0,0,0.4);
    }

    .capability-card {
      border: 1px solid var(--border);
      border-radius: 8px;
      background-color: var(--card-bg);
      padding: 20px;
      display: flex;
      flex-direction: column;
      justify-content: space-between;
    }

    .capability-header {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 12px;
    }

    .capability-title {
      font-weight: 700;
      font-size: 15px;
      color: var(--text);
    }

    .capability-desc {
      font-size: 13px;
      color: var(--text-muted);
      margin: 0;
    }

    .score-widget {
      text-align: center;
      display: flex;
      flex-direction: column;
      justify-content: center;
      align-items: center;
    }

    .score-circle {
      width: 140px;
      height: 140px;
      border-radius: 50%;
      border: 8px solid var(--border);
      display: flex;
      justify-content: center;
      align-items: center;
      font-size: 42px;
      font-weight: 800;
      margin-bottom: 16px;
      position: relative;
    }

    .score-high {
      border-color: var(--green);
      color: var(--green);
    }

    .score-medium {
      border-color: var(--yellow);
      color: var(--yellow);
    }

    .score-low {
      border-color: var(--red);
      color: var(--red);
    }

    .rating {
      font-weight: 700;
      font-size: 14px;
      letter-spacing: 1px;
      text-transform: uppercase;
    }

    h2 {
      font-size: 18px;
      margin-top: 0;
      margin-bottom: 20px;
      border-left: 3px solid var(--orange);
      padding-left: 10px;
    }

    .metric-row {
      display: flex;
      justify-content: space-between;
      padding: 10px 0;
      border-bottom: 1px solid var(--border);
    }

    .metric-row:last-child {
      border-bottom: none;
    }

    .metric-label {
      color: var(--text-muted);
    }

    .metric-val {
      font-weight: 600;
    }

    table {
      width: 100%;
      border-collapse: collapse;
      margin-top: 10px;
      margin-bottom: 30px;
      font-size: 14px;
    }

    th {
      text-align: left;
      padding: 12px;
      border-bottom: 2px solid var(--border);
      color: var(--orange);
      font-weight: 600;
    }

    td {
      padding: 12px;
      border-bottom: 1px solid var(--border);
    }

    tr:last-child td {
      border-bottom: none;
    }

    .status-badge {
      display: inline-block;
      padding: 2px 8px;
      border-radius: 4px;
      font-size: 11px;
      font-weight: 700;
      text-transform: uppercase;
      white-space: nowrap;
    }

    .status-critical {
      background-color: rgba(239, 68, 68, 0.15);
      color: var(--red);
      border: 1px solid rgba(239, 68, 68, 0.3);
    }

    .status-high {
      background-color: rgba(245, 158, 11, 0.15);
      color: var(--orange);
      border: 1px solid rgba(245, 158, 11, 0.3);
    }

    .status-medium {
      background-color: rgba(245, 158, 11, 0.1);
      color: var(--yellow);
      border: 1px solid rgba(245, 158, 11, 0.2);
    }

    .status-safe {
      background-color: rgba(16, 185, 129, 0.15);
      color: var(--green);
      border: 1px solid rgba(16, 185, 129, 0.3);
    }

    .evidence-list {
      margin: 0;
      padding-left: 20px;
      font-size: 14px;
    }

    .evidence-list li {
      margin-bottom: 8px;
      color: var(--text);
    }

    .recommendations {
      margin-top: 30px;
      background-color: #1A1A1E;
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 24px;
    }

    .recommendations ol {
      padding-left: 20px;
      margin: 0;
    }

    .recommendations li {
      margin-bottom: 12px;
    }

    .recommendations li:last-child {
      margin-bottom: 0;
    }

    .cta-container {
      margin-top: 40px;
      background: linear-gradient(135deg, #1C1510 0%, #121214 100%);
      border: 1px solid var(--orange);
      border-radius: 8px;
      padding: 30px;
      text-align: center;
    }

    .cta-container h3 {
      margin-top: 0;
      color: var(--orange);
      font-size: 20px;
      letter-spacing: 1px;
    }

    .cta-btn {
      display: inline-block;
      background-color: var(--orange);
      color: white;
      text-decoration: none;
      padding: 12px 28px;
      border-radius: 4px;
      font-weight: 700;
      font-size: 15px;
      margin-top: 15px;
      transition: background-color 0.2s;
    }

    .cta-btn:hover {
      background-color: var(--orange-dim);
    }

    .empty-state {
      color: var(--text-muted);
      text-align: center;
      padding: 20px 0;
    }

    .remediation-tip {
      font-size: 12px;
      color: var(--text-muted);
      margin-top: 4px;
    }
  </style>
</head>
<body>
  <div class="container">
    <header>
      <div class="logo-container">
        <h1>BARRIKADE <span>LENS</span></h1>
        <p>Shadow AI Workstation Vulnerability Report</p>
      </div>
      <div class="badge">100% LOCALLY AUDITED</div>
    </header>

    <div class="grid">
      <div class="card">
        <h2>Executive Summary</h2>
        <div class="metric-row">
          <span class="metric-label">Audit Timestamp</span>
          <span class="metric-val">${new Date().toLocaleString()}</span>
        </div>
        <div class="metric-row">
          <span class="metric-label">Operating System</span>
          <span class="metric-val">${results.platform.toUpperCase()}</span>
        </div>
        <div class="metric-row">
          <span class="metric-label">Discovered AI Agents</span>
          <span class="metric-val">${summary.agentsCount || 0} (${summary.agentsActive || 0} active, ${summary.agentsInstalled || 0} installed)</span>
        </div>
        <div class="metric-row">
          <span class="metric-label">Config Files Found</span>
          <span class="metric-val">${summary.configsCount}</span>
        </div>
        <div class="metric-row">
          <span class="metric-label">Active Agent Connections (MCP)</span>
          <span class="metric-val">${summary.serversCount}</span>
        </div>
        <div class="metric-row">
          <span class="metric-label">Exposed secrets or configurations</span>
          <span class="metric-val" style="color: ${summary.secretsCount > 0 ? 'var(--orange)' : 'var(--green)'}">${summary.secretsCount}</span>
        </div>
      </div>
      
      <div class="card score-widget">
        <div class="score-circle ${scoreClass}">${score}</div>
        <div class="rating ${scoreClass}">${ratingStr}</div>
      </div>
    </div>

    <!-- Capability Grid -->
    <h2 style="margin-bottom: 20px;">Autonomous AI Capability Evaluation</h2>
    <div class="capability-grid">
      <div class="capability-card">
        <div class="capability-header">
          <span class="capability-title">Tool & Code Execution</span>
          <span class="status-badge ${getHtmlBadgeClass('toolExecution', capabilities.toolExecution.status)}">
            ${getHtmlBadgeLabel('toolExecution', capabilities.toolExecution.status)}
          </span>
        </div>
        <p class="capability-desc">${capabilities.toolExecution.detail}</p>
      </div>

      <div class="capability-card">
        <div class="capability-header">
          <span class="capability-title">Local Model Inference</span>
          <span class="status-badge ${getHtmlBadgeClass('localInference', capabilities.localInference.status)}">
            ${getHtmlBadgeLabel('localInference', capabilities.localInference.status)}
          </span>
        </div>
        <p class="capability-desc">${capabilities.localInference.detail}</p>
      </div>

      <div class="capability-card">
        <div class="capability-header">
          <span class="capability-title">Agent Workspace Presence</span>
          <span class="status-badge ${getHtmlBadgeClass('workspacePresence', capabilities.workspacePresence.status)}">
            ${getHtmlBadgeLabel('workspacePresence', capabilities.workspacePresence.status)}
          </span>
        </div>
        <p class="capability-desc">${capabilities.workspacePresence.detail}</p>
      </div>

      <div class="capability-card">
        <div class="capability-header">
          <span class="capability-title">Credential Security</span>
          <span class="status-badge ${getHtmlBadgeClass('credentialExposure', capabilities.credentialExposure.status)}">
            ${getHtmlBadgeLabel('credentialExposure', capabilities.credentialExposure.status)}
          </span>
        </div>
        <p class="capability-desc">${capabilities.credentialExposure.detail}</p>
      </div>
    </div>

    <!-- Discovered Agents Card -->
    <div class="card" style="margin-bottom: 30px;">
      <h2>Discovered Shadow AI Agents Inventory</h2>
      ${!results.agents || results.agents.length === 0 ? `
        <div class="empty-state">No AI agents or tools discovered on this workstation.</div>
      ` : `
        <table>
          <thead>
            <tr>
              <th>Agent / Tool Name</th>
              <th>Status</th>
              <th>Supporting Evidence</th>
            </tr>
          </thead>
          <tbody>
            ${[...results.agents].sort((a, b) => {
    if (a.status === 'ACTIVE' && b.status !== 'ACTIVE') return -1;
    if (a.status !== 'ACTIVE' && b.status === 'ACTIVE') return 1;
    return a.name.localeCompare(b.name);
  }).map(agent => {
    const statusClass = agent.status === 'ACTIVE' ? 'status-critical' : 'status-safe';
    const cleanEvidence = agent.evidence.map(e => {
      const idx = e.indexOf(':');
      return idx !== -1 ? e.substring(0, idx).trim() : e;
    });
    const uniqueEvidence = Array.from(new Set(cleanEvidence)).join(', ');
    return `
                <tr>
                  <td><strong>${agent.name}</strong></td>
                  <td><span class="status-badge ${statusClass}">${agent.status}</span></td>
                  <td>${uniqueEvidence}</td>
                </tr>
              `;
  }).join('')}
          </tbody>
        </table>
      `}
    </div>

    <!-- Audit Evidence collected -->
    <div class="card" style="margin-bottom: 30px;">
      <h2>Collected Audit Evidence</h2>
      ${results.evidence.length === 0 ? `
        <div class="empty-state">No agent infrastructure or evidence detected on this workstation.</div>
      ` : `
        <ul class="evidence-list">
          ${results.evidence.map(e => `<li>${e}</li>`).join('')}
        </ul>
      `}
    </div>

    <!-- Active Configurations -->
    <div class="card" style="margin-bottom: 30px;">
      <h2>Active Agent & MCP Configurations</h2>
      ${configsFound.length === 0 ? `
        <div class="empty-state">No agent config files discovered.</div>
      ` : `
        <table>
          <thead>
            <tr>
              <th>Tool / Client</th>
              <th>Scope</th>
              <th>Server Name</th>
              <th>Type</th>
              <th>Command / Endpoint</th>
            </tr>
          </thead>
          <tbody>
            ${configsFound.map(c => {
    if (c.servers.length === 0) {
      return `<tr>
                  <td><strong>${c.tool}</strong></td>
                  <td>${c.scope}</td>
                  <td colspan="3" style="color: var(--text-muted); font-style: italic;">No servers configured</td>
                </tr>`;
    }
    return c.servers.map(s => `
                <tr>
                  <td><strong>${c.tool}</strong></td>
                  <td>${c.scope}</td>
                  <td>${s.disabled ? `<span style="text-decoration: line-through; opacity: 0.6;">${s.name}</span>` : s.name}</td>
                  <td>${s.type.toUpperCase()}</td>
                  <td><code>${s.type === 'sse' ? (s.url || '-') : `${s.command || '-'} ${(s.args || []).join(' ')}`}</code></td>
                </tr>
              `).join('');
  }).join('')}
          </tbody>
        </table>
      `}
    </div>

    <!-- Port Sweep -->
    <div class="card" style="margin-bottom: 30px;">
      <h2>Local Inference Server Status</h2>
      ${activePorts.length === 0 ? `
        <div class="empty-state" style="color: var(--green);">✔ No local model inference servers active.</div>
      ` : `
        <table>
          <thead>
            <tr>
              <th>Port</th>
              <th>Service</th>
              <th>Status</th>
              <th>Binding</th>
              <th>Risk Level</th>
            </tr>
          </thead>
          <tbody>
            ${activePorts.map(p => `
              <tr>
                <td><strong>${p.port}</strong></td>
                <td>${p.service}</td>
                <td><span class="status-badge status-safe">Running</span></td>
                <td><code>${p.binding}</code></td>
                <td>
                  ${p.exposed ? `
                    <span class="status-badge status-critical">Critical (Exposed to LAN)</span>
                  ` : `
                    <span class="status-badge status-safe">Safe (Local only)</span>
                  `}
                </td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      `}
    </div>

    <!-- Security Findings -->
    <div class="card" style="margin-bottom: 30px;">
      <h2>Credential & Risk Findings</h2>
      ${results.secrets.length === 0 ? `
        <div class="empty-state" style="color: var(--green);">✔ No plaintext secrets or insecure configuration flags detected.</div>
      ` : `
        <table>
          <thead>
            <tr>
              <th>Severity</th>
              <th>Source / Location</th>
              <th>Risk Type</th>
              <th>Pattern / Detail</th>
              <th>Line</th>
            </tr>
          </thead>
          <tbody>
            ${results.secrets.map(s => {
    let sevClass = 'status-medium';
    if (s.risk === 'CRITICAL') sevClass = 'status-critical';
    else if (s.risk === 'HIGH') sevClass = 'status-high';

    return `
                <tr>
                  <td><span class="status-badge ${sevClass}">${s.risk}</span></td>
                  <td><strong>${s.tool}</strong><br><span style="font-size: 11px; color: var(--text-muted);">${s.filePath.split('/').pop()}</span></td>
                  <td>${s.type}</td>
                  <td>
                    <code style="color: var(--red);">${s.matched}</code>
                    <div class="remediation-tip">${s.remediation}</div>
                  </td>
                  <td>${s.line || 'N/A'}</td>
                </tr>
              `;
  }).join('')}
          </tbody>
        </table>
      `}
    </div>

    <!-- Action items -->
    <div class="recommendations">
      <h2 style="border-left: none; padding-left: 0; color: var(--orange); margin-bottom: 15px;">Immediate Remediation Checklist</h2>
      <ol>
        ${summary.portsExposed > 0 ? `
          <li><strong>Secure Local AI Binding:</strong> Edit your Ollama/LM Studio configurations to bind solely to <code>127.0.0.1</code>. This blocks lateral access from other devices on the LAN.</li>
        ` : ''}
        ${summary.secretsCount > 0 ? `
          <li><strong>Vault Plaintext Secrets:</strong> Remove hardcoded credentials from <code>mcp.json</code> or other tool settings. Switch to environment variable interpolation (e.g. <code>\${env:OPENAI_API_KEY}</code>) which resolves secrets dynamically at runtime.</li>
        ` : ''}
        <li><strong>Enforce Command Execution Checks:</strong> Verify that Brave Mode is disabled in IDE MCP settings, and review auto-approval configuration lists.</li>
        <li><strong>Establish Central Lifecycle Governance:</strong> Ensure team members follow structured governance practices for local agent toolchains to avoid lateral network exposure.</li>
      </ol>
    </div>

    <!-- CTA Box -->
    <div class="cta-container">
      <h3>PROTECT YOUR FLEET WITH BARRIKADE</h3>
      <p style="color: var(--text-muted); max-width: 700px; margin: 10px auto;">
        Local sweeps secure individual machines. But enterprise environments need continuous visibility. 
        Barrikade Enterprise offers automated fleet monitoring, centralized credentials brokering, 
        runtime guardrails, and compliance audits for developers' local AI assistant ecosystems.
      </p>
      <a href="https://barrikade.ai" class="cta-btn" target="_blank">Book a Demo at Barrikade.ai</a>
    </div>
  </div>
</body>
</html>`;

  await fs.writeFile(outputPath, html, 'utf8');
}

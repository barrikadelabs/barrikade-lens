import { SECRET_PATTERNS, redactSecret } from '../utils/patterns.js';

/**
 * Scans audited configurations for plaintext secrets, credentials, and risky configurations.
 * 
 * @param {Array<{
 *   tool: string,
 *   filePath: string,
 *   scope: 'global' | 'project',
 *   exists: boolean,
 *   malformed: boolean,
 *   rawContent: string,
 *   servers: any[]
 * }>} auditedConfigs List of audited configuration files from config-auditor
 * @returns {Array<{
 *   filePath: string,
 *   tool: string,
 *   type: string,
 *   matched: string,
 *   line: number | null,
 *   risk: 'CRITICAL' | 'HIGH' | 'MEDIUM',
 *   remediation: string
 * }>}
 */
export function scanConfigsForSecrets(auditedConfigs) {
  /** @type {Array<{ filePath: string, tool: string, type: string, matched: string, line: number | null, risk: 'CRITICAL' | 'HIGH' | 'MEDIUM', remediation: string }>} */
  const findings = [];

  for (const config of auditedConfigs) {
    if (!config.exists || config.malformed) continue;

    const lines = config.rawContent.split('\n');

    // 1. Scan for regex secrets line by line to get line numbers
    lines.forEach((lineText, lineIdx) => {
      const lineNum = lineIdx + 1;
      
      for (const pattern of SECRET_PATTERNS) {
        pattern.regex.lastIndex = 0; // Reset state
        let match;
        while ((match = pattern.regex.exec(lineText)) !== null) {
          const rawMatch = match[0];
          findings.push({
            filePath: config.filePath,
            tool: config.tool,
            type: pattern.name,
            matched: redactSecret(rawMatch),
            line: lineNum,
            risk: pattern.risk,
            remediation: pattern.remediation
          });
        }
      }
    });

    // 2. Scan for tool-specific configuration risks
    if (config.servers && config.servers.length > 0) {
      for (const server of config.servers) {
        // Cline Auto-Approve Risk
        if (server.autoApprove && server.autoApprove.length > 0) {
          findings.push({
            filePath: config.filePath,
            tool: config.tool,
            type: 'Cline Auto-Approve Risk',
            matched: `Server '${server.name}' has auto-approve enabled for tools: [${server.autoApprove.join(', ')}]`,
            line: null,
            risk: 'HIGH',
            remediation: `Remove auto-approve permissions from ${config.tool} settings for security.`
          });
        }

        // JetBrains Brave Mode
        if (server.type === 'jetbrains' && server.braveMode) {
          findings.push({
            filePath: config.filePath,
            tool: config.tool,
            type: 'JetBrains Brave Mode Enabled',
            matched: `JetBrains configuration has 'Brave Mode' active (runs commands without confirmation)`,
            line: null,
            risk: 'CRITICAL',
            remediation: 'Disable Brave Mode in JetBrains Settings under Tools > MCP Server.'
          });
        }
      }
    }
  }

  return findings;
}

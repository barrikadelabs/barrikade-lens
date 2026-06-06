import fs from 'node:fs/promises';
import { getShellHistories } from '../utils/paths.js';
import { scanStringForSecrets } from '../utils/patterns.js';

// Agent invocation signatures in history
const AGENT_COMMANDS = [
  { term: 'aider', label: 'Aider CLI Agent' },
  { term: 'claude mcp', label: 'Claude Code MCP configuration' },
  { term: 'claude code', label: 'Claude Code execution' },
  { term: 'ollama run', label: 'Ollama model execution' },
  { term: 'ollama serve', label: 'Ollama daemon start' },
  { term: 'lmstudio', label: 'LM Studio' },
  { term: 'crewai', label: 'CrewAI agent run' },
  { term: 'langchain', label: 'LangChain script' },
  { term: 'antigravity', label: 'Antigravity CLI invocation' },
  { term: 'opencode', label: 'OpenCode CLI invocation' },
  { term: 'openclaw', label: 'OpenClaw CLI invocation' },
  { term: 'codex', label: 'Codex CLI invocation' },
  { term: 'goose', label: 'Goose CLI invocation' },
  { term: 'kiro', label: 'Kiro CLI/IDE invocation' },
  { term: 'amazonq', label: 'Amazon Q Developer CLI' },
  { term: 'npx barrikade-audit', label: 'Barrikade security scan' }
];

/**
 * Reads the last N lines of a file.
 * 
 * @param {string} filePath 
 * @param {number} maxLines 
 * @returns {Promise<string[]>}
 */
async function readLastLines(filePath, maxLines = 500) {
  try {
    const content = await fs.readFile(filePath, 'utf8');
    const lines = content.split('\n');
    if (lines.length <= maxLines) return lines;
    return lines.slice(lines.length - maxLines);
  } catch {
    return [];
  }
}

/**
 * Scans shell history files for hardcoded API keys and agent usage history.
 * 
 * @returns {Promise<{
 *   findings: Array<{
 *     filePath: string,
 *     tool: string,
 *     type: string,
 *     matched: string,
 *     line: number,
 *     risk: 'CRITICAL' | 'HIGH' | 'MEDIUM',
 *     remediation: string
 *   }>,
 *   agentInvocations: string[]
 * }>}
 */
export async function scanHistory() {
  const histories = getShellHistories();
  const findings = [];
  const agentInvocations = new Set();

  for (const historyPath of histories) {
    const lines = await readLastLines(historyPath, 500);
    const fileName = historyPath.split('/').pop();

    lines.forEach((lineText, idx) => {
      if (!lineText) return;
      
      // Clean Zsh metadata if present (format: : 1717435133:0;cmd)
      let cleanLine = lineText;
      if (lineText.startsWith(': ')) {
        const semiIdx = lineText.indexOf(';');
        if (semiIdx !== -1) {
          cleanLine = lineText.substring(semiIdx + 1);
        }
      }

      // 1. Search for plaintext keys in commands
      const secretsFound = scanStringForSecrets(cleanLine);
      for (const s of secretsFound) {
        findings.push({
          filePath: historyPath,
          tool: `Shell History (${fileName})`,
          type: `${s.type} in Command Line`,
          matched: s.matched,
          line: idx + 1, // line within our sliced slice
          risk: s.risk,
          remediation: `Revoke the key immediately. Clean history by editing ${fileName} or using history -c.`
        });
      }

      // 2. Search for agent commands
      for (const cmd of AGENT_COMMANDS) {
        if (cleanLine.toLowerCase().includes(cmd.term)) {
          agentInvocations.add(cmd.label);
        }
      }
    });
  }

  return {
    findings,
    agentInvocations: Array.from(agentInvocations)
  };
}

import fs from 'node:fs/promises';
import path from 'node:path';
import { scanStringForSecrets } from '../utils/patterns.js';

/**
 * Scans workspace .env files for hardcoded API keys and secrets.
 *
 * @param {string} [cwd=process.cwd()] Working directory to scan
 * @returns {Promise<Array<{
 *   filePath: string,
 *   tool: string,
 *   type: string,
 *   matched: string,
 *   line: number,
 *   risk: 'CRITICAL' | 'HIGH' | 'MEDIUM',
 *   remediation: string
 * }>>}
 */
export async function scanEnvFiles(cwd = process.cwd()) {
  const findings = [];

  try {
    const files = await fs.readdir(cwd);
    const envFiles = files.filter(
      (f) => f === '.env' || (f.startsWith('.env.') && !f.endsWith('.example')),
    );

    for (const fileName of envFiles) {
      const filePath = path.join(cwd, fileName);
      try {
        const stats = await fs.stat(filePath);
        if (!stats.isFile()) continue;

        const content = await fs.readFile(filePath, 'utf8');
        const lines = content.split('\n');

        lines.forEach((lineText, lineIdx) => {
          const lineNum = lineIdx + 1;
          const trimmed = lineText.trim();

          // Skip comments and empty lines
          if (trimmed.startsWith('#') || trimmed === '') return;

          // Parse key=value
          const eqIdx = trimmed.indexOf('=');
          if (eqIdx === -1) return;

          const key = trimmed.substring(0, eqIdx).trim();
          const val = trimmed
            .substring(eqIdx + 1)
            .trim()
            .replace(/^['"]|['"]$/g, ''); // strip quotes

          // Search value for credentials
          const secretsFound = scanStringForSecrets(val);
          for (const s of secretsFound) {
            findings.push({
              filePath,
              tool: `.env file (${fileName})`,
              type: `${s.type} in ${key}`,
              matched: s.matched,
              line: lineNum,
              risk: s.risk,
              remediation: `Remove hardcoded credentials from ${fileName}. Read keys from system env variables instead.`,
            });
          }
        });
      } catch {
        // Skip file if can't read
      }
    }
  } catch {
    // Current directory can't be read or has no files
  }

  return findings;
}

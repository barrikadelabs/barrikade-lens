import boxen from 'boxen';
import chalk from 'chalk';
import { orange, orangeBold } from './banner.js';
import { getScanPaths, getAgentStateDirs, getModelDirs } from '../utils/paths.js';

/**
 * Computes a risk score from 0 to 100 based on finding counts.
 * Exposing plaintext admin credentials or network ports instantly drops the score to Critical/High risk.
 * 
 * @param {number} critical 
 * @param {number} high 
 * @param {number} medium 
 * @returns {number}
 */
export function calculateRiskScore(critical, high, medium) {
  let score = 100;
  score -= (critical * 30);
  score -= (high * 20);
  score -= (medium * 8);
  
  if (critical > 0) {
    score = Math.min(score, 35); // Critical Range (< 40)
  } else if (high > 0) {
    score = Math.min(score, 55); // High Range (< 60)
  }
  
  return Math.max(0, score);
}

/**
 * Generates a visual progress bar for the score.
 * 
 * @param {number} score 
 * @returns {string}
 */
export function getProgressBar(score) {
  const totalBlocks = 20;
  const filledBlocks = Math.round((score / 100) * totalBlocks);
  const emptyBlocks = totalBlocks - filledBlocks;

  const filledStr = '█'.repeat(filledBlocks);
  const emptyStr = '░'.repeat(emptyBlocks);

  const bar = `[${filledStr}${emptyStr}] ${score}/100`;

  if (score >= 80) return chalk.green.bold(bar);
  if (score >= 50) return orangeBold(bar);
  return chalk.red.bold(bar);
}

/**
 * Maps risk score to a security grade and description label.
 * 
 * @param {number} score 
 * @returns {{ grade: string, label: string, color: Function }}
 */
export function getSecurityGrade(score) {
  if (score >= 90) return { grade: 'A', label: 'SECURE / LOW RISK', color: chalk.green.bold };
  if (score >= 75) return { grade: 'B', label: 'MODERATE RISK', color: orangeBold };
  if (score >= 60) return { grade: 'C', label: 'MODERATE RISK', color: orangeBold };
  if (score >= 40) return { grade: 'D', label: 'HIGH RISK', color: chalk.red.bold };
  return { grade: 'F', label: 'CRITICAL EXPOSURE RISK', color: chalk.red.bold };
}

/**
 * Renders the final executive summary card and Call-To-Action (CTA).
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
 *   mediumCount: number,
 *   agentsCount: number,
 *   agentsActive: number,
 *   agentsInstalled: number
 * }} summary 
 * @param {Array<any>} secrets 
 * @returns {string}
 */
export function renderSummaryCard(summary, secrets) {
  const score = calculateRiskScore(summary.criticalCount, summary.highCount, summary.mediumCount);
  const { grade, label, color } = getSecurityGrade(score);

  // Dynamic directory scan counts
  let totalPathsScanned = 45;
  try {
    totalPathsScanned = getScanPaths().length + getAgentStateDirs().length + getModelDirs().length;
  } catch {
    // Fallback
  }

  // Format secrets subtitle if present
  const secretTypes = secrets.length > 0 
    ? ` (${Array.from(new Set(secrets.map(s => s.type))).join(', ')})`
    : '';

  // Build the text body
  let body = '';
  body += chalk.white.bold('  SECURITY GRADE: ') + color(`[ ${grade} ]  (${label})\n\n`);

  body += chalk.white.bold('  AIBOM METRICS:\n');
  body += chalk.dim('    • Discovered AI Workers:     ') + chalk.white(`${summary.agentsCount || 0} (${summary.agentsActive || 0} active runtimes, ${summary.agentsInstalled || 0} offline dependencies)`) + '\n';
  body += chalk.dim('    • Target Paths Scanned:      ') + chalk.white(totalPathsScanned) + '\n';
  body += chalk.dim('    • Active Network Sinks:      ') + chalk.white(`${summary.portsOpen} (${summary.portsExposed} bound to 0.0.0.0 - Exposing your local LAN)`) + '\n';
  body += chalk.dim('    • Exposed Admin Credentials: ') + chalk.white(`${summary.secretsCount}${secretTypes}`) + '\n\n';

  // Threat Profile Mapping
  body += chalk.white.bold('  THREAT PROFILE MAPPING (OWASP LLM & MCP Top 10):\n');
  const threats = [];
  if (summary.serversCount > 0 || summary.agentsActive > 0) {
    threats.push(chalk.red.bold('    • [LLM01 / MCP03] High Vulnerability to Indirect Prompt Injection (Active Code Execution)'));
  }
  if (summary.secretsCount > 0) {
    threats.push(chalk.red.bold('    • [LLM07] System Compromise via Plaintext Credential Exposure'));
  }
  if (summary.portsExposed > 0) {
    threats.push(chalk.red.bold('    • [MCP01] Unauthorized Local LAN Access to Model Runtime (Exposed Port)'));
  }

  if (threats.length === 0) {
    body += chalk.green('    ✔ No active threats mapped to OWASP LLM & MCP Top 10.') + '\n\n';
  } else {
    body += threats.join('\n') + '\n\n';
  }

  // Remediation
  body += chalk.white.bold('  IMMEDIATE REMEDIATION:\n');
  const actions = [];
  if (summary.portsExposed > 0) {
    actions.push(chalk.white('1. Bind your local LLM server to ') + chalk.green('127.0.0.1') + chalk.white(' immediately to block unauthorized Wi-Fi access.'));
  }
  if (summary.secretsCount > 0 && secrets.length > 0) {
    actions.push(chalk.white(`2. Revoke the leaked key (${chalk.red(secrets[0].matched)}) and use Credential Indirection (environment variables).`));
  }

  // Default recommendations if clean
  if (actions.length === 0) {
    actions.push(chalk.white('1. Continue utilizing environment variable expansion for all new MCP server configs.'));
    actions.push(chalk.white('2. Ensure local LLM tools remain isolated behind the local loopback interface.'));
  }
  actions.push(chalk.white(`3. Establish central governance for agent tools and keys across your engineering team.`));
  body += actions.map(a => `    ${a}`).join('\n') + '\n';

  const scorecardBox = boxen(body, {
    padding: 1,
    margin: { top: 1, bottom: 1 },
    borderStyle: 'double',
    borderColor: 'gray',
    title: chalk.white.bold(' AUDIT REPORT CARD '),
    titleAlignment: 'center'
  });

  // Call To Action (CTA)
  let ctaText = '';
  ctaText += orangeBold('⚡ GOVERN YOUR FLEET\'S SHADOW AI FOOTPRINT ⚡\n\n');
  ctaText += chalk.white(
    'You just found active, unvetted agents and exposed credentials on this workstation.\n' +
    'How many are running across the other 500 laptops in your engineering team?\n\n' +
    'Barrikade Enterprise connects directly to your CrowdStrike Falcon or Microsoft Defender\n' +
    'APIs to discover, register, and secure your entire developer AI footprint in minutes.\n' +
    'No local laptop agent installations required.\n\n'
  );
  ctaText += chalk.white.bold('👉 Try Barrikade Enterprise free: ') + chalk.underline.cyan('https://barrikade.ai');

  const ctaBox = boxen(ctaText, {
    padding: 1,
    margin: { bottom: 1 },
    borderStyle: 'round',
    borderColor: '#FF6600',
    title: orangeBold(' BARRIKADE LENS FOR ENTERPRISE '),
    titleAlignment: 'center'
  });

  return `${scorecardBox}\n${ctaBox}`;
}

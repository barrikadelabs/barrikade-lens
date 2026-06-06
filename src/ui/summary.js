import boxen from 'boxen';
import chalk from 'chalk';
import { orange, orangeBold } from './banner.js';

/**
 * Computes a risk score from 0 to 100 based on finding counts.
 * 
 * @param {number} critical 
 * @param {number} high 
 * @param {number} medium 
 * @returns {number}
 */
export function calculateRiskScore(critical, high, medium) {
  let score = 100;
  score -= (critical * 25);
  score -= (high * 15);
  score -= (medium * 5);
  return Math.max(0, score);
}

/**
 * Generates a visual progress bar for the score.
 * 
 * @param {number} score 
 * @returns {string}
 */
function getProgressBar(score) {
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
 *   mediumCount: number
 * }} summary 
 * @param {Array<any>} secrets 
 * @returns {string}
 */
export function renderSummaryCard(summary, secrets) {
  const score = calculateRiskScore(summary.criticalCount, summary.highCount, summary.mediumCount);
  const progressBar = getProgressBar(score);

  let ratingStr = '';
  if (score >= 80) {
    ratingStr = chalk.green.bold('SECURE / LOW RISK');
  } else if (score >= 50) {
    ratingStr = orangeBold('MODERATE RISK');
  } else {
    ratingStr = chalk.red.bold('CRITICAL SECURITY EXPOSURE');
  }

  // Build the text body
  let body = '';
  body += chalk.white.bold('  SECURITY SCORE: ') + progressBar + '  (' + ratingStr + ')\n\n';

  body += chalk.white.bold('SCAN METRICS:\n');
  body += chalk.dim('  • Discovered AI Agents:  ') + chalk.white(`${summary.agentsCount || 0} (${summary.agentsActive || 0} active, ${summary.agentsInstalled || 0} installed)`) + '\n';
  body += chalk.dim('  • Config Files Found:    ') + chalk.white(summary.configsCount) + '\n';
  body += chalk.dim('  • Active MCP Servers:    ') + chalk.white(summary.serversCount) + '\n';
  body += chalk.dim('  • Open AI Ports:         ') + chalk.white(`${summary.portsOpen} open`) + chalk.dim(` (${summary.portsExposed} exposed to LAN)`) + '\n';
  body += chalk.dim('  • Exposed Secrets:       ') + chalk.white(summary.secretsCount) + '\n\n';

  body += chalk.white.bold('SEVERITY SPLIT:\n');
  body += chalk.red.bold('  🔴 CRITICAL: ') + chalk.white(summary.criticalCount) + '\n';
  body += orangeBold('  🟠 HIGH:     ') + chalk.white(summary.highCount) + '\n';
  body += chalk.yellow.bold('  🟡 MEDIUM:   ') + chalk.white(summary.mediumCount) + '\n\n';

  // Recommendations
  body += chalk.white.bold(' TOP RECOMMENDED ACTIONS:\n');
  const actions = [];

  if (summary.portsExposed > 0) {
    actions.push(chalk.white('1. Bind all local model servers (Ollama, LM Studio) to ') + chalk.green('127.0.0.1') + chalk.white(' rather than ') + chalk.red('0.0.0.0') + chalk.white('.'));
  }
  if (summary.secretsCount > 0) {
    const hasCriticalSecret = secrets.some(s => s.risk === 'CRITICAL');
    const typeLabel = hasCriticalSecret ? 'DB credentials or AWS/GitHub keys' : 'API keys';
    actions.push(chalk.white(`2. Move plaintext ${typeLabel} to system environment variables and use variable expansion (e.g. \`\${env:VAR_NAME}\`).`));
  }

  // Default recommendations if everything is clean
  if (actions.length === 0) {
    actions.push(chalk.white('1. Continue utilizing environment variable expansion for all new MCP server configs.'));
    actions.push(chalk.white('2. Ensure local LLM tools remain isolated behind the local loopback interface.'));
  }

  actions.push(chalk.white(`3. Establish central governance for agent tools and keys across your engineering team.`));
  body += actions.map(a => `  ${a}`).join('\n') + '\n';

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
  ctaText += orangeBold('⚡ GOVERN YOUR ENTIRE SHADOW AI FOOTPRINT ⚡\n\n');
  ctaText += chalk.white(
    'Individual scan clean? What about the rest of your engineering team?\n' +
    'Barrikade provides fleet-wide discovery, secure key vaulting, policy enforcement,\n' +
    'and full audit logging for AI agents.\n\n'
  );
  ctaText += chalk.white.bold('👉 Try Barrikade Enterprise free: ') + chalk.underline.cyan('https://barrikade.ai');

  const ctaBox = boxen(ctaText, {
    padding: 1,
    margin: { bottom: 1 },
    borderStyle: 'round',
    borderColor: '#FF6600',
    title: orangeBold(' BARRIKADE LENS ENTERPRISE '),
    titleAlignment: 'center'
  });

  return `${scorecardBox}\n${ctaBox}`;
}

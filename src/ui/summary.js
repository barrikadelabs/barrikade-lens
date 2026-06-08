import boxen from 'boxen';
import chalk from 'chalk';
import { orange, orangeBold } from './banner.js';
import { termWidth } from './tables.js';

/**
 * Computes a risk score from 0–100 based on finding counts.
 * Recalibrated: CRITICAL findings are far more punishing.
 *   • Each CRITICAL:  -30 pts  (was -25)
 *   • Each HIGH:      -20 pts  (was -15)
 *   • Each MEDIUM:    -5  pts  (unchanged)
 * 
 * @param {number} critical 
 * @param {number} high 
 * @param {number} medium 
 * @returns {number}
 */
export function calculateRiskScore(critical, high, medium) {
  let score = 100;
  score -= (critical * 30);
  score -= (high    * 20);
  score -= (medium  *  5);
  return Math.max(0, score);
}

/**
 * Maps a numeric score to a letter grade.
 * 
 * @param {number} score 
 * @returns {{ grade: string, color: (s: string) => string }}
 */
function getGrade(score) {
  if (score >= 90) return { grade: 'A', color: chalk.green.bold };
  if (score >= 75) return { grade: 'B', color: chalk.green };
  if (score >= 55) return { grade: 'C', color: chalk.yellow.bold };
  if (score >= 35) return { grade: 'D', color: orangeBold };
  return { grade: 'F', color: chalk.red.bold };
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
  const emptyBlocks  = totalBlocks - filledBlocks;

  const bar = `[${'\u2588'.repeat(filledBlocks)}${'\u2591'.repeat(emptyBlocks)}] ${score}/100`;

  if (score >= 75) return chalk.green.bold(bar);
  if (score >= 35) return orangeBold(bar);
  return chalk.red.bold(bar);
}

/**
 * Builds a list of OWASP LLM / MCP threat references based on findings.
 * 
 * @param {{
 *   criticalCount: number,
 *   highCount: number,
 *   portsExposed: number,
 *   secretsCount: number,
 *   agentsActive: number
 * }} summary
 * @returns {string[]}
 */
function getOwaspMappings(summary) {
  const mappings = [];

  if (summary.agentsActive > 0) {
    mappings.push('[LLM01 / MCP03] High vulnerability to Indirect Prompt Injection (Active Code Execution)');
  }
  if (summary.secretsCount > 0) {
    mappings.push('[LLM07] System compromise risk via plaintext credential exposure');
  }
  if (summary.portsExposed > 0) {
    mappings.push('[LLM08] Excessive Agent Permissions — local model server reachable from LAN (0.0.0.0)');
  }
  if (summary.serversCount > 0) {
    mappings.push('[MCP01] Unvetted MCP server registrations — potential Tool Poisoning attack surface');
  }

  return mappings;
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
  const score       = calculateRiskScore(summary.criticalCount, summary.highCount, summary.mediumCount);
  const progressBar = getProgressBar(score);
  const { grade, color: gradeColor } = getGrade(score);

  let ratingStr = '';
  if (score >= 75) {
    ratingStr = chalk.green.bold('LOW RISK');
  } else if (score >= 55) {
    ratingStr = chalk.yellow.bold('ELEVATED RISK');
  } else if (score >= 35) {
    ratingStr = orangeBold('HIGH RISK');
  } else {
    ratingStr = chalk.red.bold('CRITICAL EXPOSURE RISK');
  }

  // ── Scorecard body ────────────────────────────────────────────
  let body = '';
  body += chalk.white.bold('  SECURITY GRADE: ') + gradeColor(` [ ${grade} ] `) + chalk.dim(' · ') + ratingStr + '\n';
  body += '  ' + progressBar + '\n\n';

  body += chalk.white.bold('AIBOM METRICS:\n');
  body += chalk.dim(`  • Discovered AI Workers:   `) + chalk.white(`${summary.agentsCount} (${summary.agentsActive} active runtimes, ${summary.agentsInstalled} offline dependencies)`) + '\n';
  body += chalk.dim(`  • Active Network Sinks:    `) + chalk.white(`${summary.portsOpen} open`) +
          (summary.portsExposed > 0 ? chalk.red(` (${summary.portsExposed} bound to 0.0.0.0 — LAN exposed)`) : chalk.green(' (all loopback-only)')) + '\n';
  body += chalk.dim(`  • Exposed Admin Credentials: `) + (summary.secretsCount > 0
    ? chalk.red.bold(`${summary.secretsCount} plaintext secret${summary.secretsCount > 1 ? 's' : ''} found`)
    : chalk.green('None detected')) + '\n';
  body += chalk.dim(`  • MCP Server Configs Found: `) + chalk.white(`${summary.configsCount}`) + chalk.dim(` (${summary.serversCount} servers registered)`) + '\n\n';

  // OWASP mappings
  const owaspMappings = getOwaspMappings(summary);
  if (owaspMappings.length > 0) {
    body += chalk.white.bold('THREAT PROFILE MAPPING (OWASP LLM & MCP Top 10):\n');
    for (const m of owaspMappings) {
      body += chalk.red(`  • ${m}`) + '\n';
    }
    body += '\n';
  }

  // Severity split
  body += chalk.white.bold('SEVERITY SPLIT:\n');
  body += chalk.red.bold('  🔴 CRITICAL: ') + chalk.white(summary.criticalCount) + '\n';
  body += orangeBold(    '  🟠 HIGH:     ') + chalk.white(summary.highCount)     + '\n';
  body += chalk.yellow.bold('  🟡 MEDIUM: ') + chalk.white(summary.mediumCount)   + '\n\n';

  // Recommendations
  body += chalk.white.bold('IMMEDIATE REMEDIATION:\n');
  const actions = [];
  let actionNum = 1;

  if (summary.portsExposed > 0) {
    actions.push(chalk.white(`${actionNum++}. Bind your local LLM server to `) + chalk.green('127.0.0.1') +
      chalk.white(' immediately — 0.0.0.0 exposes your inference engine to every device on your Wi-Fi.'));
  }
  if (summary.secretsCount > 0) {
    const criticalSecret = secrets.find(s => s.risk === 'CRITICAL');
    const highSecret     = secrets.find(s => s.risk === 'HIGH');
    const refSecret      = criticalSecret || highSecret;
    const keyRef         = refSecret ? chalk.red(refSecret.matched) : chalk.red('exposed key');
    actions.push(chalk.white(`${actionNum++}. Revoke ${keyRef} immediately and replace it with environment variable indirection`) +
      chalk.dim(' (e.g. `${env:OPENAI_API_KEY}`).'));
  }
  if (summary.serversCount > 0) {
    actions.push(chalk.white(`${actionNum++}. Audit each registered MCP server for Tool Poisoning risk — unvetted servers can hijack your shell via injected tool arguments.`));
  }

  if (actions.length === 0) {
    actions.push(chalk.white('1. Continue using environment variable expansion for all new MCP server configs.'));
    actions.push(chalk.white('2. Ensure local LLM tools remain isolated behind the loopback interface (127.0.0.1).'));
    actions.push(chalk.white('3. Re-run this audit whenever you install a new AI agent or coding tool.'));
  }

  body += actions.map(a => `  ${a}`).join('\n') + '\n';

  const boxWidth = Math.max(56, termWidth() - 4);

  const scorecardBox = boxen(body, {
    width: boxWidth,
    padding: 1,
    margin: { top: 1, bottom: 1 },
    borderStyle: 'double',
    borderColor: score < 35 ? 'red' : score < 55 ? '#FF6600' : 'gray',
    title: chalk.white.bold(' AUDIT REPORT CARD '),
    titleAlignment: 'center'
  });

  // ── CTA ───────────────────────────────────────────────────────
  const hasFindings = summary.criticalCount > 0 || summary.highCount > 0 || summary.agentsActive > 0;

  let ctaText = '';
  ctaText += orangeBold('⚡ GOVERN YOUR FLEET\'S SHADOW AI FOOTPRINT\n\n');

  if (hasFindings) {
    ctaText += chalk.white(
      `You just found ${summary.agentsActive} active, unvetted agent${summary.agentsActive !== 1 ? 's' : ''}` +
      (summary.secretsCount > 0 ? ` and ${summary.secretsCount} exposed credential${summary.secretsCount !== 1 ? 's' : ''}` : '') +
      ' on this workstation.\n' +
      'How many are running across the other laptops in your engineering team?\n\n'
    );
  } else {
    ctaText += chalk.white(
      "You've secured this machine — but what about the rest of your fleet?\n" +
      "Shadow AI spreads fast across engineering teams.\n\n"
    );
  }

  ctaText += chalk.white(
    'Barrikade Enterprise connects to your CrowdStrike Falcon or Microsoft Defender\n' +
    'APIs to discover, register, and secure your entire developer AI footprint in minutes.\n' +
    'No local laptop agent installation required.\n\n'
  );
  ctaText += chalk.white.bold('👉 Secure your digital workforce: ') + chalk.underline.cyan('https://barrikade.ai');

  const ctaBox = boxen(ctaText, {
    width: boxWidth,
    padding: 1,
    margin: { bottom: 1 },
    borderStyle: 'round',
    borderColor: '#FF6600',
    title: orangeBold(' BARRIKADE LENS ENTERPRISE '),
    titleAlignment: 'center'
  });

  return `${scorecardBox}\n${ctaBox}`;
}

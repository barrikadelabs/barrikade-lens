import chalk from 'chalk';
import { printBanner, orangeBold } from './banner.js';
import { renderAgentTable, renderPortTable, renderSecretTable, renderCapabilityTable } from './tables.js';
import { renderSummaryCard } from './summary.js';

/**
 * Formats a section header with orange styling.
 * 
 * @param {string} title 
 * @returns {string}
 */
function sectionHeader(title) {
  const line = chalk.dim('─'.repeat(4));
  return `\n${orangeBold('■')} ${chalk.white.bold(title)} ${line}\n`;
}

/**
 * Outputs the full interactive CLI dashboard directly to stdout.
 * 
 * @param {{
 *   configs: Array<any>,
 *   ports: Array<any>,
 *   secrets: Array<any>,
 *   summary: any,
 *   capabilities: any,
 *   evidence: string[]
 * }} results 
 */
export function displayDashboard(results) {
  // 1. Title/Header Banner
  printBanner('0.1.0');

  // 2. Capability Analysis (Tier 1-8 Aggregation)
  console.log(sectionHeader('AUTONOMOUS AI CAPABILITY ANALYSIS'));
  console.log(renderCapabilityTable(results.capabilities));

  // 3. Evidence collected
  console.log(sectionHeader('COLLECTED AUDIT EVIDENCE'));
  if (results.evidence.length === 0) {
    console.log(chalk.dim('  No agent infrastructure or capabilities detected on this workstation.\n'));
  } else {
    for (const ev of results.evidence) {
      console.log(`  ${chalk.dim('•')} ${chalk.white(ev)}`);
    }
    console.log();
  }

  // 4. Discovered Configs and Active MCP Servers
  console.log(sectionHeader('SHADOW AGENT & CLIENT MCP INVENTORY'));
  console.log(renderAgentTable(results.configs));

  // 5. Local Inference Ports
  console.log(sectionHeader('LOCAL LLM SERVER PORT SWEEP'));
  console.log(renderPortTable(results.ports));

  // 6. Secret Scanner Findings
  console.log(sectionHeader('PLAIN-TEXT SECRET & CISO RISK ANALYSIS'));
  console.log(renderSecretTable(results.secrets));

  // 7. Final Report Card and CTA
  console.log(renderSummaryCard(results.summary, results.secrets));
}

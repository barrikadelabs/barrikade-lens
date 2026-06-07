import readline from 'node:readline';
import chalk from 'chalk';
import { printBanner, orangeBold } from './banner.js';
import { renderAgentTable, renderPortTable, renderSecretTable, renderCapabilityTable, renderAgentInventoryTable } from './tables.js';
import { renderSummaryCard, calculateRiskScore, getProgressBar } from './summary.js';

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
 * Helper to clear the terminal screen and scrollback buffer.
 */
function clearScreen() {
  process.stdout.write('\x1B[2J\x1B[3J\x1B[H');
}

/**
 * Outputs the full classic unified CLI dashboard directly to stdout.
 * 
 * @param {{
 *   configs: Array<any>,
 *   ports: Array<any>,
 *   secrets: Array<any>,
 *   summary: any,
 *   capabilities: any,
 *   evidence: string[],
 *   agents: any[]
 * }} results 
 */
function displayClassicDashboard(results) {
  printBanner(results.version || '0.1.0');

  console.log(sectionHeader('AUTONOMOUS AI CAPABILITY ANALYSIS'));
  console.log(renderCapabilityTable(results.capabilities));

  console.log(sectionHeader('DISCOVERED AI AGENTS INVENTORY'));
  console.log(renderAgentInventoryTable(results.agents));

  console.log(sectionHeader('COLLECTED AUDIT EVIDENCE'));
  if (results.evidence.length === 0) {
    console.log(chalk.dim('  No agent infrastructure or capabilities detected on this workstation.\n'));
  } else {
    for (const ev of results.evidence) {
      console.log(`  ${chalk.dim('•')} ${chalk.white(ev)}`);
    }
    console.log();
  }

  console.log(sectionHeader('MCP INVENTORY'));
  console.log(renderAgentTable(results.configs));

  console.log(sectionHeader('LOCAL LLM SERVER PORT SWEEP'));
  console.log(renderPortTable(results.ports));

  console.log(sectionHeader('PLAIN-TEXT SECRET ANALYSIS'));
  console.log(renderSecretTable(results.secrets));

  console.log(renderSummaryCard(results.summary, results.secrets));
}

/**
 * Outputs the interactive guided step-by-step dashboard.
 * 
 * @param {{
 *   configs: Array<any>,
 *   ports: Array<any>,
 *   secrets: Array<any>,
 *   summary: any,
 *   capabilities: any,
 *   evidence: string[],
 *   agents: any[],
 *   version?: string
 * }} results 
 * @returns {Promise<void>}
 */
export function displayDashboard(results) {
  const version = results.version || '0.1.0';

  return new Promise((resolve) => {
    // Fallback to classic dashboard if non-interactive environment
    if (!process.stdin.isTTY || !process.stdout.isTTY) {
      displayClassicDashboard(results);
      resolve();
      return;
    }

    // Set up keypress listener
    readline.emitKeypressEvents(process.stdin);
    if (process.stdin.isTTY) {
      process.stdin.setRawMode(true);
    }
    process.stdin.resume();

    let currentStep = 0;

    // Split summary card into scorecard and CTA boxes
    const rawSummaryCard = renderSummaryCard(results.summary, results.secrets);
    const summaryParts = rawSummaryCard.split('\n╭');
    const scorecardBox = summaryParts[0];
    const ctaBox = summaryParts[1] ? '╭' + summaryParts[1] : '';

    const steps = [
      {
        title: 'Autonomous AI Capability Analysis',
        render: () => {
          console.log(renderCapabilityTable(results.capabilities));
        },
        nextPrompt: 'Press [Enter] to view Discovered AI Agents'
      },
      {
        title: 'Discovered AI Agents Inventory',
        render: () => {
          console.log(renderAgentInventoryTable(results.agents));
        },
        nextPrompt: 'Press [Enter] to inspect Model Context Protocol (MCP) Configs'
      },
      {
        title: 'Model Context Protocol (MCP) Servers',
        render: () => {
          console.log(renderAgentTable(results.configs));
        },
        nextPrompt: 'Press [Enter] to run Local LLM Port Sweep'
      },
      {
        title: 'Local LLM Server Port Sweep',
        render: () => {
          console.log(renderPortTable(results.ports));
        },
        nextPrompt: 'Press [Enter] to audit Plain-Text Secrets'
      },
      {
        title: 'Plain-Text Secrets Scan',
        render: () => {
          console.log(renderSecretTable(results.secrets));
        },
        nextPrompt: 'Press [Enter] to view collected audit evidence'
      },
      {
        title: 'Collected Audit Evidence',
        render: () => {
          if (results.evidence.length === 0) {
            console.log(chalk.dim('  No agent infrastructure or capabilities detected on this workstation.\n'));
          } else {
            for (const ev of results.evidence) {
              console.log(`  ${chalk.dim('•')} ${chalk.white(ev)}`);
            }
            console.log();
          }
        },
        nextPrompt: 'Press [Enter] to generate Executive Audit Summary Card'
      },
      {
        title: 'Executive Audit Summary',
        render: () => {
          const score = calculateRiskScore(results.summary.criticalCount, results.summary.highCount, results.summary.mediumCount);
          const progressBar = getProgressBar(score);
          
          let ratingStr = '';
          if (score >= 80) {
            ratingStr = chalk.green.bold('SECURE / LOW RISK');
          } else if (score >= 50) {
            ratingStr = orangeBold('MODERATE RISK');
          } else {
            ratingStr = chalk.red.bold('CRITICAL SECURITY RISK');
          }

          console.log(`  ${chalk.white.bold('SECURITY SCORE:')} ${progressBar}  (${ratingStr})`);
          console.log(`  ${chalk.dim('Detected:')} ${chalk.white(`${results.agents.length} Agents`)} | ${chalk.white(`${results.configs.filter(c => c.exists).length} Config Files`)} | ${chalk.white(`${results.secrets.length} Secrets`)}`);
          console.log(scorecardBox);
        },
        nextPrompt: 'Press [Enter] to view recommendations & next steps'
      },
      {
        title: 'Audit Recommendations & Next Steps',
        render: () => {
          if (ctaBox) {
            console.log(ctaBox);
          } else {
            console.log(rawSummaryCard);
          }
        },
        nextPrompt: 'Press [Enter] to complete audit and exit'
      }
    ];

    function render() {
      clearScreen();
      printBanner(version);

      const step = steps[currentStep];
      console.log(`\n${orangeBold('■')} ${chalk.white.bold(step.title.toUpperCase())} ${chalk.dim('─'.repeat(4))}`);
      console.log(chalk.dim(`  Step ${currentStep + 1} of ${steps.length} | [Enter] Next | [Backspace] Back | [Q] Quit`));
      console.log(chalk.dim('  ' + '─'.repeat(72)) + '\n');

      step.render();

      console.log(chalk.dim('\n  ' + '─'.repeat(72)));
      console.log(chalk.cyan(`  👉 ${step.nextPrompt}`));
    }

    function cleanup() {
      process.stdin.removeListener('keypress', handleKeypress);
      if (process.stdin.isTTY) {
        process.stdin.setRawMode(false);
      }
      process.stdin.pause();
    }

    function handleKeypress(str, key) {
      if (key.ctrl && key.name === 'c') {
        cleanup();
        clearScreen();
        process.exit(0);
      }

      if (key.name === 'return' || key.name === 'enter') {
        if (currentStep === steps.length - 1) {
          // Finished all steps
          cleanup();
          clearScreen();
          console.log(chalk.dim('Audit complete. Have a secure day!'));
          resolve();
        } else {
          currentStep++;
          render();
        }
      } else if (key.name === 'backspace' || key.name === 'left') {
        if (currentStep > 0) {
          currentStep--;
          render();
        }
      } else if (str === 'q' || str === 'Q') {
        cleanup();
        clearScreen();
        console.log(chalk.dim('Audit exited. Have a secure day!'));
        resolve();
      }
    }

    process.stdin.on('keypress', handleKeypress);

    // Initial render
    render();
  });
}

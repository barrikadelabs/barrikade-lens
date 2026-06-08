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

    const steps = [
      {
        title: 'Step 1 of 7 — Autonomous AI Capability Analysis',
        render: () => {
          console.log(renderCapabilityTable(results.capabilities));
        },
        nextPrompt: 'Press [Enter] to see what AI agents are installed on this machine →'
      },
      {
        title: 'Step 2 of 7 — Discovered AI Agents Inventory',
        render: () => {
          console.log(renderAgentInventoryTable(results.agents));
        },
        nextPrompt: 'Press [Enter] to inspect registered MCP server configurations →'
      },
      {
        title: 'Step 3 of 7 — MCP Server Inventory (Shadow Agent Surface)',
        render: () => {
          console.log(renderAgentTable(results.configs));
        },
        nextPrompt: 'Press [Enter] to sweep local LLM server ports →'
      },
      {
        title: 'Step 4 of 7 — Local LLM Network Exposure',
        render: () => {
          console.log(renderPortTable(results.ports));
        },
        nextPrompt: 'Press [Enter] to scan for exposed API keys and credentials →'
      },
      {
        title: 'Step 5 of 7 — Plaintext Credential Exposure Scan',
        render: () => {
          console.log(renderSecretTable(results.secrets));
        },
        nextPrompt: 'Press [Enter] to review raw supporting evidence →'
      },
      {
        title: 'Step 6 of 7 — Raw Supporting Evidence',
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
        nextPrompt: 'Press [Enter] to generate your Audit Report Card →'
      },
      {
        title: 'Step 7 of 7 — Audit Report Card',
        render: () => {
          const score = calculateRiskScore(
            results.summary.criticalCount,
            results.summary.highCount,
            results.summary.mediumCount
          );
          const progressBar = getProgressBar(score);

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

          console.log(`\n  ${chalk.white.bold('SECURITY SCORE:')} ${progressBar}  (${ratingStr})`);
          console.log(
            `  ${chalk.dim('Detected:')} ` +
            chalk.white(`${results.agents.length} AI Workers`) +
            chalk.dim(' | ') +
            chalk.white(`${results.configs.filter(c => c.exists).length} Config Files`) +
            chalk.dim(' | ') +
            chalk.white(`${results.secrets.length} Plaintext Secrets`)
          );

          // Render scorecard & CTA dynamically using the CURRENT terminal width
          const rawSummaryCard = renderSummaryCard(results.summary, results.secrets);
          const ctaSplitIdx  = rawSummaryCard.indexOf('\n╭');
          const scorecardBox = ctaSplitIdx !== -1 ? rawSummaryCard.slice(0, ctaSplitIdx) : rawSummaryCard;
          const ctaBox       = ctaSplitIdx !== -1 ? rawSummaryCard.slice(ctaSplitIdx + 1) : '';

          console.log(scorecardBox);

          if (ctaBox) {
            console.log(ctaBox);
          }
        },
        nextPrompt: 'Press [Enter] to exit — Audit complete'
      }
    ];

    function render() {
      clearScreen();
      printBanner(version);

      const step    = steps[currentStep];
      const sepLen  = Math.max(40, Math.min((process.stdout.columns || 80) - 4, 90));
      const controls = chalk.dim(`  [Enter] Next  [Backspace] Back  [Q] Quit`);

      console.log(`\n${orangeBold('■')} ${chalk.white.bold(step.title)} ${chalk.dim('─'.repeat(4))}`);
      console.log(controls);
      console.log(chalk.dim('  ' + '─'.repeat(sepLen)) + '\n');

      step.render();

      console.log(chalk.dim('\n  ' + '─'.repeat(sepLen)));
      console.log(chalk.cyan(`  👉 ${step.nextPrompt}`));
    }

    function handleResize() {
      render();
    }

    function cleanup() {
      process.stdin.removeListener('keypress', handleKeypress);
      process.stdout.removeListener('resize', handleResize);
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
    process.stdout.on('resize', handleResize);

    // Initial render
    render();
  });
}

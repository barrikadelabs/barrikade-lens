#!/usr/bin/env node

import { program } from 'commander';
import { runAudit } from '../src/runner.js';

// Global error handling for unhandled rejections
process.on('unhandledRejection', (reason) => {
  console.error('\n✖ Unhandled Promise Rejection:', reason);
  process.exit(1);
});

process.on('uncaughtException', (error) => {
  console.error('\n✖ Uncaught Exception:', error);
  process.exit(1);
});

program
  .name('barrikade-lens')
  .description('Instant Shadow AI agent discovery & security scanner')
  .version('0.1.0')
  .option('--json', 'Output raw JSON instead of interactive dashboard')
  .option('-r, --report <path>', 'Write JSON scan report to specified file path')
  .option('--html <path>', 'Generate a self-contained HTML CISO audit report')
  .option('--no-telemetry', 'Disable anonymous high-level telemetry reporting')
  .option('--verbose', 'Show detailed execution logs')
  .action(async (options) => {
    try {
      const exitCode = await runAudit(options);
      process.exit(exitCode);
    } catch (err) {
      console.error('Fatal CLI Error:', err);
      process.exit(1);
    }
  });

program.parse(process.argv);
export {}; // Ensure file is parsed as ES Module

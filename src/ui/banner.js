import chalk from 'chalk';

// Orange accent color helper
export const orange = chalk.hex('#FF6600');
export const orangeBold = chalk.hex('#FF6600').bold;

/**
 * Prints the CLI header banner and privacy disclosures.
 * 
 * @param {string} version Version of the tool
 */
export function printBanner(version = '0.1.0') {
  const line = orange('================================================================');

  console.log('\n' + line);
  console.log(chalk.white.bold('                 B A R R I K A D E   L E N S'));
  console.log(orange('                      Shadow AI Auditor'));
  console.log(chalk.dim(`                          v${version}`));
  console.log(line);
  console.log(
    chalk.dim('  🔒 ') + 
    chalk.white.bold('100% Local Scanning') + 
    chalk.dim(' • ') + 
    chalk.white('No code or credentials leave this machine')
  );
  console.log(line + '\n');
}

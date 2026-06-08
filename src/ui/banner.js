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
  const col = process.stdout.columns || 80;
  const w = Math.max(50, Math.min(col, 64));
  const line = orange('='.repeat(w));

  const center = (text) => {
    const rawText = text.replace(/\u001B\[[0-9;]*m/g, '');
    const padLen = Math.max(0, Math.floor((w - rawText.length) / 2));
    return ' '.repeat(padLen) + text;
  };

  console.log('\n' + line);
  console.log(center(chalk.white.bold('B A R R I K A D E   L E N S')));
  console.log(center(orange('Shadow AI Auditor')));
  console.log(center(chalk.dim(`v${version}`)));
  console.log(line);

  if (col < 64) {
    console.log(center(chalk.white.bold('100% Local Scanning')));
    console.log(center(chalk.white('No code or credentials leave this machine')));
  } else {
    console.log(
      center(
        chalk.white.bold('100% Local Scanning') +
        chalk.dim(' • ') +
        chalk.white('No code or credentials leave this machine')
      )
    );
  }
  console.log(line + '\n');
}

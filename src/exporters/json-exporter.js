import fs from 'node:fs/promises';

/**
 * Exports scan results as a JSON structure.
 *
 * @param {any} results Aggregated scan results
 * @param {string} [outputPath] Optional file path to write the JSON to
 */
export async function exportJson(results, outputPath) {
  const jsonString = JSON.stringify(results, null, 2);

  if (outputPath) {
    await fs.writeFile(outputPath, jsonString, 'utf8');
  } else {
    console.log(jsonString);
  }
}

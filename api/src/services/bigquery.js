import { BigQuery } from '@google-cloud/bigquery';

const DATASET = 'lens';
const TABLE = 'telemetry_events';

// Cloud Run injects credentials automatically via the default service account.
// No key file needed — the BigQuery client uses Application Default Credentials.
const bigquery = new BigQuery();

/**
 * Streams a single telemetry row into BigQuery.
 *
 * Uses the streaming insert API which is optimised for high-throughput,
 * low-latency writes — exactly what a telemetry ingest needs.
 *
 * @param {Record<string, any>} row - A flat object matching the table schema
 * @returns {Promise<void>}
 */
export async function insertTelemetryRow(row) {
  await bigquery
    .dataset(DATASET)
    .table(TABLE)
    .insert([row]);
}

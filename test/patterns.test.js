import { test } from 'node:test';
import assert from 'node:assert/strict';
import { scanStringForSecrets } from '../src/utils/patterns.js';

test('skips Supabase local dev URL', () => {
  const url = 'postgresql://postgres:postgres@127.0.0.1:54322/postgres';
  const found = scanStringForSecrets(url);
  assert.equal(found.length, 0);
});

test('keeps a remote DB URL CRITICAL', () => {
  const url = 'postgresql://admin:hunter2@db.example.com:5432/app';
  const [finding] = scanStringForSecrets(url);
  assert.equal(finding.risk, 'CRITICAL');
});

test('flags default creds on a REMOTE host', () => {
  const url = 'postgresql://postgres:postgres@db.example.com:5432/app';
  const [finding] = scanStringForSecrets(url);
  assert.equal(finding.risk, 'CRITICAL');
});

test('downgrades a localhost DB URL with non-default creds to HIGH', () => {
  const url = 'postgresql://admin:hunter2@localhost:5432/app';
  const [finding] = scanStringForSecrets(url);
  assert.equal(finding.risk, 'HIGH');
});

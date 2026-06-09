/**
 * @typedef {{
 *   name: string,
 *   regex: RegExp,
 *   risk: 'CRITICAL' | 'HIGH' | 'MEDIUM',
 *   remediation: string,
 *   grade?: (rawMatch: string, baseRisk: 'CRITICAL' | 'HIGH' | 'MEDIUM') => ('CRITICAL' | 'HIGH' | 'MEDIUM' | null)
 * }} SecretPattern
 */

const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '::1']);

// Published, valueless defaults — public knowledge regardless of where they run.
const DEFAULT_CREDENTIALS = new Set([
  'postgres:postgres',
  'root:root',
  'admin:admin',
  'user:password',
  'postgres:', // empty password
  'root:',
]);

/** @param {URL} url */
function isDefaultCredential(url) {
  return DEFAULT_CREDENTIALS.has(`${url.username}:${url.password}`);
}

/**
 * @param {string} rawMatch
 * @param {'CRITICAL' | 'HIGH' | 'MEDIUM'} baseRisk
 * @returns {'CRITICAL' | 'HIGH' | 'MEDIUM' | null}
 */
function gradeDatabaseUrl(rawMatch, baseRisk) {
  let url;
  try {
    url = new URL(rawMatch);
  } catch {
    return baseRisk; // unparseable — fail safe
  }

  const isDefaultCred = isDefaultCredential(url);
  const isLocalHost = LOCAL_HOSTS.has(url.hostname);

  // Published default creds on a local host → genuinely not a secret (Supabase, docker dev).
  if (isLocalHost && isDefaultCred) return null;

  // A REAL credential is sensitive wherever it points. Loopback only means
  // it isn't currently network-reachable, so soften by one step — never drop.
  if (isLocalHost && !isDefaultCred) {
    return baseRisk === 'CRITICAL' ? 'HIGH' : baseRisk;
  }

  // Remote host (incl. docker service names like @db, @postgres) → unchanged.
  // Note: default creds on a REMOTE host stay flagged — that's weak-credential risk.
  return baseRisk;
}
/**
 * Resolves a finding's severity, applying a pattern's grade hook if present.
 * @param {SecretPattern} pattern
 * @param {string} rawMatch
 * @returns {'CRITICAL' | 'HIGH' | 'MEDIUM' | null} severity, or null to drop the finding
 */
export function resolveRisk(pattern, rawMatch) {
  return typeof pattern.grade === 'function'
    ? pattern.grade(rawMatch, pattern.risk)
    : pattern.risk;
}

/**
 * Plaintext secrets patterns registry for detecting credentials in configurations.
 * @type {SecretPattern[]}
 */
export const SECRET_PATTERNS = [
  {
    name: 'OpenAI Project Key',
    regex: /\bsk-proj-[a-zA-Z0-9]{20,}\b/g,
    risk: 'HIGH',
    remediation:
      'Use environment variable interpolation or read from process environment.',
  },
  {
    name: 'OpenAI API Key',
    regex: /\bsk-[a-zA-Z0-9]{20,}\b/g,
    risk: 'HIGH',
    remediation:
      'Use environment variable interpolation or read from process environment.',
  },
  {
    name: 'Anthropic API Key',
    regex: /\bsk-ant-[a-zA-Z0-9_.-]+\b/g,
    risk: 'HIGH',
    remediation: 'Move key to system environment variables and reference it.',
  },
  {
    name: 'AWS Access Key ID',
    regex: /\bAKIA[0-9A-Z]{16}\b/g,
    risk: 'CRITICAL',
    remediation:
      'Remove AWS hardcoded keys. Use ~/.aws/credentials or IAM role profiles.',
  },
  {
    name: 'AWS Temporary Credentials Key',
    regex: /\bASIA[0-9A-Z]{16}\b/g,
    risk: 'HIGH',
    remediation:
      'Remove AWS hardcoded keys. Use temporary STS session variables.',
  },
  {
    name: 'HuggingFace Token',
    regex: /\bhf_[a-zA-Z]{34}\b/g,
    risk: 'HIGH',
    remediation: 'Load via HF_TOKEN environment variable.',
  },
  {
    name: 'Google API Key',
    regex: /\bAIza[0-9A-Za-z\-_]{35}\b/g,
    risk: 'HIGH',
    remediation: 'Move to environment variables.',
  },
  {
    name: 'GitHub Classic Token',
    regex: /\bghp_[0-9a-zA-Z]{36}\b/g,
    risk: 'CRITICAL',
    remediation:
      'Revoke key immediately. Use GITHUB_TOKEN environment variable or keychain.',
  },
  {
    name: 'GitHub Fine-Grained Token',
    regex: /\bgithub_pat_[0-9a-zA-Z_]{82}\b/g,
    risk: 'CRITICAL',
    remediation:
      'Revoke key immediately. Reference fine-grained token in local env.',
  },
  {
    name: 'Stripe Secret Key',
    regex: /\bsk_live_[0-9a-zA-Z]{24,}\b/g,
    risk: 'CRITICAL',
    remediation:
      'Revoke and rotate immediately. Never store Stripe secret keys in client configs.',
  },
  {
    name: 'Slack Token',
    regex: /\bxox[baprs]-[0-9a-zA-Z-]{10,48}\b/g,
    risk: 'HIGH',
    remediation: 'Store in environment variables or vault.',
  },
  {
    name: 'PostgreSQL Database URL',
    regex: /\bpostgresql:\/\/[^\s"']+\b/g,
    risk: 'CRITICAL',
    remediation:
      'Extract connection credentials to environment variables (e.g. PGUSER, PGPASSWORD).',
    grade: gradeDatabaseUrl,
  },
  {
    name: 'MongoDB Database URL',
    regex: /\bmongodb(\+srv)?:\/\/[^\s"']+\b/g,
    risk: 'HIGH',
    remediation: 'Do not hardcode credentials in database connection URI.',
    grade: gradeDatabaseUrl,
  },
  {
    name: 'Private Key Block',
    regex: /-----BEGIN[ A-Z0-9_-]*PRIVATE KEY-----/g,
    risk: 'CRITICAL',
    remediation:
      'Store private keys in standard files with chmod 600, never in client config.',
  },
];

/**
 * Scan a target string (e.g., config value or whole JSON) for secrets.
 *
 * @param {string} value String value to inspect
 * @returns {Array<{ type: string, matched: string, risk: string, remediation: string }>}
 */
export function scanStringForSecrets(value) {
  if (typeof value !== 'string') return [];

  const findings = [];

  for (const pattern of SECRET_PATTERNS) {
    pattern.regex.lastIndex = 0; // Reset regex state
    let match;
    while ((match = pattern.regex.exec(value)) !== null) {
      const rawMatch = match[0];
      const risk = resolveRisk(pattern, rawMatch);
      if (risk === null) continue;
      findings.push({
        type: pattern.name,
        matched: redactSecret(rawMatch),
        risk,
        remediation: pattern.remediation,
      });
    }
  }

  return findings;
}

/**
 * Redacts secrets, keeping only the first 4 and last 4 characters if long enough.
 *
 * @param {string} secret
 * @returns {string}
 */
export function redactSecret(secret) {
  if (!secret) return '';
  if (
    secret.startsWith('postgresql://') ||
    secret.startsWith('mongodb://') ||
    secret.startsWith('mongodb+srv://')
  ) {
    // Redact password in URI, e.g. postgresql://user:pass@host:port/db
    try {
      const url = new URL(secret);
      if (url.password) {
        url.password = '********';
      }
      return url.toString();
    } catch {
      // Fallback simple regex replace for credentials
      return secret.replace(/(:\/\/)([^:]+):([^@]+)(@)/, '$1$2:********$4');
    }
  }

  if (secret.length <= 10) {
    return '********';
  }

  return `${secret.substring(0, 4)}...${secret.substring(secret.length - 4)}`;
}

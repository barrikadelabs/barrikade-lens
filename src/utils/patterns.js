/**
 * @typedef {{ name: string, regex: RegExp, risk: 'CRITICAL' | 'HIGH' | 'MEDIUM', remediation: string }} SecretPattern
 */

/**
 * Plaintext secrets patterns registry for detecting credentials in configurations.
 * @type {SecretPattern[]}
 */
export const SECRET_PATTERNS = [
  {
    name: 'OpenAI Project Key',
    regex: /\bsk-proj-[a-zA-Z0-9]{20,}\b/g,
    risk: 'HIGH',
    remediation: 'Use environment variable interpolation or read from process environment.'
  },
  {
    name: 'OpenAI API Key',
    regex: /\bsk-[a-zA-Z0-9]{20,}\b/g,
    risk: 'HIGH',
    remediation: 'Use environment variable interpolation or read from process environment.'
  },
  {
    name: 'Anthropic API Key',
    regex: /\bsk-ant-[a-zA-Z0-9_.-]+\b/g,
    risk: 'HIGH',
    remediation: 'Move key to system environment variables and reference it.'
  },
  {
    name: 'AWS Access Key ID',
    regex: /\bAKIA[0-9A-Z]{16}\b/g,
    risk: 'CRITICAL',
    remediation: 'Remove AWS hardcoded keys. Use ~/.aws/credentials or IAM role profiles.'
  },
  {
    name: 'AWS Temporary Credentials Key',
    regex: /\bASIA[0-9A-Z]{16}\b/g,
    risk: 'HIGH',
    remediation: 'Remove AWS hardcoded keys. Use temporary STS session variables.'
  },
  {
    name: 'HuggingFace Token',
    regex: /\bhf_[a-zA-Z]{34}\b/g,
    risk: 'HIGH',
    remediation: 'Load via HF_TOKEN environment variable.'
  },
  {
    name: 'Google API Key',
    regex: /\bAIza[0-9A-Za-z\-_]{35}\b/g,
    risk: 'HIGH',
    remediation: 'Move to environment variables.'
  },
  {
    name: 'GitHub Classic Token',
    regex: /\bghp_[0-9a-zA-Z]{36}\b/g,
    risk: 'CRITICAL',
    remediation: 'Revoke key immediately. Use GITHUB_TOKEN environment variable or keychain.'
  },
  {
    name: 'GitHub Fine-Grained Token',
    regex: /\bgithub_pat_[0-9a-zA-Z_]{82}\b/g,
    risk: 'CRITICAL',
    remediation: 'Revoke key immediately. Reference fine-grained token in local env.'
  },
  {
    name: 'Stripe Secret Key',
    regex: /\bsk_live_[0-9a-zA-Z]{24,}\b/g,
    risk: 'CRITICAL',
    remediation: 'Revoke and rotate immediately. Never store Stripe secret keys in client configs.'
  },
  {
    name: 'Slack Token',
    regex: /\bxox[baprs]-[0-9a-zA-Z-]{10,48}\b/g,
    risk: 'HIGH',
    remediation: 'Store in environment variables or vault.'
  },
  {
    name: 'PostgreSQL Database URL',
    regex: /\bpostgresql:\/\/[^\s"']+\b/g,
    risk: 'CRITICAL',
    remediation: 'Extract connection credentials to environment variables (e.g. PGUSER, PGPASSWORD).'
  },
  {
    name: 'MongoDB Database URL',
    regex: /\bmongodb(\+srv)?:\/\/[^\s"']+\b/g,
    risk: 'HIGH',
    remediation: 'Do not hardcode credentials in database connection URI.'
  },
  {
    name: 'Private Key Block',
    regex: /-----BEGIN[ A-Z0-9_-]*PRIVATE KEY-----/g,
    risk: 'CRITICAL',
    remediation: 'Store private keys in standard files with chmod 600, never in client config.'
  }
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
      // Redact the match
      const redacted = redactSecret(rawMatch);
      findings.push({
        type: pattern.name,
        matched: redacted,
        risk: pattern.risk,
        remediation: pattern.remediation
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
  if (secret.startsWith('postgresql://') || secret.startsWith('mongodb://') || secret.startsWith('mongodb+srv://')) {
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

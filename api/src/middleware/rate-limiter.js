/**
 * In-memory IP-based rate limiter.
 *
 * Limits each IP to 10 requests per 60-second window.
 * No Redis dependency - Cloud Run instances are ephemeral, so worst case
 * an IP gets 10 req/min x N instances. Acceptable for anonymous telemetry.
 *
 * The window map is pruned every 5 minutes to avoid unbounded memory growth.
 */

const WINDOW_MS = 60_000;   // 1 minute
const MAX_REQUESTS = 10;    // per window per IP
const PRUNE_INTERVAL = 300_000; // 5 minutes

/** @type {Map<string, { count: number, resetAt: number }>} */
const windows = new Map();

// Periodic cleanup of expired windows
setInterval(() => {
  const now = Date.now();
  for (const [ip, entry] of windows) {
    if (now > entry.resetAt) {
      windows.delete(ip);
    }
  }
}, PRUNE_INTERVAL).unref(); // .unref() so this doesn't keep the process alive

/**
 * Express middleware: rate limiter.
 */
export function rateLimiter(req, res, next) {
  // Only rate-limit write endpoints
  if (req.method === 'GET') {
    return next();
  }

  const ip = req.ip || req.socket?.remoteAddress || 'unknown';
  const now = Date.now();

  let entry = windows.get(ip);

  if (!entry || now > entry.resetAt) {
    // New window
    entry = { count: 1, resetAt: now + WINDOW_MS };
    windows.set(ip, entry);
    return next();
  }

  entry.count++;

  if (entry.count > MAX_REQUESTS) {
    const retryAfter = Math.ceil((entry.resetAt - now) / 1000);
    res.set('Retry-After', String(retryAfter));
    return res.status(429).json({
      error: 'Too many requests',
      retryAfterSeconds: retryAfter
    });
  }

  next();
}

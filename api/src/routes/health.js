import { Router } from 'express';

export const healthRouter = Router();

/**
 * GET /lens/health
 *
 * Simple liveness probe for Cloud Run and monitoring.
 */
healthRouter.get('/health', (_req, res) => {
  res.status(200).json({
    status: 'ok',
    service: 'barrikade-lens-api',
    timestamp: new Date().toISOString(),
  });
});

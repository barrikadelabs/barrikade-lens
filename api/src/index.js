import express from 'express';
import { telemetryRouter } from './routes/telemetry.js';
import { healthRouter } from './routes/health.js';
import { rateLimiter } from './middleware/rate-limiter.js';

const app = express();

// -- Global middleware --
// Cap request body at 2KB - our telemetry payload is ~500 bytes
// Anything larger is malformed or abusive
app.use(express.json({ limit: '2kb' }));

// IP-based rate limiting (10 req/min per IP)
app.use('/lens', rateLimiter);

// -- Routes --
app.use('/lens', telemetryRouter);
app.use('/lens', healthRouter);

// 404 fallback
app.use((_req, res) => {
  res.status(404).json({ error: 'Not found' });
});

// Start
const PORT = process.env.PORT || 8080;
app.listen(PORT, () => {
  console.log(`barrikade-lens-api listening on :${PORT}`);
});

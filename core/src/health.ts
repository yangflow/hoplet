/** Container health check: exit 0 when the core answers /api/health with 200. */
import { get } from 'node:http';

const port = Number(process.env.HOPLET_PORT ?? 7788);
get(
  `http://127.0.0.1:${port}/api/health`,
  { headers: { authorization: `Bearer ${process.env.HOPLET_TOKEN ?? ''}` } },
  (res) => {
    process.exit(res.statusCode === 200 ? 0 : 1);
  },
).on('error', () => {
  process.exit(1);
});

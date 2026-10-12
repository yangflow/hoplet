import { defineConfig } from '@playwright/test';

// `pnpm e2e` drives the Vite dev server (the /__states routes); `pnpm e2e:image` drives the built image
// with a real agent behind it (E2E_TARGET=image, web/e2e/image-env.ts).
const image = process.env.E2E_TARGET === 'image';
const DEV_PORT = 5199;

export default defineConfig({
  testDir: '.',
  testMatch: image
    ? ['cockpit', 'sync', 'runtime', 'handoff', 'compose'].map((name) => `${name}.spec.ts`)
    : 'states.spec.ts',
  fullyParallel: !image,
  ...(image ? { workers: 1 } : {}),
  retries: 0,
  reporter: [['list']],
  timeout: image ? 60_000 : 30_000,
  expect: { timeout: image ? 15_000 : 5_000 },
  use: {
    baseURL: image ? undefined : `http://127.0.0.1:${DEV_PORT}`,
    viewport: { width: 1280, height: 800 },
    locale: 'en-US',
    trace: 'retain-on-failure',
  },
  outputDir: '../../tmp/e2e-results',
  ...(image
    ? { globalSetup: './image-env.ts' }
    : {
        webServer: {
          command: `pnpm --filter @hoplet/web dev --host 127.0.0.1 --port ${DEV_PORT} --strictPort`,
          url: `http://127.0.0.1:${DEV_PORT}/`,
          reuseExistingServer: !process.env.CI,
          timeout: 60_000,
        },
      }),
});

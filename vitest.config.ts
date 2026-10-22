import { defineConfig } from 'vitest/config';

export default defineConfig({
  plugins: [
    {
      name: 'stub-css',
      transform(_code, id) {
        if (id.includes('.css')) return { code: 'export default {}', map: null };
      },
    },
  ],
  test: {
    include: [
      'cli/test/**/*.test.ts',
      'core/test/**/*.test.ts',
      'sdk/test/**/*.test.ts',
      'shared/test/**/*.test.ts',
      'scripts/test/**/*.test.ts',
      'web/test/**/*.test.ts',
    ],
  },
});

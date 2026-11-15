import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  outputDir: '../../../test-results/flaky',
  reporter: [['../../../dist/index.js']],
  retries: 2,
});

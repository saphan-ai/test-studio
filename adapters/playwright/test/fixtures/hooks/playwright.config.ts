import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  outputDir: '../../../test-results/hooks',
  reporter: [['../../../dist/index.js']],
  retries: 0,
  workers: 1,
  timeout: 5_000,
});

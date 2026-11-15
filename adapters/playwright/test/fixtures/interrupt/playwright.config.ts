import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  outputDir: '../../../test-results/interrupt',
  reporter: [['../../../dist/index.js']],
  fullyParallel: true,
  workers: 2,
  maxFailures: 1,
  retries: 0,
  timeout: 60_000,
});

import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  outputDir: '../../../test-results/outcomes',
  reporter: [['../../../dist/index.js']],
  retries: 0,
  timeout: 1_000,
});

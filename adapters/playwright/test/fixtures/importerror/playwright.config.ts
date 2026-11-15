import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  outputDir: '../../../test-results/importerror',
  reporter: [['../../../dist/index.js']],
  retries: 0,
});

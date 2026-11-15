import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  outputDir: '../../../test-results/setuperror',
  reporter: [['../../../dist/index.js']],
  globalSetup: './setup.ts',
  retries: 0,
});

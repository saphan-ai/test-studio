import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  outputDir: '../../../test-results/allpass',
  reporter: [['../../../dist/index.js']],
});

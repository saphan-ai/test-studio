import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: '.',
  outputDir: '../../../test-results/trace',
  reporter: [['../../../dist/index.js']],
  use: { trace: 'on' },
});

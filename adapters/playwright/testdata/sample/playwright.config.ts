import { defineConfig } from '@playwright/test';

// The sample project the conformance suite runs the reporter over: four items (a passing, a
// failing and a skipped test, and one whose fixture fails before its body runs). It needs no
// browser, so it runs wherever Playwright's test runner does.
export default defineConfig({
  testDir: '.',
  outputDir: '../../test-results/sample',
  reporter: [['../../dist/index.js']],
  retries: 0,
  use: { trace: 'on' },
});

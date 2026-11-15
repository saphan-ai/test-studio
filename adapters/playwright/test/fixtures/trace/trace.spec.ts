import { expect, test } from '@playwright/test';

// Uses the request fixture, so Playwright records a trace of the test.
test('records a trace', async ({ request }) => {
  expect(request).toBeDefined();
});

import { expect, test } from '@playwright/test';

// Two workers: the first test fails while the second is still running, so the run stops under
// maxFailures and Playwright interrupts the one that is running.
test('fails while the other runs', async () => {
  await new Promise((resolve) => setTimeout(resolve, 1_000));
  expect(1).toBe(2);
});

test('is still running when the run stops', async () => {
  await new Promise((resolve) => setTimeout(resolve, 30_000));
});

import { expect, test } from '@playwright/test';

test('fails once, then passes', async ({}, testInfo) => {
  expect(testInfo.retry).toBeGreaterThanOrEqual(1);
});

test('always fails', async () => {
  expect('a').toBe('b');
});

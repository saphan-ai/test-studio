import { expect, test } from '@playwright/test';

// Passes whether or not a core is there: only the reporter can make a run of this project exit non-zero.
test('passes on its own', async () => {
  expect(1 + 1).toBe(2);
});

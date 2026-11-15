import { expect, test } from '@playwright/test';

test('passes', async () => {
  expect(1 + 1).toBe(2);
});

test('fails an assertion', async () => {
  expect(1 + 1).toBe(3);
});

test('is skipped', async () => {
  test.skip(true, 'skipped on purpose');
});

test('times out', async () => {
  test.setTimeout(150);
  await new Promise<never>(() => undefined);
});

test('is expected to fail and does', async () => {
  test.fail();
  expect(1).toBe(2);
});

test('is expected to fail and passes', async () => {
  test.fail();
  expect(1).toBe(1);
});

test.describe('a group', () => {
  test('has a step', async () => {
    await test.step('first step', async () => {
      expect(true).toBe(true);
    });
    await test.step('second step', async () => {
      await test.step('inner step', async () => undefined);
    });
  });
});

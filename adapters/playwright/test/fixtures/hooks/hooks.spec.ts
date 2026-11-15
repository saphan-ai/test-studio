import { expect, test as base } from '@playwright/test';

const withBrokenFixture = base.extend<{ broken: string }>({
  // eslint-disable-next-line no-empty-pattern
  broken: async ({}, use) => {
    if (Date.now() > 0) throw new Error('the fixture failed on purpose');
    await use('never');
  },
});

withBrokenFixture('uses a broken fixture', async ({ broken }) => {
  expect(broken).toBe('never');
});

base.describe('a group with a failing beforeEach', () => {
  base.beforeEach(() => {
    throw new Error('the beforeEach failed on purpose');
  });
  base('has a failing beforeEach', async () => undefined);
});

base.describe('a group with a failing beforeAll', () => {
  base.beforeAll(() => {
    throw new Error('the beforeAll failed on purpose');
  });
  base('first after a failed beforeAll', async () => undefined);
  base('second after a failed beforeAll', async () => undefined);
});

base.describe('a serial group', () => {
  base.describe.configure({ mode: 'serial' });
  base('serial one fails', async () => {
    expect(1).toBe(2);
  });
  base('serial two is skipped by the group', async () => undefined);
});

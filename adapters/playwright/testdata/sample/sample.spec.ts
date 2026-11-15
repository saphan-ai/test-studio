import { expect, test } from '@playwright/test';
import { traceHeaders } from '../../dist/tracectx.js';

const sutURL = process.env['TESTS_CONFORMANCE_SUT_URL'] ?? '';

// A test whose fixture fails before its body can run.
function connectToDatabase(): string {
  throw new Error('fixture setup failed (database unreachable): the test body never ran');
}

const brokenFixture = test.extend<{ database: string }>({
  database: async ({}, use) => {
    await use(connectToDatabase());
  },
});

test('passes', async ({ request }, testInfo) => {
  const response = await request.get(`${sutURL}/sut/ping`, { headers: await traceHeaders(testInfo) });
  expect(response.ok()).toBe(true);
  await testInfo.attach('screenshot', { body: Buffer.from('the bytes of an attachment'), contentType: 'image/png' });
});

test('fails', async ({ request }, testInfo) => {
  await request.get(`${sutURL}/sut/ping`, { headers: await traceHeaders(testInfo) });
  expect(4).toBe(3);
});

test.skip('is skipped', () => {
  throw new Error('a skipped test never runs');
});

brokenFixture('fixture fails', async ({ database }) => {
  expect(database).toBe('never reached');
});

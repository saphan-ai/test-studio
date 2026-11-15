import { expect, test } from '@playwright/test';
import { stepTrace } from '../../../dist/tracectx.js';

test('the worker holds no credential', async () => {
  expect(process.env['MOCKS_RUN_TOKEN']).toBeUndefined();
  expect(process.env['TESTS_RUN_TOKEN']).toBeUndefined();
});

test('the worker is handed its own trace context', async ({}, testInfo) => {
  const trace = await stepTrace(testInfo);
  expect(trace?.traceparent).toMatch(/^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/u);
  expect(trace?.baggage).toContain('mocks.step=');
});

test('an attachment becomes evidence', async ({}, testInfo) => {
  await testInfo.attach('shot', { body: Buffer.from([0x89, 0x50, 0x4e, 0x47]), contentType: 'image/png' });
  await testInfo.attach('notes', { body: 'a note that names no credential', contentType: 'text/plain' });
  await testInfo.attach('data', { body: Buffer.from('{"k":1}'), contentType: 'application/json' });
});

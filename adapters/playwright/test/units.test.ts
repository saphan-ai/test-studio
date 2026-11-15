// The parts of the reporter that need no Playwright process.
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, test } from 'node:test';
import { CoreClient, RETRY } from '../src/client.ts';
import { collectEvidence, evidenceKind, sha256 } from '../src/evidence.ts';
import { errorText, mapOutcome, stripAnsi, type ResultLike, type TestLike } from '../src/outcome.ts';
import { stepTrace, traceHeaders, ENV_STEP_FILE } from '../src/tracectx.ts';
import { addResult, emptyCounts, verdictOf } from '../src/wire.ts';

function testLike(outcome: TestLike['outcome'] extends () => infer O ? O : never, last: Partial<ResultLike>): TestLike {
  return { outcome: () => outcome, results: [{ status: 'failed', duration: 12, errors: [], ...last }] };
}

describe('mapOutcome', () => {
  const cases: Array<[string, TestLike, boolean, string]> = [
    ['an expected outcome passes', testLike('expected', { status: 'passed' }), false, 'pass'],
    ['a flaky test that finally passed passes', testLike('flaky', { status: 'passed' }), false, 'pass'],
    ['a skipped test is skipped', testLike('skipped', { status: 'skipped' }), false, 'skip'],
    ['a failed assertion fails', testLike('unexpected', { errors: [{ message: 'expected 3' }] }), false, 'fail'],
    ['a timeout is an error', testLike('unexpected', { status: 'timedOut' }), false, 'error'],
    ['an interruption is an error', testLike('unexpected', { status: 'interrupted' }), false, 'error'],
    ['a failed fixture is an error, whatever the status', testLike('unexpected', { errors: [{ message: 'db down' }] }), true, 'error'],
    ['an expected failure that passed fails', testLike('unexpected', { status: 'passed' }), false, 'fail'],
  ];
  for (const [name, t, infra, want] of cases) {
    test(name, () => {
      const got = mapOutcome(t, infra);
      assert.equal(got.result, want);
      assert.equal(got.failure !== '', want === 'fail' || want === 'error', 'failure text exactly where failing');
    });
  }

  test('a failure without error text still carries text', () => {
    assert.notEqual(mapOutcome(testLike('unexpected', { errors: [] }), false).failure.trim(), '');
  });

  test('terminal colours are removed from failure text', () => {
    assert.equal(stripAnsi('\u001b[31mexpected\u001b[39m'), 'expected');
    assert.equal(errorText({ status: 'failed', duration: 0, errors: [{ message: '\u001b[2ma\u001b[22m' }, { value: 'b' }] }), 'a\n\nb');
  });
});

describe('the tally', () => {
  test('the verdict follows specification section 7', () => {
    const table: Array<[string[], string]> = [
      [['pass', 'skip'], 'pass'],
      [[], 'pass'],
      [['pass', 'fail'], 'fail'],
      [['fail', 'error', 'pass'], 'error'],
    ];
    for (const [results, want] of table) {
      const c = emptyCounts();
      for (const r of results) addResult(c, r as 'pass');
      assert.equal(verdictOf(c), want, results.join(','));
      assert.equal(c.total, c.passed + c.failed + c.errored + c.skipped);
    }
  });
});

describe('evidence', () => {
  test('kinds follow the content type', () => {
    assert.equal(evidenceKind({ name: 'a', contentType: 'image/png' }), 'screenshot');
    assert.equal(evidenceKind({ name: 'a', contentType: 'text/plain; charset=utf-8' }), 'log');
    assert.equal(evidenceKind({ name: 'trace', contentType: 'application/zip' }), 'artifact');
    assert.equal(evidenceKind({ name: 'video', contentType: 'video/webm' }), 'artifact');
  });

  test('a text attachment is redacted before its digest is taken, and an oversize one is named, not dropped silently', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'evidence-'));
    try {
      const big = path.join(dir, 'big.zip');
      writeFileSync(big, Buffer.alloc(100));
      const got = await collectEvidence(
        [
          { name: 'log', contentType: 'text/plain', body: Buffer.from('token SECRETVALUE here') },
          { name: 'same', contentType: 'text/plain', body: Buffer.from('token SECRETVALUE here') },
          { name: 'big', contentType: 'application/zip', path: big },
          { name: 'gone', contentType: 'application/zip', path: path.join(dir, 'missing') },
        ],
        50,
        (s) => s.replaceAll('SECRETVALUE', '[redacted]'),
      );
      assert.equal(got.items.length, 1, 'the same bytes are one item');
      assert.equal(got.items[0]?.bytes.toString('utf8'), 'token [redacted] here');
      assert.equal(got.items[0]?.digest, sha256(Buffer.from('token [redacted] here')));
      assert.equal(got.notes.length, 2);
      assert.match(got.notes.join('\n'), /"big" was not uploaded: it is 100 bytes, over the limit of 50/u);
      assert.match(got.notes.join('\n'), /"gone" was not uploaded/u);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

async function serve(handler: http.RequestListener): Promise<{ url: string; stop: () => Promise<void> }> {
  const server = http.createServer(handler);
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
  return { url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`, stop: () => new Promise((r) => server.close(() => r())) };
}

describe('the client', () => {
  const host = (said: string[]) => ({ redact: (s: string) => s, say: (l: string) => said.push(l) });

  test('a refusal is printed once and never sent again', async () => {
    let calls = 0;
    const srv = await serve((_req, res) => {
      calls++;
      res.writeHead(403, { 'Content-Type': 'application/json' }).end(JSON.stringify({ message: 'no', reason_code: 'run-credential-invalid' }));
    });
    try {
      const said: string[] = [];
      const answer = await new CoreClient(host(said)).send({ method: 'GET', url: `${srv.url}/x` });
      assert.equal(answer.status, 403);
      assert.equal(calls, 1);
      assert.deepEqual(said, ['refused [run-credential-invalid]: no']);
    } finally {
      await srv.stop();
    }
  });

  test('a server error is retried within the bound, then returned', async () => {
    let calls = 0;
    const srv = await serve((_req, res) => {
      calls++;
      res.writeHead(503).end();
    });
    try {
      const answer = await new CoreClient(host([])).send({ method: 'GET', url: `${srv.url}/x` });
      assert.equal(answer.status, 503);
      assert.equal(calls, RETRY.attempts);
    } finally {
      await srv.stop();
    }
  });

  test('a core that cannot be reached gives status 0 and a reason', async () => {
    const srv = await serve((_req, res) => res.end());
    const url = srv.url;
    await srv.stop();
    const answer = await new CoreClient(host([])).send({ method: 'GET', url: `${url}/x` });
    assert.equal(answer.status, 0);
    assert.ok((answer.error ?? '') !== '');
  });

  test('a credential in a failure is removed from the reason', async () => {
    const answer = await new CoreClient({ redact: (s) => s.replaceAll('127.0.0.1', '[redacted]'), say: () => undefined }).send({ method: 'GET', url: 'http://127.0.0.1:1/x' });
    assert.equal(answer.status, 0);
    assert.ok(!(answer.error ?? '').includes('127.0.0.1'));
  });
});

describe('the helper for test authors', () => {
  test('outside the reporter it does nothing', async () => {
    const saved = process.env[ENV_STEP_FILE];
    delete process.env['TESTS_STEP_FILE'];
    try {
      assert.equal(await stepTrace({ testId: 'a' }), undefined);
      assert.deepEqual(await traceHeaders({ testId: 'a' }), {});
    } finally {
      if (saved !== undefined) process.env[ENV_STEP_FILE] = saved;
    }
  });

  test('under the reporter it returns the test\'s own context, and waits only up to its deadline for the file', async () => {
    const dir = mkdtempSync(path.join(tmpdir(), 'tracectx-'));
    const file = path.join(dir, 'steps.json');
    process.env[ENV_STEP_FILE] = file;
    try {
      await assert.rejects(stepTrace({ testId: 'a' }, { timeoutMs: 100 }), /could not be read/u);
      writeFileSync(file, JSON.stringify({ steps: { a: { traceparent: 'tp', baggage: 'bg' } } }));
      assert.deepEqual(await traceHeaders({ testId: 'a' }), { traceparent: 'tp', baggage: 'bg' });
      assert.equal(await stepTrace({ testId: 'unknown' }), undefined);
    } finally {
      delete process.env['TESTS_STEP_FILE'];
      rmSync(dir, { recursive: true, force: true });
    }
  });
});

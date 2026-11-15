// The reporter, run by Playwright itself over small projects against an in-process core.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { after, before, describe, test } from 'node:test';
import { startFakeCore, type FakeCore, type RecordedStep } from './fakecore.ts';

const packageDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const playwright = path.join(packageDir, 'node_modules', '.bin', 'playwright');
/** Longest a Playwright run of a fixture may take. */
const RUN_LIMIT_MS = 120_000;

interface Run {
  code: number | null;
  stdout: string;
  stderr: string;
}

/** Runs one fixture project with exactly the given environment (plus the host's PATH, HOME and TMPDIR). */
function runFixture(name: string, env: Record<string, string>, extraArgs: string[] = []): Promise<Run> {
  const hostEnv: Record<string, string> = {};
  for (const k of ['PATH', 'HOME', 'TMPDIR']) {
    const v = process.env[k];
    if (v !== undefined) hostEnv[k] = v;
  }
  return new Promise((resolve, reject) => {
    const child = spawn(playwright, ['test', '--config', path.join(packageDir, 'test', 'fixtures', name, 'playwright.config.ts'), ...extraArgs], {
      cwd: packageDir, env: { ...hostEnv, ...env }, stdio: ['ignore', 'pipe', 'pipe'],
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (c: Buffer) => (stdout += c.toString('utf8')));
    child.stderr.on('data', (c: Buffer) => (stderr += c.toString('utf8')));
    const timer = setTimeout(() => child.kill('SIGKILL'), RUN_LIMIT_MS);
    child.on('error', reject);
    child.on('close', (code) => {
      clearTimeout(timer);
      resolve({ code, stdout, stderr });
    });
  });
}

function stepNamed(core: FakeCore, part: string): RecordedStep {
  const found = [...core.steps.values()].find((s) => s.stepId.endsWith(` > ${part}`));
  assert.ok(found, `no step whose id ends with "${part}" among ${[...core.steps.keys()].join(' | ')}`);
  return found;
}

describe('a run against a core', () => {
  let core: FakeCore;
  before(async () => {
    core = await startFakeCore();
  });
  after(async () => {
    await core.close();
  });

  test('every outcome is reported as the result it is, and a failure makes the run exit non-zero', async () => {
    const run = await runFixture('outcomes', { TESTS_CORE_URL: core.url });
    assert.notEqual(run.code, 0, `Playwright must exit non-zero when a test failed:\n${run.stdout}${run.stderr}`);
    const expected: Array<[string, string]> = [
      ['passes', 'pass'],
      ['fails an assertion', 'fail'],
      ['is skipped', 'skip'],
      ['times out', 'error'],
      ['is expected to fail and does', 'pass'],
      ['is expected to fail and passes', 'fail'],
      ['has a step', 'pass'],
    ];
    assert.equal(core.steps.size, expected.length);
    for (const [part, result] of expected) {
      assert.equal(stepNamed(core, part).result, result, `step "${part}"`);
    }
    assert.match(stepNamed(core, 'fails an assertion').failure ?? '', /toBe/u);
    assert.match(stepNamed(core, 'times out').failure ?? '', /timed out/u);
    assert.equal(core.closed.length, 1);
    assert.deepEqual(core.closed[0]?.['counts'], { total: 7, passed: 3, failed: 2, errored: 1, skipped: 1 });
    assert.equal(core.closed[0]?.['verdict'], 'error');
    assert.match(run.stdout, /^- evidence: test-studio-run run-fake0001\/tr-fake0001 verdict=error /mu);
  });

  test('every step has a log, and a test.step becomes an event of its own', () => {
    for (const step of core.steps.values()) {
      assert.ok(core.evidence.some((e) => e.stepId === step.stepId && e.kind === 'log'), `no log for ${step.stepId}`);
    }
    const summaries = core.events.filter((e) => e['kind'] === 'x-playwright.step').map((e) => String(e['summary']));
    assert.ok(summaries.some((s) => s.startsWith('first step')), summaries.join(' | '));
    assert.ok(summaries.some((s) => s.startsWith('inner step')), summaries.join(' | '));
  });

});

describe('a run that stops early', () => {
  test('accounts for every test that never started as an error that says so', async () => {
    const core = await startFakeCore();
    try {
      const run = await runFixture('outcomes', { TESTS_CORE_URL: core.url }, ['--max-failures=1', '--workers=1']);
      assert.notEqual(run.code, 0);
      assert.equal(core.steps.size, 7, 'every test of the project has a step');
      assert.equal(stepNamed(core, 'passes').result, 'pass');
      assert.equal(stepNamed(core, 'fails an assertion').result, 'fail');
      for (const part of ['is skipped', 'times out', 'is expected to fail and does', 'is expected to fail and passes', 'has a step']) {
        const step = stepNamed(core, part);
        assert.equal(step.result, 'error', part);
        assert.match(step.failure ?? '', /never ran/u, part);
      }
      assert.deepEqual(core.closed[0]?.['counts'], { total: 7, passed: 1, failed: 1, errored: 5, skipped: 0 });
    } finally {
      await core.close();
    }
  });
});

describe('a recorded trace', () => {
  test('is stored as an artifact of its step', async () => {
    const core = await startFakeCore();
    try {
      const run = await runFixture('trace', { TESTS_CORE_URL: core.url });
      assert.equal(run.code, 0, `${run.stdout}${run.stderr}`);
      const step = stepNamed(core, 'records a trace');
      const traces = core.evidence.filter((e) => e.stepId === step.stepId && e.kind === 'artifact' && e.bytes.subarray(0, 2).toString('latin1') === 'PK');
      assert.equal(traces.length, 1, `evidence kinds: ${core.evidence.map((e) => e.kind).join(',')}`);
    } finally {
      await core.close();
    }
  });
});

describe('a retried test', () => {
  test('is one step with the final outcome, and the earlier attempts are events', async () => {
    const core = await startFakeCore();
    try {
      const run = await runFixture('flaky', { TESTS_CORE_URL: core.url });
      assert.notEqual(run.code, 0, 'the always-failing test must fail the run');
      assert.equal(core.steps.size, 2);
      assert.equal(stepNamed(core, 'fails once, then passes').result, 'pass');
      assert.equal(stepNamed(core, 'always fails').result, 'fail');
      const attempts = core.events.filter((e) => e['kind'] === 'x-playwright.attempt');
      assert.equal(attempts.length, 1 + 2, 'one earlier attempt of the flaky test and two of the failing one');
    } finally {
      await core.close();
    }
  });
});

describe('the worker side', () => {
  test('holds no credential, gets its own trace context and has its attachments stored', async () => {
    const core = await startFakeCore();
    try {
      const run = await runFixture('probe', { TESTS_CORE_URL: core.url, TESTS_RUN_TOKEN: 'a-credential-a-parent-handed-down', MOCKS_RUN_TOKEN: 'another-credential-value' });
      assert.equal(run.code, 0, `${run.stdout}${run.stderr}`);
      for (const step of core.steps.values()) assert.equal(step.result, 'pass', step.stepId);
      const kinds = core.evidence.map((e) => e.kind);
      assert.ok(kinds.includes('screenshot'), kinds.join(','));
      assert.ok(core.evidence.some((e) => e.kind === 'log' && e.bytes.toString('utf8') === 'a note that names no credential'));
      assert.ok(core.evidence.some((e) => e.kind === 'artifact' && e.bytes.toString('utf8') === '{"k":1}'));
    } finally {
      await core.close();
    }
  });
});

describe('a core that cannot be used', () => {
  test('the same project exits zero when the core is there (so the non-zero exits below are the reporter\'s)', async () => {
    const core = await startFakeCore();
    try {
      const run = await runFixture('allpass', { TESTS_CORE_URL: core.url });
      assert.equal(run.code, 0, `${run.stdout}${run.stderr}`);
      assert.equal(core.closed[0]?.['verdict'], 'pass');
    } finally {
      await core.close();
    }
  });

  test('an unreachable core fails the run with one plain line on standard error', async () => {
    const core = await startFakeCore();
    const url = core.url;
    await core.close();
    const run = await runFixture('allpass', { TESTS_CORE_URL: url });
    assert.notEqual(run.code, 0, 'the run must not pass when nothing could be reported');
    const lines = run.stderr.split('\n').filter((l) => l.startsWith('tests-playwright-reporter:'));
    assert.equal(lines.length, 1, run.stderr);
    assert.match(lines[0] ?? '', /GET \/v1\/spec could not be read/u);
  });

  test('no core address fails the run and names the variable', async () => {
    const run = await runFixture('allpass', {});
    assert.notEqual(run.code, 0);
    assert.match(run.stderr, /TESTS_CORE_URL is not set/u);
  });

  test('a core that serves no version this reporter speaks is refused by name', async () => {
    const core = await startFakeCore(['v2']);
    try {
      const run = await runFixture('allpass', { TESTS_CORE_URL: core.url });
      assert.notEqual(run.code, 0);
      assert.match(run.stderr, /serves spec versions \[v2\] and this reporter speaks v1/u);
      assert.equal(core.steps.size, 0, 'nothing may be started on a core that was refused');
    } finally {
      await core.close();
    }
  });
});

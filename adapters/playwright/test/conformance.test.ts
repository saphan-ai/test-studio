// The one test that lets the conformance suite judge this adapter: it builds the suite's runner
// and runs it over the sample project in both modes. The suite starts its stubs on loopback and
// needs the Go toolchain, so the test skips, naming itself, where either is missing; it fails
// instead when TESTS_CONFORMANCE_REQUIRE_SOCKETS is set (spec/TEST_RUNNER_V1.md section 12).
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import net from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { test } from 'node:test';

const packageDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = path.resolve(packageDir, '..', '..');
const requireSockets = process.env.TESTS_CONFORMANCE_REQUIRE_SOCKETS !== undefined;
/** Number of test items in testdata/sample. */
const SAMPLE_ITEMS = '4';
const allPassed = /SUMMARY ([1-9][0-9]*) checks, ([1-9][0-9]*) passed, 0 failed/;

function loopbackAllowed(): Promise<boolean> {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.once('error', () => resolve(false));
    server.listen(0, '127.0.0.1', () => server.close(() => resolve(true)));
  });
}

function goAvailable(): boolean {
  return spawnSync('go', ['version'], { encoding: 'utf8' }).status === 0;
}

for (const mode of ['standalone', 'leg']) {
  test(`the conformance suite accepts the reporter in ${mode} mode`, async (t) => {
    if (!(await loopbackAllowed()) || !goAvailable()) {
      assert.ok(!requireSockets, 'loopback or the Go toolchain is missing and TESTS_CONFORMANCE_REQUIRE_SOCKETS is set');
      t.skip('UNRUN: needs loopback and the Go toolchain; run with TESTS_CONFORMANCE_REQUIRE_SOCKETS=1 where both exist');
      return;
    }
    const bin = mkdtempSync(path.join(tmpdir(), 'tests-conformance-'));
    try {
      const runner = path.join(bin, 'tests-conformance');
      const built = spawnSync('go', ['-C', path.join(repoRoot, 'conformance'), 'build', '-o', runner, './cmd/tests-conformance'], { encoding: 'utf8' });
      assert.equal(built.status, 0, `go build failed:\n${built.stdout}${built.stderr}`);
      const playwright = path.join(packageDir, 'node_modules', '.bin', 'playwright');
      const run = spawnSync(
        runner,
        ['--items', SAMPLE_ITEMS, '--mode', mode, '--spec-dir', path.join(repoRoot, 'spec'), '--',
          playwright, 'test', '--config', 'testdata/sample/playwright.config.ts'],
        { cwd: packageDir, encoding: 'utf8' },
      );
      const out = `${run.stdout}${run.stderr}`;
      t.diagnostic(out);
      assert.equal(run.status, 0, `tests-conformance --mode ${mode} exited ${run.status}:\n${out}`);
      const summary = allPassed.exec(out);
      assert.ok(summary !== null && summary[1] === summary[2] && !out.includes('FAIL '), `not every check passed in ${mode} mode:\n${out}`);
    } finally {
      rmSync(bin, { recursive: true, force: true });
    }
  });
}

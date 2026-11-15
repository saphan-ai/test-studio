# tests-playwright-reporter — the Playwright adapter

`tests-playwright-reporter` is a [Playwright](https://playwright.dev) reporter. It reports every
test of a Playwright run to a Test Studio core, following the
[test runner specification v1](../../spec/TEST_RUNNER_V1.md). Your tests stay plain Playwright
tests; you add the reporter to the configuration and set the address of the core.

It is accepted by the conformance suite in [`conformance/`](../../conformance): see
[Checking it against the conformance suite](#checking-it-against-the-conformance-suite).

**Profile.** It works in the `live` profile of specification section 1.1: it starts every step
before any test runs, and hands each test its step's `traceparent` (see
[The helper for test authors](#the-helper-for-test-authors)). It does not claim the `import`
profile.

## Install

It needs Node 22.18 or newer and `@playwright/test` 1.40 or newer (it is tested with 1.65). From a
checkout of this repository:

```
npm --prefix adapters/playwright install
npm --prefix adapters/playwright run build
```

The build writes `adapters/playwright/dist/`. Playwright takes a reporter by path or by package
name; use the path to that directory until the package is published.

## Run

Add the reporter to `playwright.config.ts`:

```ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
  reporter: [
    ['list'],
    ['/path/to/adapters/playwright/dist/index.js', { project: 'my-app' }],
  ],
});
```

Then run Playwright as you always do, with the address of the core:

```
TESTS_CORE_URL=<base URL of the core> npx playwright test
```

Options of the reporter (the second element of its entry):

| option | meaning | default |
|---|---|---|
| `project` | the project name the core records | the name of the directory that holds the configuration |
| `mocks` | mocks to ask for when a standalone test run is opened | none |
| `maxEvidenceBytes` | the largest evidence item it uploads | 64 MiB |

Playwright's own output is unchanged: the reporter prints nothing to the terminal besides the
evidence line (below) and its own error lines, and Playwright keeps showing its usual terminal
output beside it. Name a terminal reporter such as `list` yourself if you want a particular one.

### The environment it reads

| variable | meaning |
|---|---|
| `TESTS_CORE_URL` | base URL of the Test Studio core (required) |
| `TESTS_RESULT_FILE` | file the run result is written to |
| `MOCKS_RUN_ID`, `MOCKS_RUN_TOKEN` | inside a Saphan leg: attach to this run with this credential, never open a second one |
| `MOCKS_CORE_URL` | inside a leg: must name the same service as the core's answer, or the reporter refuses with `mocks-core-mismatch` |
| `TESTS_RUN_TOKEN` | a test-run credential a parent process hands down; never put on a command line |

The credentials are read once, when the reporter is created, and removed from the environment of
the process before Playwright starts its workers, so a test never inherits one. Any credential the
reporter has held is replaced by `[redacted]` in every event, log, summary and message it sends or
prints.

### Exit status

Playwright's own exit status is kept: a failed test fails the run. The reporter adds failures of
its own, and **it never swallows one**: when the core cannot be used, it writes one plain line to
standard error and makes the Playwright run exit non-zero, even if every test passed.

```
tests-playwright-reporter: the core's GET /v1/spec could not be read (status 0, error fetch failed: bad port), so no version was agreed. NEXT — check TESTS_CORE_URL.
```

The same happens when `TESTS_CORE_URL` is not set, when the core serves no version the reporter
speaks (`v1`), when the core refuses to open or attach the test run (a missing or refused
credential prints the one line that names the core's refusal, and no second line), when it refuses
to close it,
and when any call during the run was refused or failed (the line says how many). The tests still
run in these cases: a reporter cannot stop Playwright before it starts them.

## What it reports

For each Playwright test (one item per test and project) it follows the order of the specification:

1. `GET /v1/spec`; it **refuses by name** when the core serves no version it speaks.
2. Opens a test run, or **attaches** to the leg's run when `MOCKS_RUN_ID` is set.
3. Starts one **step** for every test before any test runs. The `step_id` is the project, file and
   titles of the test, joined with ` > ` and cut to printable ASCII (it travels in an HTTP header).
4. When a test ends, attaches its **evidence**, finishes its step, and emits the events
   `step.started`, `step.finished`, `evidence.stored`, `run.opened` and, **before** closing,
   `run.closed`. It also emits its own kinds: `x-playwright.step` for each `test.step` of a test
   (at most 200 per test) and `x-playwright.attempt` for each earlier attempt of a retried test.
5. Closes the run with the tally the core accepted, writes the run result to `TESTS_RESULT_FILE`
   when set, and prints the result's evidence line on standard output.

How an outcome becomes a result:

| Playwright | result |
|---|---|
| passed, or flaky (failed first, passed on a retry), or failed as `test.fail()` says it should | `pass` |
| failed an assertion or threw | `fail`, with Playwright's error text |
| marked `test.fail()` but passed | `fail` |
| skipped (`test.skip`, a skipping annotation) | `skip` |
| timed out | `error`, naming the timeout |
| a fixture or a hook threw (the body may never have run) | `error`, naming the fixture or hook |
| skipped by a failed `beforeAll` or an earlier failure in a `serial` group, though nothing in the test says to skip it | `error` |
| interrupted while running (another worker's failure under `--max-failures`, Ctrl-C), or never started because the run stopped early | `error`, saying it was interrupted or never ran |
| an error outside any test (a spec file that cannot load, a failing `globalSetup`) | one extra step, `error` |

A failed test is never reported as a pass, and an error outside any test makes the run result's
verdict `error`. A test that Playwright retries is **one step**, finished
once with its final outcome; the earlier attempts are `x-playwright.attempt` events and are written
in the step's log.

### Evidence

Every step gets a **log** (the result, every attempt with its errors, the `test.step` tree of the
last attempt, and what the test wrote to standard output and standard error), so a run always has
evidence. Every attachment Playwright recorded for the test, in any attempt, is stored as evidence
of its step: an image is a `screenshot`, plain text is a `log`, and everything else — a trace
archive, a video, an error context — is an `artifact`. Text evidence has credentials removed first.
Nothing else is uploaded. An attachment larger than `maxEvidenceBytes`, or one whose file cannot be
read, is not uploaded and is named in the step's log.

Playwright decides what is attached: turn on `trace`, `screenshot` and `video` in `use` of your
configuration to have them stored.

### The helper for test authors

The system under test must see the step's `traceparent` and `baggage`, so that a mock's event and
your test's step meet on one correlation id. The reporter starts every step before the tests run
and writes the values to a file named by `TESTS_STEP_FILE`; a test asks for its own:

```ts
import { test, expect } from '@playwright/test';
import { traceHeaders } from 'tests-playwright-reporter/tracectx';

test('lists orders', async ({ request }, testInfo) => {
  const response = await request.get('/orders', { headers: await traceHeaders(testInfo) });
  expect(response.ok()).toBe(true);
});
```

- `traceHeaders(testInfo)` returns `{ traceparent, baggage }`, or `{}` when the tests do not run
  under the reporter, so the same tests run with or without it.
- `stepTrace(testInfo)` returns the two values as an object, or `undefined`.
- Both wait, up to 30 s (`timeoutMs` changes it), for the reporter to have started the steps.

## Checking it against the conformance suite

The sample project the suite runs it over is [`testdata/sample`](testdata/sample): a passing, a
failing and a skipped test, and one whose fixture fails before its body runs (4 items). It needs
no browser. The suite starts its stubs on loopback, so this needs a host that allows listening on
`127.0.0.1`, and the Go toolchain to build the suite. From the repository root:

```
go -C conformance build -o /tmp/tests-conformance ./cmd/tests-conformance
npm --prefix adapters/playwright install
npm --prefix adapters/playwright run build
cd adapters/playwright
/tmp/tests-conformance --items 4 --mode standalone -- node_modules/.bin/playwright test --config testdata/sample/playwright.config.ts
/tmp/tests-conformance --items 4 --mode leg        -- node_modules/.bin/playwright test --config testdata/sample/playwright.config.ts
```

Both must exit 0 with every check `PASS`. `npm --prefix adapters/playwright test` builds the
package and runs all of its tests, including these two runs; the conformance test skips, naming
itself, where loopback or Go is missing, and fails when `TESTS_CONFORMANCE_REQUIRE_SOCKETS=1` is
set. `npm --prefix adapters/playwright run lint` and `run typecheck` are the other two gates.

## Honest limits

- **Steps start before the tests, so a test finds its context in a file.** A Playwright reporter
  learns of a test only after its worker has started it, too late to hand it a `traceparent`. So
  every step is started in `onBegin` and a test that calls `traceHeaders` reads its own from the
  file. A test that does not call the helper runs normally and reports normally, but nothing
  carries its `traceparent` to the system under test.
- **A large suite pays for it up front.** One call per test before the first test can read its
  context; a test waits at most 30 s for them. Your own figure is the time between the start of
  the run and the first `step.started` event.
- **One test run per Playwright process.** With `--shard`, each shard is its own run of the core.
- **A core that is down does not stop the tests.** They run, and the run exits non-zero.
- **An error outside any test** (a spec file that cannot load, a failing `globalSetup` or
  `globalTeardown`) is reported as one extra step, `playwright > errors outside any test`, with
  result `error` and Playwright's error text. It counts as one item in the run result's tally, and
  the run result's verdict is `error`. Playwright stops the whole run when a spec file cannot load,
  so in that case that step is the only item.
- **A test skipped by a failed `beforeAll` or by an earlier test of a `serial` group is an
  `error`.** Its body never ran and nothing in its source says it was meant to be skipped, so the
  reporter will not call it a `skip`. A test you skip yourself (`test.skip`, `test.fixme`) stays a
  `skip`.
- **A failed fixture is an `error`, whether it failed before the body or while tearing down after
  it.** Playwright does not say whether the body ran, so the reporter treats a fixture or hook
  failure as "the test could not be judged".
- **Transport failures are retried; refusals are not.** A call that gets no answer or a `5xx` is
  tried up to 3 times, 250 ms and then 500 ms apart. A `4xx` refusal is a decision and is printed
  once, never sent again.
- **Mock scenarios are not controlled from the test.** The reporter does not call the Mock Studio
  core's `setScenario`; scenario control is not part of this version.
- **Not claimed:** the `import` profile (reading results after the run), and `announceCorrelation`
  (it is for protocols that cannot carry the headers).

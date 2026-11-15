// The helper for test authors. The system under test must see the step's `traceparent` and
// `baggage`, so that a mock's event and your test's step meet on one correlation id. The reporter
// starts every step before the tests run and writes the two values to a file it names in
// TESTS_STEP_FILE; a test asks for its own with `traceHeaders(testInfo)`.
import { readFile } from 'node:fs/promises';

/** The environment variable that names the file the reporter writes the step trace contexts to. */
export const ENV_STEP_FILE = 'TESTS_STEP_FILE';
/** How long a test waits for the reporter to have started its steps, unless it says otherwise. */
export const DEFAULT_WAIT_MS = 30_000;
const POLL_MS = 25;

export interface StepTrace {
  traceparent: string;
  baggage: string;
}

/** The file the reporter writes: one trace context per Playwright test id. */
export interface StepFile {
  steps: Record<string, StepTrace>;
}

export interface TestInfoLike {
  testId: string;
}

export interface WaitOptions {
  /** Longest wait, in milliseconds, for the reporter's file to appear. */
  timeoutMs?: number;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function readStepFile(path: string, deadline: number): Promise<StepFile> {
  for (;;) {
    try {
      return JSON.parse(await readFile(path, 'utf8')) as StepFile;
    } catch (err) {
      const missing = (err as NodeJS.ErrnoException).code === 'ENOENT';
      if (!missing || Date.now() >= deadline) {
        throw new Error(
          `the reporter's step file ${path} could not be read (${err instanceof Error ? err.message : String(err)}). ` +
            'Check that tests-playwright-reporter is in the reporter list of the Playwright configuration.',
          { cause: err },
        );
      }
      await sleep(POLL_MS);
    }
  }
}

/**
 * Returns the trace context of the running test, or `undefined` when the tests do not run under
 * the reporter (so the same tests run with or without it). It waits, up to the deadline, for the
 * reporter to have started its steps.
 */
export async function stepTrace(testInfo: TestInfoLike, options: WaitOptions = {}): Promise<StepTrace | undefined> {
  const path = process.env[ENV_STEP_FILE];
  if (path === undefined || path === '') return undefined;
  const file = await readStepFile(path, Date.now() + (options.timeoutMs ?? DEFAULT_WAIT_MS));
  return file.steps[testInfo.testId];
}

/**
 * The `traceparent` and `baggage` headers of the running test, to put on every request to the
 * system under test; empty when the tests do not run under the reporter.
 */
export async function traceHeaders(testInfo: TestInfoLike, options: WaitOptions = {}): Promise<Record<string, string>> {
  const trace = await stepTrace(testInfo, options);
  return trace === undefined ? {} : { traceparent: trace.traceparent, baggage: trace.baggage };
}

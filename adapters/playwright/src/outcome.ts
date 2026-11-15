// How a Playwright test's final outcome becomes a step result (specification section 6.1).
import type { StepResultKind } from './wire.ts';

/** The parts of Playwright's TestError that matter here. */
export interface ErrorLike {
  message?: string | undefined;
  value?: string | undefined;
}

/** The parts of Playwright's TestResult that matter here. */
export interface ResultLike {
  status: 'passed' | 'failed' | 'timedOut' | 'skipped' | 'interrupted';
  duration: number;
  errors: readonly ErrorLike[];
}

/** The parts of Playwright's TestCase that matter here. */
export interface TestLike {
  outcome(): 'skipped' | 'expected' | 'unexpected' | 'flaky';
  results: readonly ResultLike[];
}

export interface Mapped {
  result: StepResultKind;
  /** Text for fail and error; empty otherwise. */
  failure: string;
}

const ANSI = /\u001b\[[0-9;]*[A-Za-z]/g;
const NO_TEXT = 'the test did not pass and left no error text';

/** Removes terminal colour codes, which are noise in a stored failure. */
export function stripAnsi(text: string): string {
  return text.replace(ANSI, '');
}

/** The text of every error of a result, one block per error. */
export function errorText(result: ResultLike | undefined): string {
  if (result === undefined) return '';
  const blocks = result.errors.map((e) => stripAnsi(e.message ?? e.value ?? '').trim()).filter((s) => s !== '');
  return blocks.join('\n\n');
}

/**
 * Maps the final outcome of a test to one result. A failed assertion is `fail`. A test that never
 * got to run its body (a failed fixture or hook), one that timed out and one that was interrupted
 * are `error`: the test did not say the system is wrong, the run could not judge it. A test that
 * Playwright expected to fail and that failed passes; one that was expected to fail and passed fails.
 * `infrastructureFailed` is true when a fixture or a hook of this test threw.
 */
export function mapOutcome(test: TestLike, infrastructureFailed: boolean): Mapped {
  const last = test.results.at(-1);
  switch (test.outcome()) {
    case 'skipped':
      return { result: 'skip', failure: '' };
    case 'expected':
    case 'flaky':
      return { result: 'pass', failure: '' };
    case 'unexpected':
      break;
  }
  const text = errorText(last);
  if (infrastructureFailed) {
    return { result: 'error', failure: `a fixture or hook failed, so the test could not be judged: ${text || NO_TEXT}` };
  }
  if (last?.status === 'timedOut') {
    return { result: 'error', failure: `the test timed out after ${last.duration} ms: ${text || NO_TEXT}` };
  }
  if (last?.status === 'interrupted') {
    return { result: 'error', failure: `the test run was interrupted before the test finished: ${text || NO_TEXT}` };
  }
  if (last?.status === 'passed') {
    return { result: 'fail', failure: 'the test is marked as expected to fail (test.fail) but it passed' };
  }
  return { result: 'fail', failure: text || NO_TEXT };
}

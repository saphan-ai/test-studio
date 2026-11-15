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
  annotations?: readonly { type: string }[] | undefined;
}

/** The parts of Playwright's TestCase that matter here. */
export interface TestLike {
  outcome(): 'skipped' | 'expected' | 'unexpected' | 'flaky';
  results: readonly ResultLike[];
  expectedStatus?: string | undefined;
  annotations?: readonly { type: string }[] | undefined;
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
 * True when a test was skipped although nothing says it was meant to be: no skip or fixme
 * annotation, and Playwright did not expect it to be skipped. That is what a test looks like when
 * a failed hook or a failed serial group before it kept its body from running.
 */
function skippedWithoutReason(test: TestLike, last: ResultLike | undefined): boolean {
  if (last?.status !== 'skipped') return false;
  if (test.expectedStatus === undefined || test.expectedStatus === 'skipped') return false;
  const notes = [...(test.annotations ?? []), ...(last.annotations ?? [])];
  return !notes.some((a) => a.type === 'skip' || a.type === 'fixme');
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
  // Playwright's own outcome() leaves interrupted and timed-out results out of its judgement, so the
  // last result is read first: an interrupted test must never fall through to `skip`.
  if (last?.status === 'interrupted') {
    return { result: 'error', failure: `the test run was interrupted before the test finished: ${errorText(last) || NO_TEXT}` };
  }
  if (last?.status === 'timedOut' && !infrastructureFailed && test.outcome() !== 'flaky') {
    return { result: 'error', failure: `the test timed out after ${last.duration} ms: ${errorText(last) || NO_TEXT}` };
  }
  switch (test.outcome()) {
    case 'skipped':
      return skippedWithoutReason(test, last) ? { result: 'error', failure: 'the test did not run and was not marked as skipped: a hook or a serial group before it failed' } : { result: 'skip', failure: '' };
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
  if (last?.status === 'passed') {
    return { result: 'fail', failure: 'the test is marked as expected to fail (test.fail) but it passed' };
  }
  return { result: 'fail', failure: text || NO_TEXT };
}

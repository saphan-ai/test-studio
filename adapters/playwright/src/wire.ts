// The wire documents of specification section 6, written from spec/runner-v1.openapi.yaml and
// spec/schemas/.

/** Adapter identity, written into every event and into the run result. */
export const ADAPTER_NAME = 'tests-playwright-reporter';
export const ADAPTER_VERSION = '0.1.0';
export const SPEC_VERSION = 'v1';

/** Environment variables of specification section 4. */
export const ENV_CORE_URL = 'TESTS_CORE_URL';
export const ENV_RESULT_FILE = 'TESTS_RESULT_FILE';
export const ENV_LEG_RUN_ID = 'MOCKS_RUN_ID';
export const ENV_LEG_TOKEN = 'MOCKS_RUN_TOKEN';
export const ENV_MOCKS_CORE_URL = 'MOCKS_CORE_URL';
export const ENV_RUN_TOKEN = 'TESTS_RUN_TOKEN';

export const HEADER_SPEC_VERSION = 'Tests-Spec-Version';
export const HEADER_EVIDENCE_KIND = 'Tests-Evidence-Kind';
export const HEADER_STEP_ID = 'Tests-Step-Id';

/** The result of one step, as the specification names them. */
export type StepResultKind = 'pass' | 'fail' | 'error' | 'skip';
export type Verdict = 'pass' | 'fail' | 'error';
export type EvidenceKind = 'screenshot' | 'log' | 'artifact';

export interface Identity {
  name: string;
  version: string;
  spec: string;
}

export interface OpenedRun {
  test_run_id: string;
  run_id: string;
  credential: string;
  run_credential?: string;
  mocks_core_url?: string;
}

export interface StepStarted {
  step_id: string;
  correlation_id: string;
  traceparent: string;
  baggage: string;
}

export interface Counts {
  total: number;
  passed: number;
  failed: number;
  errored: number;
  skipped: number;
}

export interface EventPayload {
  redacted: true;
  digest: string;
  size: number;
}

export interface TestsEvent {
  schema: 'tests.event/v1';
  event_id: string;
  at: string;
  run_id: string;
  test_run_id: string;
  step_id?: string;
  correlation_id?: string;
  kind: string;
  emitter: Identity;
  summary: string;
  outcome?: StepResultKind;
  payload?: EventPayload;
}

export interface Refusal {
  message?: string;
  reason_code?: string;
  allowed?: string[];
}

export function emptyCounts(): Counts {
  return { total: 0, passed: 0, failed: 0, errored: 0, skipped: 0 };
}

/** Adds one accepted result to the tally. */
export function addResult(counts: Counts, result: StepResultKind): void {
  counts.total += 1;
  switch (result) {
    case 'pass':
      counts.passed += 1;
      break;
    case 'fail':
      counts.failed += 1;
      break;
    case 'error':
      counts.errored += 1;
      break;
    case 'skip':
      counts.skipped += 1;
      break;
  }
}

/** The verdict of specification section 7: error outranks fail, and a skip never fails a run. */
export function verdictOf(counts: Counts): Verdict {
  if (counts.errored > 0) return 'error';
  if (counts.failed > 0) return 'fail';
  return 'pass';
}

/** Formats a time as UTC with millisecond precision, as specification section 2 requires. */
export function timestamp(date: Date = new Date()): string {
  return date.toISOString();
}

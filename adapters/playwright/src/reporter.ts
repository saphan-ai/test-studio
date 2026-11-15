// The Playwright reporter: it speaks the Test Studio test runner specification v1
// (spec/TEST_RUNNER_V1.md) and reports every test of a Playwright run to a core.
import { mkdirSync, mkdtempSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import type {
  FullConfig,
  FullResult,
  Reporter,
  Suite,
  TestCase,
  TestError,
  TestResult,
  TestStep,
} from '@playwright/test/reporter';
import { CoreClient, type Answer } from './client.ts';
import { collectEvidence, DEFAULT_MAX_EVIDENCE_BYTES, sha256, type AttachmentLike, type EvidenceItem } from './evidence.ts';
import { mapOutcome, stripAnsi, type Mapped } from './outcome.ts';
import { ENV_STEP_FILE, type StepTrace } from './tracectx.ts';
import {
  ADAPTER_NAME,
  ADAPTER_VERSION,
  addResult,
  emptyCounts,
  ENV_CORE_URL,
  ENV_LEG_RUN_ID,
  ENV_LEG_TOKEN,
  ENV_MOCKS_CORE_URL,
  ENV_RESULT_FILE,
  ENV_RUN_TOKEN,
  HEADER_EVIDENCE_KIND,
  HEADER_SPEC_VERSION,
  HEADER_STEP_ID,
  SPEC_VERSION,
  timestamp,
  verdictOf,
  type Counts,
  type OpenedRun,
  type StepResultKind,
  type StepStarted,
  type TestsEvent,
} from './wire.ts';

/** Options of the reporter, given in the reporter's entry of the Playwright configuration. */
export interface ReporterOptions {
  /** The project the core records. Default: the name of the directory that holds the configuration. */
  project?: string;
  /** Mocks to ask for when a standalone test run is opened. */
  mocks?: string[];
  /** The largest evidence item to upload, in bytes. */
  maxEvidenceBytes?: number;
}

const MAX_SUMMARY_CHARS = 512;
const MAX_FAILURE_BYTES = 8 << 10;
const MAX_LOG_BYTES = 1 << 20;
const MAX_STEP_ID_CHARS = 128;
const MAX_NOTED_STEPS = 200;
const MIN_SECRET_LENGTH = 4;
const REDACTED = '[redacted]';
const STEP_FILE_NAME = 'steps.json';

/** What the reporter knows about one test while the run goes on. */
interface Item {
  test: TestCase;
  stepId: string;
  display: string;
  correlation: string;
  /** A fixture or a hook of the current attempt threw. */
  infrastructureFailed: boolean;
  notedSteps: NotedStep[];
  finished: boolean;
}

interface NotedStep {
  depth: number;
  title: string;
  durationMs: number;
  error: string;
}

function truncateChars(text: string, max: number): string {
  const chars = [...text];
  return chars.length > max ? `${chars.slice(0, max - 1).join('')}…` : text;
}

function truncateBytes(text: string, max: number): string {
  const mark = '\n…[truncated]';
  const buf = Buffer.from(text, 'utf8');
  if (buf.byteLength <= max) return text;
  return `${buf.subarray(0, max - mark.length).toString('utf8').replace(/�+$/u, '')}${mark}`;
}

/** The text of one Playwright output chunk. */
function chunkText(chunk: string | Buffer): string {
  return typeof chunk === 'string' ? chunk : chunk.toString('utf8');
}

export default class TestStudioReporter implements Reporter {
  private readonly options: ReporterOptions;
  private readonly env: Record<string, string>;
  private readonly secrets: string[] = [];
  private readonly client: CoreClient;
  private readonly items = new Map<string, Item>();
  private readonly counts: Counts = emptyCounts();
  private readonly globalErrors: string[] = [];
  private readonly traces: Record<string, StepTrace> = {};
  private queue: Promise<void> = Promise.resolve();
  private coreURL = '';
  /** What the last refused or failed call looked like, for the line that names it. */
  private lastFailure = '';
  private opened: OpenedRun | undefined;
  private stepDir = '';
  private startedAt = timestamp();
  private events = 0;
  private refusals = 0;
  private fatal = false;
  private project = '';

  constructor(options: ReporterOptions = {}) {
    this.options = options;
    this.env = {};
    for (const [k, v] of Object.entries(process.env)) if (v !== undefined) this.env[k] = v;
    for (const name of [ENV_LEG_TOKEN, ENV_RUN_TOKEN]) this.addSecret(this.env[name]);
    // The tests' workers are forked from this process: they must never inherit a credential.
    delete process.env['MOCKS_RUN_TOKEN']; // ENV_LEG_TOKEN
    delete process.env['TESTS_RUN_TOKEN']; // ENV_RUN_TOKEN
    this.client = new CoreClient({ redact: (s) => this.redact(s), say: (l) => this.say(l) });
  }

  printsToStdio(): boolean {
    return false;
  }

  onBegin(config: FullConfig, suite: Suite): void {
    this.project = this.options.project ?? path.basename(config.rootDir);
    this.startedAt = timestamp();
    // Workers are forked after this call returns, so the variable is set before any test starts.
    this.stepDir = mkdtempSync(path.join(tmpdir(), 'tests-playwright-'));
    process.env[ENV_STEP_FILE] = path.join(this.stepDir, STEP_FILE_NAME);
    this.registerItems(suite.allTests());
    this.enqueue(() => this.begin());
  }

  onTestBegin(test: TestCase): void {
    const item = this.items.get(test.id);
    if (item !== undefined) {
      item.infrastructureFailed = false;
      item.notedSteps = [];
    }
  }

  onStepEnd(test: TestCase, _result: TestResult, step: TestStep): void {
    const item = this.items.get(test.id);
    if (item === undefined) return;
    if ((step.category === 'fixture' || step.category === 'hook') && step.error !== undefined) {
      item.infrastructureFailed = true;
    }
    if (step.category === 'test.step' && item.notedSteps.length < MAX_NOTED_STEPS) {
      let depth = 0;
      for (let p = step.parent; p !== undefined; p = p.parent) if (p.category === 'test.step') depth++;
      item.notedSteps.push({
        depth,
        title: step.title,
        durationMs: step.duration,
        error: step.error === undefined ? '' : stripAnsi(step.error.message ?? step.error.value ?? ''),
      });
    }
  }

  onError(error: TestError): void {
    this.globalErrors.push(stripAnsi(error.message ?? error.value ?? 'an error outside any test'));
  }

  onTestEnd(test: TestCase, result: TestResult): void {
    const final = result.status === 'skipped' || result.status === test.expectedStatus || result.retry >= test.retries || result.status === 'interrupted';
    if (!final) return;
    this.enqueue(() => this.finishItem(test));
  }

  async onEnd(fullResult: FullResult): Promise<{ status: 'failed' } | undefined> {
    await this.queue;
    await this.closeRun(fullResult);
    if (this.stepDir !== '') rmSync(this.stepDir, { recursive: true, force: true });
    const failed = this.fatal || this.refusals > 0;
    if (failed && this.refusals > 0) this.say(`${this.refusals} calls were refused or failed; the run result may not show every test.`);
    return failed ? { status: 'failed' } : undefined;
  }

  // ---- the conversation with the core ----

  /** Runs one stage after the stages before it; a stage that throws makes the run fail, never vanish. */
  private enqueue(stage: () => Promise<void>): void {
    this.queue = this.queue.then(stage).catch((err: unknown) => {
      this.fatal = true;
      this.say(`the reporter stopped: ${err instanceof Error ? err.message : String(err)}`);
    });
  }

  private async begin(): Promise<void> {
    try {
      if (await this.open()) await this.startSteps();
    } finally {
      this.publishSteps();
    }
  }

  /** Negotiates the version and opens (or attaches to) the test run. Returns false after saying why not. */
  private async open(): Promise<boolean> {
    const coreURL = (this.env[ENV_CORE_URL] ?? '').replace(/\/+$/u, '');
    if (coreURL === '') {
      this.fail(`${ENV_CORE_URL} is not set, so there is no Test Studio core to report to. NEXT — set it to the core's base URL.`);
      return false;
    }
    const spec = await this.client.send({ method: 'GET', url: `${coreURL}/v1/spec` });
    if (spec.status !== 200) {
      this.fail(`the core's GET /v1/spec could not be read (status ${spec.status}, error ${spec.error ?? 'none'}), so no version was agreed. NEXT — check ${ENV_CORE_URL}.`);
      return false;
    }
    const versions = (JSON.parse(spec.body) as { versions?: string[] }).versions ?? [];
    if (!versions.includes(SPEC_VERSION)) {
      this.fail(`refused: the core serves spec versions [${versions.join(', ')}] and this reporter speaks ${SPEC_VERSION}. NEXT — use a core that serves ${SPEC_VERSION}.`);
      return false;
    }
    this.coreURL = coreURL;
    return this.openRun();
  }

  private async openRun(): Promise<boolean> {
    const headers = { [HEADER_SPEC_VERSION]: SPEC_VERSION };
    const leg = this.env[ENV_LEG_RUN_ID] ?? '';
    const answer =
      leg === ''
        ? await this.client.send({ method: 'POST', url: `${this.coreURL}/v1/test-runs`, headers, json: { project: this.project, ...(this.options.mocks?.length ? { mocks: this.options.mocks } : {}) } })
        : await this.client.send({ method: 'POST', url: `${this.coreURL}/v1/runs/${encodeURIComponent(leg)}/test-runs`, token: this.env[ENV_LEG_TOKEN] ?? '', headers, json: { project: this.project } });
    const opened = answer.status === 201 ? (JSON.parse(answer.body) as OpenedRun) : undefined;
    if (opened === undefined || !opened.test_run_id || !opened.credential) {
      this.fail(`the core did not open or attach the test run (status ${answer.status}, error ${answer.error ?? 'none'}). NEXT — read the refusal above, or check ${ENV_CORE_URL} and ${ENV_LEG_RUN_ID}.`);
      return false;
    }
    this.addSecret(opened.credential);
    this.addSecret(opened.run_credential);
    const mocksEnv = (this.env[ENV_MOCKS_CORE_URL] ?? '').replace(/\/+$/u, '');
    const mocksAnswer = (opened.mocks_core_url ?? '').replace(/\/+$/u, '');
    if (leg !== '' && mocksEnv !== '' && mocksAnswer !== '' && mocksEnv !== mocksAnswer) {
      // Nothing was reported yet, and the specification says nothing is closed after this refusal.
      this.fail(`refused [mocks-core-mismatch]: ${ENV_MOCKS_CORE_URL} is ${mocksEnv} and the core's answer names ${mocksAnswer}, which must be one service. NEXT — start the leg again with matching endpoints.`);
      return false;
    }
    this.opened = opened;
    await this.emit('run.opened', '', '', `test run opened for project ${this.project}`);
    return true;
  }

  /** Starts the step of every test, before any of them runs, so each test can carry its traceparent. */
  private async startSteps(): Promise<void> {
    for (const [id, item] of this.items) {
      const started = await this.post<StepStarted>('/steps', { step_id: item.stepId, name: truncateChars(item.display, MAX_STEP_ID_CHARS) });
      if (started === undefined || !started.traceparent) {
        this.refusals++;
        this.say(`the step for ${item.display} was not started, so the test is not in the result.`);
        continue;
      }
      item.correlation = started.correlation_id;
      this.traces[id] = { traceparent: started.traceparent, baggage: started.baggage };
      await this.emit('step.started', item.stepId, item.correlation, `step ${item.display} started`);
    }
  }

  /** Makes one item for every test, at once: the callbacks of a running test may come before the core answers. */
  private registerItems(tests: TestCase[]): void {
    const used = new Set<string>();
    for (const test of tests) {
      const display = displayName(test);
      this.items.set(test.id, { test, stepId: uniqueStepId(display, used), display, correlation: '', infrastructureFailed: false, notedSteps: [], finished: false });
    }
  }

  /** Writes the file the tests read their trace context from; written whole, then renamed into place. */
  private publishSteps(): void {
    if (this.stepDir === '') return;
    mkdirSync(this.stepDir, { recursive: true });
    const target = path.join(this.stepDir, STEP_FILE_NAME);
    const partial = `${target}.partial`;
    writeFileSync(partial, JSON.stringify({ steps: this.traces }), { mode: 0o600 });
    renameSync(partial, target);
  }

  /** Finishes the step of a test whose final result is known. */
  private async finishItem(test: TestCase): Promise<void> {
    const item = this.items.get(test.id);
    if (item === undefined || item.finished || this.opened === undefined || item.correlation === '') return;
    const mapped = mapOutcome(test, item.infrastructureFailed);
    const attachments: AttachmentLike[] = test.results.flatMap((r) => r.attachments);
    const last = test.results.at(-1);
    await this.finishStep(item, mapped, Math.max(0, Math.round(last?.duration ?? 0)), attachments, this.logOf(item, mapped, test));
    for (const r of test.results.slice(0, -1)) {
      await this.emit('x-playwright.attempt', item.stepId, item.correlation, `attempt ${r.retry + 1} of ${test.results.length} of ${item.display} ended: ${r.status}`);
    }
  }

  /** Attaches the evidence, finishes the step and emits its events. */
  private async finishStep(item: Item, mapped: Mapped, durationMs: number, attachments: readonly AttachmentLike[], log: string): Promise<void> {
    item.finished = true;
    const { items, notes } = await collectEvidence(attachments, this.options.maxEvidenceBytes ?? DEFAULT_MAX_EVIDENCE_BYTES, (s) => this.redact(s));
    const logText = truncateBytes(this.redact(notes.length > 0 ? `${log}\n${notes.join('\n')}\n` : log), MAX_LOG_BYTES);
    const logBytes = Buffer.from(logText, 'utf8');
    await this.attach(item, { name: 'log', kind: 'log', bytes: logBytes, digest: sha256(logBytes) });
    for (const ev of items) await this.attach(item, ev);
    for (const s of item.notedSteps) {
      await this.emit('x-playwright.step', item.stepId, item.correlation, truncateChars(this.redact(`${s.title} (${s.durationMs} ms)${s.error === '' ? '' : ' failed'}`), MAX_SUMMARY_CHARS));
    }
    const failure = mapped.result === 'fail' || mapped.result === 'error' ? truncateBytes(this.redact(mapped.failure), MAX_FAILURE_BYTES) : '';
    const accepted = await this.post('/step-results', { step_id: item.stepId, result: mapped.result, duration_ms: durationMs, ...(failure === '' ? {} : { failure }) });
    if (accepted === undefined) {
      this.refusals++;
      this.say(`the result of step ${item.display} was not accepted (${this.lastFailure}).`);
      return;
    }
    addResult(this.counts, mapped.result);
    await this.emit('step.finished', item.stepId, item.correlation, `step ${item.display} finished: ${mapped.result}`, mapped.result);
  }

  private async attach(item: Item, ev: EvidenceItem): Promise<void> {
    const answer = await this.client.send({
      method: 'PUT',
      url: `${this.testRunURL()}/evidence/${ev.digest}`,
      token: this.opened?.credential ?? '',
      headers: { [HEADER_EVIDENCE_KIND]: ev.kind, [HEADER_STEP_ID]: item.stepId, 'Content-Type': 'application/octet-stream' },
      bytes: ev.bytes,
    });
    if (Math.floor(answer.status / 100) !== 2) {
      this.refusals++;
      this.say(`the ${ev.kind} evidence "${ev.name}" of ${item.display} was not accepted (${describeAnswer(answer)}).`);
      return;
    }
    await this.emit('evidence.stored', item.stepId, item.correlation, `${ev.kind} evidence stored for step ${item.display}`, undefined, { redacted: true, digest: `sha256:${ev.digest}`, size: ev.bytes.byteLength });
  }

  private logOf(item: Item, mapped: Mapped, test: TestCase): string {
    const out: string[] = [`test: ${item.display}`, `result: ${mapped.result} (Playwright outcome: ${test.outcome()}, ${test.results.length} attempt(s))`];
    if (mapped.failure !== '') out.push(`failure: ${mapped.failure}`);
    test.results.forEach((r, i) => {
      out.push(`attempt ${i + 1}: ${r.status} in ${r.duration} ms`);
      for (const e of r.errors) out.push(`  error: ${stripAnsi(e.message ?? e.value ?? '')}`);
    });
    if (item.notedSteps.length > 0) out.push('steps of the last attempt:');
    for (const s of item.notedSteps) out.push(`${'  '.repeat(s.depth + 1)}- ${s.title} (${s.durationMs} ms)${s.error === '' ? '' : ` failed: ${s.error}`}`);
    for (const [label, chunks] of [['stdout', test.results.at(-1)?.stdout ?? []], ['stderr', test.results.at(-1)?.stderr ?? []]] as const) {
      if (chunks.length > 0) out.push(`--- ${label} ---`, chunks.map(chunkText).join(''));
    }
    return `${out.join('\n')}\n`;
  }

  /** Reports every test that never finished, emits run.closed, closes the run and publishes the result. */
  private async closeRun(fullResult: FullResult): Promise<void> {
    if (this.opened === undefined) return;
    for (const item of this.items.values()) {
      if (item.finished) continue;
      const never: Mapped = { result: 'error', failure: `the test never ran: the run ended with status ${fullResult.status} before it started` };
      if (item.correlation === '') continue; // its step was never accepted; the core has nothing to finish
      await this.finishStep(item, never, 0, [], this.logOf(item, never, item.test));
    }
    for (const e of this.globalErrors) {
      this.say(`Playwright reported an error outside any test: ${truncateChars(e.split('\n')[0] ?? '', MAX_SUMMARY_CHARS)}`);
    }
    const verdict = verdictOf(this.counts);
    // Closing revokes the credential, so run.closed must come first (specification section 9.2).
    await this.emit('run.closed', '', '', `test run closed with verdict ${verdict}`, verdict);
    const answer = await this.client.send({
      method: 'POST',
      url: `${this.testRunURL()}/close`,
      token: this.opened.credential,
      json: { verdict, counts: this.counts, started_at: this.startedAt, ended_at: timestamp(), adapter: { name: ADAPTER_NAME, version: ADAPTER_VERSION, spec: SPEC_VERSION } },
    });
    const evidenceLine = answer.status === 200 ? (JSON.parse(answer.body) as { evidence_line?: string }).evidence_line : undefined;
    if (evidenceLine === undefined || evidenceLine === '') {
      this.fail(`the core did not close the test run with a run result (status ${answer.status}, error ${answer.error ?? 'none'}). NEXT — read the refusal above.`);
      return;
    }
    const resultFile = this.env[ENV_RESULT_FILE] ?? '';
    if (resultFile !== '') {
      try {
        writeFileSync(resultFile, `${answer.body.trimEnd()}\n`, { mode: 0o600 });
      } catch (err) {
        this.fail(`the run result could not be written to ${ENV_RESULT_FILE}: ${err instanceof Error ? err.message : String(err)}.`);
      }
    }
    process.stdout.write(`${evidenceLine}\n`);
  }

  // ---- small helpers ----

  private testRunURL(): string {
    return `${this.coreURL}/v1/test-runs/${encodeURIComponent(this.opened?.test_run_id ?? '')}`;
  }

  /** Posts a JSON document under the test run and returns the answer's document, or undefined on a refusal. */
  private async post<T>(suffix: string, body: unknown): Promise<T | undefined> {
    const answer = await this.client.send({ method: 'POST', url: `${this.testRunURL()}${suffix}`, token: this.opened?.credential ?? '', json: body });
    if (Math.floor(answer.status / 100) !== 2) {
      this.lastFailure = describeAnswer(answer);
      return undefined;
    }
    return (answer.body === '' ? {} : JSON.parse(answer.body)) as T;
  }

  /** Emits one tests.event/v1 document. A refused event is counted and never fatal. */
  private async emit(kind: string, stepId: string, correlation: string, summary: string, outcome?: StepResultKind, payload?: TestsEvent['payload']): Promise<void> {
    if (this.opened === undefined) return;
    this.events += 1;
    const event: TestsEvent = {
      schema: 'tests.event/v1',
      event_id: `${this.opened.test_run_id}-evt-${String(this.events).padStart(4, '0')}`,
      at: timestamp(),
      run_id: this.opened.run_id,
      test_run_id: this.opened.test_run_id,
      kind,
      emitter: { name: ADAPTER_NAME, version: ADAPTER_VERSION, spec: SPEC_VERSION },
      summary: truncateChars(this.redact(summary), MAX_SUMMARY_CHARS),
    };
    if (stepId !== '') event.step_id = stepId;
    if (correlation !== '') event.correlation_id = correlation;
    if (outcome !== undefined) event.outcome = outcome;
    if (payload !== undefined) event.payload = payload;
    if ((await this.post('/events', event)) === undefined) {
      this.refusals++;
      this.say(`the event ${kind} was not accepted (${this.lastFailure}).`);
    }
  }

  private addSecret(secret: string | undefined): void {
    if (secret !== undefined && secret.length >= MIN_SECRET_LENGTH) this.secrets.push(secret);
  }

  /** Removes every credential the reporter has held from text. */
  private redact(text: string): string {
    let out = text;
    for (const s of this.secrets) out = out.split(s).join(REDACTED);
    return out;
  }

  /** One plain line on the reporter's own error output, credentials removed. */
  private say(line: string): void {
    process.stderr.write(`${ADAPTER_NAME}: ${this.redact(line)}\n`);
  }

  /** Says why the run cannot be reported, and makes the Playwright run exit non-zero. */
  private fail(line: string): void {
    this.fatal = true;
    this.say(line);
  }
}

function describeAnswer(answer: Answer): string {
  return answer.status === 0 ? `no answer: ${answer.error ?? 'unknown error'}` : `status ${answer.status}`;
}

/** The display name of a test: project, file and titles, as Playwright prints them. */
function displayName(test: TestCase): string {
  return test.titlePath().filter((p) => p !== '').join(' > ');
}

/** A step_id of at most 128 characters, unique in the run: a cut id gets a counter. */
function uniqueStepId(display: string, used: Set<string>): string {
  // The id travels in an HTTP header (Tests-Step-Id), so it holds printable ASCII only.
  const ascii = display.replace(/[^\x20-\x7E]/gu, '_').trim();
  const base = ascii.slice(0, MAX_STEP_ID_CHARS - 8);
  let id = [...ascii].length > MAX_STEP_ID_CHARS ? base : ascii;
  for (let n = 2; used.has(id); n++) id = `${base}~${n}`;
  used.add(id);
  return id;
}

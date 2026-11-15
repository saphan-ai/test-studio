// A small in-process Test Studio core for the reporter's own tests: it answers the calls of
// specification section 6 and records what it was sent. It checks nothing the conformance suite
// checks; it exists so that a test can look at what the reporter sent.
import { createHash, randomBytes } from 'node:crypto';
import http from 'node:http';
import type { AddressInfo } from 'node:net';

export interface RecordedStep {
  stepId: string;
  correlationId: string;
  result?: string;
  durationMs?: number;
  failure?: string;
}

export interface RecordedEvidence {
  stepId: string;
  kind: string;
  digest: string;
  bytes: Buffer;
}

export interface FakeCore {
  url: string;
  credential: string;
  steps: Map<string, RecordedStep>;
  evidence: RecordedEvidence[];
  events: Array<Record<string, unknown>>;
  closed: Array<Record<string, unknown>>;
  /** Authorization headers seen, in order, with the path they came with. */
  auth: Array<{ path: string; header: string | undefined }>;
  close(): Promise<void>;
}

function body(req: http.IncomingMessage): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    req.on('data', (c: Buffer) => chunks.push(c));
    req.on('end', () => resolve(Buffer.concat(chunks)));
    req.on('error', reject);
  });
}

function send(res: http.ServerResponse, status: number, doc: unknown): void {
  res.writeHead(status, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(doc));
}

/** Starts a core on loopback. `versions` is what GET /v1/spec serves. */
export async function startFakeCore(versions: string[] = ['v1']): Promise<FakeCore> {
  const credential = `cred-${randomBytes(8).toString('hex')}`;
  const core: FakeCore = {
    url: '', credential, steps: new Map(), evidence: [], events: [], closed: [], auth: [],
    close: () => new Promise((resolve) => server.close(() => resolve())),
  };
  const server = http.createServer((req, res) => {
    void handle(core, versions, req, res).catch((err: unknown) => send(res, 500, { message: String(err), reason_code: 'internal' }));
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  core.url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return core;
}

async function handle(core: FakeCore, versions: string[], req: http.IncomingMessage, res: http.ServerResponse): Promise<void> {
  const path = new URL(req.url ?? '/', 'http://x').pathname;
  const data = await body(req);
  core.auth.push({ path, header: req.headers.authorization });
  if (req.method === 'GET' && path === '/v1/spec') return send(res, 200, { versions });
  if (req.method === 'POST' && (path === '/v1/test-runs' || /^\/v1\/runs\/[^/]+\/test-runs$/u.test(path))) {
    return send(res, 201, { test_run_id: 'tr-fake0001', run_id: 'run-fake0001', credential: core.credential });
  }
  const m = /^\/v1\/test-runs\/tr-fake0001\/(steps|step-results|events|close)$/u.exec(path);
  const doc = data.length > 0 && req.method === 'POST' ? (JSON.parse(data.toString('utf8')) as Record<string, unknown>) : {};
  if (req.method === 'POST' && m?.[1] === 'steps') {
    const stepId = String(doc['step_id']);
    const correlationId = randomBytes(16).toString('hex');
    core.steps.set(stepId, { stepId, correlationId });
    return send(res, 201, { step_id: stepId, correlation_id: correlationId, traceparent: `00-${correlationId}-${randomBytes(8).toString('hex')}-01`, baggage: `mocks.run=run-fake0001,mocks.step=${stepId}` });
  }
  if (req.method === 'POST' && m?.[1] === 'step-results') {
    const step = core.steps.get(String(doc['step_id']));
    if (step === undefined) return send(res, 422, { message: 'unknown step', reason_code: 'step-unknown' });
    Object.assign(step, { result: doc['result'], durationMs: doc['duration_ms'], failure: doc['failure'] });
    return send(res, 200, {});
  }
  if (req.method === 'POST' && m?.[1] === 'events') {
    core.events.push(doc);
    return send(res, 202, {});
  }
  if (req.method === 'POST' && m?.[1] === 'close') {
    core.closed.push(doc);
    return send(res, 200, { run_id: 'run-fake0001', test_run_id: 'tr-fake0001', verdict: doc['verdict'], evidence_line: `- evidence: test-studio-run run-fake0001/tr-fake0001 verdict=${String(doc['verdict'])} events=sha256:${'0'.repeat(64)}` });
  }
  const ev = /^\/v1\/test-runs\/tr-fake0001\/evidence\/([0-9a-f]{64})$/u.exec(path);
  if (req.method === 'PUT' && ev?.[1] !== undefined) {
    const digest = createHash('sha256').update(data).digest('hex');
    if (digest !== ev[1]) return send(res, 422, { message: 'digest mismatch', reason_code: 'evidence-digest-mismatch' });
    core.evidence.push({ stepId: String(req.headers['tests-step-id']), kind: String(req.headers['tests-evidence-kind']), digest, bytes: data });
    return send(res, 200, {});
  }
  return send(res, 404, { message: 'no such call', reason_code: 'request-invalid' });
}

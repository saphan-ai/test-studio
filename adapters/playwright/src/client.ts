// The HTTP half of the reporter: one request at a time to the core, with every credential removed
// from what it prints. A refusal (a 4xx answer) is a decision and is never sent again; a transport
// failure (no answer, a reset connection, a 5xx) is retried within a documented bound
// (specification section 10.1).
import type { Refusal } from './wire.ts';

/** Bounds of the transport-failure retry, documented in the README. */
export const RETRY = { attempts: 3, firstBackoffMs: 250 } as const;
export const HTTP_TIMEOUT_MS = 30_000;
export const MAX_RESPONSE_BYTES = 8 << 20;
const STATUS_REFUSAL_MIN = 400;
const STATUS_SERVER_ERROR_MIN = 500;

export interface Answer {
  /** HTTP status; 0 when no answer arrived. */
  status: number;
  body: string;
  /** The transport failure, redacted; set only when status is 0. */
  error?: string;
}

export interface Request {
  method: 'GET' | 'POST' | 'PUT';
  url: string;
  token?: string;
  headers?: Record<string, string>;
  json?: unknown;
  bytes?: Uint8Array;
}

/** What the client needs from its owner: a way to remove credentials, and a place to say things. */
export interface ClientHost {
  redact(text: string): string;
  say(line: string): void;
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function readCapped(response: Response): Promise<string> {
  const reader = response.body?.getReader();
  if (reader === undefined) return '';
  const chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > MAX_RESPONSE_BYTES) {
      await reader.cancel();
      throw new Error(`the answer is larger than ${MAX_RESPONSE_BYTES} bytes`);
    }
    chunks.push(value);
  }
  return Buffer.concat(chunks).toString('utf8');
}

/** Removes a query string, so a URL can be printed. */
export function withoutQuery(url: string): string {
  const i = url.indexOf('?');
  return i < 0 ? url : url.slice(0, i);
}

export class CoreClient {
  private readonly host: ClientHost;
  private readonly fetchImpl: typeof fetch;

  constructor(host: ClientHost, fetchImpl: typeof fetch = fetch) {
    this.host = host;
    this.fetchImpl = fetchImpl;
  }

  /** Sends a request. A refusal is printed once and returned; it is never retried. */
  async send(req: Request): Promise<Answer> {
    let answer: Answer = { status: 0, body: '', error: 'no attempt was made' };
    for (let attempt = 0; attempt < RETRY.attempts; attempt++) {
      if (attempt > 0) await sleep(RETRY.firstBackoffMs * 2 ** (attempt - 1));
      answer = await this.once(req);
      if (answer.status !== 0 && answer.status < STATUS_SERVER_ERROR_MIN) break;
    }
    if (answer.status >= STATUS_REFUSAL_MIN && answer.status < STATUS_SERVER_ERROR_MIN) this.sayRefusal(req, answer);
    return answer;
  }

  private async once(req: Request): Promise<Answer> {
    const headers: Record<string, string> = { ...req.headers };
    let body: Blob | string | undefined;
    if (req.json !== undefined) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(req.json);
    } else if (req.bytes !== undefined) {
      body = new Blob([new Uint8Array(req.bytes)]);
    }
    if (req.token !== undefined && req.token !== '') headers['Authorization'] = `Bearer ${req.token}`;
    try {
      const init: RequestInit = { method: req.method, headers, signal: AbortSignal.timeout(HTTP_TIMEOUT_MS) };
      if (body !== undefined) init.body = body;
      const response = await this.fetchImpl(req.url, init);
      return { status: response.status, body: await readCapped(response) };
    } catch (err) {
      return { status: 0, body: '', error: this.host.redact(describe(err)) };
    }
  }

  private sayRefusal(req: Request, answer: Answer): void {
    let refusal: Refusal = {};
    try {
      refusal = JSON.parse(answer.body) as Refusal;
    } catch {
      // The answer is not a refusal document; the status line below says what was measured.
    }
    if (refusal.reason_code !== undefined && refusal.reason_code !== '') {
      this.host.say(`refused [${refusal.reason_code}]: ${refusal.message ?? ''}`);
    } else {
      this.host.say(`the core answered ${req.method} ${withoutQuery(req.url)} with status ${answer.status}`);
    }
  }
}

function describe(err: unknown): string {
  if (err instanceof Error) {
    const cause = err.cause instanceof Error ? `: ${err.cause.message}` : '';
    return `${err.message}${cause}`;
  }
  return String(err);
}

// Evidence: what Playwright recorded for a test (screenshots, traces, videos, anything a test
// attached), turned into the content-addressed items of specification section 6.2.
import { createHash } from 'node:crypto';
import { readFile, stat } from 'node:fs/promises';
import type { EvidenceKind } from './wire.ts';

/** The parts of Playwright's attachment that matter here. */
export interface AttachmentLike {
  name: string;
  contentType: string;
  path?: string | undefined;
  body?: Buffer | undefined;
}

export interface EvidenceItem {
  name: string;
  kind: EvidenceKind;
  bytes: Buffer;
  /** Lowercase hex SHA-256 of `bytes`: the exact bytes that are sent. */
  digest: string;
}

export interface Collected {
  items: EvidenceItem[];
  /** One plain sentence per attachment that was not uploaded, and why. */
  notes: string[];
}

/** The largest evidence item the reporter uploads, in bytes, unless the reporter's options say otherwise. */
export const DEFAULT_MAX_EVIDENCE_BYTES = 64 << 20;

/** A screenshot is an image, a log is plain text, and everything else (trace, video) is an artifact. */
export function evidenceKind(a: AttachmentLike): EvidenceKind {
  const type = a.contentType.toLowerCase();
  if (type.startsWith('image/')) return 'screenshot';
  if (type.startsWith('text/plain')) return 'log';
  return 'artifact';
}

export function sha256(bytes: Uint8Array): string {
  return createHash('sha256').update(bytes).digest('hex');
}

async function read(a: AttachmentLike, maxBytes: number): Promise<Buffer | string> {
  if (a.body !== undefined) {
    return a.body.byteLength > maxBytes ? `it is ${a.body.byteLength} bytes, over the limit of ${maxBytes}` : a.body;
  }
  if (a.path === undefined) return 'it has neither a body nor a file';
  try {
    const info = await stat(a.path);
    if (!info.isFile()) return 'its path is not a file';
    if (info.size > maxBytes) return `it is ${info.size} bytes, over the limit of ${maxBytes}`;
    return await readFile(a.path);
  } catch (err) {
    return `its file could not be read (${err instanceof Error ? err.message : String(err)})`;
  }
}

/**
 * Reads every attachment of a test. A text attachment is passed through `redact` first, so a
 * credential never leaves in one; the digest is that of the bytes after redaction. An attachment
 * over `maxBytes`, or one that cannot be read, is left out and named in `notes`, never silently.
 * The same bytes attached twice are one item.
 */
export async function collectEvidence(
  attachments: readonly AttachmentLike[],
  maxBytes: number,
  redact: (text: string) => string,
): Promise<Collected> {
  const items: EvidenceItem[] = [];
  const notes: string[] = [];
  const seen = new Set<string>();
  for (const a of attachments) {
    const got = await read(a, maxBytes);
    if (typeof got === 'string') {
      notes.push(`attachment "${a.name}" was not uploaded: ${got}.`);
      continue;
    }
    const kind = evidenceKind(a);
    const bytes = kind === 'log' ? Buffer.from(redact(got.toString('utf8')), 'utf8') : got;
    const digest = sha256(bytes);
    if (seen.has(digest)) continue;
    seen.add(digest);
    items.push({ name: a.name, kind, bytes, digest });
  }
  return { items, notes };
}

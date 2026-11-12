import { TestBed } from '@angular/core/testing';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SessionService } from '../../core/auth/session';
import { putBytes, Uploader } from './upload';

function stubFetch(handlers: Record<string, unknown | ((vars: object) => unknown)>) {
  const calls: { op: string; vars: Record<string, unknown> }[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body)) as { query: string; variables?: object };
      const op = /(?:query|mutation)\s+(\w+)/.exec(body.query)?.[1] ?? '';
      calls.push({ op, vars: (body.variables ?? {}) as Record<string, unknown> });
      const h = handlers[op];
      if (h === 'network') throw new TypeError('fetch failed');
      const data = typeof h === 'function' ? h(body.variables ?? {}) : h;
      return new Response(JSON.stringify({ data }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      });
    }),
  );
  return calls;
}

function asset(over: Record<string, unknown> = {}) {
  return {
    id: 'a1',
    purpose: 'PHOTO',
    state: 'RESERVED',
    byteCount: 10,
    mimeType: null,
    sha256: '',
    displayName: 'one.jpg',
    errorCode: null,
    expiresAt: '2026-11-02T00:00:00Z',
    variants: [],
    createdAt: '2026-11-01T00:00:00Z',
    updatedAt: '2026-11-01T00:00:00Z',
    ...over,
  };
}

/** Minimal XMLHttpRequest double: enough surface for putBytes. */
class FakeXHR {
  static behavior: (x: FakeXHR) => void = (x) =>
    x.respond(200, JSON.stringify({ sha256: 'f'.repeat(64) }));
  static puts: { url: string; csrf: string | null }[] = [];
  url = '';
  csrf: string | null = null;
  withCredentials = false;
  status = 0;
  responseText = '';
  private listeners: Record<string, (e?: unknown) => void> = {};
  private progressCb: ((e: unknown) => void) | null = null;
  readonly upload = {
    addEventListener: (_ev: string, cb: (e: unknown) => void) => {
      this.progressCb = cb;
    },
  };
  open(_m: string, url: string): void {
    this.url = url;
  }
  setRequestHeader(k: string, v: string): void {
    if (k === 'X-CSRF-Token') this.csrf = v;
  }
  addEventListener(ev: string, cb: (e?: unknown) => void): void {
    this.listeners[ev] = cb;
  }
  send(_body: unknown): void {
    FakeXHR.puts.push({ url: this.url, csrf: this.csrf });
    FakeXHR.behavior(this);
  }
  progress(loaded: number, total: number): void {
    this.progressCb?.({ lengthComputable: true, loaded, total });
  }
  respond(status: number, text: string): void {
    this.status = status;
    this.responseText = text;
    this.listeners['load']();
  }
  netFail(): void {
    this.listeners['error']();
  }
}

describe('putBytes', () => {
  beforeEach(() => {
    FakeXHR.behavior = (x) => x.respond(200, JSON.stringify({ sha256: 'f'.repeat(64) }));
    FakeXHR.puts = [];
    vi.stubGlobal('XMLHttpRequest', FakeXHR);
  });
  afterEach(() => vi.unstubAllGlobals());

  it.each([
    {},
    null,
    { sha256: '' },
    { sha256: 'ff' },
    { sha256: 123 },
    { sha256: 'z'.repeat(64) },
  ])('rejects a malformed successful upload receipt: %j', async (body) => {
    FakeXHR.behavior = (x) => x.respond(200, JSON.stringify(body));
    expect(await putBytes('a1', new Blob(['photo']), null, () => {})).toEqual({
      ok: false,
      code: 'BAD_RESPONSE',
      message: 'The server returned an invalid upload checksum. Retry the upload.',
    });
  });

  it('reports progress and resolves with the server checksum', async () => {
    const seen: number[] = [];
    FakeXHR.behavior = (x) => {
      x.progress(5, 10);
      x.respond(200, JSON.stringify({ sha256: 'f'.repeat(64) }));
    };
    const result = await putBytes('a1', new Blob(['x']), 'tok', (pct) => seen.push(pct));
    expect(result).toEqual({ ok: true, sha256: 'f'.repeat(64) });
    expect(seen).toEqual([50]);
    expect(FakeXHR.puts[0].url).toBe('/api/uploads/a1');
    expect(FakeXHR.puts[0].csrf).toBe('tok');
  });

  it('surfaces the problem envelope code for a 415', async () => {
    FakeXHR.behavior = (x) =>
      x.respond(
        415,
        JSON.stringify({
          problems: [
            { code: 'UNSUPPORTED_MEDIA_TYPE', message: 'Content is not an allowed image type.' },
          ],
        }),
      );
    const res = await putBytes('a1', new Blob(['x']), null, () => {});
    expect(res).toEqual({
      ok: false,
      code: 'UNSUPPORTED_MEDIA_TYPE',
      message: 'Content is not an allowed image type.',
    });
  });

  it('resolves a network failure honestly', async () => {
    FakeXHR.behavior = (x) => x.netFail();
    const res = await putBytes('a1', new Blob(['x']), null, () => {});
    expect(res.ok).toBe(false);
    if (!res.ok) expect(res.code).toBe('NETWORK');
  });
});

describe('Uploader', () => {
  let session: SessionService;

  beforeEach(() => {
    TestBed.configureTestingModule({});
    session = TestBed.inject(SessionService);
    session.state.set('authed');
    session.info.set({ workspaceId: 'w1', expiresAt: '2026-12-01T00:00:00Z', csrfToken: 'c' });
  });

  afterEach(() => vi.unstubAllGlobals());

  it('reserve returns the payload problem verbatim', async () => {
    stubFetch({
      ReserveUpload: {
        reserveUpload: {
          asset: null,
          problems: [{ code: 'QUOTA_EXCEEDED', field: null, message: 'Storage quota reached.' }],
        },
      },
    });
    const res = await TestBed.inject(Uploader).reserve('PHOTO', 'one.jpg', 10);
    expect(res).toEqual({ ok: false, code: 'QUOTA_EXCEEDED', message: 'Storage quota reached.' });
  });

  it('waitTerminal: FAILED surfaces the stored errorCode', async () => {
    stubFetch({
      PendingUploads: {
        pendingUploads: { items: [asset({ state: 'FAILED', errorCode: 'PROCESSING_FAILED' })] },
      },
    });
    const t = await TestBed.inject(Uploader).waitTerminal('a1', 5_000, 1);
    expect(t).toEqual({ outcome: 'failed', errorCode: 'PROCESSING_FAILED' });
  });

  it('waitTerminal: leaving the pending set reads as gone', async () => {
    stubFetch({ PendingUploads: { pendingUploads: { items: [] } } });
    const t = await TestBed.inject(Uploader).waitTerminal('a1', 5_000, 1);
    expect(t.outcome).toBe('gone');
  });

  it('waitTerminal: still processing past the deadline times out', async () => {
    stubFetch({ PendingUploads: { pendingUploads: { items: [asset({ state: 'PROCESSING' })] } } });
    const t = await TestBed.inject(Uploader).waitTerminal('a1', 0, 1);
    expect(t.outcome).toBe('timeout');
  });

  it('finalize sends the client checksum', async () => {
    const calls = stubFetch({
      FinalizeUpload: { finalizeUpload: { asset: asset({ state: 'UPLOADED' }), problems: [] } },
    });
    const res = await TestBed.inject(Uploader).finalize('a1', 'deadbeef');
    expect(res.ok).toBe(true);
    expect(calls[0].vars).toEqual({ input: { assetId: 'a1', sha256: 'deadbeef' } });
  });
  it('passes explicit stable reservation and finalization keys without changing their request bodies', async () => {
    const calls = stubFetch({
      ReserveUpload: { reserveUpload: { asset: asset(), problems: [] } },
      FinalizeUpload: { finalizeUpload: { asset: asset({ state: 'READY' }), problems: [] } },
    });
    const uploader = TestBed.inject(Uploader);
    await uploader.reserve('PHOTO', 'tree.jpg', 10, 'round-reserve-key');
    await uploader.finalize('a1', 'a'.repeat(64), 'round-finalize-key');
    expect(calls[0].vars).toEqual({
      input: { purpose: 'PHOTO', filename: 'tree.jpg', claimedBytes: 10 },
      clientMutationId: 'round-reserve-key',
    });
    expect(calls[1].vars).toEqual({
      input: { assetId: 'a1', sha256: 'a'.repeat(64) },
      clientMutationId: 'round-finalize-key',
    });
  });
});
